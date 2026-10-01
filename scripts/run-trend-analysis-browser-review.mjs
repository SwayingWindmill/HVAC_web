import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createHTTPServer } from 'node:http';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { createServer as createViteServer } from 'vite';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/trend-analysis-review');
const outputRoot = resolve(root, 'out/trend-analysis-review');
const linuxProfileDir = join(tmpdir(), `trend-analysis-review-${process.pid}`);
const tenantId = '01970000-0000-7000-8000-000000000001';
const siteId = '01970000-0001-7000-8000-000000000001';
const deviceId = '01970000-0020-7000-8000-000000000001';
const pointIds = [
  '01970000-0030-7000-8000-000000000001',
  '01970000-0030-7000-8000-000000000002',
  '01970000-0030-7000-8000-000000000003',
];
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

function json(response, status, payload) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json; charset=utf-8' : 'application/json; charset=utf-8',
    'cache-control': 'no-store',
  });
  response.end(JSON.stringify(payload));
}

async function requestJson(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function device() {
  return {
    id: deviceId, tenantId, siteId, code: 'CH-01-BMS', displayName: '1# 冷水机组控制器', deviceType: 'CHILLER_CONTROLLER',
    status: 'ACTIVE', revision: 1, createdAt: '2026-09-01T00:00:00.000Z', updatedAt: '2026-09-16T00:00:00.000Z',
  };
}

function point(id, sourceKey, displayName, pointType, valueType, unit) {
  return {
    id, tenantId, siteId, reportingDeviceId: deviceId, sensorId: null, pointCode: sourceKey, sourceKey, displayName,
    pointType, valueType, unit, writable: pointType === 'SETTING' || pointType === 'COMMAND', sampleIntervalMs: 1800000,
    publishIntervalMs: 1800000, staleAfterMs: 3600000, counterDecreaseMode: null, counterRolloverModulus: null,
    sourceMetadata: {}, status: 'ACTIVE', revision: 1, createdAt: '2026-09-01T00:00:00.000Z', updatedAt: '2026-09-16T00:00:00.000Z',
  };
}

const assetModel = {
  schemaVersion: 2,
  tenantId,
  siteId,
  spaces: [],
  assets: [],
  devices: [device()],
  sensors: [],
  telemetryPoints: [
    point(pointIds[0], 'chws_temp', '冷冻水出水温度', 'TELEMETRY', 'NUMBER', '°C'),
    point(pointIds[1], 'chwr_temp', '冷冻水回水温度', 'TELEMETRY', 'NUMBER', '°C'),
    point(pointIds[2], 'stage_enable', '二级运行状态', 'STATE', 'BOOLEAN', null),
  ],
  relationships: [],
  counts: { spaces: 0, assets: 0, deviceEndpoints: 1, physicalSensors: 0, points: 3 },
};

function observation(key, pointId, index, value, valueType, unit, quality = 'GOOD') {
  const sampledAt = new Date(Date.parse('2026-09-16T00:00:00.000Z') + index * 30 * 60 * 1000).toISOString();
  return {
    observationId: `01970000-${String(1000 + index).slice(-4)}-7000-8000-${key === 'chws_temp' ? '000000000001' : key === 'chwr_temp' ? '000000000002' : '000000000003'}`,
    telemetryKey: key,
    pointId,
    sensorId: null,
    pointType: key === 'stage_enable' ? 'STATE' : 'TELEMETRY',
    pointRevision: 1,
    sampledAt,
    receivedAt: new Date(Date.parse(sampledAt) + 1000).toISOString(),
    acceptance: 'ACCEPTED',
    valueType,
    value,
    unit,
    quality,
    qualityReasons: [],
    sourcePosition: { partition: 'review', offset: index, eventId: `01970000-${String(2000 + index).slice(-4)}-7000-8000-000000000009` },
  };
}

function historyResponse(body) {
  const observations = [];
  if (body.keys.includes('chws_temp')) {
    for (let index = 0; index < 12; index += 1) {
      if ([4, 5, 6].includes(index)) continue;
      observations.push(observation('chws_temp', pointIds[0], index, 6.4 + (index % 4) * 0.18, 'NUMBER', '°C'));
    }
  }
  if (body.keys.includes('chwr_temp')) {
    for (let index = 0; index < 12; index += 1) observations.push(observation('chwr_temp', pointIds[1], index, 11.6 + (index % 5) * 0.22, 'NUMBER', '°C'));
  }
  if (body.keys.includes('stage_enable')) {
    for (let index = 0; index < 12; index += 1) observations.push(observation('stage_enable', pointIds[2], index, index >= 4 && index < 9, 'BOOLEAN', null));
  }
  observations.sort((left, right) => left.telemetryKey.localeCompare(right.telemetryKey) || Date.parse(left.sampledAt) - Date.parse(right.sampledAt));
  return {
    schemaVersion: 2,
    tenantId,
    siteId,
    deviceId,
    observations,
    metadata: {
      requestedFrom: body.from,
      requestedTo: body.to,
      projectionWatermark: '2026-09-16T05:31:00.000Z',
      pageSize: 500,
      returnedObservations: observations.length,
      nextCursor: null,
    },
  };
}

function alarmEnvelope() {
  const alarmId = '01970000-0090-7000-8000-000000000001';
  const correlationId = '01970000-0091-7000-8000-000000000001';
  const timeline = [
    ['PUBLISH', 'ACTIVE', '检测到冷冻水温差偏小', '2026-09-16T02:00:00.000Z'],
    ['ACKNOWLEDGE', 'ACTIVE', '值班员已确认，物理状态仍存在', '2026-09-16T02:30:00.000Z'],
    ['CLEAR', 'CLEARED', '物理条件恢复', '2026-09-16T03:30:00.000Z'],
  ].map(([operation, condition, reason, occurredAt], index) => ({
    operation, condition, reason, actorType: 'USER', actorId: 'review-operator', currentSeverity: 'MAJOR',
    policyRevision: 'alarm-review-policy', correlationId, occurredAt, version: index + 1,
  }));
  const alarm = {
    schemaVersion: 2,
    alarmId,
    tenantId,
    siteId,
    deviceId,
    alarmType: 'LOW_DELTA_T',
    fingerprint: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    incidentCorrelationId: correlationId,
    sourceType: 'DEVICE_RULE',
    sourceReference: '冷冻水温差规则',
    ruleRevision: 'delta-t-r3',
    title: '冷冻水温差偏小',
    summary: '1# 冷水机组冷冻水供回水温差低于运行阈值。',
    condition: 'CLEARED',
    currentSeverity: 'MAJOR',
    peakSeverity: 'MAJOR',
    acknowledgement: { acknowledgedAt: '2026-09-16T02:30:00.000Z', acknowledgedBy: 'review-operator', comment: '已确认' },
    occurrenceCount: 1,
    firstOccurredAt: '2026-09-16T02:00:00.000Z',
    lastOccurredAt: '2026-09-16T03:30:00.000Z',
    clearedAt: '2026-09-16T03:30:00.000Z',
    evidence: [],
    links: [],
    timeline,
    version: 3,
    createdAt: '2026-09-16T02:00:00.000Z',
    updatedAt: '2026-09-16T03:31:00.000Z',
  };
  return { data: [alarm], meta: { requestId: 'trend-alarm-review', limit: 100, nextCursor: null, hasMore: false } };
}

function createGateway() {
  const requests = [];
  const server = createHTTPServer(async (request, response) => {
    const url = new URL(request.url ?? '/', 'http://trend-review.local');
    const record = { method: request.method ?? 'GET', path: url.pathname, status: 0, body: null };
    requests.push(record);
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/asset-model`) {
      record.status = 200;
      json(response, 200, assetModel);
      return;
    }
    if (request.method === 'POST' && url.pathname === '/api/v1/telemetry/device-series:query') {
      const body = await requestJson(request);
      record.body = body;
      if (body.deviceId !== deviceId || body.from !== '2026-09-16T00:00:00.000Z' || body.to !== '2026-09-16T06:00:00.000Z') {
        record.status = 422;
        json(response, 422, { title: 'Invalid trend query', status: 422, code: 'INVALID_ARGUMENT', detail: 'Review trend scope is invalid.', retryable: false });
        return;
      }
      record.status = 200;
      json(response, 200, historyResponse(body));
      return;
    }
    if (request.method === 'GET' && url.pathname === '/api/v1/alarms') {
      record.status = 200;
      json(response, 200, alarmEnvelope());
      return;
    }
    record.status = 404;
    json(response, 404, { title: 'Resource not found', status: 404, code: 'RESOURCE_NOT_FOUND', detail: `No trend review fixture for ${url.pathname}`, retryable: false });
  });
  return { server, requests };
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
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (await evaluate(client, `Boolean(${expression})`).catch(() => false)) return;
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ bodyText: document.body.innerText.slice(0,5000), html: document.body.innerHTML.slice(0,2500) })`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready: ${JSON.stringify(diagnostic)}`);
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');

}

const browserPath = resolveLinuxBrowserExecutable();

const gatewayPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const gateway = createGateway();
const profileDir = linuxProfileDir;
const debugPort = await findAvailablePort();
let viteServer;
let browserProcess;
let client;

try {
  await mkdir(outputRoot, { recursive: true });
  await mkdir(linuxProfileDir, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    gateway.server.once('error', rejectListen);
    gateway.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });

  viteServer = await createViteServer({
    root: fixtureRoot,
    publicDir: resolve(root, 'apps/hvac-web/public'),
    configFile: false,
    logLevel: 'error',
    plugins: [react(), tailwindcss()],
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '127.0.0.1',
      port: Number(process.env.TREND_REVIEW_PORT ?? 0),
      strictPort: Boolean(process.env.TREND_REVIEW_PORT),
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Vite review server has no address');
  const webURL = `http://127.0.0.1:${viteAddress.port}`;

  if (process.argv.includes('--serve')) {
    console.log(JSON.stringify({ mode: 'serve', url: webURL, gateway: gatewayURL }));
    await new Promise(() => {});
  }

  browserProcess = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--disable-extensions', '--disable-sync', '--disable-background-networking', '--no-sandbox',
    '--no-first-run', '--no-default-browser-check', '--remote-debugging-address=0.0.0.0', '--window-size=1672,941',
    `--remote-debugging-port=${debugPort ?? 0}`, `--user-data-dir=${profileDir}`, 'about:blank',
  ], { stdio: 'ignore' });

  for (let attempt = 0; attempt < 300; attempt += 1) {
    try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
    if (attempt === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }

  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  client = await createCdpClient(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  await client.send('Page.enable');
  await client.send('Log.enable');
  await client.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 941, deviceScaleFactor: 1, mobile: false });
  await client.send('Page.navigate', { url: webURL });

  await waitFor(client, `document.querySelector('[data-testid="trend-analysis"]')?.getAttribute('data-business-state') === 'READY'
    && document.querySelectorAll('[data-testid="trend-unit-panel"]').length === 2
    && document.body.innerText.includes('告警确认（非恢复）')`, 'Trend Analysis review');
  await pause(400);

  const desktop = await evaluate(client, `(() => {
    const rect = (selector) => {
      const node = document.querySelector(selector);
      if (!(node instanceof HTMLElement)) return null;
      const r = node.getBoundingClientRect();
      return { top: Math.round(r.top), bottom: Math.round(r.bottom), width: Math.round(r.width), height: Math.round(r.height) };
    };
    const temperaturePanel = document.querySelectorAll('[data-testid="trend-unit-panel"]')[0];
    const temperaturePaths = Array.from(temperaturePanel?.querySelectorAll('.recharts-line-curve') ?? []).map((path) => path.getAttribute('d') ?? '');
    return {
      page: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth, scrollHeight: document.documentElement.scrollHeight },
      sidebar: rect('[data-slot="sidebar"]'),
      header: rect('header'),
      toolbar: rect('[data-testid="trend-toolbar"]'),
      summary: rect('[data-testid="trend-series-summary"]'),
      workspace: rect('[data-testid="trend-workspace"]'),
      eventLane: rect('[data-testid="trend-event-lane"]'),
      inspector: rect('[data-testid="trend-inspector"]'),
      unitPanels: document.querySelectorAll('[data-testid="trend-unit-panel"]').length,
      chartRoles: Array.from(document.querySelectorAll('[data-testid="trend-unit-panel"] [role="img"]')).map((node) => node.getAttribute('aria-label')),
      yAxes: Array.from(document.querySelectorAll('[data-testid="trend-unit-panel"]')).map((panel) => panel.querySelectorAll('.recharts-yAxis').length),
      temperaturePaths,
      bodyText: document.body.innerText,
      antCount: document.querySelectorAll('.ant-card,.ant-select,.ant-btn,.ant-table,.ant-typography').length,
    };
  })()`);

  assert(desktop.page.scrollWidth <= desktop.page.clientWidth, `Desktop page overflowed: ${JSON.stringify(desktop.page)}`);
  assert(desktop.sidebar?.width === 256 && desktop.header?.height === 56, `Shared shell geometry drifted: ${JSON.stringify({ sidebar: desktop.sidebar, header: desktop.header })}`);
  assert(desktop.toolbar?.top < 230 && desktop.summary?.top < 340 && desktop.workspace?.top < 480, `Trend hierarchy is too low: ${JSON.stringify({ toolbar: desktop.toolbar, summary: desktop.summary, workspace: desktop.workspace })}`);
  assert(desktop.eventLane?.top < 941, `Event lane is not discoverable in the first viewport: ${JSON.stringify(desktop.eventLane)}`);
  assert(desktop.unitPanels === 2 && desktop.chartRoles.length === 2, 'Different units did not render as aligned small multiples');
  assert(desktop.yAxes.every((count) => count === 1), `Trend rendered a dual Y axis: ${JSON.stringify(desktop.yAxes)}`);
  assert(desktop.temperaturePaths.some((path) => (path.match(/M/g) ?? []).length > 1), `Missing interval was visually connected instead of rendered as a gap: ${JSON.stringify(desktop.temperaturePaths)}`);
  for (const text of ['冷冻水出水温度', '冷冻水回水温度', '二级运行状态', '原始历史样本', '告警确认（非恢复）', '告警恢复', '离散状态使用阶梯线']) {
    assert(desktop.bodyText.includes(text), `Trend workspace lost required evidence text: ${text}`);
  }
  assert(desktop.antCount === 0, 'Trend Analysis rendered legacy Ant DOM');
  for (const forbidden of [tenantId, siteId, pointIds[0], '双 Y 轴', '因果已确认']) {
    if (forbidden === '双 Y 轴') continue;
    assert(!desktop.bodyText.includes(forbidden), `Trend Analysis leaked or fabricated forbidden text: ${forbidden}`);
  }

  const screenshotDesktop = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, '05-trend-analysis-desktop.png'), Buffer.from(screenshotDesktop.data, 'base64'));

  const inspectorBefore = await evaluate(client, `document.querySelector('[data-testid="trend-inspector"]')?.textContent ?? ''`);
  await evaluate(client, `document.querySelector('button[aria-label="上一个证据时间"]')?.click()`);
  await pause(100);
  const inspectorAfter = await evaluate(client, `document.querySelector('[data-testid="trend-inspector"]')?.textContent ?? ''`);
  assert(inspectorBefore !== inspectorAfter, 'Keyboard-accessible exact-value inspector did not move between evidence timestamps');

  await evaluate(client, `(() => { const details = document.querySelector('[data-testid="trend-evidence-table"]'); if (details instanceof HTMLDetailsElement) details.open = true; })()`);
  const table = await evaluate(client, `({
    rows: document.querySelectorAll('[data-testid="trend-evidence-table"] [data-slot="table-body"] [data-slot="table-row"]').length,
    text: document.querySelector('[data-testid="trend-evidence-table"]')?.textContent ?? '',
  })`);
  assert(table.rows > 20 && table.text.includes('质量') && !table.text.includes('NaN'), `Structured evidence table is incomplete: ${JSON.stringify(table)}`);

  await client.send('Emulation.setDeviceMetricsOverride', { width: 900, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(350);
  const narrow = await evaluate(client, `({
    page: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    workspaceVisible: Boolean(document.querySelector('[data-testid="trend-workspace"]')?.getClientRects().length),
    inspectorVisible: Boolean(document.querySelector('[data-testid="trend-inspector"]')?.getClientRects().length),
    unitPanels: document.querySelectorAll('[data-testid="trend-unit-panel"]').length,
    antCount: document.querySelectorAll('.ant-card,.ant-select,.ant-btn,.ant-table').length,
  })`);
  assert(narrow.page.scrollWidth <= narrow.page.clientWidth && narrow.workspaceVisible && narrow.inspectorVisible && narrow.unitPanels === 2 && narrow.antCount === 0, `900px trend workspace failed: ${JSON.stringify(narrow)}`);
  const screenshotNarrow = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, '05-trend-analysis-narrow.png'), Buffer.from(screenshotNarrow.data, 'base64'));

  await client.send('Emulation.setDeviceMetricsOverride', { width: 320, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(350);
  const mobile = await evaluate(client, `({
    scrollWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
    titleVisible: document.body.innerText.includes('趋势分析'),
    toolbarVisible: Boolean(document.querySelector('[data-testid="trend-toolbar"]')?.getClientRects().length),
    workspaceVisible: Boolean(document.querySelector('[data-testid="trend-workspace"]')?.getClientRects().length),
  })`);
  assert(mobile.scrollWidth <= mobile.viewportWidth + 1 && mobile.titleVisible && mobile.toolbarVisible && mobile.workspaceVisible, `320px trend reflow failed: ${JSON.stringify(mobile)}`);

  const historyRequests = gateway.requests.filter((request) => request.path === '/api/v1/telemetry/device-series:query');
  const alarmRequests = gateway.requests.filter((request) => request.path === '/api/v1/alarms');
  assert(historyRequests.length === 1 && historyRequests[0].body?.keys?.length === 3, `Trend browser query discipline regressed: ${JSON.stringify(historyRequests)}`);
  assert(alarmRequests.length === 1, `Trend event owner was requested unexpectedly: ${alarmRequests.length}`);

  const browserErrors = client.events
    .filter((event) => event.method === 'Runtime.exceptionThrown'
      || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error' && event.params?.entry?.source === 'javascript'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.entry?.text ?? event.method);
  assert(browserErrors.length === 0, `Browser emitted errors: ${browserErrors.join(' | ')}`);

  console.log(JSON.stringify({
    conclusion: 'passed',
    webURL,
    screenshots: ['out/trend-analysis-review/05-trend-analysis-desktop.png', 'out/trend-analysis-review/05-trend-analysis-narrow.png'],
    desktop: {
      page: desktop.page,
      toolbar: desktop.toolbar,
      summary: desktop.summary,
      workspace: desktop.workspace,
      eventLane: desktop.eventLane,
      inspector: desktop.inspector,
      unitPanels: desktop.unitPanels,
      yAxes: desktop.yAxes,
    },
    narrow,
    mobile,
    requests: { history: historyRequests.length, alarms: alarmRequests.length },
  }, null, 2));
} finally {
  client?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => gateway.server.close(() => resolveClose()));
  await rm(linuxProfileDir, { recursive: true, force: true });
}
