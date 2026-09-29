import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createHTTPServer } from 'node:http';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { createServer as createViteServer } from 'vite';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/comfort-review');
const outputRoot = resolve(root, 'out/comfort-review');
const linuxProfileDir = join(tmpdir(), `comfort-review-${process.pid}`);
const tenantId = '01950000-0000-7000-8000-000000000001';
const siteId = '01950000-0001-7000-8000-000000000001';
const routePolicyRevision = 'comfort-review-policy:1';
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function id(group, index) {
  return `01950000-${String(group).padStart(4, '0')}-7000-8000-${String(index).padStart(12, '0')}`;
}

const createdAt = '2026-08-01T00:00:00.000Z';
const buildingId = id(10, 1);
const floor1Id = id(11, 1);
const floor2Id = id(11, 2);
const comfortSpaces = [
  { id: id(12, 1), parentSpaceId: floor1Id, code: 'F1-ZA', displayName: '开放办公区 A', spaceType: 'ZONE' },
  { id: id(12, 2), parentSpaceId: floor1Id, code: 'F1-ZB', displayName: '开放办公区 B', spaceType: 'ZONE' },
  { id: id(12, 3), parentSpaceId: floor1Id, code: 'F1-MR', displayName: '会议室 101', spaceType: 'ROOM' },
  { id: id(12, 4), parentSpaceId: floor2Id, code: 'F2-ZA', displayName: '研发区 A', spaceType: 'ZONE' },
  { id: id(12, 5), parentSpaceId: floor2Id, code: 'F2-ZB', displayName: '研发区 B', spaceType: 'ZONE' },
  { id: id(12, 6), parentSpaceId: floor2Id, code: 'F2-TS', displayName: '租户共享区', spaceType: 'TENANT_SPACE' },
  { id: id(12, 7), parentSpaceId: floor2Id, code: 'F2-LAB', displayName: '测试实验室', spaceType: 'ROOM' },
  { id: id(12, 8), parentSpaceId: floor2Id, code: 'F2-QUIET', displayName: '静音间', spaceType: 'ROOM' },
];

const spaces = [
  { id: buildingId, parentSpaceId: null, code: 'RND', displayName: '研发楼', spaceType: 'BUILDING' },
  { id: floor1Id, parentSpaceId: buildingId, code: 'F1', displayName: '1F', spaceType: 'FLOOR' },
  { id: floor2Id, parentSpaceId: buildingId, code: 'F2', displayName: '2F', spaceType: 'FLOOR' },
  ...comfortSpaces,
].map((space) => ({
  ...space,
  tenantId,
  siteId,
  status: 'ACTIVE',
  revision: 1,
  createdAt,
  updatedAt: createdAt,
}));

const deviceDefinitions = [
  { spaceIndex: 0, label: '开放办公区 A 环境控制器', scenario: 'good', values: { temperature: 22.8, humidity: 46.0 } },
  { spaceIndex: 1, label: '开放办公区 B 环境控制器', scenario: 'stale', values: { temperature: 25.6, humidity: 48.0 } },
  { spaceIndex: 2, label: '会议室 101 环境控制器', scenario: 'partial', values: { temperature: 24.9, humidity: 58.0 } },
  { spaceIndex: 3, label: '研发区 A 温度控制器', scenario: 'good', values: { temperature: 23.4 } },
  { spaceIndex: 4, label: '研发区 B 湿度控制器', scenario: 'good', values: { humidity: 51.0 } },
  { spaceIndex: 5, label: '租户共享区环境控制器', scenario: 'missing', values: { temperature: 23.0, humidity: 50.0 } },
  { spaceIndex: 6, label: '测试实验室环境控制器', scenario: 'good', values: { temperature: 21.8, humidity: 42.0 } },
  { spaceIndex: 7, label: '静音间网关', scenario: 'none', values: {} },
  { spaceIndex: 0, label: '开放办公区 A 辅助温度传感器', scenario: 'good', values: { temperature: 23.2 } },
];

const devices = deviceDefinitions.map((definition, index) => ({
  id: id(20, index + 1),
  tenantId,
  siteId,
  code: `ENV-${String(index + 1).padStart(2, '0')}`,
  displayName: definition.label,
  deviceType: 'GENERIC',
  status: 'ACTIVE',
  revision: 1,
  createdAt,
  updatedAt: createdAt,
}));

const telemetryPoints = [];
let pointIndex = 0;
for (const [deviceIndex, definition] of deviceDefinitions.entries()) {
  for (const pointCode of Object.keys(definition.values)) {
    pointIndex += 1;
    telemetryPoints.push({
      id: id(30, pointIndex),
      tenantId,
      siteId,
      reportingDeviceId: devices[deviceIndex].id,
      sensorId: null,
      pointCode,
      sourceKey: pointCode,
      displayName: pointCode === 'temperature' ? '空间温度' : '相对湿度',
      pointType: 'TELEMETRY',
      valueType: 'NUMBER',
      unit: pointCode === 'temperature' ? 'Cel' : '%RH',
      writable: false,
      sampleIntervalMs: 60_000,
      publishIntervalMs: 60_000,
      staleAfterMs: 300_000,
      counterDecreaseMode: null,
      counterRolloverModulus: null,
      sourceMetadata: {},
      status: 'ACTIVE',
      revision: 1,
      createdAt,
      updatedAt: createdAt,
    });
  }
}

const relationships = deviceDefinitions.map((definition, index) => ({
  id: id(40, index + 1),
  tenantId,
  siteId,
  fromType: 'DEVICE',
  fromId: devices[index].id,
  toType: 'SPACE',
  toId: comfortSpaces[definition.spaceIndex].id,
  role: 'INSTALLED_IN',
  status: 'ACTIVE',
  validFrom: createdAt,
  validTo: null,
  revision: 1,
  createdAt,
  updatedAt: createdAt,
}));

const model = {
  schemaVersion: 2,
  tenantId,
  siteId,
  spaces,
  assets: [],
  devices,
  sensors: [],
  telemetryPoints,
  relationships,
  counts: {
    spaces: spaces.length,
    assets: 0,
    deviceEndpoints: devices.length,
    physicalSensors: 0,
    points: telemetryPoints.length,
  },
};

const definitionByDeviceId = new Map(devices.map((device, index) => [device.id, deviceDefinitions[index]]));

function snapshotFor(target) {
  const definition = definitionByDeviceId.get(target.deviceId);
  const now = Date.now();
  const stale = definition?.scenario === 'stale';
  const missing = definition?.scenario === 'missing';
  const partial = definition?.scenario === 'partial';
  const none = definition?.scenario === 'none';
  const sampledAt = new Date(now - (stale ? 12 * 60_000 : 45_000)).toISOString();
  const receivedAt = new Date(now - (stale ? 11 * 60_000 : 30_000)).toISOString();
  const values = target.keys.map((key) => {
    if (missing) return { key, state: 'MISSING', freshness: 'MISSING', missingReason: 'NEVER_OBSERVED', policyRevision: 8 };
    const value = definition?.values[key];
    return {
      key,
      state: 'PRESENT',
      value,
      valueType: 'NUMBER',
      unit: key === 'temperature' ? 'Cel' : '%RH',
      sampledAt,
      receivedAt,
      freshness: stale ? 'STALE' : 'FRESH',
      quality: partial ? 'PARTIAL' : 'GOOD',
      qualityReasons: partial ? ['SOURCE_UNTRUSTED'] : [],
      policyRevision: 8,
    };
  });
  if (none) {
    return {
      schemaVersion: 1,
      deviceId: target.deviceId,
      tenantId,
      siteId,
      businessRevision: 800,
      evaluatedAt: new Date(now).toISOString(),
      evaluationAvailability: 'AVAILABLE',
      availabilityReasons: [],
      presence: { applicability: 'NOT_APPLICABLE', currentState: null, lastSeenAt: null, policyRevision: 8, lastKnown: null },
      telemetryReadiness: 'NOT_APPLICABLE',
      displayState: null,
      values: [],
    };
  }
  return {
    schemaVersion: 1,
    deviceId: target.deviceId,
    tenantId,
    siteId,
    businessRevision: 800,
    evaluatedAt: new Date(now).toISOString(),
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: receivedAt, policyRevision: 8, lastKnown: null },
    telemetryReadiness: missing ? 'INCOMPLETE' : stale ? 'DEGRADED' : 'CURRENT',
    displayState: missing ? 'UNKNOWN' : stale ? 'STALE' : 'ONLINE',
    values,
  };
}

function problem(status, code, detail) {
  return {
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    instance: '/api/v1/comfort-review',
    code,
    traceId: '0123456789abcdef0123456789abcdef',
    retryable: false,
  };
}

async function readJson(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function writeJson(response, status, payload) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'private, no-store',
    'x-route-policy-revision': routePolicyRevision,
  });
  response.end(JSON.stringify(payload));
}

function createGateway() {
  return createHTTPServer(async (request, response) => {
    const url = new URL(request.url ?? '/', 'http://comfort-review.local');
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/asset-model`) {
      writeJson(response, 200, model);
      return;
    }
    if (request.method === 'POST' && url.pathname === '/api/v1/telemetry/observation-snapshots:batchGet') {
      const payload = await readJson(request);
      writeJson(response, 200, {
        schemaVersion: 1,
        items: payload.requests.map((target) => ({ requestId: target.requestId, deviceId: target.deviceId, status: 'OK', snapshot: snapshotFor(target) })),
      });
      return;
    }
    writeJson(response, 404, problem(404, 'RESOURCE_NOT_FOUND', `No comfort review fixture for ${url.pathname}`));
  });
}

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

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    const events = [];
    let nextId = 0;
    socket.on('open', () => resolveClient({
      events,
      send(method, params = {}) {
        const requestId = ++nextId;
        socket.send(JSON.stringify({ id: requestId, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(requestId, { resolveCommand, rejectCommand }));
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
      if (message.error) command.rejectCommand(new Error(message.error.message));
      else command.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const result = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text ?? 'Browser evaluation failed');
  return result.result.value;
}

async function waitFor(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      last = await evaluate(client, expression);
      if (last) return last;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ text: document.body?.innerText?.slice(0, 6000) ?? '', html: document.body?.innerHTML?.slice(0, 3000) ?? '' })`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready; last=${JSON.stringify(last)} diagnostic=${JSON.stringify(diagnostic)}`);
}

async function clickText(client, selector, text) {
  return evaluate(client, `(() => {
    const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)})).find((candidate) => candidate.textContent?.includes(${JSON.stringify(text)}));
    if (!(node instanceof HTMLElement)) return false;
    node.click();
    return true;
  })()`);
}

async function pointerClickText(client, selector, text) {
  return evaluate(client, `(() => {
    const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)})).find((candidate) => candidate.textContent?.includes(${JSON.stringify(text)}));
    if (!(node instanceof HTMLElement)) return false;
    node.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0, ctrlKey: false }));
    node.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true, button: 0, ctrlKey: false }));
    node.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, ctrlKey: false }));
    return true;
  })()`);
}

async function capture(client, filename) {
  const screenshot = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, filename), Buffer.from(screenshot.data, 'base64'));
}

async function stopChild(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');
}

const browserPath = resolveLinuxBrowserExecutable();

const gatewayPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const gateway = createGateway();
const browserProfileDir = linuxProfileDir;
const debugPort = await findAvailablePort();
let viteServer;
let browserProcess;
let cdp;

try {
  await mkdir(outputRoot, { recursive: true });
  await mkdir(linuxProfileDir, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    gateway.once('error', rejectListen);
    gateway.listen(gatewayPort, '127.0.0.1', resolveListen);
  });

  viteServer = await createViteServer({
    root: fixtureRoot,
    publicDir: resolve(root, 'apps/hvac-web/public'),
    configFile: false,
    logLevel: 'error',
    plugins: [react(), tailwindcss()],
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '0.0.0.0',
      port: Number(process.env.COMFORT_REVIEW_PORT ?? 0),
      strictPort: Boolean(process.env.COMFORT_REVIEW_PORT),
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Comfort review Vite server has no address');
  const webURL = `http://127.0.0.1:${viteAddress.port}`;

  if (process.argv.includes('--serve')) {
    console.log(JSON.stringify({ mode: 'serve', url: webURL, gateway: gatewayURL }));
    await new Promise(() => {});
  }

  browserProcess = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--disable-extensions', '--disable-sync', '--disable-background-networking', '--no-sandbox',
    '--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--remote-debugging-address=0.0.0.0', '--window-size=1672,941',
    `--remote-debugging-port=${debugPort ?? 0}`, `--user-data-dir=${browserProfileDir}`, 'about:blank',
  ], { stdio: 'ignore' });

  for (let attempt = 0; attempt < 300; attempt += 1) {
    try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
    if (attempt === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }

  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdp = await createCdpClient(page.webSocketDebuggerUrl);
  await cdp.send('Runtime.enable');
  await cdp.send('Network.enable');
  await cdp.send('Page.enable');
  await cdp.send('Log.enable');
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 941, deviceScaleFactor: 1, mobile: false });
  await cdp.send('Page.navigate', { url: webURL });

  await waitFor(
    cdp,
    `Boolean(document.querySelector('[data-testid="comfort-workspace"]')) && document.body.innerText.includes('当前环境数据已更新') && document.querySelectorAll('[aria-label="空间环境"] [data-slot="table-body"] [data-slot="table-row"]').length === 8`,
    'comfort ledger',
  );
  await pause(400);

  const desktop = await evaluate(cdp, `(() => {
    const rect = (selector) => {
      const node = document.querySelector(selector);
      if (!(node instanceof HTMLElement)) return null;
      const value = node.getBoundingClientRect();
      return { top: Math.round(value.top), bottom: Math.round(value.bottom), height: Math.round(value.height), width: Math.round(value.width) };
    };
    return {
      viewport: { width: innerWidth, height: innerHeight },
      body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      sidebar: rect('[data-slot="sidebar"]'),
      header: rect('header'),
      context: rect('[aria-label="舒适与室内环境上下文"]'),
      headerTitle: rect('[data-testid="real-shell-surface-identity"] h1'),
      summary: rect('[aria-label="空间环境概况"]'),
      ledger: rect('[aria-label="空间环境"]'),
      rowCount: document.querySelectorAll('[aria-label="空间环境"] [data-slot="table-body"] [data-slot="table-row"]').length,
      h1Count: document.querySelectorAll('h1').length,
      headerH1Count: document.querySelectorAll('[data-testid="real-shell-surface-identity"] h1').length,
      selectCount: document.querySelectorAll('[data-slot="select-trigger"]').length,
      inputGroupCount: document.querySelectorAll('[data-slot="input-group"]').length,
      tabsCount: document.querySelectorAll('[data-slot="tabs-list"]').length,
      antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-select, .ant-tabs').length,
      cardCount: document.querySelectorAll('[data-slot="card"]').length,
      text: document.body.innerText.slice(0, 12000),
    };
  })()`);

  assert(desktop.sidebar?.width === 256 && desktop.header?.height === 56, `Comfort workspace is not using the shared AppShell geometry: ${JSON.stringify({ sidebar: desktop.sidebar, header: desktop.header })}`);
  assert(desktop.h1Count === 1 && desktop.headerH1Count === 1 && desktop.headerTitle, `Comfort surface title must appear exactly once in AppHeader: ${JSON.stringify({ h1Count: desktop.h1Count, headerH1Count: desktop.headerH1Count, headerTitle: desktop.headerTitle })}`);
  assert(desktop.context?.top < 180 && desktop.summary?.top < 420 && desktop.ledger?.top < 720, `Comfort hierarchy drifted too far below the first viewport: ${JSON.stringify({ context: desktop.context, summary: desktop.summary, ledger: desktop.ledger })}`);
  assert(desktop.rowCount === 8, `Comfort ledger did not render 8 spaces: ${desktop.rowCount}`);
  assert(desktop.selectCount >= 2 && desktop.inputGroupCount >= 1 && desktop.tabsCount === 1, `Comfort controls are not composed from shadcn Tabs/Select/InputGroup: ${JSON.stringify({ selectCount: desktop.selectCount, inputGroupCount: desktop.inputGroupCount, tabsCount: desktop.tabsCount })}`);
  assert(desktop.antCount === 0, 'Comfort workspace rendered legacy Ant DOM');
  assert(desktop.cardCount <= 2, `Comfort workspace regressed into a card wall: ${desktop.cardCount}`);
  assert(desktop.body.scrollWidth <= desktop.body.clientWidth, 'Comfort workspace has page-level horizontal overflow');
  for (const required of ['当前只能评估环境数据，不能判断舒适或 IAQ 达标', '占用上下文', '运营目标', '未接入', '温度', '湿度', '数据状态']) assert(desktop.text.includes(required), `Comfort workspace lost required fact: ${required}`);
  for (const forbidden of ['舒适度达标率', '健康分', 'IAQ Score']) assert(!desktop.text.includes(forbidden), `Comfort workspace introduced forbidden claim: ${forbidden}`);
  assert(desktop.text.includes('2 个测点'), 'Comfort workspace averaged or hid multiple temperature sensors instead of preserving measurement plurality');
  assert(!desktop.text.includes(siteId), 'Comfort workspace leaked the internal Site UUID');
  await capture(cdp, 'comfort-ledger-desktop.png');

  assert(await clickText(cdp, '[aria-label="空间环境"] button', '开放办公区 A'), 'Comfort space inspector trigger was unavailable');
  await waitFor(cdp, `Boolean(document.querySelector('[data-slot="sheet-content"]')) && document.body.innerText.includes('当前无法判断目标偏离') && document.body.innerText.includes('温度测点')`, 'comfort space inspector');
  const inspector = await evaluate(cdp, `(() => ({
    width: Math.round(document.querySelector('[data-slot="sheet-content"]')?.getBoundingClientRect().width ?? 0),
    text: document.querySelector('[data-slot="sheet-content"]')?.textContent ?? '',
    bodyScrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }))()`);
  assert(inspector.width >= 480 && inspector.width <= 560, `Comfort inspector width drifted: ${inspector.width}`);
  assert(inspector.bodyScrollWidth <= inspector.clientWidth, 'Comfort inspector caused page-level horizontal overflow');
  for (const required of ['占用上下文', '运营目标', '未接入', '温度测点', '湿度测点', '关联设备', '当前无法判断目标偏离']) assert(inspector.text.includes(required), `Comfort inspector lost required fact: ${required}`);
  assert(inspector.text.includes('22.8') && inspector.text.includes('23.2'), 'Comfort inspector did not preserve both temperature sensor observations');
  await capture(cdp, 'comfort-inspector-desktop.png');

  await cdp.send('Runtime.evaluate', { expression: `document.querySelector('[data-slot="sheet-content"] [data-slot="sheet-close"]')?.click()` });
  await waitFor(cdp, `!document.querySelector('[data-slot="sheet-content"]')`, 'comfort space inspector close');
  const airQualityTab = await evaluate(cdp, `(() => {
    const node = Array.from(document.querySelectorAll('[data-slot="tabs-trigger"]')).find((candidate) => candidate.textContent?.includes('空气质量'));
    return node ? { disabled: node.hasAttribute('disabled') || node.getAttribute('aria-disabled') === 'true', text: node.textContent ?? '' } : null;
  })()`);
  assert(airQualityTab?.disabled && airQualityTab.text.includes('未接入'), `Air quality capability must be visible but disabled when IAQ is unavailable: ${JSON.stringify(airQualityTab)}`);
  await capture(cdp, 'comfort-iaq-disabled.png');

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 768, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(300);
  const narrow = await evaluate(cdp, `(() => ({
    viewport: { width: innerWidth, height: innerHeight },
    body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    ledgerTop: Math.round(document.querySelector('[aria-label="空间环境"]')?.getBoundingClientRect().top ?? 0),
    rowCount: document.querySelectorAll('[aria-label="空间环境"] [data-slot="table-body"] [data-slot="table-row"]').length,
    antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-select, .ant-tabs').length,
  }))()`);
  assert(narrow.body.scrollWidth <= narrow.body.clientWidth, `Comfort workspace overflows at 768px: ${JSON.stringify(narrow.body)}`);
  assert(narrow.rowCount === 8 && narrow.antCount === 0, 'Narrow comfort ledger lost rows or shadcn composition');
  await capture(cdp, 'comfort-ledger-narrow.png');

  const runtimeErrors = cdp.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.exceptionDetails?.text ?? event.params?.entry?.text ?? event.method);
  assert(runtimeErrors.length === 0, `Comfort review emitted runtime errors: ${JSON.stringify(runtimeErrors)}`);

  const evidence = {
    conclusion: 'passed',
    source: 'comfort-space-registry-and-current-telemetry-fixture',
    screenshots: [
      'out/comfort-review/comfort-ledger-desktop.png',
      'out/comfort-review/comfort-inspector-desktop.png',
      'out/comfort-review/comfort-iaq-disabled.png',
      'out/comfort-review/comfort-ledger-narrow.png',
    ],
    desktop,
    inspector,
    narrow,
    runtimeErrors,
  };
  await writeFile(join(outputRoot, 'evidence.json'), `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  console.log(JSON.stringify(evidence, null, 2));
} finally {
  cdp?.close();
  await stopChild(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => gateway.close(() => resolveClose()));
  await rm(linuxProfileDir, { recursive: true, force: true });
}
