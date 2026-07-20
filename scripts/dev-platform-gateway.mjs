import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const gatewayAddress = process.env.PLATFORM_GATEWAY_ADDR || '127.0.0.1:8080';
const gatewayTarget = process.env.PLATFORM_GATEWAY_PROXY_TARGET || `http://${gatewayAddress}`;
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
let stopping = false;

function stop(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else {
    child.kill('SIGTERM');
  }
}

const gateway = spawn(goBinary, ['run', './services/platform-gateway/cmd/platform-gateway'], {
  cwd: root,
  stdio: 'inherit',
  shell: false,
  env: {
    ...process.env,
    PLATFORM_GATEWAY_ADDR: gatewayAddress,
  },
});

const web = spawn(process.execPath, [
  resolve(root, 'node_modules/vite/bin/vite.js'),
  'apps/hvac-web',
  '--config', 'apps/hvac-web/vite.config.ts',
], {
  cwd: root,
  stdio: 'inherit',
  shell: false,
  env: {
    ...process.env,
    PLATFORM_GATEWAY_PROXY_TARGET: gatewayTarget,
    S0_GATEWAY_ONLY: 'true',
  },
});

function shutdown(exitCode = 0) {
  if (stopping) return;
  stopping = true;
  stop(web);
  stop(gateway);
  process.exitCode = exitCode;
}

for (const child of [gateway, web]) {
  child.once('error', (error) => {
    console.error(error);
    shutdown(1);
  });
  child.once('exit', (code, signal) => {
    if (stopping) return;
    console.error(`S0 development process exited: ${signal ?? code}`);
    shutdown(code ?? 1);
  });
}

process.once('SIGINT', () => shutdown(0));
process.once('SIGTERM', () => shutdown(0));
console.log(`S0 Gateway topology: HVAC Web -> ${gatewayTarget}; no NestJS or internal service is started.`);
