import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { createServer } from 'vite';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/s2-telemetry-live');
const outputPath = resolve(root, 'out/s2-telemetry-live-client/browser-live-client.json');
const debugPort = Number(process.env.S2_LIVE_DEBUG_PORT ?? 9375);
const profileDir = join(tmpdir(), `s2-live-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const browserPath = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  join('C:\\Program Files (x86)', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  join('C:\\Program Files', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium-browser', '/usr/bin/chromium',
].filter(Boolean).find((candidate) => existsSync(candidate));
if (!browserPath) throw new Error('A CDP-compatible browser was not found');

function assert(condition, message) { if (!condition) throw new Error(message); }

function cdp(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    let nextId = 0;
    socket.on('open', () => resolveClient({
      send(method, params = {}) {
        const id = ++nextId;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
    socket.on('error', rejectClient);
    socket.on('message', (raw) => {
      const message = JSON.parse(String(raw));
      const command = pending.get(message.id);
      if (!command) return;
      pending.delete(message.id);
      if (message.error) command.rejectCommand(new Error(message.error.message));
      else command.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text);
  return response.result.value;
}

let server;
let browser;
let client;
try {
  await mkdir(dirname(outputPath), { recursive: true });
  await mkdir(profileDir, { recursive: true });
  server = await createServer({
    root: fixtureRoot,
    logLevel: 'error',
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: { host: '127.0.0.1', port: 0, strictPort: false },
  });
  await server.listen();
  const address = server.httpServer?.address();
  assert(address && typeof address === 'object', 'Vite fixture server has no address');
  const url = `http://127.0.0.1:${address.port}/`;
  browser = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars',
    `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profileDir}`, url,
  ], { stdio: 'ignore' });
  for (let attempt = 0; attempt < 600; attempt += 1) {
    try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
    if (attempt === 599) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  client = await cdp(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  let result;
  for (let attempt = 0; attempt < 900; attempt += 1) {
    result = await evaluate(client, 'window.__S2_LIVE_RESULT__ ?? null');
    if (result?.done) break;
    await pause(100);
  }
  assert(result?.done, 'TelemetryLiveClient browser harness timed out');
  if (result.error) throw new Error(result.error);
  assert(result.result?.status === 'passed', 'TelemetryLiveClient browser harness did not pass');
  await writeFile(outputPath, `${JSON.stringify({ ...result.result, browser: 'real-cdp', generatedAt: new Date().toISOString() }, null, 2)}\n`);
  console.log(`S2 Ticket 07 browser live-client audit passed: ${outputPath}`);
} finally {
  client?.close();
  if (browser && browser.exitCode === null) browser.kill('SIGKILL');
  await pause(500);
  await server?.close();
  await rm(profileDir, { recursive: true, force: true }).catch(() => undefined);
}
