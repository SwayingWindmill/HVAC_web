import { execFileSync } from 'node:child_process';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

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
  const violations = [
    ...findTrackedArtifactViolations(listTrackedFiles(root)),
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
