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
const fixtureRoot = resolve(root, 'scripts/fixtures/overview-review');
const outputRoot = resolve(root, 'out/overview-review');
const linuxProfileDir = join(tmpdir(), `overview-review-${process.pid}`);
const siteId = '01940000-0001-7000-8000-000000000001';
const tenantId = '01940000-0000-7000-8000-000000000001';
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
  response.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' });
  response.end(JSON.stringify(payload));
}

const summary = {
  schemaVersion: 1,
  tenantId,
  siteId,
  siteTimezone: 'Asia/Tokyo',
  asOf: '2026-09-15T00:30:00.000Z',
  generatedAt: '2026-09-15T00:30:00.000Z',
  dataWatermark: '2026-09-15T00:29:42.000Z',
  aggregateWatermark: '2026-09-15T00:28:00.000Z',
  completeness: 'READY',
  quality: 'ATTENTION',
  reasons: ['25 台设备离线', '25 台设备数据陈旧', '3 条活动告警'],
  devicePopulation: {
    state: 'READY', registered: 200, applicable: 200, observable: 200, online: 125, offline: 25, stale: 25,
    unknown: 25, unavailable: 0, denominatorPolicy: 'APPLICABLE_WITH_KNOWN_PRESENCE', denominator: 175,
    availabilityPercent: 71.4, evaluatedAt: '2026-09-15T00:30:00.000Z',
  },
  slowMetrics: {
    siteLocalDayEnergy: { state: 'READY', value: 4286.4, unit: 'kWh', source: 'energy-aggregate', dataWatermark: '2026-09-15T00:29:00.000Z', aggregateWatermark: '2026-09-15T00:28:00.000Z', reason: null },
    cost: { state: 'READY', value: 3276.8, unit: 'CNY', source: 'cost-aggregate', dataWatermark: '2026-09-15T00:29:00.000Z', aggregateWatermark: '2026-09-15T00:28:00.000Z', reason: null },
    baselineSavings: { state: 'READY', value: 8.6, unit: '%', source: 'baseline-savings', dataWatermark: '2026-09-15T00:29:00.000Z', aggregateWatermark: '2026-09-15T00:28:00.000Z', reason: null },
    cop: { state: 'READY', value: 5.8, unit: null, source: 'plant-performance', dataWatermark: '2026-09-15T00:29:00.000Z', aggregateWatermark: '2026-09-15T00:28:00.000Z', reason: null },
  },
  fastMetrics: {
    currentPower: { state: 'READY', value: 504, unit: 'kW', source: 'telemetry-rollup', dataWatermark: '2026-09-15T00:29:42.000Z', aggregateWatermark: null, reason: null },
    openAlarms: { state: 'ATTENTION', activeCount: 3, highestSeverity: 'MAJOR', watermark: '2026-09-15T00:29:30.000Z', reason: '当前存在活动告警' },
  },
};

const overview = {
  schemaVersion: 1,
  siteId,
  asOf: '2026-09-15T00:30:00.000Z',
  weather: { temperatureC: 26, condition: '多云' },
  kpis: {
    coolingTodayRT: 1248, coolingComparePercent: 5.2, totalLoadKW: 504, loadComparePercent: -8.6,
    averageCop: 5.8, copComparePercent: 6.1, comfortRatePercent: 96.3, comfortComparePercent: 2.4,
    savingEnergyKWh: 3860, savingEnergyComparePercent: 12.5, savingCostCny: 2360, savingCostComparePercent: 12.1,
    carbonReductionTco2e: 2.8, carbonComparePercent: 12.4,
  },
  topology: [
    { key: 'cooling-tower', label: '冷却塔', running: 3, total: 3, powerKW: 42 },
    { key: 'chiller', label: '冷水机组', running: 2, total: 3, powerKW: 240 },
    { key: 'chw-pump', label: '冷冻水泵', running: 2, total: 3, powerKW: 86 },
    { key: 'cw-pump', label: '冷却水泵', running: 2, total: 2, powerKW: 54 },
    { key: 'ahu', label: 'AHU', running: 12, total: 16, powerKW: 58 },
    { key: 'vav-fcu', label: 'VAV/FCU', running: 38, total: 52, powerKW: 24 },
  ],
  loadTrend: Array.from({ length: 24 }, (_, hour) => {
    const at = `2026-09-15T${String(hour).padStart(2, '0')}:00:00.000Z`;
    // site.timezone is Asia/Tokyo (UTC+9)
    const localHour = (hour + 9) % 24;
    let curve = 0.38;
    if (localHour >= 0 && localHour < 6) {
      curve = 0.36 + 0.02 * Math.sin(localHour);
    } else if (localHour >= 6 && localHour < 8) {
      curve = 0.45 + (localHour - 6) * 0.15;
    } else if (localHour >= 8 && localHour < 12) {
      curve = 0.82 + (localHour - 8) * 0.03;
    } else if (localHour >= 12 && localHour < 13) {
      curve = 0.80;
    } else if (localHour >= 13 && localHour < 17) {
      curve = 0.95 + 0.05 * Math.sin((localHour - 13) / 2);
    } else if (localHour >= 17 && localHour < 20) {
      curve = 0.75 - (localHour - 17) * 0.12;
    } else {
      curve = 0.40;
    }
    return {
      at,
      actual: Math.round((504 * curve) / 0.98),
      baseline: Math.round((560 * curve) / 0.98),
    };
  }),
  loadSummary: { currentKW: 504, baselineKW: 560, savingKW: 56, savingRatePercent: 10.0 },
  coolingTrend: [],
  coolingSummary: { currentRT: 830.25, baselineRT: 900, savingRT: 69.75, savingRatePercent: 7.8 },
  temperatureTrend: [],
  waterTemperatures: { supplyC: 7.0, returnC: 12.4, setpointC: 7.0 },
  coolingWaterTemperatureTrend: [],
  coolingWaterTemperatures: { supplyC: 29.0, returnC: 33.0, setpointC: 29.0 },
  alarmSeverity: { critical: 1, major: 1, warning: 1 },
  priorityAlarms: [
    { severity: 'CRITICAL', title: 'CH-03 冷水机组故障', locationLabel: '冷水机组', deviceLabel: 'CH-03', occurredAt: '2026-09-15T00:21:18.000Z' },
    { severity: 'MAJOR', title: 'PMP-03 冷冻水泵待机时间过长', locationLabel: '冷冻水泵', deviceLabel: 'PMP-03', occurredAt: '2026-09-15T00:18:05.000Z' },
    { severity: 'WARNING', title: 'D区 AHU-07 过滤网压差偏高', locationLabel: '空调末端', deviceLabel: 'AHU-07', occurredAt: '2026-09-15T00:15:42.000Z' },
  ],
  deviceStatus: { total: 22, running: 18, runningPercent: 81.8, stopped: 3, stoppedPercent: 13.6, fault: 1, faultPercent: 4.6, offline: 0, offlinePercent: 0 },
  energyBreakdown: [
    { key: 'chiller', label: '冷水机组', energyKWh: 420, percent: 48.8, costCny: 1118, costPercent: 47.4 },
    { key: 'chw-pump', label: '冷冻水泵', energyKWh: 150, percent: 17.4, costCny: 418, costPercent: 17.7 },
    { key: 'ahu', label: 'AHU 系统', energyKWh: 130, percent: 15.1, costCny: 376, costPercent: 15.9 },
    { key: 'cw-pump', label: '冷却水泵', energyKWh: 90, percent: 10.5, costCny: 248, costPercent: 10.5 },
    { key: 'other', label: '其他设备', energyKWh: 70, percent: 8.2, costCny: 200, costPercent: 8.5 },
  ],
  savingsPerformance: { actualEnergyKWh: 12860, baselineEnergyKWh: 14320, savingEnergyKWh: 3860, savingRatePercent: 10.2, savingCostCny: 2360, carbonReductionTco2e: 2.8, actualTrend: [] },
  opportunities: [
    { rank: 1, title: '优化冷冻水出水温设定', priority: 'HIGH', savingKWhPerDay: 320 },
    { rank: 2, title: '夜间预冷策略优化', priority: 'HIGH', savingKWhPerDay: 280 },
    { rank: 3, title: '冷机群控优化', priority: 'MEDIUM', savingKWhPerDay: 450 },
    { rank: 4, title: '风机静压设定优化', priority: 'MEDIUM', savingKWhPerDay: 160 },
  ],
  strategies: [],
};

function workOrder(offset, title, priority, status, assigneeId) {
  const workOrderId = `01940000-00c0-7${String(offset).padStart(3, '0')}-8000-${String(offset).padStart(12, '0')}`;
  const occurredAt = `2026-09-15T00:${String(10 + offset * 3).padStart(2, '0')}:00.000Z`;
  return {
    schemaVersion: 1, workOrderId, tenantId, siteId, title, description: `${title}，由运营总览设计复审场景提供。`, priority, status,
    sourceReferences: [{ domain: 'MANUAL', resourceId: `overview-review:${offset}`, relationship: 'ORIGIN' }],
    assigneeId, tasks: { total: 3, completed: status === 'IN_PROGRESS' ? 1 : 0, blocked: 0 }, noteCount: status === 'IN_PROGRESS' ? 2 : 0,
    attachmentCount: 0, completionEvidence: [],
    timeline: [{ operation: 'CREATE', toStatus: status === 'IN_PROGRESS' ? 'IN_PROGRESS' : 'OPEN', reason: 'Overview review fixture.', actorType: 'USER', actorId: assigneeId, assigneeId, occurredAt, version: 1 }],
    version: 1, createdAt: occurredAt, updatedAt: occurredAt,
  };
}

const workOrders = [
  workOrder(1, '冷冻水温差异常检查', 'URGENT', 'IN_PROGRESS', '李工'),
  workOrder(2, '冷水机组 CH-011 离线排查', 'HIGH', 'OPEN', '王工'),
  workOrder(3, '冷冻泵振动趋势复核', 'MEDIUM', 'OPEN', '赵工'),
  workOrder(4, '月度机房巡检', 'LOW', 'OPEN', '运维一组'),
];

function createGateway() {
  return createHTTPServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/dashboard-summary`) return json(response, 200, summary);
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/dashboard-overview`) return json(response, 200, overview);
    if (request.method === 'GET' && url.pathname === '/api/v1/alarms') {
      return json(response, 200, { data: [], meta: { requestId: 'overview-review-alarms', limit: Number(url.searchParams.get('limit') ?? 8), nextCursor: null, hasMore: false } });
    }
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/work-orders`) {
      return json(response, 200, { schemaVersion: 1, items: workOrders, nextCursor: null, hasMore: false });
    }
    return json(response, 404, { title: 'Not found', detail: `No overview review fixture for ${url.pathname}` });
  });
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

async function waitForOverview(client) {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    const ready = await evaluate(client, `Boolean(document.querySelector('[data-testid="site-overview"]')) && Boolean(document.querySelector('[data-slot="sidebar"]')) && document.body.innerText.includes('优先处理') && document.body.innerText.includes('用能构成') && document.body.innerText.includes('数据状态')`).catch(() => false);
    if (ready) return;
    await pause(100);
  }
  const diagnostic = await evaluate(client, `(() => ({ bodyText: document.body.innerText.slice(0, 3000), bodyHtml: document.body.innerHTML.slice(0, 3000), title: document.title }))()`).catch((error) => ({ evaluationError: String(error) }));
  const eventErrors = client.events.filter((event) => event.method === 'Runtime.exceptionThrown' || event.method === 'Log.entryAdded').slice(-12);
  throw new Error(`Overview did not become ready: ${JSON.stringify({ diagnostic, eventErrors })}`);
}

async function waitForOperations(client) {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    const ready = await evaluate(client, `Boolean(document.querySelector('[data-testid="system-operations"]')) && document.body.innerText.includes('设备与过程') && document.body.innerText.includes('当前选择') && document.body.innerText.includes('冷冻水') && document.body.innerText.includes('供水温度')`).catch(() => false);
    if (ready) return;
    await pause(100);
  }
  const diagnostic = await evaluate(client, `(() => ({ bodyText: document.body.innerText.slice(0, 3000), bodyHtml: document.body.innerHTML.slice(0, 3000), title: document.title }))()`).catch((error) => ({ evaluationError: String(error) }));
  const eventErrors = client.events.filter((event) => event.method === 'Runtime.exceptionThrown' || event.method === 'Log.entryAdded').slice(-12);
  throw new Error(`System Operations did not become ready: ${JSON.stringify({ diagnostic, eventErrors })}`);
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
    cacheDir: resolve(root, 'node_modules/.vite-overview'),
    publicDir: resolve(root, 'apps/hvac-web/public'),
    configFile: false,
    logLevel: 'error',
    plugins: [react(), tailwindcss()],
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    define: {
      __HVAC_WEB_BUILD_ID__: JSON.stringify('overview-browser-review'),
      __HVAC_WEB_GATEWAY_BASE_PATH__: JSON.stringify('/api/v1'),
      __HVAC_WEB_REALTIME_PROTOCOL__: JSON.stringify('centrifugo-v1'),
      __HVAC_WEB_FRONTEND_REVIEW__: JSON.stringify(false),
    },
    server: {
      host: '0.0.0.0',
      port: Number(process.env.OVERVIEW_REVIEW_PORT ?? 0),
      strictPort: Boolean(process.env.OVERVIEW_REVIEW_PORT),
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
      watch: { usePolling: true, interval: 200 },
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
  await cdp.send('Page.enable');
  await cdp.send('Log.enable');
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 941, deviceScaleFactor: 1, mobile: false });
  await cdp.send('Page.navigate', { url: webURL });
  await waitForOverview(cdp);
  console.log('[overview-review] overview ready');
  await pause(800);

  const metrics = await evaluate(cdp, `(() => {
    const rect = (selector) => {
      const node = document.querySelector(selector);
      if (!(node instanceof HTMLElement)) return null;
      const value = node.getBoundingClientRect();
      return { top: Math.round(value.top), bottom: Math.round(value.bottom), height: Math.round(value.height), width: Math.round(value.width) };
    };
    const sectionRects = Array.from(document.querySelectorAll('[data-testid="site-overview"] > section')).map((node) => {
      const value = node.getBoundingClientRect();
      return { label: node.getAttribute('aria-label') ?? node.getAttribute('aria-labelledby') ?? '', top: Math.round(value.top), bottom: Math.round(value.bottom), height: Math.round(value.height) };
    });
    return {
      viewport: { width: innerWidth, height: innerHeight },
      body: { scrollHeight: document.documentElement.scrollHeight, clientHeight: document.documentElement.clientHeight },
      title: document.querySelector('h1')?.textContent ?? '',
      cardCount: document.querySelectorAll('[data-slot="card"]').length,
      antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tabs, .ant-btn').length,
      overview: rect('[data-testid="site-overview"]'),
      sidebar: rect('[data-slot="sidebar"]'),
      header: rect('header'),
      brandMarkLoaded: (() => {
        const image = document.querySelector('img[src$="quanlaihe-mark.svg"]');
        return image instanceof HTMLImageElement && image.complete && image.naturalWidth > 0;
      })(),
      workspace: rect('[aria-label="站点总览工作区"]'),
      priority: rect('[data-testid="priority-card"]'),
      siteSummary: rect('[aria-label="站点概况"]'),
      dataStatus: rect('[aria-label="数据状态"]'),
      currentOperations: rect('[data-testid="current-operations-card"]'),
      energy: rect('[aria-labelledby="site-overview-energy-title"]'),
      loadTrend: rect('[aria-label="逐时负荷走势"]'),
      groupPowers: Object.fromEntries(Array.from(document.querySelectorAll('[aria-label="当前运行设备群"] [data-group-key]')).map((row) => [row.getAttribute('data-group-key'), Number(row.getAttribute('data-group-power'))])),
      sectionRects,
      firstViewportText: document.elementFromPoint(800, 900)?.closest('section')?.textContent?.slice(0, 220) ?? '',
      bodyText: document.body.innerText.slice(0, 5000),
    };
  })()`);

  const errors = cdp.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.exceptionDetails?.text ?? event.params?.entry?.text ?? event.method);
  metrics.runtimeErrors = errors;

  assert(metrics.title === '站点总览', 'Site Overview title did not render');
  assert(metrics.antCount === 0, 'Site Overview rendered legacy Ant DOM');
  assert(metrics.runtimeErrors.length === 0, `Site Overview emitted runtime errors: ${JSON.stringify(metrics.runtimeErrors)}`);
  assert(metrics.sidebar?.width >= 220 && metrics.sidebar?.width <= 260, `Desktop sidebar width is outside the shadcn-admin application range: ${JSON.stringify(metrics.sidebar)}`);
  assert(metrics.header?.height === 56, `Application header is not 56px: ${JSON.stringify(metrics.header)}`);
  assert(metrics.brandMarkLoaded, 'Application brand mark did not load');
  assert(metrics.siteSummary?.top < 180, `Site KPI summary is not rendered in top viewport: ${JSON.stringify(metrics.siteSummary)}`);
  assert(metrics.dataStatus?.top >= metrics.siteSummary?.top, `Data status card is part of the top KPI tier: ${JSON.stringify({ siteSummary: metrics.siteSummary, dataStatus: metrics.dataStatus })}`);
  assert(metrics.energy?.top > metrics.siteSummary?.bottom, `Analytics layer rendered below KPI summary: ${JSON.stringify({ siteSummary: metrics.siteSummary, energy: metrics.energy })}`);
  assert(Math.abs((metrics.loadTrend?.top ?? 0) - (metrics.energy?.top ?? 0)) <= 2, `Load trend and energy breakdown do not share a visual top baseline: ${JSON.stringify({ loadTrend: metrics.loadTrend, energy: metrics.energy })}`);
  assert(Math.abs((metrics.loadTrend?.bottom ?? 0) - (metrics.energy?.bottom ?? 0)) <= 4, `Load trend and energy breakdown do not close at the same visual baseline: ${JSON.stringify({ loadTrend: metrics.loadTrend, energy: metrics.energy })}`);
  assert(metrics.currentOperations?.top > metrics.energy?.bottom, `Operations layer rendered below analytics: ${JSON.stringify({ energy: metrics.energy, currentOperations: metrics.currentOperations })}`);
  assert(Math.abs((metrics.currentOperations?.top ?? 0) - (metrics.priority?.top ?? 0)) <= 2, `Bottom cards do not share a visual baseline: ${JSON.stringify({ currentOperations: metrics.currentOperations, priority: metrics.priority })}`);
  assert(Math.abs((metrics.currentOperations?.bottom ?? 0) - (metrics.priority?.bottom ?? 0)) <= 4, `Bottom cards do not close at the same visual baseline: ${JSON.stringify({ currentOperations: metrics.currentOperations, priority: metrics.priority })}`);
  assert(metrics.bodyText.includes('数据 需要关注') || metrics.bodyText.includes('数据 数据延迟') || metrics.bodyText.includes('数据 数据待核验'), 'Site Overview did not expose the non-ready data-quality state');
  assert(metrics.bodyText.includes('在线可用') && metrics.bodyText.includes('125 / 175'), 'Site Overview did not use the owner-provided availability denominator');
  assert(metrics.bodyText.includes('状态未知') && metrics.bodyText.includes('25 台'), 'Site Overview did not expose the owner-provided unknown population');
  assert(!metrics.bodyText.includes('125 / 150'), 'Site Overview reintroduced a UI-derived availability denominator');

  const wcagTokens = await evaluate(cdp, `(() => {
    const root = getComputedStyle(document.documentElement);
    const hexToRgb = (value) => {
      const normalized = value.trim().replace('#', '');
      if (!/^[0-9a-f]{6}$/i.test(normalized)) return null;
      return [0, 2, 4].map((offset) => parseInt(normalized.slice(offset, offset + 2), 16));
    };
    const luminance = (rgb) => rgb.reduce((sum, channel, index) => {
      const value = channel / 255;
      const linear = value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
      return sum + linear * [0.2126, 0.7152, 0.0722][index];
    }, 0);
    const contrast = (foreground, background) => {
      const fg = hexToRgb(foreground);
      const bg = hexToRgb(background);
      if (!fg || !bg) return null;
      const first = luminance(fg);
      const second = luminance(bg);
      return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
    };
    const mix = (foreground, background, alpha) => {
      const fg = hexToRgb(foreground);
      const bg = hexToRgb(background);
      return '#' + fg.map((value, index) => Math.round(alpha * value + (1 - alpha) * bg[index]).toString(16).padStart(2, '0')).join('');
    };
    const background = root.getPropertyValue('--card').trim();
    const tokens = ['--foreground', '--muted-foreground', '--warning', '--destructive', '--success'];
    const plain = Object.fromEntries(tokens.map((token) => [token, contrast(root.getPropertyValue(token).trim(), background)]));
    const tinted = Object.fromEntries(['--warning', '--destructive', '--success'].map((token) => {
      const foreground = root.getPropertyValue(token).trim();
      return [token, contrast(foreground, mix(foreground, background, 0.10))];
    }));
    return { plain, tinted };
  })()`);
  for (const [token, value] of Object.entries(wcagTokens.plain)) assert(value >= 4.5, `${token} contrast on card is below WCAG AA: ${value}`);
  for (const [token, value] of Object.entries(wcagTokens.tinted)) assert(value >= 4.5, `${token} contrast on its 10% semantic background is below WCAG AA: ${value}`);
  metrics.wcagTokens = wcagTokens;

  const accessibleNames = await evaluate(cdp, `(() => {
    const visible = (node) => node instanceof HTMLElement && node.getClientRects().length > 0 && getComputedStyle(node).visibility !== 'hidden';
    const focusables = Array.from(document.querySelectorAll('button, a[href], input, select, textarea, [tabindex="0"]')).filter(visible);
    const unnamed = focusables.filter((node) => {
      const label = node.getAttribute('aria-label') || node.getAttribute('title') || node.getAttribute('placeholder') || node.textContent?.trim();
      return !label;
    }).map((node) => node.outerHTML.slice(0, 180));
    return { focusableCount: focusables.length, unnamed };
  })()`);
  assert(accessibleNames.unnamed.length === 0, `Visible focusable controls without accessible names: ${JSON.stringify(accessibleNames.unnamed)}`);
  metrics.accessibleNames = accessibleNames;

  const shellGeometry = await evaluate(cdp, `(() => {
    const trigger = document.querySelector('[data-slot="sidebar-trigger"]');
    const separator = document.querySelector('header [data-slot="separator"][data-orientation="vertical"]');
    const header = document.querySelector('header');
    if (!(trigger instanceof HTMLElement) || !(separator instanceof HTMLElement) || !(header instanceof HTMLElement)) return null;
    const triggerRect = trigger.getBoundingClientRect();
    const separatorRect = separator.getBoundingClientRect();
    const headerRect = header.getBoundingClientRect();
    return {
      trigger: { left: triggerRect.left, right: triggerRect.right, top: triggerRect.top, bottom: triggerRect.bottom, height: triggerRect.height },
      separator: { left: separatorRect.left, right: separatorRect.right, top: separatorRect.top, bottom: separatorRect.bottom, height: separatorRect.height },
      header: { top: headerRect.top, bottom: headerRect.bottom, height: headerRect.height },
      centerDelta: Math.abs((separatorRect.top + separatorRect.bottom) / 2 - (headerRect.top + headerRect.bottom) / 2),
      gapAfterTrigger: separatorRect.left - triggerRect.right,
    };
  })()`);
  assert(shellGeometry && Math.abs(shellGeometry.separator.height - 16) <= 0.5, `Header separator is not 16px high: ${JSON.stringify(shellGeometry)}`);
  assert(shellGeometry.centerDelta <= 1, `Header separator is not vertically centered: ${JSON.stringify(shellGeometry)}`);
  assert(shellGeometry.gapAfterTrigger >= 4 && shellGeometry.gapAfterTrigger <= 16, `Header separator is horizontally detached from SidebarTrigger: ${JSON.stringify(shellGeometry)}`);
  metrics.shellGeometry = shellGeometry;

  const accountBefore = await evaluate(cdp, `(() => {
    const trigger = document.querySelector('[data-testid="real-account-trigger"]');
    const rect = trigger instanceof HTMLElement ? trigger.getBoundingClientRect() : null;
    return {
      clientWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
      bodyOverflow: getComputedStyle(document.body).overflow,
      bodyOverflowY: getComputedStyle(document.body).overflowY,
      bodyPaddingRight: getComputedStyle(document.body).paddingRight,
      scrollLocked: document.body.hasAttribute('data-scroll-locked'),
      triggerPoint: rect ? { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 } : null,
    };
  })()`);
  assert(accountBefore.triggerPoint, 'Account dropdown trigger was not measurable');
  await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: accountBefore.triggerPoint.x, y: accountBefore.triggerPoint.y, button: 'left', clickCount: 1 });
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: accountBefore.triggerPoint.x, y: accountBefore.triggerPoint.y, button: 'left', clickCount: 1 });
  await pause(200);
  const accountOpen = await evaluate(cdp, `(() => ({
    menuOpen: Boolean(document.querySelector('[data-slot="dropdown-menu-content"]')),
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
    bodyOverflow: getComputedStyle(document.body).overflow,
    bodyOverflowY: getComputedStyle(document.body).overflowY,
    bodyPaddingRight: getComputedStyle(document.body).paddingRight,
    scrollLocked: document.body.hasAttribute('data-scroll-locked'),
  }))()`);
  assert(accountOpen.menuOpen, 'Account dropdown did not open');
  assert(accountOpen.clientWidth === accountBefore.clientWidth, `Account dropdown changed viewport client width: ${JSON.stringify({ accountBefore, accountOpen })}`);
  assert(accountOpen.scrollWidth <= accountOpen.clientWidth, `Account dropdown introduced horizontal page overflow: ${JSON.stringify({ accountBefore, accountOpen })}`);
  assert(!accountOpen.scrollLocked, `Account dropdown locked body scrolling: ${JSON.stringify({ accountBefore, accountOpen })}`);
  assert(accountOpen.bodyPaddingRight === accountBefore.bodyPaddingRight, `Account dropdown changed body scrollbar compensation: ${JSON.stringify({ accountBefore, accountOpen })}`);
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await pause(150);
  metrics.accountDropdown = { before: accountBefore, open: accountOpen };

  await evaluate(cdp, `(() => {
    const trigger = Array.from(document.querySelectorAll('button')).find((node) => node.textContent?.includes('搜索页面与功能'));
    trigger?.click();
    return Boolean(trigger);
  })()`);
  await pause(200);
  const commandPalette = await evaluate(cdp, `(() => ({
    open: Boolean(document.querySelector('[data-slot="command"]')),
    itemCount: document.querySelectorAll('[data-slot="command-item"]').length,
    inputFocused: document.activeElement?.getAttribute('data-slot') === 'command-input',
  }))()`);
  assert(commandPalette.open && commandPalette.itemCount > 0 && commandPalette.inputFocused, `Command palette did not use the expected shadcn/cmdk interaction: ${JSON.stringify(commandPalette)}`);
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await pause(150);
  metrics.commandPalette = commandPalette;

  const desktopScreenshot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, 'overview-desktop.png'), Buffer.from(desktopScreenshot.data, 'base64'));

  const overviewTextResize = await evaluate(cdp, `(() => {
    document.documentElement.style.fontSize = '32px';
    return new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve({
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      priorityVisible: document.querySelector('[data-testid="priority-card"]')?.getClientRects().length > 0,
      refreshVisible: Array.from(document.querySelectorAll('button')).some((button) => button.textContent?.includes('刷新') && button.getClientRects().length > 0),
    }))));
  })()`);
  assert(overviewTextResize.scrollWidth <= overviewTextResize.clientWidth, `Overview overflowed at 200% text size: ${JSON.stringify(overviewTextResize)}`);
  assert(overviewTextResize.priorityVisible && overviewTextResize.refreshVisible, 'Overview lost primary content or actions at 200% text size');
  metrics.textResize200 = overviewTextResize;
  await evaluate(cdp, `(() => { document.documentElement.style.removeProperty('font-size'); return true; })()`);
  await pause(150);

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 720, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(300);
  await evaluate(cdp, `(() => { document.querySelector('[data-slot="sidebar-trigger"]')?.click(); return true; })()`);
  await pause(200);
  const narrow = await evaluate(cdp, `(() => ({
    viewport: { width: innerWidth, height: innerHeight },
    body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    priorityTop: Math.round(document.querySelector('[data-testid="priority-card"]')?.getBoundingClientRect().top ?? 0),
    secondaryTop: Math.round(document.querySelector('[data-testid="current-operations-card"]')?.getBoundingClientRect().top ?? 0),
    sidebarIsMobileSheet: Boolean(document.querySelector('[data-sidebar="sidebar"][data-mobile="true"]')),
    cardCount: document.querySelectorAll('[data-slot="card"]').length,
    antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tabs, .ant-btn').length,
    titleVisible: Boolean(document.querySelector('h1')),
  }))()`);
  assert(narrow.body.scrollWidth <= narrow.body.clientWidth, `Overview has horizontal overflow at narrow width: ${JSON.stringify(narrow.body)}`);
  assert(narrow.sidebarIsMobileSheet, 'Narrow shell did not switch to the shadcn mobile Sheet navigation');
  assert(narrow.priorityTop > 0 && narrow.secondaryTop > narrow.priorityTop, 'Narrow Overview lost the attention-first information order');
  assert(narrow.antCount === 0 && narrow.titleVisible, 'Narrow Overview lost shadcn composition or title');
  metrics.narrow = narrow;
  const narrowScreenshot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, 'overview-narrow.png'), Buffer.from(narrowScreenshot.data, 'base64'));

  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 320, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(250);
  const overviewReflow = await evaluate(cdp, `(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    priorityVisible: document.querySelector('[data-testid="priority-card"]')?.getClientRects().length > 0,
    titleVisible: Boolean(document.querySelector('h1')),
  }))()`);
  assert(overviewReflow.scrollWidth <= overviewReflow.clientWidth, `Overview failed WCAG reflow at 320px: ${JSON.stringify(overviewReflow)}`);
  assert(overviewReflow.priorityVisible && overviewReflow.titleVisible, 'Overview lost primary content at 320px reflow width');
  metrics.reflow320 = overviewReflow;
  console.log('[overview-review] overview accessibility gates passed');

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1672, height: 941, deviceScaleFactor: 1, mobile: false });
  await cdp.send('Page.navigate', { url: `${webURL}/?page=operations` });
  await waitForOperations(cdp);
  console.log('[overview-review] operations ready');
  await pause(600);

  const operations = await evaluate(cdp, `(() => {
    const rect = (selector) => {
      const node = document.querySelector(selector);
      if (!(node instanceof HTMLElement)) return null;
      const value = node.getBoundingClientRect();
      return { top: Math.round(value.top), bottom: Math.round(value.bottom), height: Math.round(value.height), width: Math.round(value.width) };
    };
    return {
      viewport: { width: innerWidth, height: innerHeight },
      body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      title: document.querySelector('h1')?.textContent ?? '',
      workspace: rect('[aria-label="系统运行工作区"]'),
      context: rect('[aria-label="当前运行上下文"]'),
      process: rect('[aria-label="关键过程量"]'),
      sheetCount: document.querySelectorAll('[data-slot="sheet-content"][aria-label="运行详情"]').length,
      selectedCount: document.querySelectorAll('[aria-label="设备群运行状态"] button[aria-pressed="true"]').length,
      groupPowers: Object.fromEntries(Array.from(document.querySelectorAll('[aria-label="系统运行工作区"] [data-group-key]')).map((row) => [row.getAttribute('data-group-key'), Number(row.getAttribute('data-group-power'))])),
      workspacePopulation: document.querySelector('[data-testid="operations-workspace-population"]')?.textContent?.trim() ?? '',
      statusSummaryText: document.querySelector('dl[aria-label="运行状态摘要"]')?.textContent ?? '',
      antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tabs, .ant-btn').length,
      bodyText: document.body.innerText.slice(0, 5200),
    };
  })()`);
  assert(operations.title === '系统运行', 'System Operations title did not render');
  assert(operations.antCount === 0, 'System Operations rendered legacy Ant DOM');
  assert(operations.body.scrollWidth <= operations.body.clientWidth, 'System Operations has horizontal overflow on desktop');
  assert(operations.context?.top < operations.workspace?.top, `Operating context must precede the engineering workspace: ${JSON.stringify({ context: operations.context, workspace: operations.workspace })}`);
  assert(operations.workspace?.top < operations.viewport.height, 'System Operations primary workspace is below the first viewport');
  assert(operations.process?.top >= operations.workspace?.top && operations.process?.top < operations.viewport.height, `Process evidence is not visible in the primary workspace: ${JSON.stringify(operations.process)}`);
  assert(operations.sheetCount === 0, 'System Operations opened detail before an explicit object selection.');
  assert(operations.selectedCount === 1, `System Operations should expose one selected equipment group: ${operations.selectedCount}`);
  assert(JSON.stringify(operations.groupPowers) === JSON.stringify(metrics.groupPowers), `03/04 equipment-group power facts diverged: ${JSON.stringify({ overview: metrics.groupPowers, operations: operations.groupPowers })}`);
  assert(operations.workspacePopulation === '59 / 79', `System Operations workspace population did not use the topology scope: ${operations.workspacePopulation}`);
  assert(operations.bodyText.includes('当前工作区范围'), 'System Operations did not label the topology-derived equipment population scope');
  assert(operations.bodyText.includes('站点设备数据范围'), 'System Operations did not label site-level data quality scope');
  assert(!operations.statusSummaryText.includes('故障') && !operations.statusSummaryText.includes('离线'), `System Operations exposed unscoped deviceStatus facts in the run summary: ${operations.statusSummaryText}`);

  assert(await evaluate(cdp, `(() => { const target = document.querySelector('[aria-label="设备群运行状态"] button[data-group-key="chiller"]'); if (!(target instanceof HTMLElement)) return false; target.click(); return true; })()`), 'System Operations equipment group was not clickable on desktop');
  await waitFor(cdp, `document.querySelector('[data-slot="sheet-content"][aria-label="运行详情"]')?.getClientRects().length`, 'Desktop Operations Detail Sheet');
  const operationsSheet = await evaluate(cdp, `(() => {
    const sheet=document.querySelector('[data-slot="sheet-content"][aria-label="运行详情"]');
    const workspace=document.querySelector('[aria-label="系统运行工作区"]');
    return {
      width: sheet instanceof HTMLElement ? Math.round(sheet.getBoundingClientRect().width) : 0,
      workspaceWidth: workspace instanceof HTMLElement ? Math.round(workspace.getBoundingClientRect().width) : 0,
      overlay: Boolean(document.querySelector('[data-slot="sheet-overlay"]')),
      text: sheet?.textContent ?? '',
    };
  })()`);
  assert(operationsSheet.width >= 480 && operationsSheet.width <= 640, `System Operations Detail Sheet width drifted: ${operationsSheet.width}`);
  assert(!operationsSheet.overlay, 'Desktop Operations Detail Sheet must be non-modal/no-overlay.');
  assert(operationsSheet.workspaceWidth === operations.workspace?.width, `Operations Detail Sheet changed workspace width: ${JSON.stringify({ before: operations.workspace?.width, after: operationsSheet.workspaceWidth })}`);
  assert(!operationsSheet.text.includes('数据状态') && !operationsSheet.text.includes('数据 需关注'), `Detail Sheet exposed site quality as selected-object quality: ${operationsSheet.text}`);
  const operationsDesktop = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, 'system-operations-desktop.png'), Buffer.from(operationsDesktop.data, 'base64'));
  await evaluate(cdp, `document.querySelector('[data-slot="sheet-content"][aria-label="运行详情"] [data-slot="sheet-close"]')?.click()`);
  await pause(150);

  const operationsTextResize = await evaluate(cdp, `(() => {
    document.documentElement.style.fontSize = '32px';
    return new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve({
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      workspaceVisible: document.querySelector('[aria-label="系统运行工作区"]')?.getClientRects().length > 0,
      refreshVisible: Array.from(document.querySelectorAll('button')).some((button) => button.textContent?.includes('刷新') && button.getClientRects().length > 0),
    }))));
  })()`);
  assert(operationsTextResize.scrollWidth <= operationsTextResize.clientWidth, `System Operations overflowed at 200% text size: ${JSON.stringify(operationsTextResize)}`);
  assert(operationsTextResize.workspaceVisible && operationsTextResize.refreshVisible, 'System Operations lost primary content or actions at 200% text size');
  operations.textResize200 = operationsTextResize;
  await evaluate(cdp, `(() => { document.documentElement.style.removeProperty('font-size'); return true; })()`);
  await pause(150);

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 900, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(250);
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9, nativeVirtualKeyCode: 9 });
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9, nativeVirtualKeyCode: 9 });
  const rowFocus = await evaluate(cdp, `(() => {
    const rows = Array.from(document.querySelectorAll('[aria-label="设备群运行状态"] button[aria-pressed]'));
    const target = rows[1] ?? rows[0];
    target?.focus();
    const style = target ? getComputedStyle(target) : null;
    return {
      rowCount: rows.length,
      focusVisible: Boolean(target?.matches(':focus-visible')),
      boxShadow: style?.boxShadow ?? '',
      outlineStyle: style?.outlineStyle ?? '',
      key: target?.getAttribute('data-group-key') ?? '',
    };
  })()`);
  assert(rowFocus.rowCount > 1 && rowFocus.key === 'chiller', `System Operations keyboard target was not available: ${JSON.stringify(rowFocus)}`);
  assert(rowFocus.focusVisible || (rowFocus.boxShadow && rowFocus.boxShadow !== 'none') || (rowFocus.outlineStyle && rowFocus.outlineStyle !== 'none'), `Focused equipment row had no visible focus treatment: ${JSON.stringify(rowFocus)}`);
  assert(await evaluate(cdp, `(() => { const target = document.querySelector('[aria-label="设备群运行状态"] button[data-group-key="chiller"]'); if (!(target instanceof HTMLElement)) return false; target.click(); return true; })()`), 'System Operations equipment group was not clickable');
  await pause(250);
  const operationsNarrow = await evaluate(cdp, `(() => ({
    viewport: { width: innerWidth, height: innerHeight },
    body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    sheetOpen: Boolean(document.querySelector('[data-slot="sheet-content"][data-state="open"]')),
    sheetOverlay: Boolean(document.querySelector('[data-slot="sheet-overlay"]')?.getClientRects().length),
    selectedCount: document.querySelectorAll('[aria-label="设备群运行状态"] button[aria-pressed="true"]').length,
    selectedKey: document.querySelector('[aria-label="设备群运行状态"] button[aria-pressed="true"]')?.getAttribute('data-group-key') ?? '',
    selectedStateText: document.querySelector('[data-testid="operations-selected-state"]')?.textContent?.trim() ?? '',
    selectedStateClass: document.querySelector('[data-testid="operations-selected-state"]')?.getAttribute('class') ?? '',
    antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tabs, .ant-btn').length,
  }))()`);
  assert(operationsNarrow.body.scrollWidth <= operationsNarrow.body.clientWidth, `System Operations has horizontal overflow at 900px: ${JSON.stringify(operationsNarrow.body)}`);
  assert(operationsNarrow.sheetOpen && operationsNarrow.sheetOverlay, 'System Operations narrow detail did not use a modal Sheet + overlay.');
  assert(operationsNarrow.selectedCount === 1 && operationsNarrow.selectedKey === 'chiller' && operationsNarrow.antCount === 0, `System Operations keyboard selection or shadcn composition failed: ${JSON.stringify(operationsNarrow)}`);
  assert(operationsNarrow.selectedStateText === '2 / 3 运行' && !operationsNarrow.selectedStateClass.includes('text-success'), `Partial-running group was incorrectly presented as success: ${JSON.stringify(operationsNarrow)}`);
  const operationsNarrowScreenshot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, 'system-operations-narrow.png'), Buffer.from(operationsNarrowScreenshot.data, 'base64'));
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await pause(200);
  const inspectorFocusReturn = await evaluate(cdp, `(() => ({
    sheetClosed: !document.querySelector('[data-slot="sheet-content"][data-state="open"]'),
    activePressed: document.activeElement?.getAttribute('aria-pressed') ?? '',
    activeText: document.activeElement?.textContent?.trim() ?? '',
  }))()`);
  assert(inspectorFocusReturn.sheetClosed && inspectorFocusReturn.activePressed === 'true', `Detail Sheet did not return focus to the selected equipment control: ${JSON.stringify(inspectorFocusReturn)}`);
  operationsNarrow.rowFocus = rowFocus;
  operationsNarrow.focusReturn = inspectorFocusReturn;

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 320, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(250);
  const operationsReflow = await evaluate(cdp, `(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    workspaceVisible: document.querySelector('[aria-label="系统运行工作区"]')?.getClientRects().length > 0,
    titleVisible: Boolean(document.querySelector('h1')),
  }))()`);
  assert(operationsReflow.scrollWidth <= operationsReflow.clientWidth, `System Operations failed WCAG reflow at 320px: ${JSON.stringify(operationsReflow)}`);
  assert(operationsReflow.workspaceVisible && operationsReflow.titleVisible, 'System Operations lost primary content at 320px reflow width');
  operations.reflow320 = operationsReflow;

  operations.narrow = operationsNarrow;
  metrics.systemOperations = operations;

  const allErrors = cdp.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.exceptionDetails?.text ?? event.params?.entry?.text ?? event.method);
  assert(allErrors.length === 0, `Wireframe browser review emitted runtime errors: ${JSON.stringify(allErrors)}`);

  await writeFile(join(outputRoot, 'metrics.json'), `${JSON.stringify(metrics, null, 2)}\n`, 'utf8');
  console.log(JSON.stringify({ conclusion: 'passed', webURL, screenshots: ['out/overview-review/overview-desktop.png', 'out/overview-review/overview-narrow.png', 'out/overview-review/system-operations-desktop.png', 'out/overview-review/system-operations-narrow.png'], metrics }, null, 2));
} finally {
  cdp?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => gateway.close(() => resolveClose()));
  await rm(linuxProfileDir, { recursive: true, force: true });
}
