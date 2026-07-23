import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { existsSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const reportArgument = process.argv.find((value) => value.startsWith('--report='))?.slice('--report='.length);
const reportPath = resolve(root, reportArgument ?? 'out/s1-ticket-06/hvac-web-registry-browser.json');
const startedAt = new Date();
const profileDir = join(tmpdir(), `s1-registry-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const ids = {
  organization: '018f4e20-7a01-7abc-8def-1234567890ab',
  site: '018f4e20-7a11-7abc-8def-1234567890ab',
  equipment1: '018f4e20-7a21-7abc-8def-1234567890ab',
  equipment2: '018f4e20-7a22-7abc-8def-1234567890ab',
  device: '018f4e20-7a31-7abc-8def-1234567890ab',
  siblingSite: '018f4e20-7a12-7abc-8def-1234567890ab',
  siblingEquipment: '018f4e20-7a23-7abc-8def-1234567890ab',
  foreignEquipment: '018f4e20-7aff-7abc-8def-1234567890ab',
};
const organizationCursor = 'orgcursoraaaaaaa.orgcursorbbbbbbb';
const equipmentCursor = 'equipmentaaaaaaa.equipmentbbbbbbb';
const instant = '2026-07-23T00:00:00.000Z';
const organization = {
  id: ids.organization,
  code: 'ORG-AUTH',
  displayName: '授权能源集团',
  status: 'ACTIVE',
  revision: 4,
  createdAt: instant,
  updatedAt: instant,
};
const site = {
  id: ids.site,
  owningOrganizationId: ids.organization,
  code: 'SITE-TOKYO',
  displayName: '东京研发中心',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 7,
  createdAt: instant,
  updatedAt: instant,
};
const equipment = [
  {
    id: ids.equipment1,
    owningOrganizationId: ids.organization,
    siteId: ids.site,
    code: 'EQ-CH-01',
    displayName: '冷水机组 A',
    equipmentType: 'CHILLER',
    status: 'ACTIVE',
    revision: 3,
    createdAt: instant,
    updatedAt: instant,
  },
  {
    id: ids.equipment2,
    owningOrganizationId: ids.organization,
    siteId: ids.site,
    code: 'EQ-PUMP-02',
    displayName: '冷冻泵 B',
    equipmentType: 'PUMP',
    status: 'INACTIVE',
    revision: 2,
    createdAt: instant,
    updatedAt: instant,
  },
];
const device = {
  id: ids.device,
  owningOrganizationId: ids.organization,
  siteId: ids.site,
  code: 'DEV-GW-01',
  displayName: '冷站采集网关',
  deviceType: 'EDGE_GATEWAY',
  status: 'ACTIVE',
  revision: 5,
  createdAt: instant,
  updatedAt: instant,
};

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

function problem(status, code, detail, retryable = false) {
  return {
    type: `https://errors.hvac.local/${code.toLowerCase()}`,
    title: code === 'RESOURCE_NOT_FOUND' ? 'Resource not found' : 'Registry request failed',
    status,
    detail,
    instance: '/api/v1/registry',
    code,
    traceId: '0123456789abcdef0123456789abcdef',
    retryable,
  };
}

function json(response, status, payload, headers = {}) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'no-store',
    'x-request-id': `fixture-${Date.now()}`,
    'x-route-policy-revision': '6',
    traceparent: '00-0123456789abcdef0123456789abcdef-0123456789abcdef-01',
    ...headers,
  });
  response.end(JSON.stringify(payload));
}

function createGatewayFixture() {
  const requests = [];
  let authenticatedRequests = 0;
  const server = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    const headers = Object.fromEntries(Object.entries(request.headers).map(([key, value]) => [key, String(value)]));
    requests.push({ method: request.method, path: url.pathname, query: url.search, headers });

    if (url.pathname === '/api/v1/auth/login') {
      const returnTo = url.searchParams.get('returnTo') || '/assets';
      response.writeHead(302, {
        location: returnTo.startsWith('/') ? returnTo : '/assets',
        'set-cookie': 'hvac_fixture_session=authenticated; Path=/; HttpOnly; SameSite=Lax',
        'cache-control': 'no-store',
      });
      response.end();
      return;
    }

    if (!headers.cookie?.includes('hvac_fixture_session=authenticated')) {
      json(response, 401, problem(401, 'AUTHENTICATION_REQUIRED', 'A BFF Session is required.'));
      return;
    }
    authenticatedRequests += 1;

    if (request.method !== 'GET') {
      json(response, 405, problem(405, 'METHOD_NOT_ALLOWED', 'Only read operations are available.'));
      return;
    }

    if (url.pathname === '/api/v1/organizations') {
      const cursor = url.searchParams.get('cursor');
      if (cursor === null) {
        json(response, 200, { items: [organization], nextCursor: organizationCursor, hasMore: true });
      } else if (cursor === organizationCursor) {
        json(response, 200, { items: [], nextCursor: null, hasMore: false });
      } else {
        json(response, 400, problem(400, 'CURSOR_INVALID', 'The opaque cursor is invalid for this list action.'));
      }
      return;
    }
    if (url.pathname === `/api/v1/organizations/${ids.organization}`) {
      json(response, 200, organization);
      return;
    }
    if (url.pathname === `/api/v1/organizations/${ids.organization}/sites`) {
      json(response, 200, { items: [site], nextCursor: null, hasMore: false });
      return;
    }
    if (url.pathname === `/api/v1/sites/${ids.site}`) {
      json(response, 200, site);
      return;
    }
    if (url.pathname === `/api/v1/sites/${ids.siblingSite}/equipment`) {
      json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The requested resource was not found.'));
      return;
    }
    if (url.pathname === `/api/v1/sites/${ids.site}/equipment`) {
      const cursor = url.searchParams.get('cursor');
      if (cursor === null) {
        json(response, 200, { items: [equipment[0]], nextCursor: equipmentCursor, hasMore: true });
      } else if (cursor === equipmentCursor) {
        json(response, 200, { items: [equipment[1]], nextCursor: null, hasMore: false });
      } else {
        json(response, 400, problem(400, 'CURSOR_INVALID', 'The opaque cursor is invalid for this parent and list action.'));
      }
      return;
    }
    if (url.pathname === `/api/v1/sites/${ids.site}/devices`) {
      json(response, 200, { items: [device], nextCursor: null, hasMore: false });
      return;
    }
    if (url.pathname === `/api/v1/equipment/${ids.equipment1}`) {
      json(response, 200, equipment[0]);
      return;
    }
    if (url.pathname === `/api/v1/equipment/${ids.equipment2}`) {
      json(response, 200, equipment[1]);
      return;
    }
    if (url.pathname === `/api/v1/devices/${ids.device}`) {
      json(response, 200, device);
      return;
    }
    if (url.pathname === `/api/v1/equipment/${ids.siblingEquipment}` || url.pathname === `/api/v1/equipment/${ids.foreignEquipment}`) {
      json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The requested resource was not found.'));
      return;
    }
    if (url.pathname === '/api/v1/health') {
      json(response, 200, { status: 'ok', service: 'platform-gateway', checkedAt: instant });
      return;
    }
    json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The requested resource was not found.'));
  });
  return {
    server,
    requests,
    get authenticatedRequests() { return authenticatedRequests; },
  };
}

async function waitForHTTP(url, child, label) {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (child?.exitCode !== null || child?.signalCode !== null) throw new Error(`${label} exited before becoming ready`);
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`${label} did not become ready at ${url}`);
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped && process.platform === 'win32') spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
}

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    const events = [];
    let nextId = 0;
    socket.addEventListener('open', () => resolveClient({
      events,
      send(method, params = {}) {
        const id = ++nextId;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
    socket.addEventListener('error', (event) => rejectClient(new Error(`CDP socket error: ${String(event)}`)));
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
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
  const result = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text ?? 'Browser evaluation failed');
  return result.result.value;
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
  let diagnostic;
  try {
    diagnostic = await evaluate(client, `({
      url: location.href,
      title: document.title,
      text: document.body?.innerText?.slice(0, 4000) ?? '',
      html: document.body?.innerHTML?.slice(0, 4000) ?? '',
    })`);
  } catch (error) {
    diagnostic = { error: String(error) };
  }
  diagnostic.events = client.events.slice(-20);
  throw new Error(`${label} did not become ready; last=${JSON.stringify(last)} diagnostic=${JSON.stringify(diagnostic)}`);
}

const browserCandidates = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium',
].filter(Boolean);
const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
if (!browserPath) throw new Error('A CDP-compatible browser was not found');

const gatewayPort = await findAvailablePort();
const webPort = await findAvailablePort();
const debugPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const webURL = `http://127.0.0.1:${webPort}`;
const fixture = createGatewayFixture();
let viteProcess;
let browserProcess;
let cdpClient;
let conclusion = 'failed';
const assertions = [];

try {
  await mkdir(profileDir, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });

  viteProcess = spawn(process.execPath, [
    resolve(root, 'node_modules/vite/bin/vite.js'),
    'apps/hvac-web',
    '--config', 'apps/hvac-web/vite.config.ts',
    '--host', '127.0.0.1',
    '--port', String(webPort),
    '--strictPort',
  ], {
    cwd: root,
    stdio: 'ignore',
    shell: false,
    env: {
      ...process.env,
      VITE_API_MODE: 'real',
      S0_GATEWAY_ONLY: 'true',
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayURL,
    },
  });
  await waitForHTTP(`${webURL}/assets`, viteProcess, 'Vite real-mode server');

  browserProcess = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    '--hide-scrollbars',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`,
    'about:blank',
  ], { stdio: 'ignore' });
  await waitForHTTP(`http://127.0.0.1:${debugPort}/json/version`, browserProcess, 'browser debugger');
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Network.enable');
  await cdpClient.send('Page.enable');
  await cdpClient.send('Log.enable');
  await cdpClient.send('Page.navigate', { url: `${webURL}/api/v1/auth/login?returnTo=%2Fsystem%3Ftab%3Dsite` });

  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="real-registry-system-panel"]')) && document.body.innerText.includes('Asia/Tokyo') && document.body.innerText.includes('${ids.site}')`,
    'real System Registry panel with authoritative Site detail',
  );
  const systemState = await evaluate(cdpClient, `({
    text: document.body.innerText,
    disabledWrite: Array.from(document.querySelectorAll('button')).some((button) => button.textContent?.includes('新增节点') && button.disabled),
    mockPanel: Boolean(document.querySelector('[data-testid="mock-registry-system-panel"]')),
    url: location.pathname + location.search,
  })`);
  assert(systemState.url === '/system?tab=site', `unexpected System URL ${systemState.url}`);
  assert(systemState.text.includes('Asia/Tokyo'), 'System page did not show authoritative Site timezone');
  assert(systemState.text.includes(ids.site), 'System page did not show the platform Site UUIDv7');
  assert(systemState.text.includes('Equipment') && systemState.text.includes('Device'), 'System page did not keep Equipment and Device distinct');
  assert(systemState.disabledWrite === true, 'System real mode did not disable Registry writes');
  assert(systemState.mockPanel === false && !systemState.text.includes('Mock Tree'), 'System real mode rendered Mock Registry content');
  assertions.push('system-authoritative-registry');

  await evaluate(cdpClient, `location.assign('/assets')`);
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="real-registry-assets-page"]')) && document.body.innerText.includes('冷水机组 A')`,
    'real Assets Registry page',
  );
  const assetsState = await evaluate(cdpClient, `({
    text: document.body.innerText,
    mockNameVisible: document.body.innerText.includes('总部大楼') || document.body.innerText.includes('冷水机组 #1'),
    realPage: Boolean(document.querySelector('[data-testid="real-registry-assets-page"]')),
  })`);
  assert(assetsState.realPage === true, 'Assets real page was not rendered');
  assert(assetsState.text.includes('Asia/Tokyo'), 'Assets page did not show authoritative Site timezone');
  assert(assetsState.text.includes('S2 尚未提供'), 'Assets page synthesized Device online/telemetry state');
  assert(assetsState.mockNameVisible === false, 'Assets real mode displayed local Mock business data');
  assert(!assetsState.text.includes(ids.siblingSite) && !assetsState.text.includes(ids.siblingEquipment), 'authorized navigation disclosed sibling-Site resources');
  assert(!assetsState.text.includes(ids.foreignEquipment), 'authorized navigation disclosed a foreign-Organization resource');
  assertions.push('assets-no-mock-fallback');

  const loadMoreClicked = await evaluate(cdpClient, `(() => {
    const button = Array.from(document.querySelectorAll('button')).find((candidate) => candidate.textContent?.includes('加载更多 Equipment'));
    if (!button) return false;
    button.click();
    return true;
  })()`);
  assert(loadMoreClicked, 'Equipment cursor load-more control was not available');
  await waitForCondition(cdpClient, `document.body.innerText.includes('冷冻泵 B')`, 'second Equipment page');
  assertions.push('cursor-load-more');

  const detailClicked = await evaluate(cdpClient, `(() => {
    const button = Array.from(document.querySelectorAll('button')).find((candidate) => candidate.textContent?.trim() === '详情');
    if (!button) return false;
    button.click();
    return true;
  })()`);
  assert(detailClicked, 'Equipment detail control was not available');
  await waitForCondition(cdpClient, `document.body.innerText.includes('${ids.equipment1}') && location.search.includes('equipment=')`, 'Equipment detail');
  assertions.push('generated-detail-navigation');

  await evaluate(cdpClient, `location.assign('/assets?equipment=${ids.foreignEquipment}')`);
  await waitForCondition(cdpClient, `document.body.innerText.includes('资源不可见或不存在')`, 'foreign-Organization resource invisibility state');
  const foreignInvisibilityState = await evaluate(cdpClient, `(() => {
    const state = document.querySelector('[data-testid="registry-failure-state"][data-registry-error-kind="not-found"]');
    return { text: state?.innerText ?? '', url: location.href };
  })()`);
  assert(foreignInvisibilityState.text, 'foreign resource invisibility state was not rendered');
  assert(!foreignInvisibilityState.text.includes(ids.foreignEquipment) && !foreignInvisibilityState.text.includes('另一组织'), 'foreign resource invisibility state disclosed resource details');

  await evaluate(cdpClient, `location.assign('/assets?equipment=${ids.siblingEquipment}')`);
  await waitForCondition(cdpClient, `document.body.innerText.includes('资源不可见或不存在')`, 'sibling-Site resource invisibility state');
  const siblingInvisibilityState = await evaluate(cdpClient, `(() => {
    const state = document.querySelector('[data-testid="registry-failure-state"][data-registry-error-kind="not-found"]');
    return { text: state?.innerText ?? '', url: location.href };
  })()`);
  assert(siblingInvisibilityState.text, 'sibling-Site resource invisibility state was not rendered');
  assert(!siblingInvisibilityState.text.includes(ids.siblingEquipment) && !siblingInvisibilityState.text.includes(ids.siblingSite), 'sibling-Site invisibility state disclosed resource details');
  assert(siblingInvisibilityState.text === foreignInvisibilityState.text, 'sibling-Site and foreign-Organization resources did not produce indistinguishable public states');

  const siblingCursorReuse = await evaluate(cdpClient, `(async () => {
    const response = await fetch('/api/v1/sites/${ids.siblingSite}/equipment?limit=50&cursor=${encodeURIComponent(equipmentCursor)}');
    return { status: response.status, body: await response.json() };
  })()`);
  assert(siblingCursorReuse.status === 404 && siblingCursorReuse.body?.code === 'RESOURCE_NOT_FOUND', 'sibling-Site cursor reuse did not fail as resource-invisible');
  assert(!JSON.stringify(siblingCursorReuse.body).includes(ids.siblingSite), 'sibling-Site cursor failure disclosed the parent Site');

  const actionCursorReuse = await evaluate(cdpClient, `(async () => {
    const response = await fetch('/api/v1/sites/${ids.site}/equipment?limit=50&cursor=${encodeURIComponent(organizationCursor)}');
    return { status: response.status, body: await response.json() };
  })()`);
  assert(actionCursorReuse.status === 400 && actionCursorReuse.body?.code === 'CURSOR_INVALID', 'cross-action cursor reuse did not fail closed');
  assert(!('items' in actionCursorReuse.body), 'cross-action cursor failure returned Registry items');
  assertions.push('resource-invisibility-and-cursor-binding');

  const requestsBeforeInvalidLink = fixture.requests.length;
  await evaluate(cdpClient, `location.assign('/assets?equipment=not-a-platform-uuid')`);
  await waitForCondition(cdpClient, `document.body.innerText.includes('资源链接无效')`, 'invalid UUIDv7 detail state');
  assert(fixture.requests.length >= requestsBeforeInvalidLink, 'fixture request accounting regressed');
  assert(!fixture.requests.some((entry) => entry.path.includes('not-a-platform-uuid')), 'invalid detail ID reached the Gateway fixture');
  assertions.push('invalid-detail-id-fails-client-side');

  const forbiddenHeaders = fixture.requests.filter((entry) =>
    ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'x-scope'].some((name) => name in entry.headers));
  assert(forbiddenHeaders.length === 0, `browser sent forbidden authorization headers: ${JSON.stringify(forbiddenHeaders)}`);
  assert(fixture.authenticatedRequests > 0, 'Registry requests were not protected by the fixture Session');
  assert(fixture.requests.some((entry) => entry.path === '/api/v1/organizations' && entry.query.includes(`cursor=${encodeURIComponent(organizationCursor)}`)), 'authorized navigation did not use its Organization-bound cursor');
  assert(fixture.requests.some((entry) => entry.path === `/api/v1/sites/${ids.site}/equipment` && entry.query.includes(`cursor=${encodeURIComponent(equipmentCursor)}`)), 'Equipment load-more did not use its action/parent-bound cursor');
  assert(!fixture.requests.some((entry) => entry.path === '/api/v1/organizations' && entry.query.includes(equipmentCursor)), 'Equipment cursor was reused for Organization navigation');
  assert(fixture.requests.filter((entry) => entry.path === `/api/v1/sites/${ids.site}/equipment` && entry.query.includes(`cursor=${encodeURIComponent(organizationCursor)}`)).length === 1, 'Organization cursor was reused outside the explicit negative test');
  assert(!fixture.requests.some((entry) => entry.path.includes('/assets/tree') || entry.path.includes('/ws/telemetry')), 'real Registry journey called a Mock/Legacy asset or telemetry endpoint');
  assertions.push('gateway-only-session-requests');

  conclusion = 'passed';
  console.log('S1 HVAC Web Registry browser audit passed.');
} finally {
  cdpClient?.close();
  await stopProcess(browserProcess);
  await stopProcess(viteProcess);
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 250 });
  const endedAt = new Date();
  const report = {
    schemaVersion: 1,
    ticket: 'S1-06',
    startedAt: startedAt.toISOString(),
    endedAt: endedAt.toISOString(),
    durationMs: endedAt.getTime() - startedAt.getTime(),
    conclusion,
    environment: {
      apiMode: 'real',
      gatewayFixture: gatewayURL,
      browser: browserPath,
      node: process.version,
    },
    fixtures: ids,
    assertions,
    requestCount: fixture.requests.length,
    authenticatedRequestCount: fixture.authenticatedRequests,
    routes: [...new Set(fixture.requests.map((entry) => `${entry.method} ${entry.path}`))].sort(),
    zeroInvariants: {
      mockBusinessFallbacks: 0,
      forbiddenAuthorizationHeaders: fixture.requests.filter((entry) =>
        ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'x-scope'].some((name) => name in entry.headers)).length,
      legacyAssetTreeCalls: fixture.requests.filter((entry) => entry.path.includes('/assets/tree')).length,
      telemetryCalls: fixture.requests.filter((entry) => entry.path.includes('/ws/telemetry')).length,
    },
  };
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
}
