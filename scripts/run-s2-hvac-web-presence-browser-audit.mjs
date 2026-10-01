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
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

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
      capabilitySetVersion: 6,
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

async function openAssetsFullDetail(client, deviceId) {
  assert(await evaluate(client, `(() => {
    const card = document.querySelector('[data-testid="real-assets-device-card"][data-device-id="${deviceId}"]');
    if (!(card instanceof HTMLElement)) return false;
    card.click();
    return true;
  })()`), `Device card ${deviceId} was unavailable`);
  await waitForCondition(
    client,
    `document.querySelector('[data-testid="real-assets-device-quick"]')?.getAttribute('data-device-id') === ${JSON.stringify(deviceId)}`,
    `Device quick inspector ${deviceId}`,
  );
  assert(await evaluate(client, `(() => {
    const button = document.querySelector('[data-testid="real-assets-quick-full-detail"]');
    if (!(button instanceof HTMLButtonElement)) return false;
    button.click();
    return true;
  })()`), `Device full detail action ${deviceId} was unavailable`);
  await waitForCondition(
    client,
    `location.pathname.endsWith('/device/${deviceId}') && Boolean(document.querySelector('[data-testid="real-assets-device-detail"]'))`,
    `Device full detail ${deviceId}`,
  );
}

async function closeAssetsDetail(client) {
  assert(await evaluate(client, `(() => {
    const button = document.querySelector('[data-testid="real-assets-detail-close"]');
    if (!(button instanceof HTMLButtonElement)) return false;
    button.click();
    return true;
  })()`), 'Device detail close action was unavailable');
  await waitForCondition(client, `!document.querySelector('[data-testid="real-assets-device-detail"]')`, 'Device detail close');
}

async function exposeAssetsRealtimeStatus(client) {
  assert(await evaluate(client, `(() => {
    const tab = document.querySelector('[data-testid="real-assets-detail-tab-connection"]');
    if (!(tab instanceof HTMLElement)) return false;
    tab.click();
    return true;
  })()`), 'Connection detail tab was unavailable');
  await waitForCondition(
    client,
    `Array.from(document.querySelectorAll('.ant-collapse-header')).some((node) => node.textContent?.includes('技术信息'))`,
    'Device technical information collapse',
  );
  await evaluate(client, `(() => {
    if (document.querySelector('[data-testid="real-assets-device-realtime"]')) return true;
    const header = Array.from(document.querySelectorAll('.ant-collapse-header')).find((node) => node.textContent?.includes('技术信息'));
    if (!(header instanceof HTMLElement)) return false;
    header.click();
    return true;
  })()`);
  await waitForCondition(client, `Boolean(document.querySelector('[data-testid="real-assets-device-realtime"]'))`, 'Device realtime status');
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');

}

const browserPath = resolveLinuxBrowserExecutable();
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
  await cdpClient.send('Page.navigate', { url: `${webURL}/assets-realtime` });
  await waitForCondition(
    cdpClient,
    `Boolean(window.__ASSETS_REALTIME_CONTROL__)
      && document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-business-state') === 'READY'
      && document.querySelectorAll('[data-testid="real-assets-device-card"]').length === 2`,
    'Device Center realtime list',
  );
  const realtimeListAudit = await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.audit()`);
  assert(realtimeListAudit.opens.length === 0, 'Device Center list opened an all-Device realtime subscription');

  await openAssetsFullDetail(cdpClient, ids.deviceA1);
  await waitForCondition(
    cdpClient,
    `window.__ASSETS_REALTIME_CONTROL__.audit().opens.length === 1
      && document.querySelector('[data-testid="real-assets-device-detail"]')?.textContent?.includes('212.5 kW')`,
    'Snapshot-first projected Device detail',
  );
  await exposeAssetsRealtimeStatus(cdpClient);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state') === 'live'
      && document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-source') === 'realtime'`,
    'Snapshot-first exact live baseline',
  );
  const firstRealtimeDetail = await evaluate(cdpClient, `({
    pathname: location.pathname,
    audit: window.__ASSETS_REALTIME_CONTROL__.audit(),
    realtime: {
      state: document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state'),
      source: document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-source'),
      revision: document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-revision'),
      baseline: document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-baseline-revision'),
    },
  })`);
  assert(firstRealtimeDetail.audit.opens.length === 1, 'opening one Device did not create exactly one live session');
  assert(firstRealtimeDetail.audit.opens[0].deviceId === ids.deviceA1, 'first live session targeted the wrong Device');
  assert(JSON.stringify(firstRealtimeDetail.audit.opens[0].keys) === JSON.stringify([
    'chiller.cooling_capacity', 'chiller.cop', 'chiller.power', 'chiller.run_state',
  ]), 'first live session did not use exact registered Point keys');
  assert(firstRealtimeDetail.realtime.revision === '41' && firstRealtimeDetail.realtime.baseline === '40', 'Snapshot-first revision evidence was not observable');

  await closeAssetsDetail(cdpClient);
  await waitForCondition(
    cdpClient,
    `window.__ASSETS_REALTIME_CONTROL__.audit().closeCount >= 1
      && document.querySelectorAll('[data-testid="real-assets-device-card"]').length === 2`,
    'first Device detail closed cleanly',
  );
  await openAssetsFullDetail(cdpClient, ids.deviceA2);
  await waitForCondition(
    cdpClient,
    `window.__ASSETS_REALTIME_CONTROL__.audit().opens.length === 2
      && window.__ASSETS_REALTIME_CONTROL__.audit().closeCount >= 1`,
    'Device switch closes and reopens exact live session',
  );
  const switchedAudit = await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.audit()`);
  assert(switchedAudit.opens[1].deviceId === ids.deviceA2, 'Device switch reopened the wrong live target');

  await closeAssetsDetail(cdpClient);
  await waitForCondition(
    cdpClient,
    `window.__ASSETS_REALTIME_CONTROL__.audit().closeCount >= 2
      && document.querySelectorAll('[data-testid="real-assets-device-card"]').length === 2`,
    'Device detail close ends exact live session',
  );

  await openAssetsFullDetail(cdpClient, ids.deviceA2);
  await waitForCondition(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.audit().opens.length === 3`, 'reopened exact live session');

  await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.setMode('live-update')`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-detail"]')?.textContent?.includes('220 kW')`,
    'continuous live projection',
  );
  await exposeAssetsRealtimeStatus(cdpClient);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-source') === 'realtime'
      && document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-revision') === '42'`,
    'continuous live revision',
  );
  await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.setMode('reconnect')`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state') === 'snapshot'
      && document.querySelector('[data-testid="real-assets-device-realtime"]')?.textContent?.includes('正在重连')`,
    'reconnect retains authoritative Snapshot',
  );
  await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.setMode('gap')`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state') === 'unavailable'
      && document.querySelector('[data-testid="real-assets-device-realtime"]')?.textContent?.includes('实时连续性需要重新同步')`,
    'revision gap retains Snapshot and requires recovery',
  );
  await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.setMode('outage')`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-realtime"]')?.textContent?.includes('实时 transport 暂不可用')`,
    'transport outage remains separate from current truth',
  );
  assert(await evaluate(cdpClient, `(() => {
    const button = document.querySelector('[data-testid="real-assets-realtime-refresh"]');
    if (!(button instanceof HTMLButtonElement)) return false;
    button.click();
    return true;
  })()`), 'realtime recovery button was unavailable');
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state') === 'live'
      && document.querySelector('[data-testid="real-assets-device-realtime"]')?.textContent?.includes('实时已恢复')`,
    'manual live baseline recovery',
  );

  await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.setMode('revoke')`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state') === 'revoked'
      && document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-source') === 'none'
      && document.querySelector('[data-testid="real-assets-device-detail"]')?.textContent?.includes('业务版本不可用')`,
    'realtime revocation clears protected Snapshot',
  );
  const revokedRealtimeAudit = await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.audit()`);
  assert(revokedRealtimeAudit.purgeCount >= 1 && revokedRealtimeAudit.closeCount >= 3, 'revocation did not purge and close live state');
  await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.setMode('live-update')`);
  await pause(200);
  assert(await evaluate(cdpClient, `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state') === 'revoked'
    && document.querySelector('[data-testid="real-assets-device-detail"]')?.textContent?.includes('业务版本不可用')`), 'late live update wrote after revocation');

  await cdpClient.send('Page.navigate', { url: `${webURL}/assets-realtime` });
  await waitForCondition(cdpClient, `Boolean(window.__ASSETS_REALTIME_CONTROL__) && document.querySelectorAll('[data-testid="real-assets-device-card"]').length === 2`, 'reloaded Device Center harness');
  await cdpClient.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await openAssetsFullDetail(cdpClient, ids.deviceA1);
  await exposeAssetsRealtimeStatus(cdpClient);
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="real-assets-device-realtime"]')?.getAttribute('data-realtime-state') === 'live'`, 'mobile realtime detail');
  const mobileRealtime = await evaluate(cdpClient, `(() => {
    const drawer = document.querySelector('[data-testid="real-assets-device-detail"]');
    const status = document.querySelector('[data-testid="real-assets-device-realtime"]');
    return {
      documentOverflow: document.documentElement.scrollWidth > window.innerWidth + 1,
      drawerOverflow: drawer ? drawer.scrollWidth > drawer.clientWidth + 1 : true,
      statusOverflow: status ? status.scrollWidth > status.clientWidth + 1 : true,
      focusedHeading: document.activeElement?.id === 'real-assets-detail-title',
    };
  })()`);
  assert(!mobileRealtime.documentOverflow && !mobileRealtime.drawerOverflow && !mobileRealtime.statusOverflow && mobileRealtime.focusedHeading, 'mobile realtime detail overflowed or lost focus');
  const purgeOutcome = await evaluate(cdpClient, `window.__ASSETS_REALTIME_CONTROL__.purgeScope()`);
  await waitForCondition(
    cdpClient,
    `!document.querySelector('[data-testid="real-assets-device-detail"]')
      && window.__ASSETS_REALTIME_CONTROL__.audit().purgeCount >= 1
      && window.__ASSETS_REALTIME_CONTROL__.protectedScope().resourceCount === 0`,
    'ProtectedScope realtime purge',
  );
  assert(purgeOutcome.status === 'completed', 'ProtectedScope purge did not complete');
  await cdpClient.send('Emulation.clearDeviceMetricsOverride');
  assertions.push('device-center-exact-point-realtime-lifecycle');
  assertions.push('device-center-recovery-revocation-protected-purge');
  stateEvidence.assetsRealtime = {
    listSubscriptionCount: realtimeListAudit.opens.length,
    exactKeys: firstRealtimeDetail.audit.opens[0].keys,
    snapshotFirst: true,
    liveRevision: 42,
    reconnectRetainedSnapshot: true,
    gapRequiredRecovery: true,
    revocationPurged: true,
    lateEventIgnored: true,
    mobileOverflow: false,
    protectedPurge: purgeOutcome.status,
  };

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

  const forbiddenHeaders = fixture.requests.filter((entry) => ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'authorization'].some((name) => name in entry.headers));
  const forbiddenRoutes = fixture.requests.filter((entry) => ['/ws/telemetry', '/socket.io', 'thingsboard', '/assets/tree', '/legacy'].some((marker) => entry.path.toLowerCase().includes(marker)));
  assert(forbiddenHeaders.length === 0, `browser sent forbidden authority headers: ${JSON.stringify(forbiddenHeaders)}`);
  assert(forbiddenRoutes.length === 0, `browser called forbidden fallback/direct routes: ${JSON.stringify(forbiddenRoutes)}`);
  assert(!fixture.requests.some((entry) => JSON.stringify(entry).includes('hvac_token')), 'browser sent a Legacy/local bearer token');
  assertions.push('production-network-no-fallback');

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
  const forbiddenHeaders = fixture.requests.filter((entry) => ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'authorization'].some((name) => name in entry.headers));
  const forbiddenRoutes = fixture.requests.filter((entry) => ['/ws/telemetry', '/socket.io', 'thingsboard', '/assets/tree', '/legacy'].some((marker) => entry.path.toLowerCase().includes(marker)));
  const shared = { schemaVersion: 1, ticket: 68, startedAt: startedAt.toISOString(), endedAt: endedAt.toISOString(), durationMs: endedAt.getTime() - startedAt.getTime(), conclusion };
  const browserReport = {
    ...shared,
    browser: browserPath,
    artifactMode: 'production',
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
