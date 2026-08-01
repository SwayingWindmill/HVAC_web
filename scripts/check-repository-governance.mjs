import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { capabilityTaskMatrix } from './domain-task-matrix.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const maximumInlineCommands = 4;
const longChainBaselinePath = 'scripts/package-script-long-chain-baseline.json';

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

export const findWorkflowViolations = (workflow) => {
  const violations = [];
  for (const command of [
    'npm run --silent repo:check',
    'npm run --silent repo:governance:test',
  ]) {
    if (!workflow.includes(`- run: ${command}`)) {
      violations.push(`.github/workflows/pr-gates.yml: missing static gate \`${command}\``);
    }
  }
  return violations;
};

const listTrackedFiles = (root) => {
  const gitExecutable = process.platform === 'win32' ? 'git.exe' : 'git';
  const output = execFileSync(gitExecutable, ['-C', root, 'ls-files', '-z'], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  return output.split('\0').filter(Boolean);
};

const listServiceNames = (root) => readdirSync(join(root, 'services'), {
  withFileTypes: true,
})
  .filter((entry) => entry.isDirectory())
  .map((entry) => entry.name)
  .sort();

export const checkRepositoryGovernance = (root = repositoryRoot) => {
  const packageJson = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  const longChainBaseline = JSON.parse(readFileSync(join(root, longChainBaselinePath), 'utf8'));
  const violations = [
    ...findTrackedArtifactViolations(listTrackedFiles(root)),
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
  try {
    checkRepositoryGovernance();
    console.log('Repository governance check passed.');
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
}
