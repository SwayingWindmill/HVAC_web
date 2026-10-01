import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const outputRoot = resolve(root, 'out/hvac-monitor-browser-audit');
const linuxProfileDir = join(tmpdir(), `hvac-monitor-browser-${process.pid}`);
const baseURL = process.env.HVAC_MONITOR_PREVIEW_URL ?? 'http://127.0.0.1:5175';
const siteId = process.env.HVAC_MONITOR_SITE_ID ?? '01940000-0001-7000-8000-000000000001';
const monitorURL = `${baseURL}/sites/${siteId}/monitor`;
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  assert(address && typeof address === 'object', 'port allocator did not expose an address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

async function createCdpClient(url) {
  const socket = new WebSocket(url);
  await once(socket, 'open');
  let nextId = 1;
  const pending = new Map();
  const listeners = new Map();
  socket.on('message', (data) => {
    const message = JSON.parse(String(data));
    if (message.id) {
      const request = pending.get(message.id);
      if (!request) return;
      pending.delete(message.id);
      if (message.error) request.reject(new Error(message.error.message));
      else request.resolve(message.result);
      return;
    }
    for (const listener of listeners.get(message.method) ?? []) listener(message.params);
  });
  return {
    send(method, params = {}) {
      return new Promise((resolveRequest, rejectRequest) => {
        const id = nextId++;
        pending.set(id, { resolve: resolveRequest, reject: rejectRequest });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
    on(method, listener) {
      const current = listeners.get(method) ?? [];
      current.push(listener);
      listeners.set(method, current);
    },
    close() { socket.close(); },
  };
}

async function evaluate(client, expression) {
  const result = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text ?? 'browser evaluation failed');
  return result.result?.value;
}

async function waitForCondition(client, expression, label, timeoutMs = 15_000) {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    if (await evaluate(client, expression)) return;
    await pause(100);
  }
  throw new Error(`Timed out waiting for ${label}`);
}

async function navigate(client, url) {
  await client.send('Page.navigate', { url });
  await waitForCondition(client, `document.querySelector('[data-testid="hvac-control-workspace"]') !== null`, `Control Desk monitor at ${url}`);
  await pause(350);
}

async function captureScreenshot(client, filename) {
  const result = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, filename), Buffer.from(result.data, 'base64'));
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');
}

const browserPath = resolveLinuxBrowserExecutable();
const browserProfile = linuxProfileDir;
const debugPort = await findAvailablePort();
const debugHost = '127.0.0.1';
const debugAddress = '127.0.0.1';
let browserProcess;
let client;
const runtimeErrors = [];
const networkErrors = [];
const assertions = [];
const report = { browserPath, viewport: { width: 1672, height: 941 }, surfaces: {} };

function pass(label) {
  assertions.push(label);
  console.log(`PASS ${label}`);
}

try {
  const previewResponse = await fetch(monitorURL);
  assert(previewResponse.ok, `frontend preview unavailable: ${previewResponse.status}`);
  await mkdir(outputRoot, { recursive: true });
  await mkdir(linuxProfileDir, { recursive: true });

  browserProcess = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    '--hide-scrollbars',
    '--window-size=1672,941',
    `--remote-debugging-address=${debugAddress}`,
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${browserProfile}`,
    'about:blank',
  ], { stdio: 'ignore' });

  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      if ((await fetch(`http://${debugHost}:${debugPort}/json/version`)).ok) break;
    } catch {}
    if (attempt === 299) throw new Error(`Browser debugger did not become ready from ${browserPath}`);
    await pause(100);
  }

  const pages = await fetch(`http://${debugHost}:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  client = await createCdpClient(page.webSocketDebuggerUrl);
  const requestsById = new Map();
  client.on('Runtime.exceptionThrown', (params) => {
    const details = params.exceptionDetails;
    const description = details?.exception?.description ?? details?.exception?.value ?? details?.text ?? 'runtime exception';
    const frame = details?.stackTrace?.callFrames?.[0];
    runtimeErrors.push(`${description}${frame?.url ? ` @ ${frame.url}:${frame.lineNumber + 1}` : ''}`);
  });
  client.on('Log.entryAdded', (params) => {
    if (params.entry?.level === 'error') runtimeErrors.push(`${params.entry.text}${params.entry.url ? ` @ ${params.entry.url}` : ''}`);
  });
  client.on('Network.requestWillBeSent', (params) => {
    requestsById.set(params.requestId, { url: params.request?.url ?? '', method: params.request?.method ?? '' });
  });
  client.on('Network.responseReceived', (params) => {
    const status = params.response?.status ?? 0;
    if (status < 400) return;
    const request = requestsById.get(params.requestId);
    networkErrors.push(`${status} ${request?.method ?? ''} ${params.response?.url ?? request?.url ?? ''}`);
  });
  await client.send('Runtime.enable');
  await client.send('Network.enable');
  await client.send('Page.enable');
  await client.send('Log.enable');
  await client.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 941, deviceScaleFactor: 1, mobile: false });

  await navigate(client, monitorURL);
  await waitForCondition(client, `document.querySelectorAll('.hvac-x6-node').length >= 6`, 'Control Desk X6 topology nodes');
  const topology = await evaluate(client, `(() => {
    const box = (selector) => { const element = document.querySelector(selector); if (!element) return null; const rect = element.getBoundingClientRect(); return { x: rect.x, y: rect.y, width: rect.width, height: rect.height, bottom: rect.bottom, right: rect.right }; };
    const root = document.querySelector('[data-testid="hvac-control-workspace"]');
    return {
      text: root?.innerText ?? '',
      tabs: document.querySelectorAll('.control-monitor-tab').length,
      statusFacts: document.querySelectorAll('[data-testid="hvac-monitor-summary-fact"]').length,
      nodes: document.querySelectorAll('.hvac-x6-node').length,
      evidencePanels: document.querySelectorAll('.control-monitor-evidence-panel').length,
      legacyKpis: document.querySelectorAll('.hvac-ref__kpis').length,
      legacyProCards: document.querySelectorAll('.ant-pro-card').length,
      legacyDrawer: document.querySelectorAll('.hvac-monitor__drawer .ant-drawer-content').length,
      legacyAntDom: document.querySelectorAll('[class^="ant-"], [class*=" ant-"]').length,
      workspace: box('[data-testid="hvac-monitor-main-workspace"]'),
      canvas: box('[data-testid="hvac-monitor-main-canvas"]'),
      dock: box('.control-monitor-evidence-dock'),
      shell: document.querySelector('[data-slot="sidebar"]') !== null && document.querySelector('header') !== null,
      horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
    };
  })()`);
  assert(['系统拓扑', '异常定位', '能流证据', '当前运行', '当前告警', '继续调查'].every((label) => topology.text.includes(label)), 'Control Desk monitor responsibilities are incomplete');
  assert(topology.tabs === 3, `expected 3 workspace tabs, got ${topology.tabs}`);
  assert(topology.statusFacts === 5, `expected 5 compact status facts, got ${topology.statusFacts}`);
  assert(topology.nodes >= 6, `engineering topology is incomplete: ${topology.nodes} nodes`);
  assert(topology.evidencePanels === 3, `evidence dock drifted: ${topology.evidencePanels} panels`);
  assert(topology.legacyKpis === 0 && topology.legacyProCards === 0 && topology.legacyDrawer === 0 && topology.legacyAntDom === 0, 'monitor restored legacy Ant/Pro UI');
  assert(topology.workspace && topology.canvas && topology.dock && topology.workspace.width > 1100, 'Control Desk monitor geometry is too constrained');
  assert(topology.shell, 'monitor route escaped the standard application shell');
  assert(!topology.horizontalOverflow, 'monitor created document-level horizontal overflow');
  for (const forbidden of ['traceId', 'schemaVersion', 'CERT-DEVICE', 'CONNECTED', '权威 Summary']) {
    assert(!topology.text.includes(forbidden), `operator surface leaked internal term: ${forbidden}`);
  }
  report.surfaces.topology = topology;
  pass('Control Desk topology workspace');
  await captureScreenshot(client, '01-control-topology.png');

  await evaluate(client, `document.querySelector('.hvac-x6-node[data-device-id]')?.click()`);
  await waitForCondition(client, `document.querySelector('.control-monitor-detail-sheet') !== null`, 'Device Detail Sheet');
  const inspector = await evaluate(client, `(() => {
    const box = (selector) => { const element = document.querySelector(selector); if (!element) return null; const rect = element.getBoundingClientRect(); return { x: rect.x, y: rect.y, width: rect.width, height: rect.height }; };
    return {
      text: document.querySelector('.control-monitor-detail-sheet')?.innerText ?? '',
      sheet: box('.control-monitor-detail-sheet'),
      canvas: box('.control-monitor-canvas'),
      overlay: document.querySelector('[data-slot="sheet-overlay"]') !== null,
      legacyDrawer: document.querySelector('.ant-drawer-content') !== null,
    };
  })()`);
  assert(inspector.sheet?.width >= 480 && inspector.sheet?.width <= 640, `Device Detail Sheet width drifted: ${inspector.sheet?.width ?? 0}`);
  assert(inspector.canvas?.width === topology.canvas?.width, 'Device Detail Sheet changed the system canvas width');
  assert(!inspector.overlay, 'Desktop Device Detail Sheet should be non-modal/no-overlay');
  assert(!inspector.legacyDrawer, 'device inspection opened a legacy Drawer');
  assert(['运行', '连接', '数据新鲜度', '数据质量', '当前关键值', '活动告警', '打开完整详情'].every((label) => inspector.text.includes(label)), 'Device Detail Sheet evidence is incomplete');
  report.surfaces.inspector = inspector;
  pass('Device Detail Sheet preserves system context');
  await captureScreenshot(client, '02-device-detail-sheet.png');

  await navigate(client, `${monitorURL}?view=anomaly`);
  await waitForCondition(client, `document.querySelector('[data-testid="hvac-control-workspace"]')?.getAttribute('data-view') === 'anomaly'`, 'anomaly workspace');
  const anomaly = await evaluate(client, `({
    text: document.querySelector('[data-testid="hvac-control-workspace"]')?.innerText ?? '',
    nodes: document.querySelectorAll('.hvac-x6-node').length,
    sameCanvas: document.querySelector('[data-testid="hvac-x6-topology"]') !== null,
    evidenceDock: document.querySelector('.control-monitor-evidence-dock') !== null,
  })`);
  assert(anomaly.sameCanvas && anomaly.nodes >= 6, 'anomaly view abandoned the shared spatial topology');
  assert(anomaly.evidenceDock && anomaly.text.includes('异常对象与系统位置'), 'anomaly view lost Control Desk evidence structure');
  report.surfaces.anomaly = anomaly;
  pass('Anomaly view preserves spatial memory');
  await captureScreenshot(client, '03-anomaly-canvas.png');

  await navigate(client, `${monitorURL}?view=energy`);
  await waitForCondition(client, `document.querySelector('[data-testid="hvac-monitor-energy-evidence"]') !== null`, 'energy evidence workspace');
  const energy = await evaluate(client, `({
    text: document.querySelector('[data-testid="hvac-monitor-energy-evidence"]')?.innerText ?? '',
    flowNodes: document.querySelectorAll('.control-monitor-energy__node').length,
    dimensions: document.querySelectorAll('.control-monitor-energy__switch button').length,
    evidenceDock: document.querySelector('.control-monitor-evidence-dock') !== null,
    legacyEnergy: document.querySelector('.hvac-monitor__energy-flow-visual') !== null,
  })`);
  assert(energy.flowNodes === 4, `energy evidence chain drifted: ${energy.flowNodes}`);
  assert(energy.dimensions === 3, `energy evidence dimensions drifted: ${energy.dimensions}`);
  assert(energy.evidenceDock && !energy.legacyEnergy, 'energy view restored superseded standalone page geometry');
  assert(energy.text.includes('线宽不表达未经验证的流量或能量分配'), 'energy view lost anti-fabrication explanation');
  report.surfaces.energy = energy;
  pass('Energy evidence avoids fabricated allocation');
  await captureScreenshot(client, '04-energy-evidence.png');

  assert(runtimeErrors.length === 0, `browser runtime errors:\n${runtimeErrors.join('\n')}`);
  assert(networkErrors.length === 0, `browser network errors:\n${networkErrors.join('\n')}`);
  pass('No browser runtime or network errors');

  report.assertions = assertions;
  report.runtimeErrors = runtimeErrors;
  report.networkErrors = networkErrors;
  await writeFile(join(outputRoot, 'report.json'), JSON.stringify(report, null, 2));
  console.log(`HVAC Control Desk browser audit passed with ${assertions.length} assertions using ${browserPath}`);
} finally {
  client?.close();
  await stopBrowser(browserProcess);
  await rm(linuxProfileDir, { recursive: true, force: true });
}
