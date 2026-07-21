import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { isAbsolute, join, resolve } from 'node:path';

const root = resolve(process.cwd());
const moduleArgument = process.argv.find((value) => value.startsWith('--module='));
const modulePath = moduleArgument?.slice('--module='.length);
const args = process.argv.slice(2).filter((value) => !value.startsWith('--module='));

if (!modulePath || args.length === 0) {
  console.error('usage: node scripts/run-isolated-go.mjs --module=<path> <go arguments...>');
  process.exit(2);
}

const cwd = isAbsolute(modulePath) ? modulePath : resolve(root, modulePath);
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
await mkdir(goCacheDir, { recursive: true });

const child = spawn(goBinary, args, {
  cwd,
  stdio: 'inherit',
  shell: false,
  windowsHide: true,
  env: { ...process.env, GOCACHE: goCacheDir, GOWORK: 'off' },
});
const [code, signal] = await once(child, 'exit');
if (signal) {
  console.error(`go terminated by ${signal}`);
  process.exit(1);
}
process.exit(code ?? 1);
