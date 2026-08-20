import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { capabilityTaskMatrix } from './domain-task-matrix.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const maximumInlineCommands = 4;
const longChainBaselinePath = 'scripts/package-script-long-chain-baseline.json';
const javascriptToolingBaselinePath = 'scripts/javascript-tooling-baseline.json';
const requiredLinguistExclusions = Object.freeze([
  'scripts/** -linguist-detectable',
  'benchmarks/** -linguist-detectable',
  'pocs/** -linguist-detectable',
  'services/operations-agent-service/test/** -linguist-detectable',
  '.agents/** -linguist-detectable',
]);

const forbiddenTrackedPrefixes = Object.freeze([
  '.ai-bridge/',
  '.clones/',
  '.codegraph/',
  '.workbuddy/',
  'hvac-backend/logs/',
  'out/',
  'prototype/',
]);

// `.worktrees/` is intentionally deferred. The directory remains excluded by
// `.gitignore`, but is not a failing governance rule until its local checkout is moved.
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
const hashPathSet = (paths) => createHash('sha256').update(`${[...paths].sort().join('\n')}\n`).digest('hex');

type JavaScriptToolingFile = { path: string; bytes: number };
type JavaScriptRootBaseline = { fileCount: number; maxBytes: number };
type JavaScriptToolingBaseline = {
  schemaVersion: number;
  policy: string;
  trackedExtensions: string[];
  allowedLegacyRoots: Record<string, JavaScriptRootBaseline>;
  fileCount: number;
  maxBytes: number;
  pathSetSha256: string;
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

export const findPackageScriptViolations = ({ scripts, baseline, capabilityTasks = capabilityTaskMatrix }) => {
  const violations = [];
  if (baseline?.schemaVersion !== 1 || baseline?.maximumInlineCommands !== maximumInlineCommands
    || !baseline.scripts || typeof baseline.scripts !== 'object') {
    return [`${longChainBaselinePath}: invalid or unsupported baseline schema`];
  }

  for (const task of Object.keys(capabilityTasks)) {
    const expected = `node scripts/run-capability-task.mjs --task=${task}`;
    if (scripts[task] !== expected) {
      violations.push(`package.json: capability task \`${task}\` must delegate to \`${expected}\``);
    }
    if (Object.hasOwn(baseline.scripts, task)) {
      violations.push(`${longChainBaselinePath}: migrated capability task \`${task}\` must not remain exempted`);
    }
  }

  for (const [name, command] of Object.entries(scripts)) {
    const commandCount = countInlineCommands(command);
    if (commandCount <= maximumInlineCommands) continue;
    const baselineEntry = baseline.scripts[name];
    if (!baselineEntry) {
      violations.push(`package.json: script \`${name}\` contains ${commandCount} inline commands; migrate it or add a reviewed baseline entry`);
      continue;
    }
    if (baselineEntry.sha256 !== hashScriptCommand(command)) {
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
    || !Number.isInteger(baseline.fileCount) || !Number.isInteger(baseline.maxBytes)
    || typeof baseline.pathSetSha256 !== 'string') {
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

  const paths = normalizedFiles.map(({ path }) => path);
  const totalBytes = normalizedFiles.reduce((sum, file) => sum + file.bytes, 0);
  if (paths.length !== baseline.fileCount) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript file count changed from ${baseline.fileCount} to ${paths.length}; migrations must ratchet the reviewed baseline`);
  }
  if (hashPathSet(paths) !== baseline.pathSetSha256) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript path set changed; new .js/.mjs/.cjs/.jsx files are forbidden and migrations must update the reviewed ratchet`);
  }
  if (totalBytes > baseline.maxBytes) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript bytes grew from ${baseline.maxBytes} to ${totalBytes}; migrate tooling to TypeScript instead of growing legacy JavaScript`);
  }

  for (const [root, expected] of Object.entries(baseline.allowedLegacyRoots)) {
    const actual = rootTotals[root];
    if (!actual) continue;
    if (actual.fileCount !== expected.fileCount) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript file count changed from ${expected.fileCount} to ${actual.fileCount}`);
    }
    if (actual.bytes > expected.maxBytes) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript bytes grew from ${expected.maxBytes} to ${actual.bytes}`);
    }
  }

  return violations;
};

export const findDocumentationViolations = ({
  serviceNames,
  rootReadme,
  appReadme,
  reactVersion,
}) => {
  const violations = [];
  for (const serviceName of serviceNames) {
    if (!rootReadme.includes(`\`${serviceName}/\``)) {
      violations.push(`README.md: missing service catalog entry \`${serviceName}/\``);
    }
  }

  for (const command of ['npm run dev:demo', 'npm run dev:real']) {
    if (!rootReadme.includes(command)) {
      violations.push(`README.md: missing runtime command \`${command}\``);
    }
  }

  const reactMajor = String(reactVersion).match(/\d+/)?.[0];
  if (reactMajor && !appReadme.includes(`React ${reactMajor}`)) {
    violations.push(`apps/hvac-web/README.md: expected React ${reactMajor} runtime documentation`);
  }

  return violations;
};

export const findLinguistViolations = (gitattributes) => requiredLinguistExclusions
  .filter((entry) => !gitattributes.includes(entry))
  .map((entry) => `.gitattributes: missing ancillary Linguist exclusion \`${entry}\``);

export const findWorkflowViolations = (workflow) => {
  const violations = [];
  for (const command of [
    'npm run --silent repo:check',
  ]) {
    if (!workflow.includes(`- run: ${command}`)) {
      violations.push(`.github/workflows/pr-gates.yml: missing static gate \`${command}\``);
    }
  }
  return violations;
};

const normalizeGitDirForPlatform = (gitDir) => {
  if (process.platform !== 'win32') return gitDir;
  const match = gitDir.match(/^\/mnt\/([a-zA-Z])\/(.*)$/u);
  if (!match) return gitDir;
  return `${match[1].toUpperCase()}:\\${match[2].replaceAll('/', '\\')}`;
};

const resolveGitListFilesInvocation = (root) => {
  const gitExecutable = process.platform === 'win32' ? 'git.exe' : 'git';
  const fallbackArgs = ['-C', root, 'ls-files', '-z'];
  if (process.platform !== 'win32') return { gitExecutable, args: fallbackArgs };

  try {
    const pointer = readFileSync(join(root, '.git'), 'utf8').trim();
    if (!pointer.startsWith('gitdir: ')) return { gitExecutable, args: fallbackArgs };
    const gitDir = normalizeGitDirForPlatform(pointer.slice('gitdir: '.length));
    return {
      gitExecutable,
      args: ['--git-dir', gitDir, '--work-tree', root, 'ls-files', '-z'],
    };
  } catch {
    return { gitExecutable, args: fallbackArgs };
  }
};

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

const listServiceNames = (root) => readdirSync(join(root, 'services'), {
  withFileTypes: true,
})
  .filter((entry) => entry.isDirectory())
  .map((entry) => entry.name)
  .sort();

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

export const checkJavascriptToolingGovernance = (root = repositoryRoot) => {
  const violations = collectJavascriptToolingViolations(root);
  if (violations.length > 0) {
    throw new Error(`JavaScript tooling governance check failed:\n- ${violations.join('\n- ')}`);
  }
};

export const checkRepositoryGovernance = (root = repositoryRoot) => {
  const packageJson = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  const longChainBaseline = JSON.parse(readFileSync(join(root, longChainBaselinePath), 'utf8'));
  const trackedFiles = listTrackedFiles(root);
  const violations = [
    ...findTrackedArtifactViolations(trackedFiles),
    ...collectJavascriptToolingViolations(root, trackedFiles),
    ...findPackageScriptViolations({
      scripts: packageJson.scripts ?? {},
      baseline: longChainBaseline,
    }),
    ...findDocumentationViolations({
      serviceNames: listServiceNames(root),
      rootReadme: readFileSync(join(root, 'README.md'), 'utf8'),
      appReadme: readFileSync(join(root, 'apps/hvac-web/README.md'), 'utf8'),
      reactVersion: packageJson.dependencies?.react ?? '',
    }),
    ...findWorkflowViolations(readFileSync(join(root, '.github/workflows/pr-gates.yml'), 'utf8')),
  ];

  if (violations.length > 0) {
    throw new Error(`Repository governance check failed:\n- ${violations.join('\n- ')}`);
  }
};

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (invokedPath === import.meta.url) {
  const javascriptOnly = process.argv.includes('--javascript-only');
  try {
    if (javascriptOnly) checkJavascriptToolingGovernance();
    else checkRepositoryGovernance();
    console.log(javascriptOnly ? 'JavaScript tooling governance check passed.' : 'Repository governance check passed.');
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
}
