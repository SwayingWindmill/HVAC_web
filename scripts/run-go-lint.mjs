import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

import { installGoTool, readGoWorkspace } from './lib/go-workspace.mjs';

// Runs golangci-lint (rules in .golangci.yml) on every module in go.work and fails if any module has findings.
// The PR gates run it as the go unit profile, selected for any Go code, dependency or lint-rule change.
const root = resolve(process.cwd());
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const { goVersion, modules } = readGoWorkspace(root);
const golangciLint = installGoTool({
  root,
  goBinary: process.env.GO_BINARY ?? 'go',
  goCacheDir,
  goVersion,
  name: 'golangci-lint',
  packagePath: 'github.com/golangci/golangci-lint/v2/cmd/golangci-lint',
  version: 'v2.14.0',
});

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
