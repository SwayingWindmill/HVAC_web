import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { basename, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export type GuardVerdict =
  | { action: 'allow' }
  | { action: 'deny'; reason: string }
  | { action: 'ask'; reason: string };

export type GuardRuntime = 'agy' | 'claude';

export type GuardOptions = {
  resolveCurrentBranch?: () => string;
  depth?: number;
};

const MAX_UNWRAP_DEPTH = 4;

const SUBSHELL_RUNNERS = new Set([
  'wsl',
  'bash',
  'sh',
  'zsh',
  'cmd',
  'powershell',
  'pwsh',
]);

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
  const cleaned = normalizeSlashes(rawPath).replace(/^['"]|['"]$/gu, '').replace(/^\.\//u, '');
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

const extractGitSubcommand = (tokens: string[]): { subcommand: string; args: string[] } | null => {
  if (tokens.length === 0 || normalizeCommandName(tokens[0]) !== 'git') {
    return null;
  }

  let index = 1;
  while (index < tokens.length) {
    const token = tokens[index];
    if (token === '--') {
      index += 1;
      break;
    }
    if (GIT_FLAGS_WITH_VALUE.has(token)) {
      index += 2;
      continue;
    }
    if (token.startsWith('--git-dir=') || token.startsWith('--work-tree=') || token.startsWith('--namespace=') || token.startsWith('--super-prefix=')) {
      index += 1;
      continue;
    }
    if (/^-[Cc].+$/u.test(token)) {
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
  return { subcommand: tokens[index], args: tokens.slice(index + 1) };
};

const refspecTargetsMain = (refspec: string): boolean => {
  const cleaned = refspec.replace(/^\+/u, '');
  const colonIdx = cleaned.indexOf(':');
  const dest = colonIdx >= 0 ? cleaned.slice(colonIdx + 1) : cleaned;
  return dest === 'main' || dest === 'master' || dest === 'refs/heads/main' || dest === 'refs/heads/master';
};

const evaluateGitCommand = (tokens: string[], resolveCurrentBranch: () => string): GuardVerdict => {
  const parsed = extractGitSubcommand(tokens);
  if (!parsed) return { action: 'allow' };

  const { subcommand, args } = parsed;

  if (subcommand === 'push') {
    const positional: string[] = [];
    let afterDoubleDash = false;
    for (let i = 0; i < args.length; i += 1) {
      const arg = args[i];
      if (!afterDoubleDash && arg === '--') {
        afterDoubleDash = true;
        continue;
      }
      if (!afterDoubleDash) {
        if (arg === '--force' || arg.startsWith('--force=') || isShortFlagCluster(arg, 'f')) {
          return { action: 'deny', reason: 'git push --force / -f is blocked by repository safety guard.' };
        }
        if (GIT_PUSH_FLAGS_WITH_VALUE.has(arg)) {
          i += 1;
          continue;
        }
        if (arg.startsWith('-')) {
          continue;
        }
      }
      positional.push(arg);
    }

    if (positional.slice(1).some(refspecTargetsMain)) {
      return { action: 'deny', reason: 'Direct push to main/master is blocked; open a PR instead.' };
    }
    if (positional.length < 2) {
      const branch = resolveCurrentBranch().trim();
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
    for (const arg of args) {
      if (arg === '--') break;
      if (arg === '--force' || isShortFlagCluster(arg, 'f')) {
        return { action: 'deny', reason: 'git clean -f is blocked to protect untracked local directories.' };
      }
    }
  }

  if (subcommand === 'checkout') {
    let seenDash = false;
    for (const arg of args) {
      if (arg === '--') {
        seenDash = true;
        continue;
      }
      if ((seenDash || !arg.startsWith('-')) && arg === '.') {
        return { action: 'deny', reason: 'git checkout . discards working tree changes and is blocked.' };
      }
    }
  }

  if (subcommand === 'restore') {
    let seenDash = false;
    let hasStaged = false;
    let hasWorktree = false;
    let restoresDot = false;
    for (const arg of args) {
      if (!seenDash && arg === '--') {
        seenDash = true;
        continue;
      }
      if (!seenDash && (arg === '--staged' || isShortFlagCluster(arg, 'S'))) {
        hasStaged = true;
        continue;
      }
      if (!seenDash && (arg === '--worktree' || isShortFlagCluster(arg, 'W'))) {
        hasWorktree = true;
        continue;
      }
      if ((seenDash || !arg.startsWith('-')) && arg === '.') {
        restoresDot = true;
      }
    }
    if (restoresDot && (hasWorktree || !hasStaged)) {
      return { action: 'deny', reason: 'git restore . discards working tree changes and is blocked.' };
    }
  }

  if (subcommand === 'branch') {
    let forceDelete = false;
    let hasDelete = false;
    let hasForce = false;
    const targets: string[] = [];
    let seenDash = false;
    for (const arg of args) {
      if (!seenDash && arg === '--') {
        seenDash = true;
        continue;
      }
      if (!seenDash && arg.startsWith('-')) {
        if (isShortFlagCluster(arg, 'D')) forceDelete = true;
        if (arg === '--delete' || isShortFlagCluster(arg, 'd')) hasDelete = true;
        if (arg === '--force' || isShortFlagCluster(arg, 'f')) hasForce = true;
        continue;
      }
      targets.push(arg);
    }
    if ((forceDelete || (hasDelete && hasForce)) && targets.some((t) => t === 'main' || t === 'master')) {
      return { action: 'deny', reason: 'Deleting main/master branch with git branch -D is blocked.' };
    }
  }

  return { action: 'allow' };
};

const evaluateSegmentTokens = (rawTokens: string[], resolveCurrentBranch: () => string, depth: number): GuardVerdict => {
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

  if (SUBSHELL_RUNNERS.has(cmd) && depth < MAX_UNWRAP_DEPTH) {
    for (let i = 1; i < tokens.length - 1; i += 1) {
      const flag = tokens[i];
      if (flag === '-c' || flag === '-lc' || flag === '/c' || flag === '/C' || flag === '-Command' || flag === '-command') {
        const inner = flag.toLowerCase() === '/c' || flag.toLowerCase() === '-command'
          ? tokens.slice(i + 1).join(' ')
          : tokens[i + 1];
        const verdict = evaluateCommandGuard(inner, { resolveCurrentBranch, depth: depth + 1 });
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

  if (cmd === 'gh' && tokens[1] === 'workflow' && tokens[2] === 'run') {
    if (tokens.slice(3).some((arg) => arg.toLowerCase().includes('deploy-production'))) {
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

  const isDockerCompose = (cmd === 'docker' && tokens[1] === 'compose') || cmd === 'docker-compose';
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

export const evaluateCommandGuard = (commandLine: string, options: GuardOptions = {}): GuardVerdict => {
  const depth = options.depth ?? 0;
  if (depth > MAX_UNWRAP_DEPTH || !commandLine.trim()) {
    return { action: 'allow' };
  }

  const resolveCurrentBranch = options.resolveCurrentBranch ?? (() => '');
  const segments = tokenizeShellSegments(commandLine);
  let pendingAsk: GuardVerdict | null = null;

  for (const segment of segments) {
    const verdict = evaluateSegmentTokens(segment, resolveCurrentBranch, depth);
    if (verdict.action === 'deny') return verdict;
    if (verdict.action === 'ask' && !pendingAsk) pendingAsk = verdict;
  }

  return pendingAsk ?? { action: 'allow' };
};

const asRecord = (value: unknown): Record<string, unknown> | null =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;

const stringProp = (record: Record<string, unknown> | null, keys: string[]): string => {
  if (!record) return '';
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'string') return value;
  }
  return '';
};

export const evaluatePreToolPayload = (
  payload: unknown,
  runtime: GuardRuntime,
  options: GuardOptions = {},
): GuardVerdict => {
  const root = asRecord(payload);
  if (!root) return { action: 'allow' };

  if (runtime === 'agy') {
    const toolCall = asRecord(root.toolCall);
    const toolName = stringProp(toolCall, ['name']).toLowerCase();
    const args = asRecord(toolCall?.args);

    if (toolName === 'read_file' || toolName === 'view_file') {
      const targetPath = stringProp(args, ['Path', 'AbsolutePath', 'path']);
      if (isProtectedSecretPath(targetPath)) {
        return {
          action: 'deny',
          reason: `Reading protected secret path "${targetPath}" is blocked (reference by path only).`,
        };
      }
      return { action: 'allow' };
    }

    if (toolName === 'run_command') {
      const cmd = stringProp(args, ['CommandLine', 'command']);
      return evaluateCommandGuard(cmd, options);
    }

    return { action: 'allow' };
  }

  const toolName = stringProp(root, ['tool_name']).toLowerCase();
  const toolInput = asRecord(root.tool_input);

  if (toolName === 'read') {
    const targetPath = stringProp(toolInput, ['file_path', 'path']);
    if (isProtectedSecretPath(targetPath)) {
      return {
        action: 'deny',
        reason: `Reading protected secret path "${targetPath}" is blocked (reference by path only).`,
      };
    }
    return { action: 'allow' };
  }

  if (toolName === 'bash' || toolName === 'powershell') {
    const cmd = stringProp(toolInput, ['command']);
    return evaluateCommandGuard(cmd, options);
  }

  return { action: 'allow' };
};

const defaultResolveCurrentBranch = (): string => {
  try {
    return execFileSync('git', ['branch', '--show-current'], {
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
  const value = arg?.slice('--runtime='.length);
  return value === 'claude' ? 'claude' : 'agy';
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
