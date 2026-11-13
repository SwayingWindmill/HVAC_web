import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { readGoWorkspace } from './lib/go-workspace.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const maximumInlineCommands = 4;
export const maximumHandoffBytes = 28 * 1024;
const longChainBaselinePath = 'scripts/package-script-long-chain-baseline.json';
const javascriptToolingBaselinePath = 'scripts/javascript-tooling-baseline.json';
const requiredLinguistExclusions = Object.freeze([
  'scripts/** -linguist-detectable',
  'services/operations-agent-service/test/** -linguist-detectable',
  '.agents/** -linguist-detectable',
]);

const forbiddenTrackedPrefixes = Object.freeze([
  '.ai-bridge/',
  '.audit/',
  '.clones/',
  '.codegraph/',
  '.workbuddy/',
  '.worktrees/',
  'hvac-backend/logs/',
  'out/',
  'prototype/',
]);

const forbiddenTrackedSuffixes = Object.freeze([
  '.bak',
  '.log',
  '.orig',
  '.swp',
  '.tmp',
]);

const forbiddenTrackedBasenames = new Set(['.DS_Store', 'Thumbs.db']);

const normalizePath = (value) => value.split(sep).join('/').replaceAll('\\', '/');
const hashScriptCommand = (command) => createHash('sha256').update(command).digest('hex');
const countInlineCommands = (command) => command.split(/\s*&&\s*/u).filter(Boolean).length;

type JavaScriptToolingFile = { path: string; bytes: number };
type JavaScriptRootBaseline = { fileCount: number; maxBytes: number };
type JavaScriptToolingBaseline = {
  schemaVersion: number;
  policy: string;
  trackedExtensions: string[];
  allowedLegacyRoots: Record<string, JavaScriptRootBaseline>;
  fileCount: number;
  maxBytes: number;
};

export const createPackageScriptLongChainBaseline = (scripts) => ({
  schemaVersion: 1,
  maximumInlineCommands,
  scripts: Object.fromEntries(
    Object.entries(scripts)
      .filter(([, command]) => countInlineCommands(command) > maximumInlineCommands)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([name, command]) => [name, {
        sha256: hashScriptCommand(command),
        reason: 'Legacy inline orchestration; migrate to capabilityTaskMatrix before changing.',
      }]),
  ),
});

export const findPackageScriptViolations = ({ scripts, baseline }) => {
  const violations = [];
  if (baseline?.schemaVersion !== 1 || baseline?.maximumInlineCommands !== maximumInlineCommands
    || !baseline.scripts || typeof baseline.scripts !== 'object') {
    return [`${longChainBaselinePath}: invalid or unsupported baseline schema`];
  }

  const historicalStageScripts = Object.keys(scripts).filter((name) => /^s\d+:/u.test(name));
  for (const name of historicalStageScripts) {
    violations.push(`package.json: historical stage task \`${name}\` must be expressed through a stable domain task`);
  }

  for (const [name, command] of Object.entries(scripts)) {
    const commandCount = countInlineCommands(command);
    if (commandCount <= maximumInlineCommands) continue;
    const baselineEntry = baseline.scripts[name];
    if (!baselineEntry) {
      violations.push(`package.json: script \`${name}\` contains ${commandCount} inline commands; migrate it or add a reviewed baseline entry`);
      continue;
    }
    if (baselineEntry.mode !== 'explicit-operation' && baselineEntry.sha256 !== hashScriptCommand(command)) {
      violations.push(`package.json: long-chain script \`${name}\` changed; migrate it instead of updating inline orchestration`);
    }
  }

  for (const name of Object.keys(baseline.scripts)) {
    const command = scripts[name];
    if (!command || countInlineCommands(command) <= maximumInlineCommands) {
      violations.push(`${longChainBaselinePath}: stale exemption for \`${name}\``);
    }
  }

  return violations;
};

export const findTrackedArtifactViolations = (trackedFiles) => {
  const violations = [];
  for (const rawPath of trackedFiles) {
    const path = normalizePath(rawPath);
    const basename = path.slice(path.lastIndexOf('/') + 1);
    if (forbiddenTrackedPrefixes.some((prefix) => path.startsWith(prefix))) {
      violations.push(`${path}: generated, local coordination, or archived runtime content is tracked`);
      continue;
    }
    if (forbiddenTrackedBasenames.has(basename)
      || forbiddenTrackedSuffixes.some((suffix) => basename.endsWith(suffix))) {
      violations.push(`${path}: transient file type is tracked`);
    }
  }
  return violations;
};

export const findJavascriptToolingViolations = ({ files, baseline }: { files: JavaScriptToolingFile[]; baseline: JavaScriptToolingBaseline }) => {
  if (baseline?.schemaVersion !== 1 || baseline?.policy !== 'legacy-js-ratchet'
    || !Array.isArray(baseline.trackedExtensions)
    || !baseline.allowedLegacyRoots || typeof baseline.allowedLegacyRoots !== 'object'
    || !Number.isInteger(baseline.fileCount) || !Number.isInteger(baseline.maxBytes)) {
    return [`${javascriptToolingBaselinePath}: invalid or unsupported baseline schema`];
  }

  const normalizedFiles = files
    .map(({ path, bytes }) => ({ path: normalizePath(path), bytes }))
    .filter(({ path }) => baseline.trackedExtensions.some((extension) => path.endsWith(extension)))
    .sort((left, right) => left.path.localeCompare(right.path));
  const violations = [];
  const roots = Object.keys(baseline.allowedLegacyRoots);
  const rootTotals = Object.fromEntries(roots.map((root) => [root, { fileCount: 0, bytes: 0 }]));

  for (const file of normalizedFiles) {
    const root = roots.find((candidate) => file.path.startsWith(candidate));
    if (!root) {
      violations.push(`${file.path}: JavaScript is outside reviewed legacy tooling roots; use TypeScript for new repository or product code`);
      continue;
    }
    rootTotals[root].fileCount += 1;
    rootTotals[root].bytes += file.bytes;
  }

  const totalBytes = normalizedFiles.reduce((sum, file) => sum + file.bytes, 0);
  if (normalizedFiles.length > baseline.fileCount) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript file count grew beyond ${baseline.fileCount} to ${normalizedFiles.length}; migrate tooling to TypeScript instead of growing legacy JavaScript`);
  }
  if (totalBytes > baseline.maxBytes) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript bytes grew from ${baseline.maxBytes} to ${totalBytes}; migrate tooling to TypeScript instead of growing legacy JavaScript`);
  }

  for (const [root, expected] of Object.entries(baseline.allowedLegacyRoots)) {
    const actual = rootTotals[root];
    if (!actual) continue;
    if (actual.fileCount > expected.fileCount) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript file count grew beyond ${expected.fileCount} to ${actual.fileCount}`);
    }
    if (actual.bytes > expected.maxBytes) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript bytes grew from ${expected.maxBytes} to ${actual.bytes}`);
    }
  }

  return violations;
};

export const findLinguistViolations = (gitattributes) => requiredLinguistExclusions
  .filter((entry) => !gitattributes.includes(entry))
  .map((entry) => `.gitattributes: missing ancillary Linguist exclusion \`${entry}\``);

export const findWorkflowViolations = (workflow) => {
  const required = 'node scripts/run-pr-gate.mjs --gate=static';
  return workflow.includes(`- run: ${required}`)
    ? []
    : [`.github/workflows/pr-gates.yml: missing canonical static gate \`${required}\``];
};

const requiredGuardrailFiles = Object.freeze([
  '.agents/hooks.json',
  '.claude/settings.json',
  'scripts/hooks/pre-tool-guard.ts',
]);

const allowedAgentsSkillRootFiles = new Set(['MATTPOCOCK-LICENSE.txt', 'PSTACK-LICENSE.txt']);
const allowedClaudeSkillRootFiles = new Set(['README.md']);

export const findRequiredGuardrailFileViolations = (trackedFiles: string[]) => {
  const trackedSet = new Set(trackedFiles.map(normalizePath));
  return requiredGuardrailFiles
    .filter((path) => !trackedSet.has(path))
    .map((path) => `${path}: required repository safety hook file is missing from git tracking`);
};

const collectSkillMirrorEntries = ({
  trackedFiles,
  prefix,
  allowedRootFiles,
  violations,
}: {
  trackedFiles: string[];
  prefix: string;
  allowedRootFiles: Set<string>;
  violations: string[];
}) => {
  const entries = new Map<string, string>();
  for (const rawPath of trackedFiles) {
    const path = normalizePath(rawPath);
    if (!path.startsWith(prefix)) continue;
    const rel = path.slice(prefix.length);
    if (!rel.includes('/')) {
      if (!allowedRootFiles.has(rel)) {
        violations.push(`${path}: unexpected root file under ${prefix}`);
      }
      continue;
    }
    entries.set(rel, path);
  }
  return entries;
};

export const findSkillMirrorViolations = ({
  root,
  trackedFiles,
}: {
  root: string;
  trackedFiles: string[];
}) => {
  const violations: string[] = [];
  const agentsSkillsPrefix = '.agents/skills/';
  const claudeSkillsPrefix = '.claude/skills/';
  const agentsSkillFiles = collectSkillMirrorEntries({
    trackedFiles,
    prefix: agentsSkillsPrefix,
    allowedRootFiles: allowedAgentsSkillRootFiles,
    violations,
  });
  const claudeSkillFiles = collectSkillMirrorEntries({
    trackedFiles,
    prefix: claudeSkillsPrefix,
    allowedRootFiles: allowedClaudeSkillRootFiles,
    violations,
  });

  for (const [rel, agentsPath] of [...agentsSkillFiles.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    if (!claudeSkillFiles.has(rel)) {
      violations.push(`${agentsPath}: missing mirrored counterpart at ${claudeSkillsPrefix}${rel}; mirror both skill directories 1:1`);
    }
  }

  for (const [rel, claudePath] of [...claudeSkillFiles.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    if (!agentsSkillFiles.has(rel)) {
      violations.push(`${claudePath}: missing mirrored counterpart at ${agentsSkillsPrefix}${rel}; mirror both skill directories 1:1`);
    }
  }

  for (const [rel, agentsPath] of [...agentsSkillFiles.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    const claudePath = claudeSkillFiles.get(rel);
    if (!claudePath) continue;
    const agentsAbs = join(root, agentsPath);
    const claudeAbs = join(root, claudePath);
    if (!existsSync(agentsAbs)) {
      violations.push(`${agentsPath}: tracked skill file is missing on disk`);
      continue;
    }
    if (!existsSync(claudeAbs)) {
      violations.push(`${claudePath}: tracked skill file is missing on disk`);
      continue;
    }
    if (Buffer.compare(readFileSync(agentsAbs), readFileSync(claudeAbs)) !== 0) {
      violations.push(`${agentsPath}: byte content drifted from ${claudePath}; sync both skill files verbatim`);
    }
  }

  return violations;
};

export const findHandoffBudgetViolations = ({
  bytes,
  maxBytes = maximumHandoffBytes,
}: {
  bytes: number;
  maxBytes?: number;
}) => (bytes <= maxBytes
  ? []
  : [
    `docs/HANDOFF.md: ${bytes} bytes exceeds ${maxBytes}-byte single-read budget; keep only current state, next steps, and live environment deviations in docs/HANDOFF.md, move runbooks to docs/operations/, and encode recurring rules in checks (/correct)`,
  ]);

const resolveGitListFilesInvocation = (root) => ({
  gitExecutable: 'git',
  args: ['-C', root, 'ls-files', '-z'],
});

const listTrackedFiles = (root) => {
  const { gitExecutable, args } = resolveGitListFilesInvocation(root);
  const output = execFileSync(gitExecutable, args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  return output.split('\0').filter(Boolean);
};

const listTrackedJavaScriptFiles = (root, trackedFiles, extensions) => trackedFiles
  .map(normalizePath)
  .filter((path) => extensions.some((extension) => path.endsWith(extension)))
  .filter((path) => existsSync(join(root, path)))
  .map((path) => ({
    path,
    bytes: readFileSync(join(root, path)).byteLength,
  }));

const collectJavascriptToolingViolations = (root, trackedFiles = listTrackedFiles(root)) => {
  const baseline = JSON.parse(readFileSync(join(root, javascriptToolingBaselinePath), 'utf8'));
  return [
    ...findJavascriptToolingViolations({
      files: listTrackedJavaScriptFiles(root, trackedFiles, baseline.trackedExtensions ?? []),
      baseline,
    }),
    ...findLinguistViolations(readFileSync(join(root, '.gitattributes'), 'utf8')),
  ];
};

// Go sources are formatted by the gofmt from the Go version in go.work. PATH gofmt can be an older
// system Go than the toolchain `go` runs. Go 1.27 reformats a multi-value return of composite
// literals, so the two disagree. `go env GOROOT` with GOTOOLCHAIN set to the go.work version is
// the same gofmt CI's setup-go puts on PATH.
const workspaceGofmt = (root) => {
  const { goVersion } = readGoWorkspace(root);
  const goroot = execFileSync('go', ['env', 'GOROOT'], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, GOTOOLCHAIN: `go${goVersion}` },
  }).trim();
  const binary = join(goroot, 'bin', 'gofmt');
  if (!existsSync(binary)) {
    throw new Error(`gofmt for go${goVersion} is not at ${binary}`);
  }
  return binary;
};

const findGoFormatViolations = (root, trackedFiles) => {
  const goFiles = trackedFiles.map(normalizePath).filter((path) => path.endsWith('.go') && existsSync(join(root, path)));
  if (goFiles.length === 0) return [];
  const gofmt = workspaceGofmt(root);
  const unformatted = execFileSync(gofmt, ['-l', ...goFiles], { cwd: root, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 })
    .split('\n')
    .filter(Boolean);
  return unformatted.map((path) => `${path}: not gofmt-formatted (run ${gofmt} -w ${path})`);
};

export const checkRepositoryGovernance = (root = repositoryRoot) => {
  const packageJson = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  const longChainBaseline = JSON.parse(readFileSync(join(root, longChainBaselinePath), 'utf8'));
  const trackedFiles = listTrackedFiles(root);
  const violations = [
    ...findTrackedArtifactViolations(trackedFiles),
    ...findRequiredGuardrailFileViolations(trackedFiles),
    ...findSkillMirrorViolations({ root, trackedFiles }),
    ...collectJavascriptToolingViolations(root, trackedFiles),
    ...findPackageScriptViolations({
      scripts: packageJson.scripts ?? {},
      baseline: longChainBaseline,
    }),
    ...findWorkflowViolations(readFileSync(join(root, '.github/workflows/pr-gates.yml'), 'utf8')),
    ...findHandoffBudgetViolations({ bytes: readFileSync(join(root, 'docs/HANDOFF.md')).byteLength }),
    ...findGoFormatViolations(root, trackedFiles),
  ];

  if (violations.length > 0) {
    throw new Error(`Repository governance check failed:\n- ${violations.join('\n- ')}`);
  }
};

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (invokedPath === import.meta.url) {
  try {
    checkRepositoryGovernance();
    console.log('Repository governance check passed.');
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
}
