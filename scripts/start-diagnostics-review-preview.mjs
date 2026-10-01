import { spawn } from 'node:child_process';

const port = Number(process.env.DIAGNOSTICS_REVIEW_PORT ?? 5186);
const child = spawn(process.execPath, ['scripts/run-diagnostics-browser-review.mjs', '--serve'], {
  detached: true,
  stdio: 'ignore',
  env: { ...process.env, DIAGNOSTICS_REVIEW_PORT: String(port) },
});
child.unref();

const url = `http://127.0.0.1:${port}`;
let ready = false;
for (let attempt = 0; attempt < 80; attempt += 1) {
  try {
    const response = await fetch(url, { cache: 'no-store' });
    if (response.ok) {
      ready = true;
      break;
    }
  } catch {}
  await new Promise((resolve) => setTimeout(resolve, 100));
}

if (!ready) throw new Error(`Diagnostics preview did not become ready at ${url}`);
console.log(JSON.stringify({ url, pid: child.pid }));
