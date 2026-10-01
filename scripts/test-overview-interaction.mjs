import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { readFile, rm, writeFile } from 'node:fs/promises';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const url = 'http://localhost:5174/overview';
const browserPath = resolveLinuxBrowserExecutable();
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const profileName = `hvac-interaction-${process.pid}`;
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
    '--no-sandbox',
    '--no-first-run',
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

  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((r) => r.json());
  const page = pages.find((p) => p.type === 'page');
  const client = await createClient(page.webSocketDebuggerUrl);
  socket = client.ws;
  await client.send('Runtime.enable');
  await client.send('Page.enable');
  await client.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 1100, deviceScaleFactor: 1, mobile: false });
  await client.send('Page.navigate', { url });
  await pause(3000);

  // 1. Click the first opportunity checkbox
  await evaluate(client, `(() => {
    const checkboxes = document.querySelectorAll('button[data-slot="checkbox"]');
    if (checkboxes.length > 1) {
      checkboxes[1].click(); // click first row checkbox
    }
  })()`);
  await pause(800);

  // Capture screenshot with Dice UI ActionBar visible
  const ss1 = await client.send('Page.captureScreenshot', { format: 'png' });
  await writeFile('out/overview-actionbar.png', Buffer.from(ss1.data, 'base64'));

  // 2. Click "详情" button to open Sheet
  await evaluate(client, `(() => {
    const detailButtons = Array.from(document.querySelectorAll('button')).filter(b => b.textContent.includes('详情'));
    if (detailButtons.length > 0) {
      detailButtons[0].click();
    }
  })()`);
  await pause(1000);

  // Capture screenshot with Sheet drawer open
  const ss2 = await client.send('Page.captureScreenshot', { format: 'png' });
  await writeFile('out/overview-sheet-drawer.png', Buffer.from(ss2.data, 'base64'));

  console.log('Interactions successfully tested and screenshots saved: out/overview-actionbar.png, out/overview-sheet-drawer.png');
} finally {
  socket?.close();
  if (child && child.exitCode === null) {
    child.kill('SIGKILL');
  }
  await rm(browserProfileDir, { recursive: true, force: true }).catch(() => {});
}
