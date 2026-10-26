import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

// Runs golangci-lint (rules in .golangci.yml) on every module in go.work and fails if any module has findings.
const golangciLintVersion = 'v2.14.0';
const root = resolve(process.cwd());
const goBinary = process.env.GO_BINARY ?? 'go';
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const workspace = readFileSync(join(root, 'go.work'), 'utf8');
const goVersion = /^go (\S+)$/m.exec(workspace)?.[1];
if (!goVersion) throw new Error('go.work has no go directive');
const modules = [...workspace.matchAll(/^\s*\.\/((?:cmd|libs|modules|services|tools)\/[^\s)]+)\s*$/gm)].map((match) => match[1]);
if (modules.length === 0) throw new Error('go.work contains no modules to lint');

// golangci-lint refuses code that targets a newer Go than the one it was built with, so build it with the workspace's Go.
const toolDir = join(tmpdir(), 'hvac-go-tools', `golangci-lint-${golangciLintVersion}-go${goVersion}`);
const golangciLint = join(toolDir, 'golangci-lint');
mkdirSync(goCacheDir, { recursive: true });
if (!existsSync(golangciLint)) {
  mkdirSync(toolDir, { recursive: true });
  console.log(`=== installing golangci-lint ${golangciLintVersion} with go${goVersion} ===`);
  const install = spawnSync(goBinary, ['install', `github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${golangciLintVersion}`], {
    cwd: root,
    stdio: 'inherit',
    env: { ...process.env, GOBIN: toolDir, GOCACHE: goCacheDir, GOWORK: 'off', GOTOOLCHAIN: `go${goVersion}` },
  });
  if (install.error) throw install.error;
  if (install.status !== 0) process.exit(install.status ?? 1);
}

const failed = [];
for (const modulePath of modules) {
  console.log(`=== golangci-lint ${modulePath} ===`);
  const result = spawnSync(golangciLint, ['run', './...'], {
    cwd: resolve(root, modulePath),
    stdio: 'inherit',
    env: { ...process.env, GOCACHE: goCacheDir },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) failed.push(modulePath);
}

if (failed.length > 0) {
  console.error(`golangci-lint found problems in ${failed.length}/${modules.length} modules: ${failed.join(', ')}`);
  process.exit(1);
}
console.log(`golangci-lint passed for ${modules.length} workspace modules.`);
