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
const fixtureRoot = resolve(root, 'scripts/fixtures/energy-analysis-review');
const outputRoot = resolve(root, 'out/energy-analysis-review');
const linuxProfileDir = join(tmpdir(), `energy-analysis-review-${process.pid}`);
const siteId = '01960000-0001-7000-8000-000000000001';
const tenantId = '01960000-0000-7000-8000-000000000001';
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

function localDateKey(instant, timeZone) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(new Date(instant));
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return `${values.year}-${values.month}-${values.day}`;
}

function dailyPoints(from, count, values) {
  const start = Date.parse(from);
  return Array.from({ length: count }, (_, index) => ({
    periodStart: new Date(start + index * 24 * 60 * 60 * 1000).toISOString(),
    periodEnd: new Date(start + (index + 1) * 24 * 60 * 60 * 1000).toISOString(),
    energyKWh: values(index),
  }));
}

function energyResponse(query) {
  const startDate = localDateKey(query.from, query.timezone);
  let points;
  let partial = false;
  let revision;
  let watermark;

  if (startDate === '2026-07-01') {
    points = dailyPoints(query.from, 31, (index) => 3650 + (index % 7) * 55 + (index === 5 ? 120 : 0));
    revision = 'energy-july-r4';
    watermark = new Date(Date.parse(query.to) - 60 * 60 * 1000).toISOString();
  } else if (startDate === '2026-08-01') {
    points = dailyPoints(query.from, 31, (index) => 3820 + (index % 7) * 55 + (index === 5 ? 720 : index === 8 ? 510 : index === 11 ? 390 : 0));
    revision = 'energy-august-r7';
    watermark = new Date(Date.parse(query.to) - 60 * 60 * 1000).toISOString();
  } else if (startDate === '2026-09-01') {
    points = dailyPoints(query.from, 15, (index) => 3970 + (index % 7) * 60 + (index === 6 ? 480 : 0));
    partial = true;
    revision = 'energy-september-r2';
    watermark = '2026-09-15T12:00:00.000Z';
  } else {
    points = [];
    revision = `energy-empty-${startDate}`;
    watermark = new Date(Date.parse(query.from) + 60 * 60 * 1000).toISOString();
  }

  return {
    schemaVersion: 1,
    points,
    metadata: {
      requestedGranularity: query.granularity,
      actualGranularity: query.granularity,
      dataWatermark: watermark,
      aggregateWatermark: watermark,
      datasetRevision: revision,
      partial,
      qualitySummary: { valid: points.length, suspect: 0, invalid: 0 },
    },
  };
}

function createGateway() {
  const requests = [];
  const server = createHTTPServer(async (request, response) => {
    const url = new URL(request.url ?? '/', 'http://energy-review.local');
    const record = { method: request.method ?? 'GET', path: url.pathname, status: 0, body: null };
    requests.push(record);
    if (request.method === 'POST' && url.pathname === '/api/v1/analytics/energy-series') {
      const body = await requestJson(request);
      record.body = body;
      if (body.tenantId !== tenantId || body.siteId !== siteId || body.energyType !== 'electricity') {
        record.status = 422;
        json(response, 422, {
          type: 'https://api.quanlaihe.com/problems/invalid-energy-query',
          title: 'Invalid energy query',
          status: 422,
          code: 'INVALID_ENERGY_QUERY',
          detail: 'Review query scope is invalid.',
          retryable: false,
        });
        return;
      }
      record.status = 200;
      json(response, 200, energyResponse(body));
      return;
    }
    record.status = 404;
    json(response, 404, {
      type: 'https://api.quanlaihe.com/problems/resource-not-found',
      title: 'Resource not found',
      status: 404,
      code: 'RESOURCE_NOT_FOUND',
      detail: `No energy review fixture for ${url.pathname}`,
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
  const diagnostic = await evaluate(client, `({ bodyText: document.body.innerText.slice(0,5000), html: document.body.innerHTML.slice(0,2500) })`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready: ${JSON.stringify(diagnostic)}`);
}

async function clickText(client, selector, text) {
  return evaluate(client, `(() => {
    const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)})).find((candidate) => candidate.textContent?.trim().includes(${JSON.stringify(text)}));
    if (!(node instanceof HTMLElement)) return false;
    node.click();
    return true;
  })()`);
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
      port: Number(process.env.ENERGY_REVIEW_PORT ?? 0),
      strictPort: Boolean(process.env.ENERGY_REVIEW_PORT),
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
    `document.querySelector('[data-testid="energy-analysis"]')?.getAttribute('data-business-state') === 'READY'
      && document.body.innerText.includes('差异不是已验证节能量')
      && document.querySelectorAll('[data-testid="energy-variance-windows"] ol > li').length === 3`,
    'completed-period Energy Analysis',
  );
  await pause(350);

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
      controls: rect('[data-testid="energy-analysis-controls"]'),
      facts: rect('[data-testid="energy-headline-facts"]'),
      profile: rect('[data-testid="energy-primary-profile"]'),
      contributors: rect('[data-testid="energy-contributors"]'),
      variances: rect('[data-testid="energy-variance-windows"]'),
      state: document.querySelector('[data-testid="energy-analysis"]')?.getAttribute('data-business-state'),
      factText: document.querySelector('[data-testid="energy-headline-facts"]')?.textContent ?? '',
      factRegions: document.querySelector('[data-testid="energy-headline-facts"] > div')?.children.length ?? 0,
      controlText: document.querySelector('[data-testid="energy-analysis-controls"]')?.textContent ?? '',
      profileText: document.querySelector('[data-testid="energy-primary-profile"]')?.textContent ?? '',
      contributorText: document.querySelector('[data-testid="energy-contributors"]')?.textContent ?? '',
      detailText: document.querySelector('[data-testid="energy-professional-detail"]')?.textContent ?? '',
      varianceCount: document.querySelectorAll('[data-testid="energy-variance-windows"] ol > li').length,
      chartRole: document.querySelector('[data-testid="energy-primary-profile"] [role="img"]')?.getAttribute('aria-label') ?? '',
      antCount: document.querySelectorAll('.ant-card,.ant-select,.ant-btn,.ant-table,.ant-typography').length,
      fetchCalls: (globalThis.__energyFetchCalls ?? []).length,
      bodyText: document.body.innerText,
    };
  })()`);

  assert(desktop.page.scrollWidth <= desktop.page.clientWidth, `Desktop page overflowed: ${JSON.stringify(desktop.page)}`);
  assert(desktop.sidebar?.width === 256 && desktop.header?.height === 56, `Shared shell geometry drifted: ${JSON.stringify({ sidebar: desktop.sidebar, header: desktop.header })}`);
  assert(desktop.state === 'READY' && desktop.antCount === 0, 'Surface 14 did not render as a shadcn-only READY workspace');
  assert(desktop.controls?.top < 220 && desktop.facts?.top < 360 && desktop.profile?.top < 520, 'Energy analysis hierarchy is too low in the first viewport');
  assert(desktop.contributors?.top < 941 && desktop.variances?.top < 941, `Contributor/variance workspace is not discoverable in the first viewport: ${JSON.stringify({ contributors: desktop.contributors, variances: desktop.variances })}`);
  for (const control of ['分析周期', '锚点日期', '电力', '上一完整周期', '数据质量']) assert(desktop.controlText.includes(control), `Analysis controls lost ${control}`);
  for (const fact of ['实际用能', '与上一完整周期差异', '数据状态', '区间粒度']) assert(desktop.factText.includes(fact), `Headline facts lost ${fact}`);
  assert(desktop.factRegions === 3, `Headline facts regressed to equal KPI tiles: ${desktop.factRegions}`);
  assert(desktop.factText.includes('差异不是已验证节能量') && /[+−-].*MWh.*\([+-]\d/.test(desktop.factText), `Comparison does not expose absolute + percentage difference: ${desktop.factText}`);
  assert(desktop.profileText.includes('kWh / 区间，不表示瞬时功率'), 'Primary profile lost Energy-vs-Power semantics');
  assert(desktop.chartRole.includes('区间电量曲线'), `Primary chart lacks accessible chart semantics: ${desktop.chartRole}`);
  assert(desktop.contributorText.includes('当前没有可验证的分项贡献数据') && desktop.contributorText.includes('不会按设备额定功率猜分项') && desktop.contributorText.includes('不会把可见部分重新归一化成 100%'), 'Contributor boundary regressed');
  assert(desktop.varianceCount === 3, 'Completed comparable periods did not expose three ranked variance windows');
  for (const boundary of ['来源系统未提供分类', '当前未提供', '不会用上一周期替代基线', '差异写成节能量']) assert(desktop.detailText.includes(boundary), `Professional detail lost boundary: ${boundary}`);
  for (const forbidden of ['traceId', 'datasetRevision', tenantId, siteId, '节能率', 'Energy Health', '平均功率']) assert(!desktop.bodyText.includes(forbidden), `Surface 14 leaked or fabricated forbidden text: ${forbidden}`);

  const screenshotDesktop = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, '14-energy-analysis-desktop.png'), Buffer.from(screenshotDesktop.data, 'base64'));

  const detailsOpened = await evaluate(client, `(() => {
    const details = document.querySelector('[data-testid="energy-primary-profile"] details');
    if (!(details instanceof HTMLDetailsElement)) return false;
    details.open = true;
    details.dispatchEvent(new Event('toggle', { bubbles: true }));
    return true;
  })()`);
  assert(detailsOpened, 'Chart data alternative could not be opened');
  await waitFor(client, `document.querySelectorAll('[aria-label="区间能源数据，可横向滚动"] [data-slot="table-body"] [data-slot="table-row"]').length === 31`, 'chart data alternative');
  const alternative = await evaluate(client, `({
    role: document.querySelector('[aria-label="区间能源数据，可横向滚动"]')?.getAttribute('role'),
    headers: Array.from(document.querySelectorAll('[aria-label="区间能源数据，可横向滚动"] [data-slot="table-head"]')).map((node) => node.textContent?.trim()),
    rows: document.querySelectorAll('[aria-label="区间能源数据，可横向滚动"] [data-slot="table-body"] [data-slot="table-row"]').length,
  })`);
  assert(alternative.role === 'region' && alternative.rows === 31, `Chart data alternative is incomplete: ${JSON.stringify(alternative)}`);
  for (const header of ['当前区间', '当前电量', '比较区间', '比较电量', '差异']) assert(alternative.headers.includes(header), `Data table lost header ${header}`);

  assert(await clickText(client, 'button', '当前周期'), 'Current-period action was unavailable');
  await waitFor(
    client,
    `document.querySelector('[data-testid="energy-analysis"]')?.getAttribute('data-business-state') === 'PARTIAL'
      && document.querySelector('[data-testid="energy-headline-facts"]')?.textContent?.includes('不比较')
      && document.body.innerText.includes('当前周期为部分数据，不与完整上一周期直接比较')`,
    'partial current month semantics',
  );
  const partial = await evaluate(client, `({
    facts: document.querySelector('[data-testid="energy-headline-facts"]')?.textContent ?? '',
    bodyText: document.body.innerText,
    profileText: document.querySelector('[data-testid="energy-primary-profile"]')?.textContent ?? '',
    fetchCalls: (globalThis.__energyFetchCalls ?? []).length,
  })`);
  assert(partial.facts.includes('部分数据') && partial.facts.includes('不比较'), 'Partial period did not preserve incomplete-comparison semantics');
  assert(!partial.facts.includes('%') && !partial.bodyText.includes('差异不是已验证节能量'), 'Partial period incorrectly calculated a full-period percentage comparison');
  assert(partial.profileText.includes('比较：2026年8月'), 'Partial current month lost the named comparison period');
  assert(desktop.fetchCalls === 2 && partial.fetchCalls === 3, `Surface 14 browser fetch discipline regressed: ${JSON.stringify({ desktop: desktop.fetchCalls, partial: partial.fetchCalls })}`);

  await client.send('Page.navigate', { url: webURL });
  await waitFor(client, `document.querySelector('[data-testid="energy-analysis"]')?.getAttribute('data-business-state') === 'READY' && document.body.innerText.includes('差异不是已验证节能量')`, 'energy reset');
  await client.send('Emulation.setDeviceMetricsOverride', { width: 900, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(350);
  await evaluate(client, `(() => { const details = document.querySelector('[data-testid="energy-primary-profile"] details'); if (details instanceof HTMLDetailsElement) details.open = true; })()`);
  const narrow = await evaluate(client, `(() => {
    const table = document.querySelector('[aria-label="区间能源数据，可横向滚动"] [data-slot="table-container"]');
    return {
      page: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      controlsVisible: Boolean(document.querySelector('[data-testid="energy-analysis-controls"]')?.getClientRects().length),
      chartVisible: Boolean(document.querySelector('[data-testid="energy-primary-profile"] [role="img"]')?.getClientRects().length),
      contributorVisible: Boolean(document.querySelector('[data-testid="energy-contributors"]')?.getClientRects().length),
      table: table ? { scrollWidth: table.scrollWidth, clientWidth: table.clientWidth } : null,
      antCount: document.querySelectorAll('.ant-card,.ant-select,.ant-btn,.ant-table').length,
    };
  })()`);
  assert(narrow.page.scrollWidth <= narrow.page.clientWidth && narrow.controlsVisible && narrow.chartVisible && narrow.contributorVisible, `900px workspace failed: ${JSON.stringify(narrow)}`);
  assert(narrow.table && narrow.table.scrollWidth >= narrow.table.clientWidth, 'Interval table did not retain its own horizontal scroll region');
  assert(narrow.antCount === 0, 'Narrow Surface 14 rendered Ant DOM');
  const screenshotNarrow = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, '14-energy-analysis-narrow.png'), Buffer.from(screenshotNarrow.data, 'base64'));

  await client.send('Emulation.setDeviceMetricsOverride', { width: 320, height: 900, deviceScaleFactor: 1, mobile: false });
  await pause(350);
  const reflow = await evaluate(client, `({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    viewportWidth: window.innerWidth,
    titleVisible: document.body.innerText.includes('能源分析'),
    chartVisible: Boolean(document.querySelector('[data-testid="energy-primary-profile"]')),
  })`);
  assert(reflow.scrollWidth <= reflow.viewportWidth + 1 && reflow.titleVisible && reflow.chartVisible, `320px reflow failed: ${JSON.stringify(reflow)}`);

  const energyRequests = gateway.requests.filter((request) => request.path === '/api/v1/analytics/energy-series');
  const uniqueEnergyQueries = new Set(energyRequests.map((request) => JSON.stringify(request.body)));
  assert(uniqueEnergyQueries.size === 3 && energyRequests.every((request) => request.method === 'POST' && request.status === 200), `Energy review did not preserve the three expected analytical queries: ${JSON.stringify([...uniqueEnergyQueries])}`);
  assert(gateway.requests.every((request) => !request.path.includes('telemetry') && !request.path.includes('power')), 'Surface 14 attempted frontend power/telemetry integration');
  assert(energyRequests.every((request) => request.body?.energyType === 'electricity'), 'Surface 14 silently changed energy carrier');

  const browserErrors = client.events
    .filter((event) => event.method === 'Runtime.exceptionThrown'
      || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error' && event.params?.entry?.source === 'javascript'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.entry?.text ?? event.method);
  assert(browserErrors.length === 0, `Browser emitted errors: ${browserErrors.join(' | ')}`);

  console.log(JSON.stringify({
    conclusion: 'passed',
    webURL,
    screenshots: [
      'out/energy-analysis-review/14-energy-analysis-desktop.png',
      'out/energy-analysis-review/14-energy-analysis-narrow.png',
    ],
    desktop: {
      page: desktop.page,
      sidebar: desktop.sidebar,
      header: desktop.header,
      controls: desktop.controls,
      facts: desktop.facts,
      profile: desktop.profile,
      contributors: desktop.contributors,
      variances: desktop.variances,
      fetchCalls: desktop.fetchCalls,
    },
    partial: { facts: partial.facts, fetchCalls: partial.fetchCalls },
    narrow,
    reflow,
    uniqueEnergyQueryCount: uniqueEnergyQueries.size,
  }, null, 2));
} finally {
  client?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => gateway.server.close(() => resolveClose()));
  await rm(linuxProfileDir, { recursive: true, force: true });
}
