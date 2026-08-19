import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const require = createRequire(import.meta.url);
const typescriptBin = require.resolve('typescript/bin/tsc');
const viteBin = resolve(dirname(require.resolve('vite/package.json')), 'bin/vite.js');
const environment = {
  ...process.env,
  VITE_API_MODE: 'real',
  S0_GATEWAY_ONLY: 'true',
};

function run(label, script, args) {
  const result = spawnSync(process.execPath, [script, ...args], {
    cwd: root,
    stdio: 'inherit',
    windowsHide: true,
    env: environment,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${label} failed with ${result.signal ?? result.status}`);
}

run('TypeScript project build', typescriptBin, ['-b', 'apps/hvac-web/tsconfig.json']);
run('HVAC Web real Registry production build', viteBin, [
  'build',
  'apps/hvac-web',
  '--config',
  'apps/hvac-web/vite.config.ts',
]);

console.log('S1 HVAC Web real Registry production build passed.');
