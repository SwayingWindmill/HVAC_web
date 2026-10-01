import { spawn } from 'node:child_process';

const port = Number(process.env.REMAINING_REVIEW_PORT ?? 5210);
const child = spawn(process.execPath, ['scripts/run-remaining-pages-review.mjs'], {
  detached: true,
  stdio: 'ignore',
  env: { ...process.env, REMAINING_REVIEW_PORT: String(port) },
});
child.unref();

const url = `http://127.0.0.1:${port}`;
let ready = false;
for (let attempt = 0; attempt < 120; attempt += 1) {
  try {
    const response = await fetch(url, { cache: 'no-store', signal: AbortSignal.timeout(500) });
    if (response.ok) {
      ready = true;
      break;
    }
  } catch {}
  await new Promise((resolve) => setTimeout(resolve, 100));
}

if (!ready) throw new Error(`Remaining pages preview did not become ready at ${url}`);
console.log(JSON.stringify({ url, pid: child.pid }));
