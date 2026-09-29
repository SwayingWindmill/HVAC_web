import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const workspace = resolve(root, 'apps/hvac-web');
const workspaceRequire = createRequire(resolve(workspace, 'package.json'));

let cli;
try {
  cli = workspaceRequire.resolve('shadcn');
} catch {
  console.error('The @hvac/web workspace is missing its shadcn devDependency. Run npm install at the repository root first.');
  process.exit(1);
}

const requiredNoProxy = [
  'tablecn.com',
  'www.tablecn.com',
  'ui.shadcn.com',
  'reui.io',
  'www.reui.io',
  'diceui.com',
  'www.diceui.com',
  'kibo-ui.com',
  'www.kibo-ui.com',
  'dashboardcn.com',
  'www.dashboardcn.com',
  'shadcnblocks.com',
  'www.shadcnblocks.com',
];

const existingNoProxy = (process.env.NO_PROXY ?? process.env.no_proxy ?? '')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean);

const noProxy = Array.from(new Set([...existingNoProxy, ...requiredNoProxy])).join(',');

const child = spawn(process.execPath, [cli, ...process.argv.slice(2)], {
  cwd: workspace,
  env: {
    ...process.env,
    NO_PROXY: noProxy,
    no_proxy: noProxy,
  },
  stdio: 'inherit',
});

child.on('error', (error) => {
  console.error(error);
  process.exit(1);
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  process.exit(code ?? 1);
});
