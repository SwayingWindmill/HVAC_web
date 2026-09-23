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
import { buildCertificationInventory, certificationId } from './real-assets-certification-lib.mjs';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/diagnostics-review');
const outputRoot = resolve(root, 'out/diagnostics-review');
const linuxProfileDir = join(tmpdir(), `diagnostics-review-${process.pid}`);
const tenantId = '01940000-0000-7000-8000-000000000001';
const siteId = '01940000-0001-7000-8000-000000000001';
const inventory = buildCertificationInventory({ tenantId, siteId, namespace: '01940000' });
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const linkedAlarmId = certificationId(0x91, 1, '01940000');
const linkedWorkOrderId = certificationId(0x95, 1, '01940000');

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

function createAssetModel() {
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
  const devices = inventory.devices.map(({ certificationScenario: _scenario, ...device }) => device);
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
    telemetryPoints: [],
    relationships,
    counts: {
      spaces: spaces.length,
      assets: inventory.assets.length,
      deviceEndpoints: devices.length,
      physicalSensors: 0,
      points: 0,
    },
  };
}

function createFindings() {
  const base = Date.parse('2026-09-13T10:00:00.000Z');
  return [
    {
      id: certificationId(0x90, 1, '01940000'),
      tenantId,
      siteId,
      assetId: inventory.assets[0].id,
      findingType: '冷冻水温差偏高',
      evaluationFrom: new Date(base - 30 * 60_000).toISOString(),
      evaluationTo: new Date(base).toISOString(),
      evidenceIds: ['evidence:chw-delta:1', 'evidence:chw-delta:2', 'evidence:chw-delta:3'],
      ruleRevisionId: 'fdd-rule:chw-delta:r7',
      confidence: 0.93,
      alarmId: linkedAlarmId,
      workOrderId: linkedWorkOrderId,
      createdAt: new Date(base + 2 * 60_000).toISOString(),
    },
    {
      id: certificationId(0x90, 2, '01940000'),
      tenantId,
      siteId,
      assetId: inventory.assets[3].id,
      findingType: '冷机效率偏离基线',
      evaluationFrom: new Date(base - 90 * 60_000).toISOString(),
      evaluationTo: new Date(base - 60 * 60_000).toISOString(),
      evidenceIds: ['evidence:efficiency:1', 'evidence:efficiency:2'],
      modelDeploymentRevisionId: certificationId(0x98, 2, '01940000'),
      confidence: 0.84,
      qualityBlocker: 'SOURCE_LAG_EXCEEDED',
      createdAt: new Date(base - 58 * 60_000).toISOString(),
    },
    {
      id: certificationId(0x90, 3, '01940000'),
      tenantId,
      siteId,
      assetId: inventory.assets[7].id,
      findingType: '冷却侧换热性能下降',
      evaluationFrom: new Date(base - 180 * 60_000).toISOString(),
      evaluationTo: new Date(base - 150 * 60_000).toISOString(),
      evidenceIds: ['evidence:condenser:1'],
      ruleRevisionId: 'fdd-rule:condenser:r3',
      confidence: 0.76,
      createdAt: new Date(base - 148 * 60_000).toISOString(),
    },
    {
      id: certificationId(0x90, 4, '01940000'),
      tenantId,
      siteId,
      assetId: inventory.assets[11].id,
      findingType: '泵组运行组合需复核',
      evaluationFrom: new Date(base - 240 * 60_000).toISOString(),
      evaluationTo: new Date(base - 210 * 60_000).toISOString(),
      evidenceIds: ['evidence:pump:1', 'evidence:pump:2'],
      confidence: 0.68,
      createdAt: new Date(base - 208 * 60_000).toISOString(),
    },
    {
      id: certificationId(0x90, 5, '01940000'),
      tenantId,
      siteId,
      assetId: inventory.assets[15].id,
      findingType: '主机频繁启停迹象',
      evaluationFrom: new Date(base - 300 * 60_000).toISOString(),
      evaluationTo: new Date(base - 270 * 60_000).toISOString(),
      evidenceIds: ['evidence:cycling:1', 'evidence:cycling:2', 'evidence:cycling:3'],
      modelDeploymentRevisionId: certificationId(0x98, 5, '01940000'),
      confidence: 0.81,
      createdAt: new Date(base - 268 * 60_000).toISOString(),
    },
  ];
}

function problem(status, code, detail) {
  return {
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    instance: '/api/v1/diagnostics-review',
    code,
    traceId: '0123456789abcdef0123456789abcdef',
    retryable: false,
  };
}

function writeJson(response, status, payload) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'private, no-store',
    'x-route-policy-revision': 'diagnostics-review-policy:1',
  });
  response.end(JSON.stringify(payload));
}

function createGateway() {
  const model = createAssetModel();
  const findings = createFindings();
  return createHTTPServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://diagnostics-review.local');
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/asset-model`) {
      writeJson(response, 200, model);
      return;
    }
    if (request.method === 'GET' && url.pathname === `/api/v1/sites/${siteId}/fdd/findings`) {
      writeJson(response, 200, { items: findings });
      return;
    }
    writeJson(response, 404, problem(404, 'RESOURCE_NOT_FOUND', `No review fixture for ${url.pathname}`));
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

async function waitFor(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      last = await evaluate(client, expression);
      if (last) return last;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ text: document.body?.innerText?.slice(0, 7000) ?? '', html: document.body?.innerHTML?.slice(0, 3000) ?? '' })`).catch((error) => ({ error: String(error) }));
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
    publicDir: false,
    configFile: false,
    logLevel: 'error',
    plugins: [react(), tailwindcss()],
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '127.0.0.1',
      port: Number(process.env.DIAGNOSTICS_REVIEW_PORT ?? 0),
      strictPort: Boolean(process.env.DIAGNOSTICS_REVIEW_PORT),
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Diagnostics review Vite server has no address');
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
  await cdp.send('Page.navigate', { url: `${webURL}/?source=alarm&alarm=${encodeURIComponent(linkedAlarmId)}` });

  await waitFor(
    cdp,
    `document.querySelector('[data-testid="diagnostics-workspace"]')?.getAttribute('data-business-state') === 'READY' && document.querySelectorAll('[data-testid="diagnostics-queue"] [data-slot="table-body"] [data-slot="table-row"]').length === 5 && document.body.innerText.includes('冷冻水温差偏高') && document.body.innerText.includes('尚无根因结论')`,
    'Diagnostics workspace',
  );
  await pause(300);

  const desktop = await evaluate(cdp, `(() => ({
    viewport: { width: innerWidth, height: innerHeight },
    body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    rowCount: document.querySelectorAll('[data-testid="diagnostics-queue"] [data-slot="table-body"] [data-slot="table-row"]').length,
    cardCount: document.querySelectorAll('[data-slot="card"]').length,
    antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tag, .ant-alert').length,
    inputGroupCount: document.querySelectorAll('[data-slot="input-group"]').length,
    contextText: document.querySelector('[data-testid="diagnostics-source-context"]')?.textContent ?? '',
    detailText: document.querySelector('[data-testid="diagnostics-detail"]')?.textContent ?? '',
    allText: document.body.innerText,
    links: Array.from(document.querySelectorAll('[data-testid="diagnostics-detail"] a')).map((node) => ({ text: node.textContent ?? '', href: node.getAttribute('href') })),
  }))()`);
  assert(desktop.rowCount === 5, `Diagnostics queue lost certification findings: ${desktop.rowCount}`);
  assert(desktop.antCount === 0, 'Diagnostics workspace rendered legacy Ant DOM');
  assert(desktop.inputGroupCount === 1, 'Diagnostics queue did not use the shadcn Input Group search pattern');
  assert(desktop.cardCount <= 2, `Diagnostics workspace regressed into a card wall: ${desktop.cardCount}`);
  assert(desktop.body.scrollWidth <= desktop.body.clientWidth, 'Diagnostics workspace has page-level horizontal overflow');
  for (const responsibility of ['已验证事实', '已发布诊断结果', '根因假设', '尚无根因结论', '影响范围与关联对象', '下一验证 / 动作', '调查更新']) {
    assert(desktop.detailText.includes(responsibility), `Diagnostics detail lost responsibility: ${responsibility}`);
  }
  assert(desktop.contextText.includes('来自告警') && desktop.contextText.includes('已找到显式关联的诊断结果'), 'Alarm source context did not resolve the explicitly linked Finding');
  assert(desktop.detailText.includes('置信度 93%'), 'Authoritative Finding confidence was not shown');
  assert(desktop.detailText.includes('规则命中说明检测条件成立，不自动等于根因已经确认'), 'Deterministic result was styled as a root-cause conclusion');
  assert(!desktop.detailText.includes('SOURCE_LAG_EXCEEDED'), 'Diagnostics detail leaked an internal quality blocker code');
  for (const internalId of [siteId, linkedAlarmId, linkedWorkOrderId, inventory.assets[0].id, certificationId(0x90, 1, '01940000')]) {
    assert(!desktop.allText.includes(internalId), `Diagnostics workspace leaked an internal identifier: ${internalId}`);
  }
  assert(desktop.links.some((link) => link.text.includes('查看关联告警') && link.href?.includes('/issues?source=diagnostics&alarm=')), 'Diagnostics workspace lost the Alarm handoff');
  assert(desktop.links.some((link) => link.text.includes('查看关联工单') && link.href?.includes('/work-orders?source=diagnostics&workOrder=')), 'Diagnostics workspace lost the Work Order handoff');
  assert(desktop.links.some((link) => link.text.includes('在设备中查看') && link.href?.includes('/devices?q=')), 'Diagnostics workspace lost the Devices handoff');
  await capture(cdp, 'diagnostics-desktop.png');

  assert(await evaluate(cdp, `(() => {
    const rows = document.querySelectorAll('[data-testid="diagnostics-queue"] [data-slot="table-body"] [data-slot="table-row"]');
    if (!(rows[1] instanceof HTMLElement)) return false;
    rows[1].click();
    return true;
  })()`), 'Second diagnostics row was not selectable');
  await waitFor(cdp, `document.querySelector('[data-testid="diagnostics-detail"]')?.textContent?.includes('冷机效率偏离基线') && document.querySelector('[data-testid="diagnostics-detail"]')?.textContent?.includes('证据受限')`, 'Quality-blocked model Finding');
  const modelFinding = await evaluate(cdp, `document.querySelector('[data-testid="diagnostics-detail"]')?.textContent ?? ''`);
  assert(modelFinding.includes('模型结果') && modelFinding.includes('置信度 84%'), 'Model Finding source/confidence did not remain explicit');
  assert(modelFinding.includes('不等于根因概率') && modelFinding.includes('尚无根因结论'), 'Model Finding was promoted into a root-cause conclusion');
  assert(!modelFinding.includes('SOURCE_LAG_EXCEEDED'), 'Model Finding exposed the internal quality blocker code');
  await capture(cdp, 'diagnostics-model-limited.png');

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 768, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(300);
  const narrow = await evaluate(cdp, `(() => ({
    viewport: { width: innerWidth, height: innerHeight },
    body: { scrollHeight: document.documentElement.scrollHeight, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    rowCount: document.querySelectorAll('[data-testid="diagnostics-queue"] [data-slot="table-body"] [data-slot="table-row"]').length,
    detailTop: Math.round(document.querySelector('[data-testid="diagnostics-detail"]')?.getBoundingClientRect().top ?? 0),
    antCount: document.querySelectorAll('.ant-card, .ant-table, .ant-tag, .ant-alert').length,
  }))()`);
  assert(narrow.body.scrollWidth <= narrow.body.clientWidth, `Diagnostics workspace overflows at 768px: ${JSON.stringify(narrow.body)}`);
  assert(narrow.rowCount === 5 && narrow.antCount === 0, 'Narrow Diagnostics workspace lost queue density or shadcn composition');
  await capture(cdp, 'diagnostics-narrow.png');

  const runtimeErrors = cdp.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error' && event.params?.entry?.source === 'javascript'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.entry?.text ?? event.method);
  assert(runtimeErrors.length === 0, `Diagnostics review emitted runtime errors: ${JSON.stringify(runtimeErrors)}`);

  const evidence = {
    conclusion: 'passed',
    source: 'real-assets-certification-library',
    screenshots: [
      'out/diagnostics-review/diagnostics-desktop.png',
      'out/diagnostics-review/diagnostics-model-limited.png',
      'out/diagnostics-review/diagnostics-narrow.png',
    ],
    desktop,
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
