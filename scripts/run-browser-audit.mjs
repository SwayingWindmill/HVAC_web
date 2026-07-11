import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const targetScript = process.argv[2];
if (!targetScript) throw new Error('Usage: node scripts/run-browser-audit.mjs <audit-script>');

const port = Number(process.env.HVAC_AUDIT_DEV_PORT ?? 5173);
const defaultBaseUrl = `http://127.0.0.1:${port}`;
const requestedBaseUrl = process.env.HVAC_AUDIT_BASE_URL;
const pause = (ms) => new Promise((resolvePause) => setTimeout(resolvePause, ms));

async function isReachable(baseUrl) {
  try {
    const response = await fetch(`${baseUrl}/dashboard`);
    return response.ok;
  } catch {
    return false;
  }
}

async function waitForServer(baseUrl) {
  for (let index = 0; index < 160; index += 1) {
    if (await isReachable(baseUrl)) return;
    await pause(100);
  }
  throw new Error(`Audit server did not become ready at ${baseUrl}`);
}

function runAudit(baseUrl) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(process.execPath, [targetScript], {
      cwd: root,
      stdio: 'inherit',
      env: { ...process.env, HVAC_AUDIT_BASE_URL: baseUrl },
      shell: false,
    });
    child.once('error', rejectRun);
    child.once('exit', (code, signal) => {
      if (code === 0) resolveRun();
      else rejectRun(new Error(`${targetScript} failed with ${signal ?? code}`));
    });
  });
}

async function stopServer(server) {
  if (!server || server.exitCode !== null || server.signalCode !== null) return;
  server.kill('SIGTERM');
  const stopped = await Promise.race([
    once(server, 'exit').then(() => true),
    pause(1200).then(() => false),
  ]);
  if (!stopped && process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(server.pid), '/T', '/F'], { stdio: 'ignore' });
  }
}

let server;
try {
  if (requestedBaseUrl) {
    if (!(await isReachable(requestedBaseUrl))) {
      throw new Error(`HVAC_AUDIT_BASE_URL is not reachable: ${requestedBaseUrl}`);
    }
    await runAudit(requestedBaseUrl);
  } else if (await isReachable(defaultBaseUrl)) {
    console.log(`Using existing audit server at ${defaultBaseUrl}`);
    await runAudit(defaultBaseUrl);
  } else {
    console.log(`Starting Vite audit server at ${defaultBaseUrl}`);
    server = spawn(process.execPath, [
      resolve(root, 'node_modules/vite/bin/vite.js'),
      '--host', '127.0.0.1',
      '--port', String(port),
      '--strictPort',
    ], {
      cwd: root,
      stdio: 'inherit',
      env: process.env,
      shell: false,
    });
    server.once('error', (error) => console.error('Audit server error:', error));
    await waitForServer(defaultBaseUrl);
    await runAudit(defaultBaseUrl);
  }
} finally {
  await stopServer(server);
}
