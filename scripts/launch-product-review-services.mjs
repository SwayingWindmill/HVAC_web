import { spawn } from 'node:child_process';

const services = [
  { name: 'overview', port: 5191, script: 'scripts/run-overview-browser-review.mjs', env: { OVERVIEW_REVIEW_PORT: '5191' } },
  { name: 'assets', port: 5192, script: 'scripts/run-assets-workspace-browser-review.mjs', env: { ASSETS_REVIEW_PORT: '5192' } },
  { name: 'alarms', port: 5193, script: 'scripts/run-real-alarms-browser-audit.mjs', env: { ALARMS_REVIEW_PORT: '5193' } },
  { name: 'diagnostics', port: 5194, script: 'scripts/run-diagnostics-browser-review.mjs', env: { DIAGNOSTICS_REVIEW_PORT: '5194' } },
];

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function isReady(port) {
  try {
    const response = await fetch(`http://127.0.0.1:${port}/`, { cache: 'no-store' });
    return response.ok;
  } catch {
    return false;
  }
}

async function ensureService(service) {
  if (await isReady(service.port)) return { ...service, reused: true };
  const child = spawn(process.execPath, [service.script, '--serve'], {
    detached: true,
    stdio: 'ignore',
    env: { ...process.env, ...service.env },
  });
  child.unref();
  for (let attempt = 0; attempt < 160; attempt += 1) {
    if (await isReady(service.port)) return { ...service, pid: child.pid, reused: false };
    await pause(100);
  }
  throw new Error(`${service.name} preview did not become ready on port ${service.port}`);
}

const results = [];
for (const service of services) results.push(await ensureService(service));
console.log(JSON.stringify({ hub: 'http://127.0.0.1:5191/hub.html', services: results }));
