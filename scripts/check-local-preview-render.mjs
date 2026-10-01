import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { readFile, rm, writeFile } from 'node:fs/promises';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const url = process.argv[2];
if (!url) throw new Error('Usage: node scripts/check-local-preview-render.mjs <url>');

const browserPath = resolveLinuxBrowserExecutable();

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const profileName = `hvac-preview-render-${process.pid}`;
const browserProfileDir = `/tmp/${profileName}`;
const devToolsActivePortPath = `${browserProfileDir}/DevToolsActivePort`;
let child;
let socket;

function createClient(webSocketUrl) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(webSocketUrl);
    const pending = new Map();
    const events = [];
    let nextId = 0;
    ws.on('open', () => resolve({
      ws,
      events,
      send(method, params = {}) {
        const id = ++nextId;
        ws.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
    }));
    ws.on('error', reject);
    ws.on('message', (raw) => {
      const message = JSON.parse(String(raw));
      if (!message.id) {
        events.push(message);
        return;
      }
      const pendingCommand = pending.get(message.id);
      if (!pendingCommand) return;
      pending.delete(message.id);
      if (message.error) pendingCommand.rejectCommand(new Error(message.error.message));
      else pendingCommand.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'evaluation failed');
  return response.result.value;
}

try {
  child = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--disable-extensions',
    '--disable-sync',
    '--disable-background-networking',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    '--remote-debugging-address=0.0.0.0',
    '--remote-debugging-port=0',
    `--user-data-dir=${browserProfileDir}`,
    'about:blank',
  ], { stdio: 'ignore' });

  let debugPort = null;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    if (existsSync(devToolsActivePortPath)) {
      const raw = await readFile(devToolsActivePortPath, 'utf8');
      debugPort = Number(raw.split(/\r?\n/, 1)[0]);
      if (Number.isInteger(debugPort) && debugPort > 0) break;
    }
    await pause(50);
  }
  if (!debugPort) throw new Error('Chrome did not publish DevToolsActivePort');

  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  if (!page?.webSocketDebuggerUrl) throw new Error('No browser page available');
  const client = await createClient(page.webSocketDebuggerUrl);
  socket = client.ws;
  await client.send('Runtime.enable');
  await client.send('Page.enable');
  await client.send('Log.enable');
  await client.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 941, deviceScaleFactor: 1, mobile: false });
  await client.send('Page.navigate', { url });
  await pause(4000);

  const state = await evaluate(client, `(() => ({
    href: location.href,
    title: document.title,
    readyState: document.readyState,
    rootHtmlLength: document.querySelector('#root')?.innerHTML.length ?? -1,
    rootText: document.querySelector('#root')?.innerText.slice(0, 2000) ?? '',
    bodyText: document.body?.innerText.slice(0, 2000) ?? '',
    viteErrorOverlay: document.querySelector('vite-error-overlay')?.shadowRoot?.textContent?.slice(0, 4000) ?? '',
  }))()`);

  const runtimeErrors = client.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.entry?.text ?? event.method);

  const screenshot = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  const outPath = process.argv[3] || 'out/preview-render.png';
  await writeFile(outPath, Buffer.from(screenshot.data, 'base64'));

  console.log(JSON.stringify({ state, screenshot: outPath, runtimeErrors }, null, 2));
} finally {
  socket?.close();
  if (child && child.exitCode === null && child.signalCode === null) {
    child.kill('SIGTERM');
    const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1200).then(() => false)]);
    if (!stopped) child.kill('SIGKILL');
  }
  await rm(browserProfileDir, { recursive: true, force: true }).catch(() => {});
}
