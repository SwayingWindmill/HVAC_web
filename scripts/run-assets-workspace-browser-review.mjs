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
import { buildCertificationInventory, certificationId } from './real-assets-certification-lib.mjs';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/assets-workspace-review');
const outputRoot = resolve(root, 'out/assets-workspace-review');
const linuxProfileDir = join(tmpdir(), `assets-workspace-review-${process.pid}`);
const tenantId = '01940000-0000-7000-8000-000000000001';
const siteId = '01940000-0001-7000-8000-000000000001';
const routePolicyRevision = 'assets-workspace-review-policy:1';
const inventory = buildCertificationInventory({ tenantId, siteId, namespace: '01940000' });
const scenarioByDevice = new Map(inventory.devices.map((device) => [device.id, device.certificationScenario]));
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const pointDefinitions = [
  ['chiller.run_state', '运行状态', 'STATE', 'STRING', null],
  ['chiller.power', '主机功率', 'TELEMETRY', 'NUMBER', 'kW'],
  ['chiller.cop', '主机 COP', 'TELEMETRY', 'NUMBER', null],
  ['chiller.cooling_capacity', '制冷量', 'TELEMETRY', 'NUMBER', 'kW'],
];

function publicDevice(device) {
  const { certificationScenario: _scenario, ...publicFields } = device;
  return publicFields;
}

function assetModel() {
  const now = '2026-08-01T00:00:00.000Z';
  const plantRoomId = certificationId(0x08, 1, '01940000');
  const spaces = [{
    id: plantRoomId,
    tenantId,
    siteId,
    parentSpaceId: null,
    code: 'TOKYO-PLANT',
    displayName: '中央机房',
    spaceType: 'PLANT_ROOM',
    status: 'ACTIVE',
    revision: 1,
    createdAt: now,
    updatedAt: now,
  }];
  const devices = inventory.devices.map(publicDevice);
  const telemetryPoints = inventory.devices.flatMap((device, deviceIndex) => pointDefinitions.map(([sourceKey, displayName, pointType, valueType, unit], keyIndex) => ({
    id: certificationId(0x40 + keyIndex, deviceIndex + 1, '01940000'),
    tenantId,
    siteId,
    reportingDeviceId: device.id,
    sensorId: null,
    pointCode: sourceKey.replaceAll('.', '_'),
    sourceKey,
    displayName,
    pointType,
    valueType,
    unit,
    writable: false,
    sampleIntervalMs: 1000,
    publishIntervalMs: 1000,
    staleAfterMs: 5000,
    counterDecreaseMode: null,
    counterRolloverModulus: null,
    sourceMetadata: {},
    status: 'ACTIVE',
    revision: 1,
    createdAt: now,
    updatedAt: now,
  })));
  const relationships = [
    ...inventory.assets.map((asset, index) => ({
      id: certificationId(0x70, index + 1, '01940000'),
      tenantId,
      siteId,
      fromType: 'ASSET',
      fromId: asset.id,
      toType: 'SPACE',
      toId: plantRoomId,
      role: 'INSTALLED_IN',
      status: 'ACTIVE',
      validFrom: now,
      validTo: null,
      revision: 1,
      createdAt: now,
      updatedAt: now,
    })),
    ...inventory.bindings.map((binding) => ({
      id: binding.id,
      tenantId,
      siteId,
      fromType: 'DEVICE',
      fromId: binding.deviceId,
      toType: 'ASSET',
      toId: binding.assetId,
      role: binding.bindingRole,
      status: binding.status,
      validFrom: binding.validFrom,
      validTo: binding.validTo,
      revision: binding.revision,
      createdAt: binding.createdAt,
      updatedAt: binding.updatedAt,
    })),
  ];
  return {
    schemaVersion: 2,
    tenantId,
    siteId,
    spaces,
    assets: inventory.assets,
    devices,
    sensors: [],
    telemetryPoints,
    relationships,
    counts: {
      spaces: spaces.length,
      assets: inventory.assets.length,
      deviceEndpoints: devices.length,
      physicalSensors: 0,
      points: telemetryPoints.length,
    },
  };
}

function presentValue(key, index, scenario) {
  if (key.endsWith('run_state')) return { value: scenario === 'offline' ? 'STOPPED' : 'RUNNING', valueType: 'STRING', unit: null };
  if (scenario === 'valid-zero') return { value: 0, valueType: 'NUMBER', unit: key.endsWith('cop') ? null : 'kW' };
  if (key.endsWith('cop')) return { value: 4.6 + ((index % 5) / 10), valueType: 'NUMBER', unit: null };
  if (key.includes('capacity')) return { value: 500 + index, valueType: 'NUMBER', unit: 'kW' };
  return { value: 20 + (index % 30), valueType: 'NUMBER', unit: 'kW' };
}

function snapshotFor(target, device, index, scenario) {
  const unknown = scenario === 'unknown-device-type';
  const neverObserved = scenario === 'never-observed';
  const stale = scenario === 'stale';
  const suspect = scenario === 'suspect';
  const offline = scenario === 'offline';
  const evaluatedMs = Date.now();
  const lastSeenMs = offline ? evaluatedMs - (18 * 60_000) : evaluatedMs - (8_000 + ((index % 5) * 1_000));
  const sampledMs = stale ? evaluatedMs - 90_000 : lastSeenMs - 1_000;
  const receivedMs = stale ? sampledMs + 1_000 : lastSeenMs;
  const values = target.keys.map((key) => {
    if (neverObserved) return { key, state: 'MISSING', freshness: 'MISSING', missingReason: 'NEVER_OBSERVED', policyRevision: 14 };
    const projected = presentValue(key, index, scenario);
    return {
      key,
      state: 'PRESENT',
      ...projected,
      sampledAt: new Date(sampledMs).toISOString(),
      receivedAt: new Date(receivedMs).toISOString(),
      freshness: stale ? 'STALE' : 'FRESH',
      quality: suspect ? 'PARTIAL' : 'GOOD',
      qualityReasons: suspect ? ['SOURCE_LAG_EXCEEDED'] : [],
      policyRevision: 14,
    };
  });
  return {
    schemaVersion: 1,
    deviceId: device.id,
    tenantId,
    siteId,
    businessRevision: 10000 + index,
    evaluatedAt: new Date(evaluatedMs).toISOString(),
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: unknown
      ? { applicability: 'NOT_APPLICABLE', currentState: null, lastSeenAt: null, policyRevision: 14, lastKnown: null }
      : { applicability: 'APPLICABLE', currentState: offline ? 'OFFLINE' : 'ONLINE', lastSeenAt: new Date(lastSeenMs).toISOString(), policyRevision: 14, lastKnown: null },
    telemetryReadiness: unknown ? 'NOT_APPLICABLE' : neverObserved ? 'INCOMPLETE' : stale ? 'DEGRADED' : 'CURRENT',
    displayState: unknown ? null : offline ? 'OFFLINE' : neverObserved ? 'UNKNOWN' : stale ? 'STALE' : 'ONLINE',
    values,
  };
}

function problem(status, code, detail) {
  return {
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    instance: '/api/v1/assets-workspace-review',
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
  const model = assetModel();
  const deviceById = new Map(inventory.devices.map((device, index) => [device.id, { device, index: index + 1 }]));
  return createHTTPServer(async (request, response) => {
    const url = new URL(request.url ?? '/', 'http://assets-review.local');
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/asset-model`) {
      writeJson(response, 200, model);
      return;
    }
    if (request.method === 'POST' && url.pathname === '/api/v1/telemetry/observation-snapshots:batchGet') {
      const payload = await readJson(request);
      const items = payload.requests.map((target) => {
        const found = deviceById.get(target.deviceId);
        if (!found) return { requestId: target.requestId, deviceId: target.deviceId, status: 'ERROR', problem: problem(404, 'RESOURCE_NOT_FOUND', 'Device not visible.') };
        const scenario = scenarioByDevice.get(found.device.id);
        if (scenario === 'invalid') return { requestId: target.requestId, deviceId: target.deviceId, status: 'ERROR', problem: problem(422, 'TELEMETRY_KEY_INVALID', 'The selected point contract was rejected.') };
        return { requestId: target.requestId, deviceId: target.deviceId, status: 'OK', snapshot: snapshotFor(target, found.device, found.index, scenario) };
      });
      writeJson(response, 200, { schemaVersion: 1, items });
      return;
    }
    writeJson(response, 404, problem(404, 'RESOURCE_NOT_FOUND', `No review fixture for ${url.pathname}`));
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
  const network = client.events
    .filter((event) => event.method === 'Network.requestWillBeSent' || event.method === 'Network.responseReceived' || event.method === 'Network.loadingFailed')
    .slice(-30)
    .map((event) => ({
      method: event.method,
      url: event.params?.request?.url ?? event.params?.response?.url ?? null,
      status: event.params?.response?.status ?? null,
      error: event.params?.errorText ?? null,
    }));
  throw new Error(`${label} did not become ready; last=${JSON.stringify(last)} diagnostic=${JSON.stringify(diagnostic)} network=${JSON.stringify(network)}`);
}

async function clickText(client, selector, text) {
  return evaluate(client, `(() => {
    const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)})).find((candidate) => candidate.textContent?.includes(${JSON.stringify(text)}));
    if (!(node instanceof HTMLElement)) return false;
    node.click();
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
    cacheDir: resolve(root, 'node_modules/.vite-assets'),
    publicDir: resolve(root, 'apps/hvac-web/public'),
    configFile: false,
    logLevel: 'error',
    plugins: [react(), tailwindcss()],
    define: { __HVAC_WEB_FRONTEND_REVIEW__: 'false' },
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '0.0.0.0',
      port: Number(process.env.ASSETS_REVIEW_PORT ?? 0),
      strictPort: Boolean(process.env.ASSETS_REVIEW_PORT),
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Assets review Vite server has no address');
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
    `document.querySelector('[data-testid="assets-workspace"] h1')?.textContent?.trim() === '设备' && document.querySelector('[aria-label="设备状态摘要"]')?.innerText.includes('在线') && document.querySelectorAll('[aria-label="设备"] [data-slot="table-body"] [data-slot="table-row"]').length === 15 && document.body.innerText.includes('当前状态已更新')`,
    '200-device assets ledger',
  );
  await pause(500);

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
      summary: rect('[aria-label="设备状态摘要"]'),
      ledger: rect('[aria-label="设备"]'),
      toolbarText: document.querySelector('[role="toolbar"]')?.textContent ?? '',
      rowCount: document.querySelectorAll('[aria-label="设备"] [data-slot="table-body"] [data-slot="table-row"]').length,
      tableHeaders: [...document.querySelectorAll('[aria-label="设备"] [data-slot="table-head"]')].map((node) => node.textContent?.trim() ?? '').filter(Boolean),
      toolbarSelectCount: document.querySelectorAll('[role="toolbar"] [data-slot="select-trigger"]').length,
      searchCount: document.querySelectorAll('[role="toolbar"] input[placeholder*="搜索设备"]').length,
      antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-select, .ant-input').length,
      cardCount: document.querySelectorAll('[data-slot="card"]').length,
      text: document.body.innerText.slice(0, 5000),
    };
  })()`);
  assert(desktop.rowCount === 15, `Assets ledger did not render 15 scan rows: ${desktop.rowCount}`);
  assert(desktop.sidebar?.width === 256 && desktop.header?.height === 56, `Assets workspace is not using the shared AppShell geometry: ${JSON.stringify({ sidebar: desktop.sidebar, header: desktop.header })}`);
  assert(desktop.summary?.top < 180 && desktop.ledger?.top < 250, `Assets hierarchy drifted below the first viewport: ${JSON.stringify({ summary: desktop.summary, ledger: desktop.ledger })}`);
  assert(desktop.toolbarSelectCount === 1 && desktop.searchCount === 1, 'Assets toolbar should keep one scope Select plus one search input');
  assert(JSON.stringify(desktop.tableHeaders) === JSON.stringify(['设备', '对象与位置', '运行', '连接', '数据', '关键值', '当前事项', '更新']), `Assets ledger drifted from the promoted Surface 06 column grammar: ${JSON.stringify(desktop.tableHeaders)}`);
  for (const action of ['排序', '筛选', '列']) assert(desktop.toolbarText.includes(action), `Assets toolbar lost tablecn action: ${action}`);
  assert(desktop.antCount === 0, 'Assets workspace rendered legacy Ant DOM');
  assert(desktop.cardCount === 0, `Assets workspace regressed into card composition: ${desktop.cardCount}`);
  assert(desktop.body.scrollWidth <= desktop.body.clientWidth, 'Assets workspace has page-level horizontal overflow');
  assert(!desktop.text.includes(siteId), 'Assets workspace leaked the internal Site UUID');
  assert(!desktop.text.includes('健康分'), 'Assets workspace introduced a synthetic health score');
  await capture(cdp, 'assets-ledger-desktop.png');

  assert(await evaluate(cdp, `(() => { const row = document.querySelector('[aria-label="设备"] [data-slot="table-body"] [data-slot="table-row"]'); if (!(row instanceof HTMLElement)) return false; row.click(); return true; })()`), 'First Assets row was not selectable');
  await waitFor(
    cdp,
    `Boolean(document.querySelector('[data-slot="sheet-content"] [aria-label="设备快速查看"]'))`,
    'Assets desktop quick preview',
  );
  const inspector = await evaluate(cdp, `(() => {
    const ledger = document.querySelector('[aria-label="设备"]')?.getBoundingClientRect();
    const preview = document.querySelector('[data-slot="sheet-content"]')?.getBoundingClientRect();
    return {
      ledgerWidth: Math.round(ledger?.width ?? 0),
      previewWidth: Math.round(preview?.width ?? 0),
      handleCount: document.querySelectorAll('[data-slot="resizable-handle"]').length,
      overlayCount: document.querySelectorAll('[data-slot="sheet-overlay"]').length,
      sheetCount: document.querySelectorAll('[data-slot="sheet-content"]').length,
      bodyScrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      text: document.querySelector('[aria-label="设备快速查看"]')?.textContent ?? '',
    };
  })()`);
  assert(inspector.handleCount === 0 && inspector.sheetCount === 1 && inspector.overlayCount === 0, `Desktop preview should be non-modal and non-resizable: ${JSON.stringify(inspector)}`);
  assert(inspector.previewWidth >= 400 && inspector.previewWidth <= 480, `Quick Preview width drifted: ${inspector.previewWidth}`);
  assert(inspector.ledgerWidth === desktop.ledger?.width, `Opening Quick Preview changed Ledger width: ${JSON.stringify({ before: desktop.ledger?.width, after: inspector.ledgerWidth })}`);
  assert(inspector.bodyScrollWidth <= inspector.clientWidth, 'Quick Preview caused page-level horizontal overflow');
  for (const fact of ['当前状态', '关键值', '当前事项', '对象上下文', '打开完整设备详情']) assert(inspector.text.includes(fact), `Quick Preview lost required fact: ${fact}`);
  for (const forbidden of ['概览', '点位', '关系']) assert(!inspector.text.includes(forbidden), `Quick Preview regressed into mini detail tabs: ${forbidden}`);
  await capture(cdp, 'assets-inspector-desktop.png');

  assert(await clickText(cdp, '[aria-label="设备快速查看"] button', '打开完整设备详情'), 'Open full Device detail action was unavailable');
  await waitFor(
    cdp,
    `Boolean(document.querySelector('[data-testid="asset-device-detail"]')) && document.body.innerText.includes('当前运行') && document.body.innerText.includes('最近证据') && document.body.innerText.includes('对象关系') && document.body.innerText.includes('工程点位') && document.body.innerText.includes('设备资料与来源')`,
    'durable Device detail surface',
  );
  const detail = await evaluate(cdp, `(() => ({
    body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    identity: document.querySelector('[aria-label="设备身份"]') ? (() => { const r = document.querySelector('[aria-label="设备身份"]').getBoundingClientRect(); return { top: Math.round(r.top), bottom: Math.round(r.bottom), height: Math.round(r.height) }; })() : null,
    stateStrip: document.querySelector('[aria-label="设备独立状态"]') ? (() => { const r = document.querySelector('[aria-label="设备独立状态"]').getBoundingClientRect(); return { top: Math.round(r.top), bottom: Math.round(r.bottom), height: Math.round(r.height) }; })() : null,
    overviewTop: Math.round(document.querySelector('#overview')?.getBoundingClientRect().top ?? 0),
    sectionNavText: document.querySelector('[aria-label="设备详情分区"]')?.textContent ?? '',
    h1Count: document.querySelectorAll('h1').length,
    cardCount: document.querySelectorAll('[data-slot="card"]').length,
    engineeringRowCount: document.querySelectorAll('[aria-label="工程点位"] [data-slot="table-body"] [data-slot="table-row"]').length,
    antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tabs').length,
    text: document.body.innerText.slice(0, 12000),
  }))()`);
  assert(detail.antCount === 0, 'Device detail rendered legacy Ant DOM');
  assert(detail.body.scrollWidth <= detail.body.clientWidth, 'Device detail has page-level horizontal overflow');
  assert(detail.h1Count === 1 && detail.identity?.top < 260 && detail.stateStrip?.bottom < 430 && detail.overviewTop < 620, `Device detail hierarchy drifted: ${JSON.stringify(detail)}`);
  assert(detail.cardCount === 0, `Device detail regressed into a dashboard/card wall: ${detail.cardCount}`);
  assert(detail.engineeringRowCount === 4, `Device detail engineering points did not expose the registered point catalogue: ${detail.engineeringRowCount}`);
  for (const section of ['概览', '证据', '关系', '工程点位', '资料']) assert(detail.sectionNavText.includes(section), `Device detail lost stable section navigation: ${section}`);
  for (const fact of ['运行', '连接', '新鲜度', '质量', '当前运行', '当前事项', '最近证据', '对象关系', '工程点位', '设备资料与来源', '继续调查']) assert(detail.text.includes(fact), `Device detail drifted from Surface 07 responsibility: ${fact}`);
  assert(!detail.text.includes('健康分'), 'Device detail introduced a synthetic health score');
  assert(!detail.text.includes('Registry'), 'Device detail leaked implementation vocabulary');
  assert(!detail.text.includes('不补零'), 'Device detail leaked design/explanation copy');
  await capture(cdp, 'asset-device-detail-desktop.png');

  assert(await evaluate(cdp, `(() => {
    const target = document.querySelector('[aria-label="工程点位表格，可横向滚动"]');
    if (!(target instanceof HTMLElement)) return false;
    target.scrollIntoView({ block: 'start' });
    return true;
  })()`), 'Engineering points section was unavailable for evidence capture');
  await pause(200);
  await capture(cdp, 'asset-device-detail-evidence-desktop.png');

  await cdp.send('Runtime.evaluate', { expression: 'window.scrollTo(0, 0)' });
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 768, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(300);
  const detailNarrow = await evaluate(cdp, `(() => {
    const rect = (node) => {
      if (!(node instanceof HTMLElement)) return null;
      const value = node.getBoundingClientRect();
      return { top: Math.round(value.top), bottom: Math.round(value.bottom), width: Math.round(value.width), height: Math.round(value.height) };
    };
    return {
      viewport: { width: innerWidth, height: innerHeight },
      body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      identity: rect(document.querySelector('[aria-label="设备身份"]')),
      stateStrip: rect(document.querySelector('[aria-label="设备独立状态"]')),
      sectionNav: rect(document.querySelector('[aria-label="设备详情分区"]')),
      overview: rect(document.querySelector('#overview')),
      h1Count: document.querySelectorAll('h1').length,
      cardCount: document.querySelectorAll('[data-slot="card"]').length,
      antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tabs').length,
    };
  })()`);
  assert(detailNarrow.body.scrollWidth <= detailNarrow.body.clientWidth, `Device detail overflows at 768px: ${JSON.stringify(detailNarrow.body)}`);
  assert(detailNarrow.h1Count === 1 && detailNarrow.antCount === 0, 'Narrow Device detail lost heading semantics or shadcn composition');
  assert(detailNarrow.cardCount === 0, `Narrow Device detail regressed into Card wall: ${detailNarrow.cardCount}`);
  assert(detailNarrow.stateStrip?.bottom < detailNarrow.viewport.height && detailNarrow.sectionNav?.top < detailNarrow.viewport.height && detailNarrow.overview?.top < detailNarrow.viewport.height, `Device detail primary workflow was displaced below the first viewport: ${JSON.stringify({ stateStrip: detailNarrow.stateStrip, sectionNav: detailNarrow.sectionNav, overview: detailNarrow.overview })}`);
  await capture(cdp, 'asset-device-detail-narrow.png');

  assert(await clickText(cdp, 'button', '返回设备'), 'Device detail back action was unavailable');
  await waitFor(cdp, `Boolean(document.querySelector('[data-testid="assets-workspace"]'))`, 'Assets workspace return');
  await cdp.send('Runtime.evaluate', { expression: 'window.scrollTo(0, 0)' });
  await pause(300);
  const narrow = await evaluate(cdp, `(() => ({
    viewport: { width: innerWidth, height: innerHeight },
    body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    summaryHeight: Math.round(document.querySelector('[aria-label="设备状态摘要"]')?.getBoundingClientRect().height ?? 0),
    ledgerTop: Math.round(document.querySelector('[aria-label="设备"]')?.getBoundingClientRect().top ?? 0),
    rowCount: document.querySelectorAll('[aria-label="设备"] [data-slot="table-body"] [data-slot="table-row"]').length,
    antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-select, .ant-input').length,
  }))()`);
  assert(narrow.body.scrollWidth <= narrow.body.clientWidth, `Assets workspace overflows at 768px: ${JSON.stringify(narrow.body)}`);
  assert(narrow.rowCount === 15 && narrow.antCount === 0, 'Narrow Assets ledger lost density or shadcn composition');
  assert(narrow.ledgerTop < narrow.viewport.height, `Filters displaced the ledger below the first viewport: ${narrow.ledgerTop}`);
  assert(await evaluate(cdp, `document.querySelectorAll('[data-slot="resizable-panel-group"]').length === 0`), 'Narrow Assets workspace should not render the desktop split inspector');
  await capture(cdp, 'assets-ledger-narrow.png');

  assert(await evaluate(cdp, `(() => {
    const row = document.querySelector('[aria-label="设备"] [data-slot="table-body"] [data-slot="table-row"]');
    if (!(row instanceof HTMLElement)) return false;
    row.click();
    return true;
  })()`), 'First narrow Assets row was not selectable');

  await waitFor(
    cdp,
    `Boolean(document.querySelector('[data-slot="sheet-content"] [aria-label="设备快速查看"]')) && Boolean(document.querySelector('[data-slot="sheet-overlay"]'))`,
    'Assets narrow modal Quick Preview',
  );
  const narrowInspector = await evaluate(cdp, `(() => {
    const sheet = document.querySelector('[data-slot="sheet-content"]');
    const rect = sheet instanceof HTMLElement ? sheet.getBoundingClientRect() : null;
    return {
      sheetWidth: Math.round(rect?.width ?? 0),
      resizableCount: document.querySelectorAll('[data-slot="resizable-panel-group"]').length,
      overlayCount: document.querySelectorAll('[data-slot="sheet-overlay"]').length,
      bodyScrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      text: sheet?.textContent ?? '',
    };
  })()`);
  assert(narrowInspector.resizableCount === 0 && narrowInspector.overlayCount === 1, 'Narrow Quick Preview should use one modal Sheet overlay and no splitter');
  assert(narrowInspector.sheetWidth > 0 && narrowInspector.sheetWidth <= 540, `Narrow Sheet width drifted: ${narrowInspector.sheetWidth}`);
  assert(narrowInspector.bodyScrollWidth <= narrowInspector.clientWidth, 'Narrow Sheet caused page-level horizontal overflow');
  for (const fact of ['当前状态', '关键值', '当前事项', '对象上下文', '打开完整设备详情']) assert(narrowInspector.text.includes(fact), `Narrow Quick Preview lost required fact: ${fact}`);
  await capture(cdp, 'assets-inspector-narrow.png');

  assert(await evaluate(cdp, `(() => {
    const close = document.querySelector('[data-slot="sheet-content"] [data-slot="sheet-close"]');
    if (!(close instanceof HTMLElement)) return false;
    close.click();
    return true;
  })()`), 'Narrow Sheet close action was unavailable');
  await waitFor(cdp, `document.querySelector('[data-slot="sheet-content"]') === null`, 'Assets narrow Sheet close');

  const runtimeErrors = cdp.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.exceptionDetails?.text ?? event.params?.entry?.text ?? event.method);
  assert(runtimeErrors.length === 0, `Assets review emitted runtime errors: ${JSON.stringify(runtimeErrors)}`);

  const evidence = {
    conclusion: 'passed',
    source: 'real-assets-certification-library',
    screenshots: [
      'out/assets-workspace-review/assets-ledger-desktop.png',
      'out/assets-workspace-review/assets-inspector-desktop.png',
      'out/assets-workspace-review/asset-device-detail-desktop.png',
      'out/assets-workspace-review/asset-device-detail-evidence-desktop.png',
      'out/assets-workspace-review/asset-device-detail-narrow.png',
      'out/assets-workspace-review/assets-ledger-narrow.png',
      'out/assets-workspace-review/assets-inspector-narrow.png',
    ],
    desktop,
    inspector,
    detail,
    detailNarrow,
    narrow,
    narrowInspector,
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
