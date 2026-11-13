import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { basename, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export type GuardVerdict =
  | { action: 'allow' }
  | { action: 'deny'; reason: string }
  | { action: 'ask'; reason: string };

export type GuardRuntime = 'agy' | 'claude';

export type BranchContext = {
  workingDir?: string;
  gitDir?: string;
};

export type GuardOptions = {
  resolveCurrentBranch?: (context?: BranchContext) => string;
  depth?: number;
};

const MAX_UNWRAP_DEPTH = 4;

const POSIX_SHELL_RUNNERS = new Set(['bash', 'sh', 'zsh']);
const WINDOWS_SHELL_RUNNERS = new Set(['cmd', 'powershell', 'pwsh']);

const FILE_READ_COMMANDS = new Set([
  'cat',
  'type',
  'get-content',
  'gc',
  'head',
  'tail',
  'less',
  'more',
  'grep',
  'rg',
  'select-string',
  'sed',
  'awk',
]);

const PROTECTED_SECRET_PATTERNS = [
  /(?:^|\/)deploy\/platform\/phase1\/runtime(?:\/|$)/iu,
  /(?:^|\/)deploy\/platform\/phase1\/environments\/[^/]+\.runtime\.env$/iu,
  /(?:^|\/)deploy\/platform\/phase1\/observability\/(?:.*\/)?(?:wecom-webhook-url|smtp\.env|smtp-password)$/iu,
];

const GIT_FLAGS_WITH_VALUE = new Set(['-C', '-c', '--git-dir', '--work-tree', '--namespace', '--super-prefix']);
const GIT_PUSH_FLAGS_WITH_VALUE = new Set(['--repo', '--receive-pack', '--exec', '-o', '--push-option']);
const WSL_FLAGS_WITH_VALUE = new Set(['-d', '--distribution', '-u', '--user', '--cd']);

const normalizeSlashes = (value: string): string => value.trim().replaceAll('\\', '/');

const normalizeCommandName = (token: string): string => {
  const base = basename(normalizeSlashes(token)).toLowerCase();
  return base.replace(/\.(?:exe|cmd|bat)$/iu, '');
};

export const isProtectedSecretPath = (rawPath: string): boolean => {
  const cleaned = normalizeSlashes(rawPath)
    .replace(/^[<>]+/u, '')
    .replace(/^['"]|['"]$/gu, '')
    .replace(/^\.\//u, '');
  if (!cleaned) return false;
  return PROTECTED_SECRET_PATTERNS.some((pattern) => pattern.test(cleaned));
};

export const tokenizeShellSegments = (commandLine: string): string[][] => {
  const segments: string[][] = [];
  let currentSegment: string[] = [];
  let currentToken = '';
  let hasToken = false;
  let quote: "'" | '"' | null = null;

  const pushToken = () => {
    if (hasToken) {
      currentSegment.push(currentToken);
      currentToken = '';
      hasToken = false;
    }
  };

  const pushSegment = () => {
    pushToken();
    if (currentSegment.length > 0) {
      segments.push(currentSegment);
      currentSegment = [];
    }
  };

  for (let i = 0; i < commandLine.length; i += 1) {
    const ch = commandLine[i];
    const next = commandLine[i + 1] ?? '';

    if (quote === "'") {
      if (ch === "'") {
        quote = null;
      } else {
        currentToken += ch;
      }
      continue;
    }

    if (quote === '"') {
      if (ch === '\\' && (next === '"' || next === '\\' || next === '$' || next === '`')) {
        currentToken += next;
        i += 1;
        continue;
      }
      if (ch === '"') {
        quote = null;
      } else {
        currentToken += ch;
      }
      continue;
    }

    if (ch === "'" || ch === '"') {
      quote = ch;
      hasToken = true;
      continue;
    }

    if (ch === ';' || ch === '\r' || ch === '\n') {
      pushSegment();
      continue;
    }

    if (ch === '|' && next === '|') {
      pushSegment();
      i += 1;
      continue;
    }

    if (ch === '|') {
      pushSegment();
      continue;
    }

    if (ch === '&' && next === '&') {
      pushSegment();
      i += 1;
      continue;
    }

    if (ch === '&') {
      const prev = commandLine[i - 1] ?? '';
      if (prev === '>' || prev === '<') {
        currentToken += ch;
        hasToken = true;
      } else {
        pushSegment();
      }
      continue;
    }

    if (/\s/u.test(ch)) {
      pushToken();
      continue;
    }

    currentToken += ch;
    hasToken = true;
  }

  pushSegment();
  return segments;
};

const isEnvAssignment = (token: string): boolean => /^[A-Za-z_][A-Za-z0-9_]*=.*/u.test(token);

const isShortFlagCluster = (token: string, letter: string): boolean =>
  /^-[a-zA-Z]+$/u.test(token) && token.includes(letter);

const splitFlagsAndPositionals = (args: string[], flagsWithValue: Set<string> = new Set()) => {
  const flags: string[] = [];
  const positionals: string[] = [];
  let afterDoubleDash = false;
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (!afterDoubleDash && arg === '--') {
      afterDoubleDash = true;
      continue;
    }
    if (!afterDoubleDash && arg.startsWith('-')) {
      flags.push(arg);
      if (flagsWithValue.has(arg)) {
        i += 1;
      }
      continue;
    }
    positionals.push(arg);
  }
  return { flags, positionals };
};

const extractGitSubcommand = (tokens: string[]): { subcommand: string; args: string[]; branchContext: BranchContext } | null => {
  if (tokens.length === 0 || normalizeCommandName(tokens[0]) !== 'git') {
    return null;
  }

  const branchContext: BranchContext = {};
  let index = 1;
  while (index < tokens.length) {
    const token = tokens[index];
    if (token === '--') {
      index += 1;
      break;
    }
    if (token === '-C' && index + 1 < tokens.length) {
      branchContext.workingDir = tokens[index + 1];
      index += 2;
      continue;
    }
    if (/^-C.+$/u.test(token)) {
      branchContext.workingDir = token.slice(2);
      index += 1;
      continue;
    }
    if (token === '--git-dir' && index + 1 < tokens.length) {
      branchContext.gitDir = tokens[index + 1];
      index += 2;
      continue;
    }
    if (token.startsWith('--git-dir=')) {
      branchContext.gitDir = token.slice('--git-dir='.length);
      index += 1;
      continue;
    }
    if (GIT_FLAGS_WITH_VALUE.has(token)) {
      index += 2;
      continue;
    }
    if (token.startsWith('--work-tree=') || token.startsWith('--namespace=') || token.startsWith('--super-prefix=') || /^-c.+$/u.test(token)) {
      index += 1;
      continue;
    }
    if (token.startsWith('-')) {
      index += 1;
      continue;
    }
    break;
  }

  if (index >= tokens.length) return null;
  return { subcommand: tokens[index], args: tokens.slice(index + 1), branchContext };
};

const refspecTargetsMain = (refspec: string, resolveBranch: () => string): boolean => {
  const cleaned = refspec.replace(/^\+/u, '');
  const colonIdx = cleaned.indexOf(':');
  const dest = colonIdx >= 0 ? cleaned.slice(colonIdx + 1) : cleaned;
  if (dest === 'main' || dest === 'master' || dest === 'refs/heads/main' || dest === 'refs/heads/master') {
    return true;
  }
  if (dest === 'HEAD' || dest === '@') {
    const branch = resolveBranch().trim();
    return branch === 'main' || branch === 'master';
  }
  return false;
};

const evaluateGitCommand = (tokens: string[], resolveCurrentBranch: (context?: BranchContext) => string): GuardVerdict => {
  const parsed = extractGitSubcommand(tokens);
  if (!parsed) return { action: 'allow' };

  const { subcommand, args, branchContext } = parsed;
  const resolveBranch = () => resolveCurrentBranch(branchContext);

  if (subcommand === 'push') {
    const { flags, positionals } = splitFlagsAndPositionals(args, GIT_PUSH_FLAGS_WITH_VALUE);
    if (flags.some((flag) => flag === '--force' || flag.startsWith('--force=') || isShortFlagCluster(flag, 'f'))) {
      return { action: 'deny', reason: 'git push --force / -f is blocked by repository safety guard.' };
    }

    const hasExplicitRepo = flags.some((flag) => flag === '--repo' || flag.startsWith('--repo='));
    const refspecs = hasExplicitRepo ? positionals : positionals.slice(1);
    if (refspecs.some((spec) => refspecTargetsMain(spec, resolveBranch))) {
      return { action: 'deny', reason: 'Direct push to main/master is blocked; open a PR instead.' };
    }
    if (refspecs.length === 0) {
      const branch = resolveBranch().trim();
      if (branch === 'main' || branch === 'master') {
        return { action: 'deny', reason: `Direct push while on ${branch} is blocked; push a feature branch and open a PR.` };
      }
    }
    return { action: 'allow' };
  }

  if (subcommand === 'reset' && args.includes('--hard')) {
    return { action: 'deny', reason: 'git reset --hard is blocked to prevent discarding work.' };
  }

  if (subcommand === 'clean') {
    const { flags } = splitFlagsAndPositionals(args);
    if (flags.some((flag) => flag === '--force' || isShortFlagCluster(flag, 'f'))) {
      return { action: 'deny', reason: 'git clean -f is blocked to protect untracked local directories.' };
    }
  }

  if (subcommand === 'checkout') {
    const { positionals } = splitFlagsAndPositionals(args);
    if (positionals.includes('.')) {
      return { action: 'deny', reason: 'git checkout . discards working tree changes and is blocked.' };
    }
  }

  if (subcommand === 'restore') {
    const { flags, positionals } = splitFlagsAndPositionals(args);
    const hasStaged = flags.some((flag) => flag === '--staged' || isShortFlagCluster(flag, 'S'));
    const hasWorktree = flags.some((flag) => flag === '--worktree' || isShortFlagCluster(flag, 'W'));
    if (positionals.includes('.') && (hasWorktree || !hasStaged)) {
      return { action: 'deny', reason: 'git restore . discards working tree changes and is blocked.' };
    }
  }

  if (subcommand === 'branch') {
    const { flags, positionals } = splitFlagsAndPositionals(args);
    const forceDelete = flags.some((flag) => isShortFlagCluster(flag, 'D'));
    const hasDelete = flags.some((flag) => flag === '--delete' || isShortFlagCluster(flag, 'd'));
    const hasForce = flags.some((flag) => flag === '--force' || isShortFlagCluster(flag, 'f'));
    if ((forceDelete || (hasDelete && hasForce)) && positionals.some((t) => t === 'main' || t === 'master')) {
      return { action: 'deny', reason: 'Deleting main/master branch with git branch -D is blocked.' };
    }
  }

  return { action: 'allow' };
};

const evaluateSegmentTokens = (
  rawTokens: string[],
  resolveCurrentBranch: (context?: BranchContext) => string,
  depth: number,
): GuardVerdict => {
  let start = 0;
  while (start < rawTokens.length && isEnvAssignment(rawTokens[start])) {
    start += 1;
  }
  const tokens = rawTokens.slice(start);
  if (tokens.length === 0) return { action: 'allow' };

  const cmd = normalizeCommandName(tokens[0]);

  if (cmd === 'wsl' && depth < MAX_UNWRAP_DEPTH) {
    let idx = 1;
    while (idx < tokens.length) {
      const token = tokens[idx];
      if (token === '-e' || token === '--exec') {
        idx += 1;
        break;
      }
      if (WSL_FLAGS_WITH_VALUE.has(token)) {
        idx += 2;
        continue;
      }
      if (token.startsWith('-')) {
        idx += 1;
        continue;
      }
      break;
    }
    if (idx < tokens.length) {
      return evaluateSegmentTokens(tokens.slice(idx), resolveCurrentBranch, depth);
    }
  }

  if (POSIX_SHELL_RUNNERS.has(cmd) && depth < MAX_UNWRAP_DEPTH) {
    for (let i = 1; i < tokens.length - 1; i += 1) {
      if (/^-[a-zA-Z]*c$/u.test(tokens[i])) {
        const verdict = evaluateCommandGuard(tokens[i + 1], { resolveCurrentBranch, depth: depth + 1 });
        if (verdict.action !== 'allow') return verdict;
        break;
      }
    }
  }

  if (WINDOWS_SHELL_RUNNERS.has(cmd) && depth < MAX_UNWRAP_DEPTH) {
    for (let i = 1; i < tokens.length - 1; i += 1) {
      const lowerFlag = tokens[i].toLowerCase();
      if (lowerFlag === '/c' || lowerFlag === '-command') {
        const rest = tokens.slice(i + 1);
        const verdict = rest.length === 1
          ? evaluateCommandGuard(rest[0], { resolveCurrentBranch, depth: depth + 1 })
          : evaluateSegmentTokens(rest, resolveCurrentBranch, depth + 1);
        if (verdict.action !== 'allow') return verdict;
        break;
      }
    }
  }

  if (cmd === 'git') {
    const gitVerdict = evaluateGitCommand(tokens, resolveCurrentBranch);
    if (gitVerdict.action !== 'allow') return gitVerdict;
  }

  if (FILE_READ_COMMANDS.has(cmd)) {
    for (const arg of tokens.slice(1)) {
      if (isProtectedSecretPath(arg)) {
        return {
          action: 'deny',
          reason: `Reading or printing protected secret path "${arg}" is blocked (reference by path only).`,
        };
      }
    }
  }

  if (cmd === 'gh') {
    const { positionals } = splitFlagsAndPositionals(tokens.slice(1), new Set(['-R', '--repo']));
    if (positionals[0] === 'workflow' && positionals[1] === 'run'
      && tokens.slice(1).some((arg) => arg.toLowerCase().includes('deploy-production'))) {
      return {
        action: 'ask',
        reason: 'Triggering production deployment workflow requires explicit user approval.',
      };
    }
  }

  if (cmd === 'npm' && tokens.includes('run') && tokens.includes('local:reset')) {
    return {
      action: 'ask',
      reason: 'Resetting local database volumes (npm run local:reset) requires explicit confirmation.',
    };
  }

  if (cmd === 'node') {
    const envIdx = tokens.findIndex((t) => normalizeSlashes(t).endsWith('local-environment.mjs'));
    if (envIdx >= 1 && tokens[envIdx + 1] === 'reset') {
      return {
        action: 'ask',
        reason: 'Resetting local database volumes (local-environment.mjs reset) requires explicit confirmation.',
      };
    }
  }

  const isDockerCompose = (cmd === 'docker' && tokens.includes('compose')) || cmd === 'docker-compose';
  if (isDockerCompose && tokens.includes('down')) {
    const hasVolumeFlag = tokens.some((t) => t === '--volumes' || isShortFlagCluster(t, 'v'));
    if (hasVolumeFlag) {
      return {
        action: 'ask',
        reason: 'Running docker compose down with volume removal (-v/--volumes) requires explicit confirmation.',
      };
    }
  }

  return { action: 'allow' };
};

const extractSubshellExpressions = (commandLine: string): string[] => {
  const expressions: string[] = [];
  let quote: "'" | '"' | null = null;
  for (let i = 0; i < commandLine.length; i += 1) {
    const ch = commandLine[i];
    const next = commandLine[i + 1] ?? '';
    if (quote === "'") {
      if (ch === "'") quote = null;
      continue;
    }
    if (quote === '"') {
      if (ch === '\\' && (next === '"' || next === '\\' || next === '$' || next === '`')) {
        i += 1;
        continue;
      }
      if (ch === '"') {
        quote = null;
        continue;
      }
    } else if (ch === "'" || ch === '"') {
      quote = ch;
      continue;
    }

    if (ch === '$' && next === '(') {
      let nested = 1;
      let j = i + 2;
      while (j < commandLine.length && nested > 0) {
        if (commandLine[j] === '(') nested += 1;
        else if (commandLine[j] === ')') nested -= 1;
        j += 1;
      }
      if (nested === 0) {
        expressions.push(commandLine.slice(i + 2, j - 1));
      }
      i = j - 1;
    } else if (ch === '`') {
      const end = commandLine.indexOf('`', i + 1);
      if (end > i + 1) {
        expressions.push(commandLine.slice(i + 1, end));
        i = end;
      }
    }
  }
  return expressions;
};

export const evaluateCommandGuard = (commandLine: string, options: GuardOptions = {}): GuardVerdict => {
  const depth = options.depth ?? 0;
  if (depth > MAX_UNWRAP_DEPTH || !commandLine.trim()) {
    return { action: 'allow' };
  }

  const resolveCurrentBranch = options.resolveCurrentBranch ?? (() => '');
  let pendingAsk: GuardVerdict | null = null;

  for (const segment of tokenizeShellSegments(commandLine)) {
    const verdict = evaluateSegmentTokens(segment, resolveCurrentBranch, depth);
    if (verdict.action === 'deny') return verdict;
    if (verdict.action === 'ask' && !pendingAsk) pendingAsk = verdict;
  }

  for (const nested of extractSubshellExpressions(commandLine)) {
    const verdict = evaluateCommandGuard(nested, { resolveCurrentBranch, depth: depth + 1 });
    if (verdict.action === 'deny') return verdict;
    if (verdict.action === 'ask' && !pendingAsk) pendingAsk = verdict;
  }

  return pendingAsk ?? { action: 'allow' };
};

const isPlainObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const readStringProperty = (obj: unknown, keys: string[]): string => {
  if (!isPlainObject(obj)) return '';
  for (const key of keys) {
    const value = obj[key];
    if (typeof value === 'string') return value;
  }
  return '';
};

const checkSecretReadTarget = (argsObj: unknown, keys: string[]): GuardVerdict => {
  const targetPath = readStringProperty(argsObj, keys);
  if (isProtectedSecretPath(targetPath)) {
    return {
      action: 'deny',
      reason: `Reading protected secret path "${targetPath}" is blocked (reference by path only).`,
    };
  }
  return { action: 'allow' };
};

export const evaluatePreToolPayload = (
  payload: unknown,
  runtime: GuardRuntime,
  options: GuardOptions = {},
): GuardVerdict => {
  if (!isPlainObject(payload)) return { action: 'allow' };

  if (runtime === 'agy') {
    const toolCall = payload.toolCall;
    if (!isPlainObject(toolCall)) return { action: 'allow' };
    const toolName = readStringProperty(toolCall, ['name']).toLowerCase();
    const args = toolCall.args;

    if (toolName === 'read_file' || toolName === 'view_file') {
      return checkSecretReadTarget(args, ['Path', 'AbsolutePath', 'path']);
    }
    if (toolName === 'run_command') {
      return evaluateCommandGuard(readStringProperty(args, ['CommandLine', 'command']), options);
    }
    return { action: 'allow' };
  }

  const toolName = readStringProperty(payload, ['tool_name']).toLowerCase();
  const toolInput = payload.tool_input;

  if (toolName === 'read') {
    return checkSecretReadTarget(toolInput, ['file_path', 'path']);
  }
  if (toolName === 'bash' || toolName === 'powershell') {
    return evaluateCommandGuard(readStringProperty(toolInput, ['command']), options);
  }

  return { action: 'allow' };
};

const defaultResolveCurrentBranch = (context?: BranchContext): string => {
  try {
    const gitArgs: string[] = [];
    if (context?.workingDir) gitArgs.push('-C', context.workingDir);
    if (context?.gitDir) gitArgs.push(`--git-dir=${context.gitDir}`);
    gitArgs.push('branch', '--show-current');
    return execFileSync('git', gitArgs, {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
      windowsHide: true,
    }).trim();
  } catch {
    return '';
  }
};

const parseRuntimeArg = (argv: string[]): GuardRuntime => {
  const arg = argv.find((item) => item.startsWith('--runtime='));
  return arg?.slice('--runtime='.length) === 'claude' ? 'claude' : 'agy';
};

const runCli = () => {
  const runtime = parseRuntimeArg(process.argv.slice(2));
  let rawInput = '';
  try {
    rawInput = readFileSync(0, 'utf8');
  } catch {
    rawInput = '';
  }

  let payload: unknown = null;
  if (rawInput.trim()) {
    try {
      payload = JSON.parse(rawInput);
    } catch {
      payload = null;
    }
  }

  const verdict = evaluatePreToolPayload(payload, runtime, {
    resolveCurrentBranch: defaultResolveCurrentBranch,
  });

  if (runtime === 'agy') {
    if (verdict.action === 'allow') {
      process.stdout.write(`${JSON.stringify({ decision: 'allow' })}\n`);
    } else if (verdict.action === 'deny') {
      process.stdout.write(`${JSON.stringify({ decision: 'deny', reason: verdict.reason })}\n`);
    } else {
      process.stdout.write(`${JSON.stringify({ decision: 'force_ask', reason: verdict.reason })}\n`);
    }
    return;
  }

  if (verdict.action === 'deny') {
    process.stderr.write(`BLOCKED by scripts/hooks/pre-tool-guard.ts: ${verdict.reason}\n`);
    process.exitCode = 2;
    return;
  }

  if (verdict.action === 'ask') {
    process.stdout.write(
      `${JSON.stringify({
        hookSpecificOutput: {
          hookEventName: 'PreToolUse',
          permissionDecision: 'ask',
          permissionDecisionReason: verdict.reason,
        },
      })}\n`,
    );
  }
};

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (invokedPath === import.meta.url) {
  runCli();
}
