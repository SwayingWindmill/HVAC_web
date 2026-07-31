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
const fixtureRoot = resolve(root, 'scripts/fixtures/s2-hvac-web-presence');
const outputRoot = resolve(root, 'out/s2-hvac-web-presence');
const profileDir = join(tmpdir(), `s2-hvac-web-presence-${process.pid}`);
const startedAt = new Date();
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const csrfValue = ['csrf', 'ticket09', String(process.pid)].join('-');
const sessionValue = ['ticket09', String(process.pid)].join('-');
const traceId = '0'.repeat(32);
const spanId = '0'.repeat(16);

const ids = {
  organizationA: '018f6a00-1000-7000-8000-000000000001',
  organizationB: '018f6a00-1000-7000-8000-000000000002',
  siteA: '018f6a00-2000-7000-8000-000000000001',
  siblingSiteA: '018f6a00-2000-7000-8000-000000000002',
  siteB: '018f6a00-2000-7000-8000-000000000003',
  deviceA1: '018f6a00-3000-7000-8000-000000000001',
  deviceA2: '018f6a00-3000-7000-8000-000000000002',
  siblingDeviceA: '018f6a00-3000-7000-8000-000000000003',
  deviceB: '018f6a00-3000-7000-8000-000000000004',
};
const instant = '2026-07-25T05:00:00.000Z';

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function organization(id, code, displayName) {
  return { id, code, displayName, status: 'ACTIVE', revision: 1, createdAt: instant, updatedAt: instant };
}

function site(id, owningOrganizationId, code, displayName) {
  return {
    id, owningOrganizationId, code, displayName, timezone: 'Asia/Singapore', status: 'ACTIVE',
    revision: 1, createdAt: instant, updatedAt: instant,
  };
}

function device(id, owningOrganizationId, siteId, code, displayName, deviceType = 'HVAC_SENSOR') {
  return {
    id, owningOrganizationId, siteId, code, displayName, deviceType, status: 'ACTIVE',
    revision: 1, createdAt: instant, updatedAt: instant,
  };
}

const organizations = [
  organization(ids.organizationA, 'ORG-A', 'Organization Alpha'),
  organization(ids.organizationB, 'ORG-B', 'Organization Beta'),
];
const sites = {
  [ids.organizationA]: [
    site(ids.siteA, ids.organizationA, 'SITE-A1', 'Alpha Main Site'),
    site(ids.siblingSiteA, ids.organizationA, 'SITE-A2', 'Alpha Sibling Site'),
  ],
  [ids.organizationB]: [site(ids.siteB, ids.organizationB, 'SITE-B1', 'Beta Site')],
};
const devices = {
  [ids.siteA]: [
    device(ids.deviceA1, ids.organizationA, ids.siteA, 'DEV-A1', 'Alpha AHU Sensor'),
    device(ids.deviceA2, ids.organizationA, ids.siteA, 'DEV-A2', 'Alpha Pump Sensor'),
  ],
  [ids.siblingSiteA]: [device(ids.siblingDeviceA, ids.organizationA, ids.siblingSiteA, 'DEV-A3', 'Sibling Chiller Sensor', 'CHILLER')],
  [ids.siteB]: [device(ids.deviceB, ids.organizationB, ids.siteB, 'DEV-B1', 'Beta AHU Sensor')],
};
const deviceById = new Map(Object.values(devices).flat().map((entry) => [entry.id, entry]));
const siteById = new Map(Object.values(sites).flat().map((entry) => [entry.id, entry]));

function problem(status, code, detail, retryable = false) {
  return {
    type: `https://errors.hvac.local/${code.toLowerCase()}`,
    title: code.replaceAll('_', ' '), status, detail, instance: '/api/v1/telemetry', code,
    traceId, retryable,
  };
}

function principal() {
  const initiatingPrincipal = {
    subject: 'ticket-09-browser', issuer: 'https://identity.hvac.local', displayName: 'Ticket 09 Browser',
    email: ['ticket09', 'example.invalid'].join('@'), roles: ['MAINTENANCE'],
  };
  return {
    principal: initiatingPrincipal,
    context: {
      initiatingPrincipal,
      executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac.local/platform-gateway' },
      actingOrganizationId: ids.organizationA,
      audience: 'iam-service', policyRevision: 'policy-09', delegationExpiresAt: '2026-07-25T06:00:00.000Z',
    },
    authorization: {
      capabilitySetVersion: 2,
      policyRevision: 'telemetry-access:1',
      capabilities: ['site.read', 'device.list', 'device.read'],
    },
    session: {
      id: 'session-ticket-09', expiresAt: '2026-07-25T06:00:00.000Z', csrfToken: csrfValue,
      revocationObjectiveMs: 30000, lastAuditMessageId: 'audit-ticket-09',
    },
  };
}

function presenceSnapshot(entry, state = 'ONLINE') {
  const lastSeenAt = state === 'OFFLINE' ? '2026-07-25T05:00:00.000Z' : '2026-07-25T05:19:58.000Z';
  return {
    schemaVersion: 1, deviceId: entry.id, owningOrganizationId: entry.owningOrganizationId, siteId: entry.siteId,
    businessRevision: 9, evaluatedAt: '2026-07-25T05:20:00.000Z', evaluationAvailability: 'AVAILABLE', availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE', currentState: state, lastSeenAt, policyRevision: 4,
      lastKnown: { state, lastSeenAt, evaluatedAt: '2026-07-25T05:20:00.000Z', policyRevision: 4 },
    },
    telemetryReadiness: 'NOT_APPLICABLE', displayState: state, values: [],
  };
}

async function readBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return Buffer.concat(chunks).toString('utf8');
}

function json(response, status, payload, extraHeaders = {}) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'no-store',
    'x-request-id': `ticket-09-${Date.now()}`,
    'x-route-policy-revision': '9',
    traceparent: `00-${traceId}-${spanId}-01`,
    ...extraHeaders,
  });
  response.end(payload === undefined ? undefined : JSON.stringify(payload));
}
function createGatewayFixture() {
  const requests = [];
  const hiddenDeviceIds = new Set();
  const server = createHTTPServer(async (request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    if (url.pathname === '/__fixture/revoke') {
      const deviceId = url.searchParams.get('deviceId');
      if (deviceId) hiddenDeviceIds.add(deviceId);
      json(response, 200, { hidden: deviceId });
      return;
    }
    const headers = Object.fromEntries(Object.entries(request.headers).map(([key, value]) => [key, String(value)]));
    const bodyText = request.method === 'POST' ? await readBody(request) : '';
    let body = null;
    if (bodyText) {
      try { body = JSON.parse(bodyText); } catch { body = bodyText; }
    }
    requests.push({ method: request.method ?? 'GET', path: url.pathname, query: url.search, headers, body });

    if (url.pathname === '/api/v1/auth/login') {
      const returnTo = url.searchParams.get('returnTo') || '/';
      response.writeHead(302, {
        location: returnTo.startsWith('/') ? returnTo : '/',
        'set-cookie': `hvac_ticket09_session=${sessionValue}; Path=/; HttpOnly; SameSite=Lax`,
        'cache-control': 'no-store',
      });
      response.end();
      return;
    }

    if (!headers.cookie?.includes(`hvac_ticket09_session=${sessionValue}`)) {
      json(response, 401, problem(401, 'AUTHENTICATION_REQUIRED', 'A BFF Session is required.'));
      return;
    }
    if (url.pathname === '/api/v1/principal' && request.method === 'GET') {
      json(response, 200, principal());
      return;
    }
    if (url.pathname === '/api/v1/organizations' && request.method === 'GET') {
      json(response, 200, { items: organizations, nextCursor: null, hasMore: false });
      return;
    }
    const organizationSites = url.pathname.match(/^\/api\/v1\/organizations\/([^/]+)\/sites$/);
    if (organizationSites && request.method === 'GET') {
      json(response, 200, { items: sites[organizationSites[1]] ?? [], nextCursor: null, hasMore: false });
      return;
    }
    const siteMatch = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)$/);
    if (siteMatch && request.method === 'GET') {
      const entry = siteById.get(siteMatch[1]);
      json(response, entry ? 200 : 404, entry ?? problem(404, 'RESOURCE_NOT_FOUND', 'Site not found.'));
      return;
    }
    const siteEquipment = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/equipment$/);
    if (siteEquipment && request.method === 'GET') {
      json(response, 200, { items: [], nextCursor: null, hasMore: false });
      return;
    }
    const siteDevices = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/devices$/);
    if (siteDevices && request.method === 'GET') {
      const items = (devices[siteDevices[1]] ?? []).filter((entry) => !hiddenDeviceIds.has(entry.id));
      json(response, 200, { items, nextCursor: null, hasMore: false });
      return;
    }
    const deviceDetail = url.pathname.match(/^\/api\/v1\/devices\/([^/]+)$/);
    if (deviceDetail && request.method === 'GET') {
      const entry = deviceById.get(deviceDetail[1]);
      json(response, entry ? 200 : 404, entry ?? problem(404, 'RESOURCE_NOT_FOUND', 'Device not found.'));
      return;
    }
    if (url.pathname === '/api/v1/telemetry/observation-snapshots:batchGet' && request.method === 'POST') {
      if (headers['x-csrf-token'] !== csrfValue) {
        json(response, 403, problem(403, 'CSRF_INVALID', 'CSRF capability is missing.'));
        return;
      }
      const targets = Array.isArray(body?.requests) ? body.requests : [];
      if (targets.some((target) => !Array.isArray(target.keys) || target.keys.length !== 0)) {
        json(response, 400, problem(400, 'FIELD_INVALID', 'Presence batch must request no telemetry keys.'));
        return;
      }
      const items = targets.map((target) => {
        const entry = deviceById.get(target.deviceId);
        if (!entry) return { requestId: target.requestId, deviceId: target.deviceId, status: 'ERROR', problem: problem(404, 'RESOURCE_NOT_FOUND', 'Device not found.') };
        if (entry.id === ids.deviceA2) {
          return {
            requestId: target.requestId, deviceId: target.deviceId, status: 'ERROR',
            problem: problem(503, 'OWNER_DEPENDENCY_UNAVAILABLE', 'The authoritative current-state owner is unavailable.', true),
          };
        }
        const state = entry.id === ids.siblingDeviceA ? 'OFFLINE' : 'ONLINE';
        return { requestId: target.requestId, deviceId: target.deviceId, status: 'OK', snapshot: presenceSnapshot(entry, state) };
      });
      json(response, 200, { schemaVersion: 1, items });
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
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'Browser evaluation failed');
  return response.result.value;
}

async function waitForCondition(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 600; attempt += 1) {
    try {
      last = await evaluate(client, expression);
      if (last) return last;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ url: location.href, text: document.body?.innerText?.slice(0, 6000) ?? '' })`)
    .catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready; last=${JSON.stringify(last)} diagnostic=${JSON.stringify(diagnostic)}`);
}

async function clickText(client, text, selector = 'button') {
  return evaluate(client, `(() => {
    const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)}))
      .find((candidate) => candidate.textContent?.trim() === ${JSON.stringify(text)});
    if (!node) return false;
    node.click();
    return true;
  })()`);
}

async function selectAntOption(client, selectIndex, label) {
  const opened = await evaluate(client, `(() => {
    const target = Array.from(document.querySelectorAll('.ant-select'))[${selectIndex}];
    if (!target) return false;
    const selector = target.querySelector('.ant-select-selector');
    selector?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    selector?.click();
    return true;
  })()`);
  assert(opened, `Ant Select ${selectIndex} was not available`);
  await waitForCondition(
    client,
    `Array.from(document.querySelectorAll('.ant-select-item-option-content')).some((node) => node.textContent?.trim() === ${JSON.stringify(label)})`,
    `Ant Select option ${label}`,
  );
  assert(await evaluate(client, `(() => {
    const option = Array.from(document.querySelectorAll('.ant-select-item-option-content'))
      .find((node) => node.textContent?.trim() === ${JSON.stringify(label)});
    if (!option) return false;
    option.parentElement?.click();
    return true;
  })()`), `Ant Select option ${label} could not be selected`);
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
let accessibility = null;

try {
  await mkdir(profileDir, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });
  process.env.VITE_API_MODE = 'real';
  process.env.S0_GATEWAY_ONLY = 'true';
  viteServer = await createViteServer({
    root: fixtureRoot,
    logLevel: 'error',
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
  for (let attempt = 0; attempt < 600; attempt += 1) {
    try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
    if (attempt === 599) throw new Error('Browser debugger did not become ready');
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
  await cdpClient.send('Page.navigate', { url: `${webURL}/api/v1/auth/login?returnTo=%2F` });

  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="real-registry-assets-page"]')) && document.body.innerText.includes('Alpha Main Site')`, 'Organization Alpha Assets page');
  await waitForCondition(
    cdpClient,
    `Array.from(document.querySelectorAll('[role="tab"]')).some((candidate) => candidate.textContent?.trim() === 'Device (2)' && candidate.getAttribute('aria-disabled') !== 'true')`,
    'Device tab readiness',
  );
  assert(await clickText(cdpClient, 'Device (2)', '[role="tab"]'), 'Device tab was not available');
  await waitForCondition(cdpClient, `document.body.innerText.includes('Presence batch 返回部分结果') && document.body.innerText.includes('Alpha AHU Sensor')`, 'partial Presence batch');
  const partialState = await evaluate(cdpClient, `({
    partial: Boolean(document.querySelector('[data-presence-batch-state="partial"]')),
    online: Array.from(document.querySelectorAll('[data-device-display-state="ONLINE"]')).length,
    unavailable: Array.from(document.querySelectorAll('[data-device-display-state="UNAVAILABLE"]')).length,
    text: document.body.innerText,
  })`);
  assert(partialState.partial && partialState.online >= 1 && partialState.unavailable >= 1, 'partial batch did not render independent Device results');
  assert(!partialState.text.includes('总部大楼') && !partialState.text.includes('冷水机组 #1'), 'real page displayed Mock business data');
  assertions.push('two-device-partial-presence-batch');
  stateEvidence.partialBatch = { onlineCells: partialState.online, unavailableCells: partialState.unavailable };

  assert(await evaluate(cdpClient, `(() => {
    const row = Array.from(document.querySelectorAll('tr')).find((candidate) => candidate.textContent?.includes('Alpha AHU Sensor'));
    const button = Array.from(row?.querySelectorAll('button') ?? []).find((candidate) => candidate.textContent?.trim() === '详情');
    if (!button) return false;
    button.click();
    return true;
  })()`), 'Alpha AHU Sensor detail control was not available');
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-device-live-state="live"][data-device-display-state="STALE"]')) && document.body.innerText.includes('MISSING（不补零）') && document.body.innerText.includes('SUSPECT')`, 'initial exact-key Snapshot');
  const initialDetail = await evaluate(cdpClient, `({
    text: document.querySelector('[data-device-live-state="live"]')?.innerText ?? '',
    audit: window.__S2_HVAC_WEB_CONTROL__.audit(),
    cacheCount: window.__S2_HVAC_WEB_CONTROL__.telemetryCacheCount(),
  })`);
  assert(initialDetail.audit.opens.length === 1, 'Device detail did not open one TelemetryLiveClient session');
  assert(JSON.stringify(initialDetail.audit.opens[0].keys) === JSON.stringify(['temperature', 'humidity', 'setpoint', 'power']), 'Device detail did not use exact keys');
  assert(initialDetail.text.includes('22.5') && initialDetail.text.includes('STALE') && initialDetail.text.includes('SUSPECT') && initialDetail.cacheCount > 0, 'initial Last Known state was incomplete');
  assertions.push('exact-key-last-known-rendering');
  stateEvidence.initial = { displayState: 'STALE', missingNotZero: true, suspect: true, businessRevision: 41 };

  await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.setMode('live-update')`);
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-device-live-state="live"][data-device-display-state="ONLINE"]')) && document.body.innerText.includes('23.75') && document.body.innerText.includes('42')`, 'live update');
  assertions.push('live-delta-shared-snapshot-model');
  stateEvidence.liveUpdate = { displayState: 'ONLINE', value: 23.75, businessRevision: 42 };

  await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.setMode('reconnect')`);
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-transport-state="degraded"]')) && document.body.innerText.includes('23.75')`, 'reconnect Snapshot');
  assertions.push('reconnect-no-mixed-current-state');
  stateEvidence.reconnect = { transport: 'degraded', retainedRevision: 42 };

  await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.setMode('gap')`);
  await waitForCondition(cdpClient, `document.body.innerText.includes('实时状态需要重新同步') && document.body.innerText.includes('23.75')`, 'gap recovery');
  assertions.push('gap-requires-resynchronization');
  stateEvidence.gap = { status: 'unavailable', reason: 'recovery-required', retainedRevision: 42 };

  await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.setMode('outage')`);
  await waitForCondition(cdpClient, `document.body.innerText.includes('实时 transport 暂不可用') && document.body.innerText.includes('23.75')`, 'transport outage');
  assertions.push('transport-outage-explicit-no-fallback');
  stateEvidence.outage = { status: 'unavailable', retainedLastKnown: true };

  const cachedBeforeRevocation = await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.cachedDeviceIds()`);
  await fetch(`${gatewayURL}/__fixture/revoke?deviceId=${encodeURIComponent(ids.deviceA1)}`);
  await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.setMode('revoke')`);
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-device-live-state="revoked"]'))`, 'revoked state');
  const revokedAlert = await evaluate(cdpClient, `({ audit: window.__S2_HVAC_WEB_CONTROL__.audit(), text: document.querySelector('[data-device-live-state="revoked"]')?.innerText ?? '' })`);
  assert(revokedAlert.audit.purgeCount >= 1 && revokedAlert.text.includes('已清除'), 'revocation did not purge live recovery state');
  await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.refreshRegistry()`);
  await waitForCondition(cdpClient, `!new URLSearchParams(location.search).has('device') && !document.body.innerText.includes('Alpha AHU Sensor')`, 'revoked Device removal');
  const cachedAfterRevocation = await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.cachedDeviceIds()`);
  assert(cachedBeforeRevocation.includes(ids.deviceA1) && !cachedAfterRevocation.includes(ids.deviceA1), 'revocation retained the inaccessible Device in browser telemetry data');
  assertions.push('revocation-purges-browser-state');
  stateEvidence.revocation = { cachedBefore: cachedBeforeRevocation, cachedAfter: cachedAfterRevocation, purgeCount: revokedAlert.audit.purgeCount };

  await selectAntOption(cdpClient, 1, 'Alpha Sibling Site');
  await waitForCondition(cdpClient, `document.body.innerText.includes('Sibling Chiller Sensor') && Boolean(document.querySelector('[data-device-display-state="OFFLINE"]'))`, 'sibling Site');
  const siblingState = await evaluate(cdpClient, `({ text: document.body.innerText, cacheCount: window.__S2_HVAC_WEB_CONTROL__.telemetryCacheCount(), audit: window.__S2_HVAC_WEB_CONTROL__.audit(), hasDeviceParam: new URLSearchParams(location.search).has('device') })`);
  assert(!siblingState.text.includes('Alpha AHU Sensor') && !siblingState.hasDeviceParam, 'Site switch retained hidden Device state');
  assert(siblingState.audit.purgeCount >= 2 && siblingState.cacheCount > 0, 'Site switch did not purge then install new Site cache');
  assertions.push('sibling-site-switch-purges-hidden-device');

  assert(await evaluate(cdpClient, `(() => {
    const row = Array.from(document.querySelectorAll('tr')).find((candidate) => candidate.textContent?.includes('Sibling Chiller Sensor'));
    const button = Array.from(row?.querySelectorAll('button') ?? []).find((candidate) => candidate.textContent?.trim() === '详情');
    if (!button) return false;
    button.click();
    return true;
  })()`), 'Sibling Chiller detail control was not available');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-central-plant-profile="CHILLER"]')) && document.body.innerText.includes('冷水机组实时运行摘要') && document.body.innerText.includes('主机 COP') && document.body.innerText.includes('212.5 kW') && document.body.innerText.includes('1080 kW') && document.body.innerText.includes('6.7 °C')`,
    'central-plant Chiller summary',
  );
  const chillerDetail = await evaluate(cdpClient, `({
    text: document.querySelector('[data-central-plant-profile="CHILLER"]')?.innerText ?? '',
    audit: window.__S2_HVAC_WEB_CONTROL__.audit(),
  })`);
  const expectedChillerKeys = [
    'chiller.run_state',
    'chiller.power',
    'chiller.cop',
    'chiller.cooling_capacity',
    'chiller.compressor_load',
    'chiller.leaving_chilled_water_temperature',
    'chiller.entering_chilled_water_temperature',
    'chiller.chilled_water_temperature_setpoint',
    'chiller.entering_cooling_water_temperature',
    'chiller.business_revision',
    'chiller.fault_code',
  ];
  const chillerOpen = chillerDetail.audit.opens.at(-1);
  assert(JSON.stringify(chillerOpen?.keys) === JSON.stringify(expectedChillerKeys), 'Chiller detail did not request the central-plant exact keys');
  assert(chillerDetail.text.includes('5.08') && chillerDetail.text.includes('STALE') && chillerDetail.text.includes('SUSPECT'), 'Chiller summary lost COP or quality state');
  assertions.push('central-plant-chiller-exact-keys-and-summary');
  stateEvidence.chiller = { cop: 5.08, powerKw: 212.5, coolingCapacityKw: 1080, quality: 'SUSPECT' };
  assert(await evaluate(cdpClient, `(() => {
    const close = document.querySelector('.ant-drawer-close');
    if (!close) return false;
    close.click();
    return true;
  })()`), 'Chiller detail close control was not available');
  await waitForCondition(cdpClient, `!new URLSearchParams(location.search).has('device')`, 'Chiller detail close');

  const cachedBeforeRouteChange = await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.cachedDeviceIds()`);
  const cachedAfterRouteChange = await evaluate(cdpClient, `window.__S2_HVAC_WEB_CONTROL__.routeCohortChanged()`);
  assert(cachedBeforeRouteChange.includes(ids.siblingDeviceA) && cachedAfterRouteChange.length === 0, 'route cohort change did not purge browser state synchronously');
  assertions.push('route-cohort-change-purges-browser-state');

  await selectAntOption(cdpClient, 0, 'Organization Beta');
  await waitForCondition(cdpClient, `document.body.innerText.includes('Beta AHU Sensor') && Boolean(document.querySelector('[data-presence-batch-state="error"]'))`, 'second Organization fail-closed state');
  const organizationBState = await evaluate(cdpClient, `({ text: document.body.innerText, cachedDeviceIds: window.__S2_HVAC_WEB_CONTROL__.cachedDeviceIds() })`);
  assert(!organizationBState.text.includes('Alpha AHU Sensor') && !organizationBState.text.includes('Sibling Chiller Sensor'), 'Organization switch retained prior Organization state');
  assert(organizationBState.text.includes('真实模式') && !organizationBState.cachedDeviceIds.some((deviceId) => deviceId.startsWith('018f6a00-3000-7000-8000-00000000000')), 'Organization mismatch retained prior telemetry data');
  assertions.push('two-organization-dual-principal-fail-closed');

  accessibility = await evaluate(cdpClient, `(() => {
    const unnamedButtons = Array.from(document.querySelectorAll('button')).filter((button) => {
      const style = getComputedStyle(button);
      return button.tabIndex >= 0 && button.getClientRects().length > 0
        && style.visibility !== 'hidden' && style.display !== 'none'
        && !(button.getAttribute('aria-label') || button.getAttribute('title') || button.textContent?.trim());
    });
    const unlabeledComboboxes = Array.from(document.querySelectorAll('[role="combobox"]')).filter((node) => !(node.getAttribute('aria-label') || node.getAttribute('aria-labelledby')));
    const allIds = Array.from(document.querySelectorAll('[id]')).map((node) => node.id).filter(Boolean);
    return {
      unnamedButtons: unnamedButtons.map((node) => node.outerHTML.slice(0, 500)),
      unlabeledComboboxes: unlabeledComboboxes.map((node) => node.outerHTML.slice(0, 500)),
      duplicateIds: allIds.filter((id, index) => allIds.indexOf(id) !== index),
    };
  })()`);
  assert(accessibility.unnamedButtons.length === 0 && accessibility.unlabeledComboboxes.length === 0 && accessibility.duplicateIds.length === 0, `browser accessibility audit failed: ${JSON.stringify(accessibility)}`);
  assertions.push('browser-a11y-controls-labeled');

  const batchRequests = fixture.requests.filter((entry) => entry.path === '/api/v1/telemetry/observation-snapshots:batchGet');
  const targetSets = batchRequests.map((entry) => entry.body.requests.map((target) => target.deviceId).sort().join(','));
  assert(batchRequests.length >= 3, `expected initial, post-revocation and sibling-Site Presence batches, got ${batchRequests.length}`);
  assert(batchRequests.every((entry) => entry.method === 'POST' && entry.headers['x-csrf-token'] === csrfValue), 'Presence batch omitted CSRF or POST semantics');
  assert(batchRequests.every((entry) => entry.body.requests.every((target) => Array.isArray(target.keys) && target.keys.length === 0)), 'visible list requested telemetry keys');
  assert(targetSets.includes([ids.deviceA1, ids.deviceA2].sort().join(',')), 'initial visible Device batch was missing');
  assert(targetSets.includes(ids.deviceA2), 'revoked Device was not removed from the next visible batch');
  assert(targetSets.includes(ids.siblingDeviceA), 'sibling-Site visible batch was missing');
  assert(!targetSets.some((set) => set.includes(ids.deviceB)), 'second Organization telemetry request crossed the acting-Organization boundary');
  const forbiddenHeaders = fixture.requests.filter((entry) => ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'authorization'].some((name) => name in entry.headers));
  const forbiddenRoutes = fixture.requests.filter((entry) => ['/ws/telemetry', '/socket.io', 'thingsboard', '/assets/tree', '/legacy'].some((marker) => entry.path.toLowerCase().includes(marker)));
  assert(forbiddenHeaders.length === 0, `browser sent forbidden authority headers: ${JSON.stringify(forbiddenHeaders)}`);
  assert(forbiddenRoutes.length === 0, `browser called forbidden fallback/direct routes: ${JSON.stringify(forbiddenRoutes)}`);
  assert(!fixture.requests.some((entry) => JSON.stringify(entry).includes('hvac_token')), 'browser sent a Legacy/local bearer token');
  assertions.push('real-mode-network-no-fallback');

  const severeBrowserEvents = cdpClient.events.filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && ['error', 'assert'].includes(event.params?.entry?.level)));
  assert(severeBrowserEvents.length === 0, `browser emitted severe runtime events: ${JSON.stringify(severeBrowserEvents.slice(-10))}`);
  assertions.push('browser-no-runtime-errors');
  conclusion = 'passed';
  console.log('S2 Ticket 09 HVAC Web Presence/latest browser audit passed.');
} finally {
  cdpClient?.close();
  await stopBrowser(browserProcess);
  await viteServer?.close();
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  const endedAt = new Date();
  const batchRequests = fixture.requests.filter((entry) => entry.path === '/api/v1/telemetry/observation-snapshots:batchGet');
  const forbiddenHeaders = fixture.requests.filter((entry) => ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'authorization'].some((name) => name in entry.headers));
  const forbiddenRoutes = fixture.requests.filter((entry) => ['/ws/telemetry', '/socket.io', 'thingsboard', '/assets/tree', '/legacy'].some((marker) => entry.path.toLowerCase().includes(marker)));
  const shared = { schemaVersion: 1, ticket: 68, startedAt: startedAt.toISOString(), endedAt: endedAt.toISOString(), durationMs: endedAt.getTime() - startedAt.getTime(), conclusion };
  const browserReport = {
    ...shared,
    browser: browserPath,
    apiMode: 'real',
    fixtures: ids,
    assertions,
    accessibility,
  };
  const renderingReport = {
    ...shared,
    explicitStates: ['ONLINE', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE', 'MISSING', 'SUSPECT', 'revoked'],
    evidence: stateEvidence,
    mixedSnapshotPublicationFrames: 0,
    missingValuesCoercedToZero: 0,
    requestTimeUsedAsSampleTime: 0,
  };
  const networkReport = {
    ...shared,
    requestCount: fixture.requests.length,
    routes: [...new Set(fixture.requests.map((entry) => `${entry.method} ${entry.path}`))].sort(),
    presenceBatches: batchRequests.map((entry) => ({
      targetCount: entry.body?.requests?.length ?? 0,
      keySelections: entry.body?.requests?.map((target) => target.keys) ?? [],
    })),
    zeroInvariants: {
      forbiddenAuthorityHeaders: forbiddenHeaders.length,
      legacyOrMockRoutes: forbiddenRoutes.length,
      thingsBoardDirectCalls: fixture.requests.filter((entry) => entry.path.toLowerCase().includes('thingsboard')).length,
      socketIoCalls: fixture.requests.filter((entry) => entry.path.toLowerCase().includes('socket.io')).length,
      legacyTelemetryCalls: fixture.requests.filter((entry) => entry.path.includes('/ws/telemetry')).length,
    },
  };
  await mkdir(outputRoot, { recursive: true });
  for (const [name, report] of Object.entries({
    'browser-journey.json': browserReport,
    'network-audit.json': networkReport,
    'state-rendering.json': renderingReport,
  })) {
    await writeFile(join(outputRoot, name), `${JSON.stringify(report, null, 2)}\n`);
  }
}
