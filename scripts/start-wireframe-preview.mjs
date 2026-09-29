import { spawn } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const preferredPort = Number(process.env.WIREFRAME_PREVIEW_PORT || '5173');
const revision = 'shadcn-energy-visual-spec-v16';

async function reachable(url) {
  try {
    const response = await fetch(url, { cache: 'no-store' });
    return response.ok;
  } catch {
    return false;
  }
}

async function findFreshPort(startPort) {
  for (let port = startPort; port < startPort + 20; port += 1) {
    if (!(await reachable(`http://127.0.0.1:${port}/`))) return port;
  }
  throw new Error(`No free wireframe preview port found from ${startPort}`);
}

async function revisionIsCurrent(port) {
  try {
    const response = await fetch(`http://127.0.0.1:${port}/main.tsx?revision=${Date.now()}`, { cache: 'no-store' });
    if (!response.ok) return false;
    return (await response.text()).includes(revision);
  } catch {
    return false;
  }
}

const port = await findFreshPort(preferredPort);
const overviewUrl = `http://127.0.0.1:${port}/`;
const operationsUrl = `http://127.0.0.1:${port}/?page=operations`;

const child = spawn(
  process.execPath,
  [resolve(root, 'scripts/run-overview-browser-review.mjs'), '--serve'],
  {
    cwd: root,
    detached: true,
    stdio: 'ignore',
    env: { ...process.env, OVERVIEW_REVIEW_PORT: String(port) },
  },
);
child.unref();

let ready = false;
for (let attempt = 0; attempt < 160; attempt += 1) {
  if ((await reachable(overviewUrl)) && (await revisionIsCurrent(port))) {
    ready = true;
    break;
  }
  await new Promise((resolveWait) => setTimeout(resolveWait, 100));
}

if (!ready) throw new Error(`Wireframe preview did not become current at ${overviewUrl}`);

console.log(`revision: ${revision}`);
console.log(`03 站点总览: ${overviewUrl}`);
console.log(`04 系统运行: ${operationsUrl}`);
console.log(`36 用户与权限: http://127.0.0.1:${port}/settings/access`);
