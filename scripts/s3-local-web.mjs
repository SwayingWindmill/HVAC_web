import { spawn, spawnSync } from 'node:child_process';
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';

const root = resolve(process.cwd());
const out = resolve(root, 'out/s3-local');
const statePath = resolve(out, 'web-processes.json');
const gatewayLogPath = resolve(out, 'web-gateway-port-forward.log');
const viteLogPath = resolve(out, 'web-vite.log');
const webURL = 'http://127.0.0.1:5173/commands';
const gatewayURL = 'http://127.0.0.1:18080/api/v1/health';

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function readState() {
  if (!existsSync(statePath)) return null;
  try {
    return JSON.parse(readFileSync(statePath, 'utf8'));
  } catch {
    return null;
  }
}

function processAlive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function stopPID(pid) {
  if (!processAlive(pid)) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    return;
  }
  try {
    process.kill(-pid, 'SIGTERM');
  } catch {
    try { process.kill(pid, 'SIGTERM'); } catch {}
  }
}

function stop() {
  const state = readState();
  if (state) {
    stopPID(Number(state.vitePID));
    stopPID(Number(state.portForwardPID));
  }
  rmSync(statePath, { force: true });
  console.log('S3 local Web processes stopped.');
}

async function waitForURL(url, label, attempts = 120) {
  let lastError = '';
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      const response = await fetch(url, { redirect: 'manual' });
      if (response.ok) return response;
      lastError = `HTTP ${response.status}`;
    } catch (error) {
      lastError = error instanceof Error ? error.message : String(error);
    }
    await pause(250);
  }
  throw new Error(`${label} did not become ready at ${url}: ${lastError}`);
}

function spawnDetached(command, args, env, logPath) {
  const fd = openSync(logPath, 'a');
  const child = spawn(command, args, {
    cwd: root,
    detached: true,
    windowsHide: true,
    stdio: ['ignore', fd, fd],
    env: { ...process.env, ...env },
  });
  child.unref();
  closeSync(fd);
  return child.pid;
}

async function start() {
  mkdirSync(out, { recursive: true });
  const current = readState();
  if (current && processAlive(Number(current.vitePID)) && processAlive(Number(current.portForwardPID))) {
    try {
      await waitForURL(webURL, 'HVAC Web', 4);
      await waitForURL(gatewayURL, 'local Web Gateway', 4);
      console.log(`S3 local Web is already running at ${webURL}`);
      return;
    } catch {
      stop();
    }
  } else if (current) {
    stop();
  }

  run('bash', ['scripts/s3-local.sh', 'status'], { stdio: 'ignore' });
  run('bash', ['-lc', 'kubectl -n s3-local rollout status deployment/s3-local-web-gateway --timeout=60s'], { stdio: 'ignore' });

  rmSync(gatewayLogPath, { force: true });
  rmSync(viteLogPath, { force: true });
  const portForwardPID = spawnDetached('bash', [
    '-lc', 'exec kubectl -n s3-local port-forward service/s3-local-web-gateway 18080:8080 --address 127.0.0.1',
  ], {}, gatewayLogPath);

  const vitePID = spawnDetached(process.execPath, [
    resolve(root, 'node_modules/vite/bin/vite.js'), 'apps/hvac-web',
    '--config', 'apps/hvac-web/vite.config.ts', '--host', '127.0.0.1', '--port', '5173', '--strictPort',
  ], {
    PLATFORM_GATEWAY_PROXY_TARGET: 'http://127.0.0.1:18080',
    S0_GATEWAY_ONLY: 'true',
    VITE_API_MODE: 'real',
    VITE_S3_LOCAL_COMMANDS: 'true',
  }, viteLogPath);

  writeFileSync(statePath, `${JSON.stringify({
    schemaVersion: 1,
    portForwardPID,
    vitePID,
    webURL,
    gatewayURL,
    startedAt: new Date().toISOString(),
    formalCertificationClaim: false,
  }, null, 2)}\n`);

  try {
    await waitForURL(gatewayURL, 'local Web Gateway');
    await waitForURL(webURL, 'HVAC Web');
  } catch (error) {
    stop();
    throw error;
  }
  console.log(`S3 local HVAC Web ready: ${webURL}`);
}

async function status() {
  const state = readState();
  const processStatus = state ? {
    portForward: processAlive(Number(state.portForwardPID)),
    vite: processAlive(Number(state.vitePID)),
  } : { portForward: false, vite: false };
  let gateway = false;
  let web = false;
  try { gateway = (await fetch(gatewayURL)).ok; } catch {}
  try { web = (await fetch(webURL)).ok; } catch {}
  console.log(JSON.stringify({
    status: processStatus.portForward && processStatus.vite && gateway && web ? 'running' : 'stopped-or-degraded',
    processStatus,
    gateway,
    web,
    webURL,
    formalCertificationClaim: false,
  }, null, 2));
  if (!gateway || !web) process.exitCode = 2;
}

const action = process.argv[2] ?? 'start';
if (action === 'start') await start();
else if (action === 'stop') stop();
else if (action === 'status') await status();
else throw new Error('usage: node scripts/s3-local-web.mjs {start|status|stop}');
