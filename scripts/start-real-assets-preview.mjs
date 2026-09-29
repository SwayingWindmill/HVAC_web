import { spawn } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const port = process.env.REAL_ASSETS_PREVIEW_PORT || '5175';
const siteId = '01940000-0001-7000-8000-000000000001';
const url = `http://127.0.0.1:${port}/devices?site=${siteId}`;

const child = spawn(
  process.execPath,
  [resolve(root, 'scripts/run-real-assets-certification.mjs'), '--preview'],
  {
    cwd: root,
    detached: true,
    stdio: 'ignore',
    env: { ...process.env, REAL_ASSETS_PREVIEW_PORT: port },
  },
);

child.unref();

let ready = false;
for (let attempt = 0; attempt < 80; attempt += 1) {
  try {
    const response = await fetch(url);
    if (response.ok) {
      ready = true;
      break;
    }
  } catch {}
  await new Promise((resolveWait) => setTimeout(resolveWait, 100));
}

if (!ready) {
  throw new Error(`Real Assets preview did not become ready at ${url}`);
}

console.log(`Real Assets preview running at ${url}`);
