import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const environment = {
  ...process.env,
  VITE_API_MODE: 'real',
  S0_GATEWAY_ONLY: 'true',
};

function run(label, script, args) {
  const result = spawnSync(process.execPath, [resolve(root, script), ...args], {
    cwd: root,
    stdio: 'inherit',
    windowsHide: true,
    env: environment,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${label} failed with ${result.signal ?? result.status}`);
}

run('TypeScript project build', 'node_modules/typescript/bin/tsc', ['-b', 'apps/hvac-web/tsconfig.json']);
run('HVAC Web real Registry production build', 'node_modules/vite/bin/vite.js', [
  'build',
  'apps/hvac-web',
  '--config',
  'apps/hvac-web/vite.config.ts',
]);

console.log('S1 HVAC Web real Registry production build passed.');
