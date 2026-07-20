import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const args = process.argv.slice(2);

if (args.length === 0) {
  console.error('usage: node scripts/run-go.mjs <go arguments...>');
  process.exit(2);
}

await Promise.all([
  mkdir(join(root, 'out'), { recursive: true }),
  mkdir(goCacheDir, { recursive: true }),
]);

const child = spawn(goBinary, args, {
  cwd: root,
  stdio: 'inherit',
  shell: false,
  env: { ...process.env, GOCACHE: goCacheDir },
});
const [code, signal] = await once(child, 'exit');
if (signal) {
  console.error(`go terminated by ${signal}`);
  process.exitCode = 1;
} else {
  process.exitCode = code ?? 1;
}
