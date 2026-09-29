import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const outputRoot = resolve(root, 'out/alarm-center-review');
const profileRoot = join(tmpdir(), `alarm-center-review-${process.pid}`);
const pause = (ms) => new Promise((resolvePause) => setTimeout(resolvePause, ms));

function assert(condition, message) { if (!condition) throw new Error(message); }
async function findPort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  assert(address && typeof address === 'object', 'Unable to allocate port');
  await new Promise((resolveClose) => server.close(resolveClose));
  return address.port;
}
function cdpClient(url) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(url);
    const pending = new Map();
    const events = [];
    let nextId = 0;
    socket.on('open', () => resolveClient({
      events,
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
      if (!message.id) { events.push(message); return; }
      const command = pending.get(message.id);
      if (!command) return;
      pending.delete(message.id);
      if (message.error) command.rejectCommand(new Error(message.error.message)); else command.resolveCommand(message.result);
    });
  });
}
async function evaluate(client, expression) {
  const result = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text ?? 'Browser evaluation failed');
  return result.result.value;
}
async function waitFor(client, expression, label) {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    if (await evaluate(client, `Boolean(${expression})`).catch(() => false)) return;
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ text: document.body.innerText.slice(0,4000), html: document.body.innerHTML.slice(0,2000) })`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready: ${JSON.stringify(diagnostic)}`);
}
async function clickText(client, selector, text) {
  return evaluate(client, `(() => { const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)})).find((candidate) => candidate.textContent?.trim().includes(${JSON.stringify(text)})); if (!(node instanceof HTMLElement)) return false; node.click(); return true; })()`);
}
async function stop(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');
}

const browserPath = resolveLinuxBrowserExecutable();

const webPort = await findPort();
const webURL = `http://127.0.0.1:${webPort}`;
const serverProcess = spawn(process.execPath, ['scripts/run-real-alarms-browser-audit.mjs', '--serve'], {
  cwd: root,
  env: { ...process.env, ALARMS_REVIEW_PORT: String(webPort) },
  stdio: 'ignore',
});
let browserProcess;
let client;
const profileDir = profileRoot;
const debugPort = await findPort();

try {
  await mkdir(outputRoot, { recursive: true });
  await mkdir(profileRoot, { recursive: true });
  for (let i = 0; i < 300; i += 1) {
    try { if ((await fetch(webURL)).ok) break; } catch {}
    if (i === 299) throw new Error('Alarm review server did not become ready');
    await pause(100);
  }

  browserProcess = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--disable-extensions', '--disable-sync', '--disable-background-networking', '--no-sandbox',
    '--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--remote-debugging-address=0.0.0.0', '--window-size=1672,941',
    `--remote-debugging-port=${debugPort ?? 0}`, `--user-data-dir=${profileDir}`, 'about:blank',
  ], { stdio: 'ignore' });

  for (let i = 0; i < 300; i += 1) {
    try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
    if (i === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page');
  client = await cdpClient(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable'); await client.send('Page.enable'); await client.send('Log.enable');
  await client.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 941, deviceScaleFactor: 1, mobile: false });
  await client.send('Page.navigate', { url: webURL });
  await waitFor(client, `document.querySelector('[data-testid="alarm-center"]') && document.body.innerText.includes('CH-03 冷水机组出水温度偏高')`, 'Alarm Center');
  await pause(500);

  const desktop = await evaluate(client, `(() => {
    const rect = (selector) => { const node = document.querySelector(selector); if (!(node instanceof HTMLElement)) return null; const r=node.getBoundingClientRect(); return {top:Math.round(r.top),bottom:Math.round(r.bottom),height:Math.round(r.height),width:Math.round(r.width)}; };
    return {
      body:{scrollWidth:document.documentElement.scrollWidth,clientWidth:document.documentElement.clientWidth,scrollHeight:document.documentElement.scrollHeight},
      sidebar:rect('[data-slot="sidebar"]'), header:rect('header'), load:rect('[data-testid="alarm-load-context"]'), ledger:rect('[data-testid="alarm-triage-ledger"]'),
      tabs:Array.from(document.querySelectorAll('[data-slot="tabs-trigger"]')).map((n)=>n.textContent?.trim()),
      headers:Array.from(document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="table-head"]')).map((n)=>n.textContent?.trim()),
      rows:document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="table-body"] [data-slot="table-row"]').length,
      tableRegion:document.querySelector('[aria-label="告警，可横向滚动"]')?.getAttribute('role'),
      oldText:['关联分析','规则与通知','人工关闭'].filter((value)=>document.querySelector('[data-testid="alarm-center"]')?.textContent?.includes(value)),
      ant:document.querySelectorAll('.ant-table,.ant-card,.ant-tabs,.ant-drawer,.ant-modal').length,
      text:document.body.innerText,
    };
  })()`);
  assert(desktop.body.scrollWidth <= desktop.body.clientWidth, `Desktop page overflowed: ${JSON.stringify(desktop.body)}`);
  assert(desktop.sidebar?.width >= 248 && desktop.sidebar?.width <= 264 && desktop.header?.height === 56, `Alarm Center lost shared shadcn shell geometry: ${JSON.stringify({ sidebar: desktop.sidebar, header: desktop.header })}`);
  assert(desktop.load?.top < 260 && desktop.ledger?.top < 520, 'Triage ledger is not visible early enough in the first viewport');
  assert(desktop.rows === 2, `Active ledger expected 2 rows, got ${desktop.rows}`);
  assert(JSON.stringify(desktop.tabs) === JSON.stringify(['当前活动','历史','已搁置','告警绩效']), `Peer views drifted: ${JSON.stringify(desktop.tabs)}`);
  for (const header of ['等级','告警 / 来源','物理状态','确认','负责人','持续','重复','搁置','最近变化']) assert(desktop.headers.includes(header), `Ledger lost column ${header}`);
  assert(desktop.tableRegion === 'region' && desktop.ant === 0 && desktop.oldText.length === 0, `Ledger composition or semantics regressed: ${JSON.stringify(desktop)}`);
  for (const fact of ['活动告警','未确认','未指派','已搁置']) assert(desktop.text.includes(fact), `Load context lost ${fact}`);
  const desktopShot = await client.send('Page.captureScreenshot', { format:'png', captureBeyondViewport:false });
  await writeFile(join(outputRoot,'09-active-desktop.png'), Buffer.from(desktopShot.data,'base64'));

  const rowKeyboard = await evaluate(client, `(() => { const row=document.querySelector('[data-testid="alarm-triage-ledger"] [data-slot="table-body"] [data-slot="table-row"]'); if (!(row instanceof HTMLElement)) return false; row.focus(); row.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})); return true; })()`);
  assert(rowKeyboard, 'Could not keyboard-select Alarm row');
  await waitFor(client, `document.querySelector('aside[aria-label="告警详情"] [data-testid="alarm-inspector-content"]') && location.search.includes('selected=')`, 'Desktop inspector');
  const inspector = await evaluate(client, `(() => ({
    text:document.querySelector('aside[aria-label="告警详情"]')?.textContent ?? '',
    visible:Boolean(document.querySelector('aside[aria-label="告警详情"]')?.getClientRects().length),
    sheetOverlay:Boolean(document.querySelector('[data-slot="sheet-overlay"]')),
    internalIdVisible:/01910000-/.test(document.body.innerText),
  }))()`);
  assert(inspector.visible && !inspector.sheetOverlay, `Desktop Inspector incorrectly used modal Sheet: ${JSON.stringify(inspector)}`);
  assert(!inspector.internalIdVisible, 'Alarm Center leaked internal UUID into operator text');
  for (const item of ['首次发生','持续时间','确认状态','负责人','重复次数','来源','进入诊断','系统运行','设备详情','进入工单']) assert(inspector.text.includes(item), `Inspector lost ${item}`);
  const selectedShot = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, '09-selected-desktop.png'), Buffer.from(selectedShot.data, 'base64'));

  assert(await clickText(client, 'aside[aria-label="告警详情"] button', '确认告警'), 'ACK action unavailable');
  await waitFor(client, `document.querySelector('[data-testid="alarm-ack-dialog"]')`, 'ACK dialog');
  const ackText = await evaluate(client, `document.querySelector('[data-testid="alarm-ack-dialog"]')?.textContent ?? ''`);
  assert(ackText.includes('不会改变告警的物理活动状态'), 'ACK dialog lost physical-state independence warning');
  await client.send('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27,nativeVirtualKeyCode:27});
  await client.send('Input.dispatchKeyEvent',{type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27,nativeVirtualKeyCode:27});
  await pause(150);

  await client.send('Page.navigate',{url:`${webURL}/?alarmView=performance`});
  await waitFor(client, `document.body.innerText.includes('当前没有可用的告警绩效统计')`, 'Performance empty state');
  const performanceText = await evaluate(client, `document.body.innerText`);
  assert(performanceText.includes('不使用前端阈值推算') && !performanceText.includes('10 alarms / 10 min'), 'Performance view invented ownerless alarm KPI');

  await client.send('Page.navigate',{url:webURL});
  await waitFor(client, `document.querySelector('[data-testid="alarm-triage-ledger"] [data-slot="table-body"] [data-slot="table-row"]')`, 'Alarm ledger reset');
  await client.send('Emulation.setDeviceMetricsOverride',{width:900,height:900,deviceScaleFactor:1,mobile:false});
  await pause(250);
  await evaluate(client, `(() => { const row=document.querySelector('[data-testid="alarm-triage-ledger"] [data-slot="table-body"] [data-slot="table-row"]'); if (!(row instanceof HTMLElement)) return false; row.focus(); row.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})); return true; })()`);
  await waitFor(client, `document.querySelector('[data-slot="sheet-content"]')?.getClientRects().length`, 'Narrow Inspector Sheet');
  const narrow = await evaluate(client, `(() => ({
    page:{scrollWidth:document.documentElement.scrollWidth,clientWidth:document.documentElement.clientWidth},
    sheet:Boolean(document.querySelector('[data-slot="sheet-content"]')?.getClientRects().length),
    desktopInspector:Boolean(document.querySelector('aside[aria-label="告警详情"]')?.getClientRects().length),
    tableContainer:(()=>{const n=document.querySelector('[aria-label="告警，可横向滚动"] [data-slot="table-container"]'); return n ? {scrollWidth:n.scrollWidth,clientWidth:n.clientWidth}:null;})(),
  }))()`);
  assert(narrow.page.scrollWidth <= narrow.page.clientWidth && narrow.sheet && !narrow.desktopInspector, `Narrow layout failed: ${JSON.stringify(narrow)}`);
  assert(narrow.tableContainer && narrow.tableContainer.scrollWidth >= narrow.tableContainer.clientWidth, 'Narrow table did not keep its own scroll region');
  await client.send('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27,nativeVirtualKeyCode:27});
  await client.send('Input.dispatchKeyEvent',{type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27,nativeVirtualKeyCode:27});
  await pause(300);
  const focusReturn = await evaluate(client, `document.activeElement?.matches('[data-slot="table-row"]') ?? false`);
  assert(focusReturn, 'Narrow Inspector did not return focus to selected Alarm row');
  const narrowShot = await client.send('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});
  await writeFile(join(outputRoot,'09-active-narrow.png'),Buffer.from(narrowShot.data,'base64'));

  await client.send('Emulation.setDeviceMetricsOverride',{width:320,height:900,deviceScaleFactor:1,mobile:false});
  await pause(250);
  const reflow = await evaluate(client, `({scrollWidth:document.documentElement.scrollWidth,clientWidth:document.documentElement.clientWidth,ledger:Boolean(document.querySelector('[data-testid="alarm-triage-ledger"]'))})`);
  assert(reflow.scrollWidth <= reflow.clientWidth && reflow.ledger, `Alarm Center failed 320px reflow: ${JSON.stringify(reflow)}`);

  const errors = client.events.filter((event)=>event.method==='Runtime.exceptionThrown'||(event.method==='Log.entryAdded'&&event.params?.entry?.level==='error')).map((event)=>event.params?.exceptionDetails?.exception?.description??event.params?.entry?.text??event.method);
  assert(errors.length===0,`Browser errors: ${errors.join(' | ')}`);
  console.log(JSON.stringify({ conclusion:'passed', webURL, screenshots:['out/alarm-center-review/09-active-desktop.png','out/alarm-center-review/09-active-narrow.png'], desktop, narrow, reflow },null,2));
} finally {
  client?.close();
  await stop(browserProcess);
  await stop(serverProcess);
  await rm(profileRoot,{recursive:true,force:true});
}
