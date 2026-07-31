import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createHTTPServer } from 'node:http';
import { existsSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer as createViteServer } from 'vite';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/real-alarms');
const outputRoot = resolve(root, 'out/real-alarms-certification');
const profileDir = join(tmpdir(), `real-alarms-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const organizationId = '01910000-0000-7000-8000-000000000001';
const siteAId = '01910000-0001-7000-8000-000000000001';
const siteBId = '01910000-0002-7000-8000-000000000002';
const alarmAOpenId = '01910000-1000-7000-8000-000000000001';
const alarmAClosedId = '01910000-1000-7000-8000-000000000002';
const alarmBId = '01910000-1000-7000-8000-000000000003';

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

function transition(toStatus, reason, occurredAt, version, fromStatus) {
  return {
    ...(fromStatus ? { fromStatus } : {}),
    toStatus,
    reason,
    actorType: version === 1 ? 'WORKLOAD' : 'PRINCIPAL',
    ...(version === 1 ? {} : { actorId: 'principal:alarm-operator' }),
    occurredAt,
    version,
  };
}

function alarm({ alarmId, siteId, title, summary, severity, status, occurrenceCount, sourceReference, deviceId, lastOccurredAt }) {
  const firstOccurredAt = '2026-07-31T09:00:00Z';
  const transitions = status === 'OPEN'
    ? [transition('OPEN', 'ALARM_PUBLISHED', firstOccurredAt, 1)]
    : [
        transition('OPEN', 'ALARM_PUBLISHED', firstOccurredAt, 1),
        transition(status, `ALARM_${status}`, lastOccurredAt, 2, 'OPEN'),
      ];
  return {
    schemaVersion: 1,
    alarmId,
    organizationId,
    siteId,
    ...(deviceId ? { deviceId } : {}),
    sourceType: deviceId ? 'DEVICE_RULE' : 'SITE_RULE',
    sourceReference,
    title,
    summary,
    severity,
    status,
    occurrenceCount,
    firstOccurredAt,
    lastOccurredAt,
    evidence: [{ kind: 'telemetry-snapshot', reference: `snapshot:${alarmId.slice(-3)}`, capturedAt: lastOccurredAt }],
    transitions,
    version: transitions.at(-1).version,
    createdAt: firstOccurredAt,
    updatedAt: lastOccurredAt,
  };
}

const alarmsBySite = new Map([
  [siteAId, [
    alarm({
      alarmId: alarmAOpenId,
      siteId: siteAId,
      deviceId: '01910000-2000-7000-8000-000000000001',
      title: 'Tokyo supply temperature drift',
      summary: 'Alarm Service published a repeated supply-temperature exception.',
      severity: 'CRITICAL',
      status: 'OPEN',
      occurrenceCount: 3,
      sourceReference: 'rule:tokyo-supply-temperature-drift:v4',
      lastOccurredAt: '2026-07-31T09:15:00Z',
    }),
    alarm({
      alarmId: alarmAClosedId,
      siteId: siteAId,
      title: 'Tokyo plant differential pressure',
      summary: 'Alarm Service published and later closed the Site-level pressure exception.',
      severity: 'MAJOR',
      status: 'CLOSED',
      occurrenceCount: 1,
      sourceReference: 'rule:tokyo-plant-pressure:v2',
      lastOccurredAt: '2026-07-31T09:10:00Z',
    }),
  ]],
  [siteBId, [
    alarm({
      alarmId: alarmBId,
      siteId: siteBId,
      title: 'Osaka condenser approach',
      summary: 'Alarm Service published an acknowledged condenser approach exception.',
      severity: 'WARNING',
      status: 'ACKNOWLEDGED',
      occurrenceCount: 2,
      sourceReference: 'rule:osaka-condenser-approach:v1',
      lastOccurredAt: '2026-07-31T09:20:00Z',
    }),
  ]],
]);

function problem(status, code, detail, retryable = false) {
  return {
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    code,
    retryable,
  };
}

function json(response, status, payload) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'private, no-store',
  });
  response.end(JSON.stringify(payload));
}

function createGatewayFixture() {
  const requests = [];
  const server = createHTTPServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    requests.push({ method: request.method ?? 'GET', path: url.pathname, query: url.search });
    const detailMatch = url.pathname.match(/^\/api\/v1\/local\/sites\/([^/]+)\/alarms\/([^/]+)$/);
    if (detailMatch && request.method === 'GET') {
      const [, siteId, alarmId] = detailMatch;
      const item = (alarmsBySite.get(siteId) ?? []).find((entry) => entry.alarmId === alarmId);
      json(response, item ? 200 : 404, item ?? problem(404, 'RESOURCE_NOT_FOUND', 'The Alarm resource is not visible.'));
      return;
    }
    const listMatch = url.pathname.match(/^\/api\/v1\/local\/sites\/([^/]+)\/alarms$/);
    if (listMatch && request.method === 'GET') {
      const siteId = listMatch[1];
      const status = url.searchParams.get('status');
      const severity = url.searchParams.get('severity');
      const limit = Number(url.searchParams.get('limit') ?? '50');
      if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
        json(response, 400, problem(400, 'ALARM_FILTER_INVALID', 'The Alarm list limit is invalid.'));
        return;
      }
      const items = (alarmsBySite.get(siteId) ?? [])
        .filter((entry) => !status || entry.status === status)
        .filter((entry) => !severity || entry.severity === severity)
        .slice(0, limit);
      json(response, 200, { schemaVersion: 1, items, nextCursor: null, hasMore: false });
      return;
    }
    json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Route not found.'));
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
      if (!message.id) {
        events.push(message);
        return;
      }
      const command = pending.get(message.id);
      if (!command) return;
      pending.delete(message.id);
      if (message.error) command.rejectCommand(new Error(message.error.message));
      else command.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'Browser evaluation failed');
  }
  return response.result.value;
}

async function waitForCondition(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 400; attempt += 1) {
    try {
      last = await evaluate(client, expression);
      if (last) return last;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ url: location.href, text: document.body?.innerText?.slice(0, 5000) ?? '', html: document.body?.innerHTML?.slice(0, 5000) ?? '' })`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready; last=${JSON.stringify(last)} diagnostic=${JSON.stringify(diagnostic)}`);
}

async function selectValue(client, testId, value) {
  return evaluate(client, `(() => {
    const node = document.querySelector('[data-testid="${testId}"]');
    if (!(node instanceof HTMLSelectElement)) return false;
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set;
    setter?.call(node, ${JSON.stringify(value)});
    node.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
  })()`);
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped && process.platform === 'win32') spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  else if (!stopped) child.kill('SIGKILL');
}

const browserCandidates = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium-browser', '/usr/bin/chromium',
].filter(Boolean);
const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
if (!browserPath) throw new Error('A CDP-compatible browser was not found');

const gatewayPort = await findAvailablePort();
const debugPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const fixture = createGatewayFixture();
let viteServer;
let browserProcess;
let cdpClient;
let conclusion = 'failed';
const assertions = [];
const stateEvidence = {};

try {
  await mkdir(profileDir, { recursive: true });
  await mkdir(outputRoot, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });
  process.env.VITE_API_MODE = 'real';
  process.env.VITE_S4_LOCAL_ALARMS = 'true';
  viteServer = await createViteServer({
    root: fixtureRoot,
    configFile: false,
    logLevel: 'error',
    define: { __HVAC_WEB_BUILD_TARGET__: JSON.stringify('real') },
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: { host: '127.0.0.1', port: 0, strictPort: false, proxy: { '/api': { target: gatewayURL, changeOrigin: true } } },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Vite fixture server has no address');
  const webURL = `http://127.0.0.1:${viteAddress.port}`;

  browserProcess = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--no-sandbox', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars',
    `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profileDir}`, 'about:blank',
  ], { stdio: 'ignore' });
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
    if (attempt === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Network.enable');
  await cdpClient.send('Page.enable');
  await cdpClient.send('Log.enable');
  await cdpClient.send('Page.navigate', { url: webURL });

  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-business-state') === 'READY' && document.body.innerText.includes('Tokyo supply temperature drift') && document.body.innerText.includes('Tokyo plant differential pressure')`,
    'authoritative Site A Alarm list',
  );
  const initial = await evaluate(cdpClient, `({
    state: document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-business-state'),
    siteId: document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-site-id'),
    text: document.body.innerText,
    cacheKeys: globalThis.__REAL_ALARMS_AUDIT__.cacheKeys(),
  })`);
  assert(initial.state === 'READY' && initial.siteId === siteAId, 'Site A Alarm scope was not ready');
  assert(!initial.text.includes('冷冻机房') && !initial.text.includes('温度过高'), 'Real Alarm UI displayed Demo Alarm content');
  assertions.push('site-a-authoritative-list-no-demo-contamination');
  stateEvidence.siteA = { count: 2, businessState: initial.state };

  assert(await evaluate(cdpClient, `(() => {
    const button = Array.from(document.querySelectorAll('.real-alarms__list button')).find((candidate) => candidate.textContent?.includes('Tokyo supply temperature drift'));
    if (!button) return false;
    button.click();
    return true;
  })()`), 'Site A Alarm detail control was unavailable');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="real-alarm-detail"][data-alarm-status="OPEN"]')) && document.body.innerText.includes('rule:tokyo-supply-temperature-drift:v4') && document.body.innerText.includes('ALARM_PUBLISHED') && document.body.innerText.includes('snapshot:001')`,
    'authoritative Alarm detail',
  );
  assertions.push('detail-source-evidence-lifecycle');
  stateEvidence.detail = { alarmId: alarmAOpenId, status: 'OPEN', occurrenceCount: 3 };

  assert(await selectValue(cdpClient, 'real-alarm-status-filter', 'CLOSED'), 'Alarm status filter was unavailable');
  await waitForCondition(
    cdpClient,
    `document.querySelector('.real-alarms__list')?.innerText.includes('Tokyo plant differential pressure') && !document.querySelector('.real-alarms__list')?.innerText.includes('Tokyo supply temperature drift')`,
    'closed Alarm filter',
  );
  assertions.push('server-owned-lifecycle-filter');

  assert(await selectValue(cdpClient, 'real-alarm-status-filter', ''), 'Alarm status filter could not reset');
  assert(await selectValue(cdpClient, 'real-alarm-severity-filter', 'INFO'), 'Alarm severity filter was unavailable');
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-business-state') === 'EMPTY' && Boolean(document.querySelector('[data-testid="real-alarms-empty"]'))`,
    'authoritative empty filter result',
  );
  assertions.push('authoritative-empty-not-health-claim');
  stateEvidence.empty = { businessState: 'EMPTY', inferredHealth: false };

  assert(await selectValue(cdpClient, 'real-alarm-severity-filter', ''), 'Alarm severity filter could not reset');
  await waitForCondition(cdpClient, `document.body.innerText.includes('Tokyo supply temperature drift')`, 'Site A list reset');
  await evaluate(cdpClient, `globalThis.__REAL_ALARMS_AUDIT__.switchSite()`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-site-id') === '${siteBId}' && document.body.innerText.includes('Osaka condenser approach')`,
    'Site B Alarm list',
  );
  const afterSwitch = await evaluate(cdpClient, `({
    siteId: globalThis.__REAL_ALARMS_AUDIT__.siteId(),
    cacheKeys: globalThis.__REAL_ALARMS_AUDIT__.cacheKeys(),
    text: document.body.innerText,
  })`);
  assert(afterSwitch.siteId === siteBId, 'Alarm fixture did not switch to Site B');
  assert(!JSON.stringify(afterSwitch.cacheKeys).includes(siteAId), 'old Site Alarm cache survived Site transition');
  assert(!afterSwitch.text.includes('Tokyo supply temperature drift') && !afterSwitch.text.includes('Tokyo plant differential pressure'), 'old Site Alarm content survived Site transition');
  assertions.push('cross-site-cache-and-view-purge');
  stateEvidence.siteB = { count: 1, businessState: 'READY' };

  const alarmRequests = fixture.requests.filter((entry) => entry.path.includes('/alarms'));
  assert(alarmRequests.length >= 6, 'Alarm browser audit did not exercise list, detail, filters and Site transition');
  assert(alarmRequests.every((entry) => entry.method === 'GET'), 'Alarm browser audit issued a write request');
  assert(alarmRequests.every((entry) => entry.path.startsWith('/api/v1/local/sites/')), 'Alarm browser audit bypassed the local Site-scoped read seam');
  assert(fixture.requests.every((entry) => !entry.path.includes('/telemetry/')), 'Alarm browser audit used Telemetry as an Alarm source');
  assertions.push('read-only-network-no-telemetry-inference');

  conclusion = 'passed';
  const evidence = {
    schemaVersion: 1,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    stateEvidence,
    network: { requests: fixture.requests },
    safety: {
      productionTrafficPercent: 0,
      localReadOnly: true,
      telemetryInference: false,
      demoContamination: false,
    },
  };
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify(evidence, null, 2));
  console.log(`Real Alarm browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
} finally {
  cdpClient?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true });
  if (conclusion !== 'passed') {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({ schemaVersion: 1, passed: false, generatedAt: new Date().toISOString(), assertions, stateEvidence, network: { requests: fixture.requests } }, null, 2));
  }
}
