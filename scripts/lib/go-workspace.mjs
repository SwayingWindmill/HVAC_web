import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// The Go version and module directories (e.g. "cmd/energy-api") listed in go.work.
export function readGoWorkspace(root) {
  const workspace = readFileSync(join(root, 'go.work'), 'utf8');
  const goVersion = /^go (\S+)$/m.exec(workspace)?.[1];
  if (!goVersion) throw new Error('go.work has no go directive');
  const modules = [...workspace.matchAll(/^\s*\.\/((?:cmd|libs|modules|services|tools)\/[^\s)]+|deploy\/platform\/phase1\/cd)\s*$/gm)].map((match) => match[1]);
  if (modules.length === 0) throw new Error('go.work lists no modules');
  return { goVersion, modules };
}

// Installs a pinned Go tool once per (version, Go version) and returns its path. The tool is built with the
// workspace's Go: golangci-lint and govulncheck cannot load code that targets a newer Go than they were built with.
// GOFLAGS is cleared so a gate's -race does not build a race-instrumented tool.
export function installGoTool({ root, goBinary, goCacheDir, goVersion, name, packagePath, version }) {
  const toolDir = join(tmpdir(), 'hvac-go-tools', `${name}-${version}-go${goVersion}`);
  const binary = join(toolDir, name);
  if (existsSync(binary)) return binary;
  mkdirSync(toolDir, { recursive: true });
  mkdirSync(goCacheDir, { recursive: true });
  console.log(`=== installing ${name} ${version} with go${goVersion} ===`);
  const install = spawnSync(goBinary, ['install', `${packagePath}@${version}`], {
    cwd: root,
    stdio: 'inherit',
    windowsHide: true,
    env: { ...process.env, GOBIN: toolDir, GOCACHE: goCacheDir, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: `go${goVersion}` },
  });
  if (install.error) throw install.error;
  if (install.status !== 0) process.exit(install.status ?? 1);
  return binary;
}
