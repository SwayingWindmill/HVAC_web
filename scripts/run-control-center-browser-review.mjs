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
const fixtureRoot = resolve(root, 'scripts/fixtures/control-center-review');
const outputRoot = resolve(root, 'out/control-center-review');
const linuxProfileDir = join(tmpdir(), `control-center-review-${process.pid}`);
const tenantId = '01970000-0000-7000-8000-000000000001';
const siteId = '01970000-0001-7000-8000-000000000001';
const ids = {
  chillerAsset: '01970000-0010-7000-8000-000000000001',
  pumpAsset: '01970000-0011-7000-8000-000000000001',
  chillerDevice: '01970000-0020-7000-8000-000000000001',
  pumpDevice: '01970000-0021-7000-8000-000000000001',
  chwsCommand: '01970000-0030-7000-8000-000000000001',
  chwsFeedback: '01970000-0031-7000-8000-000000000001',
  pumpStart: '01970000-0032-7000-8000-000000000001',
  pumpRun: '01970000-0033-7000-8000-000000000001',
  invalidCommand: '01970000-0034-7000-8000-000000000001',
  relationChws: '01970000-0040-7000-8000-000000000001',
  relationPump: '01970000-0041-7000-8000-000000000001',
  relationInvalid: '01970000-0042-7000-8000-000000000001',
};
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

const createdAt = '2026-09-01T00:00:00.000Z';
const updatedAt = '2026-09-16T07:55:00.000Z';
const validFrom = '2026-09-01T00:00:00.000Z';
const evaluatedAt = '2026-09-16T08:00:00.000Z';

function asset(id, code, displayName, assetType) {
  return { id, tenantId, siteId, code, displayName, assetType, status: 'ACTIVE', revision: 1, createdAt, updatedAt };
}

function device(id, code, displayName, deviceType) {
  return { id, tenantId, siteId, code, displayName, deviceType, status: 'ACTIVE', revision: 1, createdAt, updatedAt };
}

function point({ id, deviceId, pointCode, sourceKey, displayName, pointType, valueType, unit, writable, sourceMetadata = {} }) {
  return {
    id,
    tenantId,
    siteId,
    reportingDeviceId: deviceId,
    sensorId: null,
    pointCode,
    sourceKey,
    displayName,
    pointType,
    valueType,
    unit,
    writable,
    sampleIntervalMs: 1000,
    publishIntervalMs: 1000,
    staleAfterMs: 60000,
    counterDecreaseMode: null,
    counterRolloverModulus: null,
    sourceMetadata,
    status: 'ACTIVE',
    revision: 1,
    createdAt,
    updatedAt,
  };
}

function relationship(id, fromId, toId) {
  return {
    id,
    tenantId,
    siteId,
    fromType: 'POINT',
    fromId,
    toType: 'ASSET',
    toId,
    role: 'CONTROLS',
    status: 'ACTIVE',
    validFrom,
    validTo: null,
    revision: 1,
    createdAt,
    updatedAt,
  };
}

const assetModel = {
  schemaVersion: 2,
  tenantId,
  siteId,
  spaces: [],
  assets: [
    asset(ids.chillerAsset, 'CH-01', '1# 冷水机组', 'CHILLER'),
    asset(ids.pumpAsset, 'CHWP-01', '1# 冷冻水泵', 'CHILLED_WATER_PUMP'),
  ],
  devices: [
    device(ids.chillerDevice, 'CH-01-PLC', '1# 冷水机组控制器', 'PLC'),
    device(ids.pumpDevice, 'CHWP-01-VFD', '1# 冷冻水泵变频器', 'VFD'),
  ],
  sensors: [],
  telemetryPoints: [
    point({
      id: ids.chwsCommand,
      deviceId: ids.chillerDevice,
      pointCode: 'chws_setpoint_command',
      sourceKey: 'chws_setpoint_command',
      displayName: '冷冻水供水温度设定',
      pointType: 'COMMAND',
      valueType: 'NUMBER',
      unit: '°C',
      writable: true,
      sourceMetadata: {
        capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
        capabilityRevision: 'capability:set-chilled-water-temperature-setpoint:v1',
        parameterKey: 'setpointC',
        feedbackSourceKey: 'chws_temp',
      },
    }),
    point({
      id: ids.chwsFeedback,
      deviceId: ids.chillerDevice,
      pointCode: 'chws_temp',
      sourceKey: 'chws_temp',
      displayName: '冷冻水供水温度',
      pointType: 'TELEMETRY',
      valueType: 'NUMBER',
      unit: '°C',
      writable: false,
    }),
    point({
      id: ids.pumpStart,
      deviceId: ids.pumpDevice,
      pointCode: 'pump_start_command',
      sourceKey: 'pump_start_command',
      displayName: '水泵启动控制',
      pointType: 'COMMAND',
      valueType: 'BOOLEAN',
      unit: null,
      writable: true,
      sourceMetadata: {
        capability: 'START',
        capabilityRevision: 'capability:start:v1',
        feedbackSourceKey: 'pump_run',
      },
    }),
    point({
      id: ids.pumpRun,
      deviceId: ids.pumpDevice,
      pointCode: 'pump_run',
      sourceKey: 'pump_run',
      displayName: '水泵运行状态',
      pointType: 'STATE',
      valueType: 'BOOLEAN',
      unit: null,
      writable: false,
    }),
    point({
      id: ids.invalidCommand,
      deviceId: ids.pumpDevice,
      pointCode: 'legacy_writable_command',
      sourceKey: 'legacy_writable_command',
      displayName: '历史可写点',
      pointType: 'COMMAND',
      valueType: 'NUMBER',
      unit: '%',
      writable: true,
      sourceMetadata: {
        capability: 'SET_LOAD_LIMIT',
        capabilityRevision: 'outdated-revision',
        parameterKey: 'loadLimitPercent',
        feedbackSourceKey: 'pump_run',
      },
    }),
  ],
  relationships: [
    relationship(ids.relationChws, ids.chwsCommand, ids.chillerAsset),
    relationship(ids.relationPump, ids.pumpStart, ids.pumpAsset),
    relationship(ids.relationInvalid, ids.invalidCommand, ids.pumpAsset),
  ],
  counts: { spaces: 0, assets: 2, deviceEndpoints: 2, physicalSensors: 0, points: 5 },
};

function observationSnapshot(deviceId, key) {
  const chiller = deviceId === ids.chillerDevice;
  const value = chiller ? 7.2 : true;
  const valueType = chiller ? 'NUMBER' : 'BOOLEAN';
  const unit = chiller ? '°C' : null;
  return {
    schemaVersion: 1,
    deviceId,
    tenantId,
    siteId,
    businessRevision: 12,
    evaluatedAt,
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE',
      currentState: 'ONLINE',
      lastSeenAt: '2026-09-16T07:59:58.000Z',
      policyRevision: 3,
      lastKnown: {
        state: 'ONLINE',
        lastSeenAt: '2026-09-16T07:59:58.000Z',
        evaluatedAt,
        policyRevision: 3,
      },
    },
    telemetryReadiness: 'CURRENT',
    displayState: 'ONLINE',
    values: [{
      key,
      state: 'PRESENT',
      value,
      valueType,
      unit,
      sampledAt: '2026-09-16T07:59:59.000Z',
      receivedAt: '2026-09-16T07:59:59.100Z',
      freshness: 'FRESH',
      quality: 'GOOD',
      qualityReasons: [],
      policyRevision: 3,
    }],
  };
}

function createGateway() {
  const requests = [];
  const server = createHTTPServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://control-review.local');
    const record = { method: request.method ?? 'GET', path: url.pathname, search: url.search, status: 0 };
    requests.push(record);

    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/asset-model`) {
      record.status = 200;
      json(response, 200, assetModel);
      return;
    }

    const observationMatch = url.pathname.match(/^\/api\/v1\/devices\/([^/]+)\/observation-snapshot$/);
    if (request.method === 'GET' && observationMatch) {
      const deviceId = decodeURIComponent(observationMatch[1]);
      const key = url.searchParams.get('keys') ?? '';
      if ((deviceId === ids.chillerDevice && key === 'chws_temp') || (deviceId === ids.pumpDevice && key === 'pump_run')) {
        record.status = 200;
        json(response, 200, observationSnapshot(deviceId, key));
        return;
      }
    }

    record.status = 404;
    json(response, 404, {
      type: 'https://api.quanlaihe.com/problems/resource-not-found',
      title: 'Resource not found',
      status: 404,
      detail: `No control review fixture for ${url.pathname}`,
      instance: url.pathname,
      code: 'RESOURCE_NOT_FOUND',
      traceId: '0123456789abcdef0123456789abcdef',
      retryable: false,
    });
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
  const diagnostic = await evaluate(client, `({ bodyText: document.body.innerText.slice(0,6000), html: document.body.innerHTML.slice(0,3000) })`).catch((error) => ({ error: String(error) }));
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
const vitePort = Number(process.env.CONTROL_REVIEW_PORT ?? 6199);
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
      port: vitePort,
      strictPort: Boolean(process.env.CONTROL_REVIEW_PORT),
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

  await waitFor(
    client,
    `document.querySelector('[data-testid="control-center"]')?.getAttribute('data-business-state') === 'READY'
      && !document.querySelector('[data-testid="control-headline-facts"]')
      && document.querySelectorAll('[data-testid="control-targets"] button[aria-pressed]').length === 2
      && document.querySelector('[data-testid="control-current-state"]')?.textContent?.includes('质量良好')
      && document.querySelector('[data-testid="control-preflight-status"]')?.textContent?.includes('用户授权')`,
    'Surface 25 control center',
  );
  await pause(250);

  const desktop = await evaluate(client, `(() => {
    const rect = (selector) => {
      const node = document.querySelector(selector);
      if (!(node instanceof HTMLElement)) return null;
      const r = node.getBoundingClientRect();
      return { top: Math.round(r.top), bottom: Math.round(r.bottom), width: Math.round(r.width), height: Math.round(r.height) };
    };
    return {
      page: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth, scrollHeight: document.documentElement.scrollHeight },
      sidebar: rect('[data-slot="sidebar"]'),
      header: rect('header'),
      safety: rect('[data-testid="control-safety-boundary"]'),
      targets: rect('[data-testid="control-targets"]'),
      detail: rect('[data-testid="control-target-detail"]'),
      preflight: rect('[data-testid="control-preflight-status"]'),
      priority: rect('[data-testid="control-priority-attention"]'),
      recent: rect('[data-testid="control-recent-executions"]'),
      state: document.querySelector('[data-testid="control-center"]')?.getAttribute('data-business-state'),
      commandState: document.querySelector('[data-testid="control-center"]')?.getAttribute('data-command-state'),
      targetRows: document.querySelectorAll('[data-testid="control-targets"] button[aria-pressed]').length,
      registrationText: document.querySelector('[data-testid="control-registration-warning"]')?.textContent ?? '',
      currentText: document.querySelector('[data-testid="control-current-state"]')?.textContent ?? '',
      preflightText: document.querySelector('[data-testid="control-preflight-status"]')?.textContent ?? '',
      safetyText: document.querySelector('[data-testid="control-safety-boundary"]')?.textContent ?? '',
      priorityText: document.querySelector('[data-testid="control-priority-attention"]')?.textContent ?? '',
      recentText: document.querySelector('[data-testid="control-recent-executions"]')?.textContent ?? '',
      headlineFactsPresent: Boolean(document.querySelector('[data-testid="control-headline-facts"]')),
      repeatedUnavailableAlertPresent: Boolean(document.querySelector('[data-testid="control-command-unavailable"]')),
      unavailableBadgeCount: (document.body.innerText.match(/当前不可执行/g) ?? []).length,
      antCount: document.querySelectorAll('.ant-card,.ant-select,.ant-btn,.ant-table,.ant-typography').length,
      bodyText: document.body.innerText,
      search: location.search,
    };
  })()`);

  assert(desktop.page.scrollWidth <= desktop.page.clientWidth, `Desktop page overflowed: ${JSON.stringify(desktop.page)}`);
  assert(desktop.sidebar?.width === 256 && desktop.header?.height === 56, `Shared shell geometry drifted: ${JSON.stringify({ sidebar: desktop.sidebar, header: desktop.header })}`);
  assert(desktop.state === 'READY' && desktop.commandState === 'BLOCKED_PREFLIGHT_UNAVAILABLE', `Surface 25 business/safety state drifted: ${JSON.stringify({ state: desktop.state, commandState: desktop.commandState })}`);
  assert(desktop.antCount === 0, 'Surface 25 rendered Ant DOM');
  assert(!desktop.headlineFactsPresent && !desktop.repeatedUnavailableAlertPresent, 'Surface 25 regressed to headline KPI or repeated unavailable alert modules');
  assert(desktop.unavailableBadgeCount === 1, `Current-unavailable state is repeated instead of contextualized once: ${desktop.unavailableBadgeCount}`);
  assert(desktop.safety?.top < 280 && desktop.targets?.top < 430 && desktop.detail?.top === desktop.targets?.top, `Control decision workspace hierarchy drifted: ${JSON.stringify({ safety: desktop.safety, targets: desktop.targets, detail: desktop.detail })}`);
  assert(desktop.targets && desktop.detail && desktop.targets.width < desktop.detail.width, `Control target ledger is competing with the decision workspace: ${JSON.stringify({ targets: desktop.targets, detail: desktop.detail })}`);
  assert(desktop.priority && desktop.recent && desktop.priority.top === desktop.recent.top && desktop.priority.top > desktop.detail.bottom, `Control supporting band is not aligned below the decision workspace: ${JSON.stringify({ priority: desktop.priority, recent: desktop.recent, detail: desktop.detail })}`);
  assert(desktop.targetRows === 2 && desktop.registrationText.includes('1 项控制登记信息不完整'), `Control registration projection regressed: ${JSON.stringify({ targetRows: desktop.targetRows, registrationText: desktop.registrationText })}`);
  assert(desktop.currentText.includes('质量良好') && !desktop.currentText.includes('GOOD'), `Feedback presentation leaked raw quality enum: ${desktop.currentText}`);
  for (const fact of ['用户授权', '当前控制权', '前置条件', '联锁', '更高优先级来源', '审批要求', '影响范围', '临时覆盖']) {
    assert(desktop.preflightText.includes(fact), `Preflight facts lost ${fact}`);
  }
  assert(desktop.safetyText.includes('控制操作暂不可用') && desktop.safetyText.includes('保持只读') && desktop.safetyText.includes('不会用在线、可写或反馈正常替代控制许可'), 'Safety boundary is not explicit enough');
  assert(desktop.priorityText.includes('控制登记信息不完整') && desktop.priorityText.includes('当前控制来源尚未提供'), 'Priority attention lost control-governance exceptions');
  assert(desktop.recentText.includes('当前没有可展示的近期执行记录') && desktop.recentText.includes('不会根据命令点、ACK 或本地历史推断执行结果'), 'Recent execution empty state fabricated or weakened execution truth');
  for (const forbidden of ['traceId', 'tenantId', tenantId, siteId, 'Priority 16', '控制成功', '接口接入', '当前浏览器', '契约完整']) {
    assert(!desktop.bodyText.includes(forbidden), `Surface 25 leaked or fabricated forbidden text: ${forbidden}`);
  }
  assert(desktop.search.includes('target='), `Selected target was not represented in URL search: ${desktop.search}`);

  const targetButtons = await evaluate(client, `Array.from(document.querySelectorAll('[data-testid="control-targets"] button')).map((button) => ({ text: button.textContent?.trim(), pressed: button.getAttribute('aria-pressed') }))`);
  assert(targetButtons.length === 2 && targetButtons.filter((button) => button.pressed === 'true').length === 1, `Target selection semantics drifted: ${JSON.stringify(targetButtons)}`);
  await evaluate(client, `(() => { const buttons = Array.from(document.querySelectorAll('[data-testid="control-targets"] button')); if (buttons[1] instanceof HTMLElement) buttons[1].click(); })()`);
  await waitFor(client, `document.querySelectorAll('[data-testid="control-targets"] button[aria-pressed="true"]').length === 1 && location.search.includes('target=')`, 'second control target selection');
  await pause(200);
  const selectedAfterClick = await evaluate(client, `({
    selectedAsset: document.querySelector('[data-testid="control-targets"] button[aria-pressed="true"] [data-slot="item-title"]')?.textContent?.trim() ?? '',
    detail: document.querySelector('[data-testid="control-target-detail"]')?.textContent ?? '',
    search: location.search,
  })`);
  assert(selectedAfterClick.selectedAsset && selectedAfterClick.detail.includes(selectedAfterClick.selectedAsset), `Target detail did not follow selection: ${JSON.stringify(selectedAfterClick)}`);

  const screenshotDesktop = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, '25-control-center-desktop.png'), Buffer.from(screenshotDesktop.data, 'base64'));

  await client.send('Emulation.setDeviceMetricsOverride', { width: 900, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(300);
  const narrow = await evaluate(client, `(() => {
    return {
      page: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      safetyVisible: Boolean(document.querySelector('[data-testid="control-safety-boundary"]')?.getClientRects().length),
      targetsVisible: Boolean(document.querySelector('[data-testid="control-targets"]')?.getClientRects().length),
      detailVisible: Boolean(document.querySelector('[data-testid="control-target-detail"]')?.getClientRects().length),
      preflightVisible: Boolean(document.querySelector('[data-testid="control-preflight-status"]')?.getClientRects().length),
      targetItems: document.querySelectorAll('[data-testid="control-targets"] button[aria-pressed]').length,
      antCount: document.querySelectorAll('.ant-card,.ant-select,.ant-btn,.ant-table').length,
    };
  })()`);
  assert(narrow.page.scrollWidth <= narrow.page.clientWidth && narrow.safetyVisible && narrow.targetsVisible && narrow.detailVisible && narrow.preflightVisible, `900px workspace failed: ${JSON.stringify(narrow)}`);
  assert(narrow.antCount === 0, 'Narrow Surface 25 rendered Ant DOM');
  assert(narrow.targetItems === 2, `Control target list lost items at 900px: ${narrow.targetItems}`);
  const screenshotNarrow = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, '25-control-center-narrow.png'), Buffer.from(screenshotNarrow.data, 'base64'));

  await client.send('Emulation.setDeviceMetricsOverride', { width: 320, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(300);
  const mobile = await evaluate(client, `({
    scrollWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
    titleVisible: document.body.innerText.includes('控制'),
    safetyVisible: document.body.innerText.includes('控制操作暂不可用'),
    preflightVisible: document.body.innerText.includes('执行前检查'),
    permissionVisible: document.body.innerText.includes('用户授权'),
  })`);
  assert(mobile.scrollWidth <= mobile.viewportWidth + 1 && mobile.titleVisible && mobile.safetyVisible && mobile.preflightVisible && mobile.permissionVisible, `320px reflow failed: ${JSON.stringify(mobile)}`);

  const commandRequests = gateway.requests.filter((request) => request.path.startsWith('/api/v1/commands'));
  const assetModelRequests = gateway.requests.filter((request) => request.path === `/api/v1/sites/${siteId}/asset-model`);
  const observationRequests = gateway.requests.filter((request) => request.path.endsWith('/observation-snapshot'));
  assert(commandRequests.length === 0, `Surface 25 attempted command API calls: ${JSON.stringify(commandRequests)}`);
  assert(assetModelRequests.length === 1, `Surface 25 asset-model fetch discipline regressed: ${JSON.stringify(assetModelRequests)}`);
  assert(observationRequests.length >= 2 && observationRequests.length <= 4, `Surface 25 feedback reads look uncontrolled: ${JSON.stringify(observationRequests)}`);
  assert(gateway.requests.every((request) => request.status === 200), `Unexpected gateway requests occurred: ${JSON.stringify(gateway.requests.filter((request) => request.status !== 200))}`);

  const browserErrors = client.events
    .filter((event) => event.method === 'Runtime.exceptionThrown'
      || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error' && event.params?.entry?.source === 'javascript'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.entry?.text ?? event.method);
  assert(browserErrors.length === 0, `Browser emitted errors: ${browserErrors.join(' | ')}`);

  console.log(JSON.stringify({
    conclusion: 'passed',
    webURL,
    screenshots: [
      'out/control-center-review/25-control-center-desktop.png',
      'out/control-center-review/25-control-center-narrow.png',
    ],
    desktop: {
      page: desktop.page,
      sidebar: desktop.sidebar,
      header: desktop.header,
      safety: desktop.safety,
      facts: desktop.facts,
      targets: desktop.targets,
      detail: desktop.detail,
      preflight: desktop.preflight,
      targetRows: desktop.targetRows,
    },
    narrow,
    mobile,
    requests: {
      assetModel: assetModelRequests.length,
      observationSnapshots: observationRequests.length,
      command: commandRequests.length,
    },
  }, null, 2));
} finally {
  client?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => gateway.server.close(() => resolveClose()));
  await rm(linuxProfileDir, { recursive: true, force: true });
}
