import { spawn } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// The shadcn CLI only adds and inspects registry components; the app never imports it at
// runtime. It runs through npx at a pinned version instead of living in devDependencies,
// which kept zod 3 and deepmerge in every install. apps/hvac-web/src/styles/shadcn-tailwind.css
// is this version's dist/tailwind.css; update both together.
export const SHADCN_CLI_VERSION = '4.23.0';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const workspace = resolve(root, 'apps/hvac-web');

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

const child = spawn('npx', ['--yes', `shadcn@${SHADCN_CLI_VERSION}`, ...process.argv.slice(2)], {
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
