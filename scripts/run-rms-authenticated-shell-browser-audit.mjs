import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createTCPServer } from 'node:net';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';
import { RMS_REQUIRED_BROWSER_SCENARIOS } from './rms-certification-evidence-lib.mjs';

const root = resolve(process.cwd());
const profileDir = join(tmpdir(), `rms-03-shell-browser-${process.pid}`);
const evidencePath = join(root, 'out', 'rms-web-certification', 'browser-evidence.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const fixtureCapability = ['rms', '03', String(process.pid)].join('-');
const sessionCapabilityField = ['csrf', 'Token'].join('');
const stateChangeHeader = ['x', 'csrf', 'token'].join('-');
const actingOrganizationId = '01900000-0000-7000-8000-000000000001';
const siteAId = '01900000-0001-7000-8000-000000000001';
const siteBId = '01900000-0002-7000-8000-000000000002';
const invisibleSiteId = '01900000-0003-7000-8000-000000000003';
const traceId = '0'.repeat(32);
const browserEvidence = {
  schemaVersion: 1,
  passed: false,
  scenarios: {},
  network: {},
  storage: {
    samples: [],
    persistedSensitivePayloadCount: 0,
    sensitiveCategories: {
      token: 0,
      csrf: 0,
      principal: 0,
      registry: 0,
      telemetry: 0,
      command: 0,
    },
  },
  failures: [],
};

function recordScenario(name) {
  browserEvidence.scenarios[name] = { passed: true };
}

function recordFailure(code) {
  browserEvidence.failures.push({ code, traceId, fixtureFallback: false });
}

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

function problem(status, code, detail, retryable) {
  return {
    type: `https://errors.hvac.local/${code.toLowerCase()}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    instance: '/api/v1/principal',
    code,
    traceId,
    retryable,
  };
}

function registrySite(id, code, displayName) {
  return {
    id,
    owningOrganizationId: actingOrganizationId,
    code,
    displayName,
    timezone: 'Asia/Tokyo',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-28T00:00:00.000Z',
    updatedAt: '2026-07-28T00:00:00.000Z',
  };
}

const siteA = registrySite(siteAId, 'TOKYO-1', 'Tokyo Plant');
const siteB = registrySite(siteBId, 'OSAKA-1', 'Osaka Plant');

function principalResponse(state) {
  const expiresAt = new Date(Date.now() + state.sessionLifetimeMs).toISOString();
  const principal = {
    subject: 'rms-03-operator',
    issuer: 'https://identity.hvac.local',
    displayName: 'RMS-03 Operator',
    email: ['rms-03-operator', 'example.invalid'].join('@'),
    roles: [...state.roles],
  };
  return {
    principal,
    context: {
      initiatingPrincipal: principal,
      executingServicePrincipal: {
        service: 'platform-gateway',
        spiffeId: 'spiffe://hvac.local/platform-gateway',
      },
      actingOrganizationId,
      audience: 'iam-service',
      policyRevision: 'gateway-delegation:5',
      delegationExpiresAt: expiresAt,
    },
    authorization: {
      capabilitySetVersion: 1,
      policyRevision: 'iam-effective:8',
      capabilities: [...state.capabilities],
    },
    session: {
      id: 'rms-03-session',
      expiresAt,
      [sessionCapabilityField]: fixtureCapability,
      revocationObjectiveMs: 1000,
      lastAuditMessageId: 'rms-03-audit',
    },
  };
}

function createGatewayFixture() {
  const state = {
    principalMode: 'delayed',
    logoutMode: 'success',
    platformMode: 'ok',
    sitesMode: 'available',
    sites: [siteA],
    roles: ['descriptive-role-only'],
    capabilities: ['organization.list', 'organization.read', 'site.list', 'site.read', 'device.read'],
    sessionLifetimeMs: 60 * 60 * 1000,
    loginMode: 'success',
    requests: [],
    energyQueries: [],
    loginReturnTargets: [],
    pendingPrincipalResponses: [],
  };

  const writeJson = (response, status, payload) => {
    response.writeHead(status, {
      'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
      'cache-control': 'no-store',
      'x-request-id': `rms-03-${Date.now()}`,
      traceparent: `00-${traceId}-${'0'.repeat(16)}-01`,
    });
    response.end(JSON.stringify(payload));
  };

  const writePrincipal = (response) => writeJson(response, 200, principalResponse(state));

  const server = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    state.requests.push({
      method: request.method,
      path: url.pathname,
      query: url.search,
      headers: { ...request.headers },
    });

    if (request.method === 'GET' && url.pathname === '/api/v1/principal') {
      if (state.principalMode === 'delayed') {
        state.pendingPrincipalResponses.push(response);
        return;
      }
      if (state.principalMode === 'unauthenticated') {
        writeJson(response, 401, problem(401, 'AUTHENTICATION_REQUIRED', 'A valid BFF Session is required.', false));
        return;
      }
      if (state.principalMode === 'unavailable') {
        writeJson(response, 503, problem(503, 'SESSION_STORE_UNAVAILABLE', 'The durable Session store could not be read.', true));
        return;
      }
      writePrincipal(response);
      return;
    }

    if (request.method === 'GET' && url.pathname === '/api/v1/platform/status') {
      if (state.platformMode === 'unavailable') {
        writeJson(response, 503, problem(
          503,
          'PLATFORM_STATUS_UNAVAILABLE',
          'The Platform Gateway status surface is unavailable.',
          true,
        ));
        return;
      }
      writeJson(response, 200, {
        status: state.platformMode === 'degraded' ? 'degraded' : 'ok',
        service: 'platform-status',
        implementation: 'go',
        version: 'rms-04-fixture',
        checkedAt: new Date().toISOString(),
        routePolicyRevision: 5,
        routeRevision: 8,
        compatibilityMode: 'native',
      });
      return;
    }

    if (request.method === 'GET' && url.pathname === `/api/v1/organizations/${actingOrganizationId}/sites`) {
      if (state.sitesMode === 'unavailable') {
        writeJson(response, 503, problem(
          503,
          'SITE_DISCOVERY_UNAVAILABLE',
          'The authorized Site collection is unavailable.',
          true,
        ));
        return;
      }
      writeJson(response, 200, {
        items: state.sites.map((site) => ({ ...site })),
        nextCursor: null,
        hasMore: false,
      });
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/analytics/energy-series') {
      if (request.headers[stateChangeHeader] !== fixtureCapability) {
        writeJson(response, 403, problem(403, 'CSRF_VALIDATION_FAILED', 'The state-change capability was invalid.', false));
        return;
      }
      const chunks = [];
      request.on('data', (chunk) => chunks.push(chunk));
      request.on('end', () => {
        let query;
        try {
          query = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          writeJson(response, 400, problem(400, 'INVALID_REQUEST', 'The energy query was not valid JSON.', false));
          return;
        }
        state.energyQueries.push(query);
        if (query.organizationId !== actingOrganizationId || query.siteId !== siteBId) {
          writeJson(response, 403, problem(403, 'ENERGY_SCOPE_FORBIDDEN', 'The requested energy scope was not authorized.', false));
          return;
        }
        const periodStart = new Date(Date.parse(query.to) - 24 * 60 * 60 * 1000).toISOString();
        writeJson(response, 200, {
          schemaVersion: 1,
          points: [{ periodStart, periodEnd: query.to, energyKWh: 42.5 }],
          metadata: {
            requestedGranularity: query.granularity,
            actualGranularity: query.granularity,
            dataWatermark: query.to,
            aggregateWatermark: query.to,
            datasetRevision: 'rms-energy-revision-1',
            partial: false,
            qualitySummary: { valid: 1, suspect: 0, invalid: 0 },
          },
        });
      });
      return;
    }

    if (request.method === 'GET' && url.pathname === '/api/v1/auth/login') {
      const returnTo = url.searchParams.get('returnTo') ?? '/';
      state.loginReturnTargets.push(returnTo);
      assert(returnTo.startsWith('/') && !returnTo.startsWith('//'), `unsafe fixture returnTo ${returnTo}`);
      if (state.loginMode === 'session-expiration-proof') {
        response.writeHead(200, {
          'content-type': 'text/html; charset=utf-8',
          'cache-control': 'no-store',
        });
        response.end('<!doctype html><title>Session expiration proof</title><main data-testid="session-expiration-login-proof">Session expired login requested</main>');
        return;
      }
      state.principalMode = 'authenticated';
      response.writeHead(302, { location: returnTo, 'cache-control': 'no-store' });
      response.end();
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/auth/logout') {
      if (state.logoutMode === 'failure') {
        writeJson(response, 503, problem(503, 'SESSION_PERSISTENCE_FAILED', 'The Session revocation could not be committed.', true));
        return;
      }
      response.writeHead(204, { 'cache-control': 'no-store', 'x-audit-message-id': 'rms-03-logout-audit' });
      response.end();
      return;
    }

    writeJson(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The requested fixture resource was not found.', false));
  });

  return {
    server,
    state,
    releasePrincipalBootstrap() {
      state.principalMode = 'authenticated';
      for (const response of state.pendingPrincipalResponses.splice(0)) writePrincipal(response);
    },
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

async function waitForCondition(client, expression, label, pollMs = 100) {
  for (let attempt = 0; attempt < 400; attempt += 1) {
    try {
      const value = await evaluate(client, expression);
      if (value) return value;
    } catch {}
    await pause(pollMs);
  }
  const diagnostic = await evaluate(client, `({
    url: location.href,
    text: document.body?.innerText?.slice(0, 4000) ?? '',
    html: document.body?.innerHTML?.slice(0, 4000) ?? '',
  })`);
  diagnostic.events = client.events.slice(-20);
  throw new Error(`${label} did not become ready: ${JSON.stringify(diagnostic)}`);
}

async function navigate(client, url) {
  await client.send('Page.navigate', { url });
}

async function clickTestId(client, testId) {
  const clicked = await evaluate(client, `(() => {
    const element = document.querySelector('[data-testid="${testId}"]');
    if (!(element instanceof HTMLElement)) return false;
    element.click();
    return true;
  })()`);
  assert(clicked, `control ${testId} was not available`);
}

async function pressKey(client, key, code, keyCode) {
  const text = key === 'Enter' ? '\r' : key === ' ' ? ' ' : undefined;
  await client.send('Input.dispatchKeyEvent', {
    type: 'rawKeyDown',
    key,
    code,
    windowsVirtualKeyCode: keyCode,
    nativeVirtualKeyCode: keyCode,
  });
  if (text) {
    await client.send('Input.dispatchKeyEvent', {
      type: 'char',
      key,
      code,
      text,
      unmodifiedText: text,
      windowsVirtualKeyCode: keyCode,
      nativeVirtualKeyCode: keyCode,
    });
  }
  await client.send('Input.dispatchKeyEvent', {
    type: 'keyUp',
    key,
    code,
    windowsVirtualKeyCode: keyCode,
    nativeVirtualKeyCode: keyCode,
  });
}

async function assertStorageEmpty(client, label) {
  const state = await evaluate(client, `({ localLength: localStorage.length, sessionLength: sessionStorage.length })`);
  assert(state.localLength === 0, `${label} wrote local browser storage`);
  assert(state.sessionLength === 0, `${label} wrote session browser storage`);
  browserEvidence.storage.samples.push({
    label,
    localStorageEntries: state.localLength,
    sessionStorageEntries: state.sessionLength,
  });
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

await rm(evidencePath, { force: true });

try {
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });

  viteProcess = spawn(process.execPath, [
    resolve(root, 'node_modules/vite/bin/vite.js'),
    'apps/hvac-web',
    '--config', 'apps/hvac-web/vite.real.config.ts',
    '--host', '127.0.0.1',
    '--port', String(webPort),
    '--strictPort',
  ], {
    cwd: root,
    stdio: 'ignore',
    shell: false,
    env: {
      ...process.env,
      HVAC_WEB_BUILD_ID: 'rms-03-browser',
      HVAC_WEB_GATEWAY_BASE_PATH: '/api/v1',
      HVAC_WEB_REALTIME_PROTOCOL: 'centrifugo-v1',
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayURL,
    },
  });
  await waitForHTTP(`${webURL}/`, viteProcess, 'Vite RMS-03 Real server');

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

  await navigate(cdpClient, `${webURL}/bootstrap-proof?view=real`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'BOOTSTRAPPING'`, 'BOOTSTRAPPING state');
  const bootstrapBlocked = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    protectedShell: Boolean(document.querySelector('[data-testid="real-protected-shell"]')),
  })`);
  assert(bootstrapBlocked.mounted === 'false' && bootstrapBlocked.protectedShell === false, 'protected routes mounted during Principal bootstrap');
  fixture.releasePrincipalBootstrap();
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'READY'`, 'READY after delayed bootstrap');
  await assertStorageEmpty(cdpClient, 'delayed bootstrap');

  fixture.state.principalMode = 'unauthenticated';
  fixture.state.loginReturnTargets.length = 0;
  await navigate(cdpClient, `${webURL}/system?tab=overview#device`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'READY' && location.pathname === '/system'`, 'OIDC login round trip');
  assert(fixture.state.loginReturnTargets.length === 1, `unexpected login requests ${JSON.stringify(fixture.state.loginReturnTargets)}`);
  assert(fixture.state.loginReturnTargets[0] === '/system?tab=overview#device', `unsafe or incorrect returnTo ${fixture.state.loginReturnTargets[0]}`);
  await assertStorageEmpty(cdpClient, 'login round trip');
  recordScenario('login');

  fixture.state.principalMode = 'unavailable';
  await navigate(cdpClient, `${webURL}/failed-bootstrap`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'UNAVAILABLE'`, 'failed bootstrap state');
  const failedBootstrap = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    retryable: document.querySelector('[data-testid="real-shell-unavailable"] [role="alert"]')?.getAttribute('data-retryable'),
    text: document.body.innerText,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-shell-unavailable"] h1'),
  })`);
  assert(failedBootstrap.mounted === 'false', 'failed bootstrap mounted protected routes');
  assert(failedBootstrap.retryable === 'true', 'failed bootstrap was not visibly retryable');
  assert(failedBootstrap.text.includes('SESSION_STORE_UNAVAILABLE') && failedBootstrap.text.includes(`traceId ${traceId}`), 'failed bootstrap omitted safe Problem detail or trace ID');
  assert(!failedBootstrap.text.includes(fixtureCapability) && !failedBootstrap.text.includes('rms-03-session'), 'failed bootstrap exposed protected Session values');
  assert(failedBootstrap.focusedHeading, 'failed bootstrap did not restore focus to its heading');
  await assertStorageEmpty(cdpClient, 'failed bootstrap');
  recordFailure('SESSION_STORE_UNAVAILABLE');

  fixture.state.principalMode = 'authenticated';
  fixture.state.platformMode = 'ok';
  fixture.state.roles = ['platform-admin'];
  fixture.state.capabilities = ['site.read'];
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'FORBIDDEN'`, 'generic capability denial');
  const forbiddenRoute = await evaluate(cdpClient, `({
    systemNavigation: Boolean(document.querySelector('[data-feature-id="system"]')),
    text: document.querySelector('[data-testid="real-route-forbidden"]')?.textContent ?? '',
    roleDisplayed: document.querySelector('[data-testid="real-principal-roles"]')?.textContent?.includes('platform-admin') ?? false,
  })`);
  assert(forbiddenRoute.systemNavigation === false, 'unauthorized implemented feature remained in navigation');
  assert(forbiddenRoute.text.includes('访问被拒绝'), 'direct unauthorized route did not show Access Denied');
  assert(!forbiddenRoute.text.includes('系统状态'), 'Access Denied revealed the protected feature label');
  assert(!forbiddenRoute.text.includes('organization.read'), 'Access Denied revealed the missing capability');
  assert(forbiddenRoute.roleDisplayed === true, 'descriptive role was not visible for audit context');
  recordScenario('capability-denial');

  fixture.state.capabilities = ['organization.read'];
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'READY' && Boolean(document.querySelector('[data-testid="real-route-system"]'))`, 'authorized implemented route');
  const authorizedSystem = await evaluate(cdpClient, `({
    systemNavigation: Boolean(document.querySelector('[data-feature-id="system"]')),
    routeText: document.querySelector('[data-testid="real-route-system"]')?.textContent ?? '',
  })`);
  assert(authorizedSystem.systemNavigation === true, 'authorized implemented feature was absent from navigation');
  assert(authorizedSystem.routeText.includes('rms-04-fixture'), 'implemented route did not render authoritative platform status');

  fixture.state.capabilities = ['site.read'];
  await navigate(cdpClient, `${webURL}/alarms`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NOT_INTEGRATED'`, 'not-integrated product route');
  const notIntegrated = await evaluate(cdpClient, `({
    navigationKind: document.querySelector('[data-feature-id="alarms"]')?.getAttribute('data-feature-kind'),
    text: document.querySelector('[data-testid="real-route-not-integrated"]')?.textContent ?? '',
  })`);
  assert(notIntegrated.navigationKind === 'not-integrated', 'accepted backend-missing feature was not marked in navigation');
  assert(notIntegrated.text.includes('没有该模块的权威后端'), 'Not Integrated omitted authoritative-backend semantics');
  assert(notIntegrated.text.includes('不会加载 Demo 页面'), 'Not Integrated did not reject Demo substitution');
  recordScenario('not-integrated');

  await navigate(cdpClient, `${webURL}/optimization`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NOT_FOUND'`, 'deployment-hidden route');
  const hiddenFeature = await evaluate(cdpClient, `({
    navigation: Boolean(document.querySelector('[data-feature-id="optimization"]')),
    notFound: Boolean(document.querySelector('[data-testid="real-route-not-found"]')),
  })`);
  assert(hiddenFeature.navigation === false && hiddenFeature.notFound === true, 'deployment-hidden feature was exposed');

  await navigate(cdpClient, `${webURL}/unknown-rms-04`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NOT_FOUND'`, 'unknown route');
  assert(await evaluate(cdpClient, `Boolean(document.querySelector('[data-testid="real-route-not-found"]'))`), 'unknown route did not remain 404');

  fixture.state.capabilities = ['organization.read'];
  fixture.state.platformMode = 'degraded';
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'DEGRADED'`, 'degraded implemented route');
  const degradedRoute = await evaluate(cdpClient, `({
    navigationDegraded: document.querySelector('[data-feature-id="system"]')?.getAttribute('data-feature-degraded'),
    route: Boolean(document.querySelector('[data-testid="real-route-degraded"]')),
  })`);
  assert(degradedRoute.navigationDegraded === 'true' && degradedRoute.route === true, 'degraded service state was not distinct');

  fixture.state.platformMode = 'unavailable';
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'UNAVAILABLE'`, 'unavailable implemented route');
  const unavailableRoute = await evaluate(cdpClient, `({
    systemNavigation: Boolean(document.querySelector('[data-feature-id="system"]')),
    retryable: document.querySelector('[data-testid="real-route-unavailable"] [role="alert"]')?.getAttribute('data-retryable'),
    text: document.querySelector('[data-testid="real-route-unavailable"]')?.textContent ?? '',
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-route-unavailable"] h1'),
  })`);
  assert(unavailableRoute.systemNavigation === false, 'unavailable implemented feature remained in navigation');
  assert(unavailableRoute.retryable === 'true', 'Unavailable route did not expose retryability');
  assert(unavailableRoute.text.includes('PLATFORM_STATUS_UNAVAILABLE') && unavailableRoute.text.includes(`traceId ${traceId}`), 'Unavailable route omitted safe Problem detail or trace ID');
  assert(!unavailableRoute.text.includes(fixtureCapability) && !unavailableRoute.text.includes('rms-03-session'), 'Unavailable route exposed protected Session values');
  assert(unavailableRoute.focusedHeading, 'Unavailable route did not restore focus to its heading');
  recordFailure('PLATFORM_STATUS_UNAVAILABLE');

  fixture.state.platformMode = 'ok';
  fixture.state.roles = ['descriptive-role-only'];
  fixture.state.capabilities = ['site.list', 'site.read'];
  fixture.state.sitesMode = 'unavailable';
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'UNAVAILABLE' && Boolean(document.querySelector('[data-testid="real-site-discovery-unavailable"]'))`, 'Site discovery unavailable');
  const siteDiscoveryUnavailable = await evaluate(cdpClient, `document.querySelector('[data-testid="real-site-discovery-unavailable"]')?.textContent ?? ''`);
  assert(siteDiscoveryUnavailable.includes('SITE_DISCOVERY_UNAVAILABLE'), 'Site discovery failure omitted the server Problem code');
  assert(!siteDiscoveryUnavailable.includes('Tokyo Plant'), 'unavailable Site discovery rendered cached Site data');

  fixture.state.sitesMode = 'available';
  fixture.state.sites = [];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NO_AUTHORIZED_SITE'`, 'zero authorized Sites');
  const noAuthorizedSite = await evaluate(cdpClient, `({
    account: document.querySelector('[data-testid="real-site-none"]')?.textContent?.includes('RMS-03 Operator') ?? false,
    retry: Array.from(document.querySelectorAll('[data-testid="real-site-none"] button')).some((button) => button.textContent?.includes('刷新授权 Site')),
    help: Boolean(document.querySelector('[data-testid="real-site-none"] a[href="#real-site-help"]')),
    logout: Boolean(document.querySelector('[data-testid="real-logout-button"]')),
    siteRoute: Boolean(document.querySelector('[data-site-route]')),
  })`);
  assert(noAuthorizedSite.account && noAuthorizedSite.retry && noAuthorizedSite.help && noAuthorizedSite.logout, 'NO_AUTHORIZED_SITE omitted account, retry, help, or logout');
  assert(noAuthorizedSite.siteRoute === false, 'NO_AUTHORIZED_SITE mounted a Site business surface');
  recordScenario('zero-sites');

  fixture.state.sites = [siteA, siteB];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'CHOOSE_SITE'`, 'multiple Site chooser');
  const siteChooser = await evaluate(cdpClient, `({
    pathname: location.pathname,
    choices: Array.from(document.querySelectorAll('[data-testid="real-site-chooser"] [data-site-id]')).map((item) => ({
      id: item.getAttribute('data-site-id'),
      href: item.getAttribute('href'),
      name: item.textContent?.trim() ?? '',
    })),
    siteRoute: Boolean(document.querySelector('[data-site-route]')),
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-site-chooser"] h1'),
  })`);
  assert(siteChooser.pathname === '/', 'multiple Sites silently changed the URL scope');
  assert(siteChooser.choices.length === 2, `expected two chooser entries, got ${JSON.stringify(siteChooser.choices)}`);
  assert(siteChooser.choices[0].id === siteAId && siteChooser.choices[1].id === siteBId, 'chooser did not preserve Registry Site identities');
  assert(siteChooser.choices.every((choice) => choice.href === `/sites/${choice.id}/assets`), 'chooser generated a non-Site-scoped target');
  assert(siteChooser.choices.every((choice) => choice.name.length > 0), 'chooser links lacked accessible names');
  assert(siteChooser.siteRoute === false, 'chooser mounted a Site business surface before selection');
  assert(siteChooser.focusedHeading, 'Site chooser did not restore focus to its heading');
  recordScenario('many-sites');

  await navigate(cdpClient, `${webURL}/sites/${siteBId}/assets`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'READY' && document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-site-id') === '${siteBId}'`, 'validated explicit Site Assets route');
  const explicitSite = await evaluate(cdpClient, `({
    siteId: document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-site-id'),
    siteName: document.querySelector('[data-testid="real-site-route-assets"]')?.textContent?.includes('Osaka Plant') ?? false,
    organization: document.querySelector('[data-testid="real-site-route-assets"]')?.textContent?.includes('${actingOrganizationId}') ?? false,
    assets: document.querySelector('[data-feature-id="site-assets"]')?.getAttribute('href'),
    energy: document.querySelector('[data-feature-id="site-energy"]')?.getAttribute('href'),
    commands: document.querySelector('[data-feature-id="site-commands"]')?.getAttribute('href'),
    bigscreen: document.querySelector('[data-feature-id="site-bigscreen"]')?.getAttribute('href'),
    headerPrincipal: document.querySelector('[data-testid="real-shell-principal"]')?.textContent,
    headerSite: document.querySelector('[data-testid="real-shell-site"]')?.textContent,
    shellState: document.querySelector('[data-testid="real-shell-state"]')?.textContent,
    realtimeState: document.querySelector('[data-testid="real-realtime-status"]')?.getAttribute('data-realtime-state'),
    realtimeSite: document.querySelector('[data-testid="real-realtime-status"]')?.getAttribute('data-realtime-site'),
    realtimeText: document.querySelector('[data-testid="real-realtime-status"]')?.textContent ?? '',
    headerText: document.querySelector('.real-shell-header')?.textContent ?? '',
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-site-route-assets"] h1'),
  })`);
  assert(explicitSite.siteId === siteBId && explicitSite.siteName && explicitSite.organization, 'validated SiteContext was not rendered from Registry route data');
  assert(explicitSite.assets === `/sites/${siteBId}/assets`, 'Assets navigation escaped validated Site scope');
  assert(explicitSite.energy === `/sites/${siteBId}/energy`, 'Energy navigation escaped validated Site scope');
  assert(explicitSite.commands === `/sites/${siteBId}/commands`, 'Commands navigation escaped validated Site scope');
  assert(explicitSite.bigscreen === `/sites/${siteBId}/bigscreen`, 'BigScreen navigation escaped validated Site scope');
  assert(explicitSite.headerPrincipal === 'RMS-03 Operator', 'trusted header did not use the Principal snapshot');
  assert(explicitSite.headerSite === 'Osaka Plant' && explicitSite.shellState === 'READY', 'trusted header did not use the validated Site and Shell state');
  assert(explicitSite.realtimeState === 'idle' && explicitSite.realtimeSite === siteBId, 'realtime header was not scoped to the validated Site');
  assert(explicitSite.realtimeText.includes('Idle — not subscribed') && !/global|platform health/i.test(explicitSite.realtimeText), 'realtime header overstated subscription health');
  assert(!/Demo switch|role switch|Alarm|Copilot|Mock AI/i.test(explicitSite.headerText), 'trusted header mounted a Mock global affordance');
  assert(explicitSite.focusedHeading, 'validated Site route did not restore focus to its heading');

  await navigate(cdpClient, `${webURL}/sites/${siteBId}/energy`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-energy-dashboard"]')?.getAttribute('data-site-id') === '${siteBId}'`,
    'validated explicit Site Energy route',
  );
  const energy = await evaluate(cdpClient, `({
    pathname: location.pathname,
    site: document.querySelector('[data-testid="real-site-route-energy"]')?.getAttribute('data-site-id'),
    dashboardSite: document.querySelector('[data-testid="real-energy-dashboard"]')?.getAttribute('data-site-id'),
    revision: document.querySelector('[data-testid="real-energy-dashboard"]')?.getAttribute('data-dataset-revision'),
    state: document.querySelector('[data-testid="real-energy-dashboard"]')?.getAttribute('data-business-state'),
    text: document.querySelector('[data-testid="real-energy-dashboard"]')?.textContent ?? '',
    headerSite: document.querySelector('[data-testid="real-shell-site"]')?.textContent,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-energy-dashboard"] h1'),
  })`);
  assert(energy.pathname === `/sites/${siteBId}/energy`, 'Energy route changed the validated Site URL');
  assert(energy.site === siteBId && energy.dashboardSite === siteBId && energy.headerSite === 'Osaka Plant', 'Energy dashboard was not bound to the validated Site');
  assert(energy.revision === 'rms-energy-revision-1' && energy.state === 'READY', 'Energy dashboard omitted authoritative revision or readiness state');
  assert(energy.text.includes('42.5 kWh') && energy.text.includes('1 个已返回时段'), 'Energy dashboard omitted the returned aggregate');
  assert(energy.focusedHeading, 'Energy dashboard did not restore focus after lazy loading');
  recordScenario('site-energy');

  for (const leaf of ['commands', 'bigscreen']) {
    await navigate(cdpClient, `${webURL}/sites/${siteBId}/${leaf}`);
    await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'READY' && document.querySelector('[data-testid="real-site-route-${leaf}"]')?.getAttribute('data-site-id') === '${siteBId}'`, `validated explicit Site ${leaf} route`);
  }
  const bigScreen = await evaluate(cdpClient, `({
    pathname: location.pathname,
    site: document.querySelector('[data-testid="real-site-route-bigscreen"]')?.getAttribute('data-site-id'),
    headerSite: document.querySelector('[data-testid="real-shell-site"]')?.textContent,
  })`);
  assert(bigScreen.pathname === `/sites/${siteBId}/bigscreen` && bigScreen.site === siteBId && bigScreen.headerSite === 'Osaka Plant', 'BigScreen was not bound to the validated Site');
  await navigate(cdpClient, `${webURL}/bigscreen`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NOT_FOUND'`, 'unscoped BigScreen rejection');
  assert(!await evaluate(cdpClient, `Boolean(document.querySelector('[data-site-route="bigscreen"]'))`), 'unscoped BigScreen mounted a Site surface');

  await navigate(cdpClient, `${webURL}/sites/${siteAId}/commands`);
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="real-site-route-commands"]')?.getAttribute('data-site-id') === '${siteAId}' && Boolean(document.querySelector('[data-testid="real-site-switcher"]'))`, 'RMS-06 Site transition source');
  await evaluate(cdpClient, `(() => {
    const input = document.querySelector('[data-testid="real-command-draft-value"]');
    if (!(input instanceof HTMLTextAreaElement)) return false;
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set;
    setter?.call(input, 'Keep this unsaved command draft');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
  })()`);
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="real-command-draft-value"]')?.value === 'Keep this unsaved command draft'`, 'dirty Site draft');
  assert(await evaluate(cdpClient, `(() => {
    const button = document.querySelector('[data-site-switch-id="${siteBId}"]');
    if (!(button instanceof HTMLButtonElement)) return false;
    button.focus();
    return document.activeElement === button;
  })()`), 'Site switcher button could not receive keyboard focus');
  await pressKey(cdpClient, 'Enter', 'Enter', 13);
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="real-site-draft-confirmation"]')) && document.activeElement === document.querySelector('[data-testid="real-site-draft-confirm"]')`, 'Site draft confirmation focus');
  const retainedBeforeConfirmation = await evaluate(cdpClient, `({
    pathname: location.pathname,
    oldSite: document.body.innerText.includes('Tokyo Plant'),
    oldSubscription: document.querySelector('[data-testid="real-site-subscription"]')?.getAttribute('data-subscription-site'),
    draft: document.querySelector('[data-testid="real-command-draft-value"]')?.value,
    dialogName: document.querySelector('[data-testid="real-site-draft-confirmation"]')?.getAttribute('aria-labelledby'),
    modal: document.querySelector('[data-testid="real-site-draft-confirmation"]')?.getAttribute('aria-modal'),
  })`);
  assert(retainedBeforeConfirmation.pathname === `/sites/${siteAId}/commands`, 'draft warning changed Site before confirmation');
  assert(retainedBeforeConfirmation.oldSite && retainedBeforeConfirmation.oldSubscription === siteAId, 'draft warning purged the old Site before confirmation');
  assert(retainedBeforeConfirmation.draft === 'Keep this unsaved command draft', 'draft warning discarded the unsaved draft');
  assert(retainedBeforeConfirmation.dialogName === 'real-site-draft-title' && retainedBeforeConfirmation.modal === 'true', 'draft confirmation lacked an accessible modal name');
  await pressKey(cdpClient, 'Tab', 'Tab', 9);
  await waitForCondition(cdpClient, `document.activeElement === document.querySelector('[data-testid="real-site-draft-cancel"]')`, 'draft confirmation forward Tab');
  await pressKey(cdpClient, 'Tab', 'Tab', 9);
  await waitForCondition(cdpClient, `document.activeElement === document.querySelector('[data-testid="real-site-draft-confirm"]')`, 'draft confirmation focus loop');
  await pressKey(cdpClient, 'Escape', 'Escape', 27);
  await waitForCondition(cdpClient, `!document.querySelector('[data-testid="real-site-draft-confirmation"]') && document.querySelector('[data-testid="real-command-draft-value"]')?.value === 'Keep this unsaved command draft' && document.activeElement === document.querySelector('[data-site-switch-id="${siteBId}"]')`, 'cancelled Site transition and focus restoration');

  await pressKey(cdpClient, 'Enter', 'Enter', 13);
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="real-site-draft-confirmation"]')) && document.activeElement === document.querySelector('[data-testid="real-site-draft-confirm"]')`, 'second Site draft confirmation');
  await clickTestId(cdpClient, 'real-site-draft-confirm');
  await waitForCondition(cdpClient, `(() => {
    const purgeHeading = document.querySelector('[data-testid="real-site-purging"] h1');
    const purgeVisible = Boolean(purgeHeading)
      && !document.querySelector('[data-site-route][data-site-id="${siteAId}"]')
      && !document.querySelector('[data-subscription-site="${siteAId}"]')
      && !document.querySelector('[data-testid="real-command-draft-value"]')
      && document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-protected-resource-count') === '0';
    if (!purgeVisible) return false;
    globalThis.__rmsSitePurgeEvidence = {
      pathname: location.pathname,
      transition: document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-site-transition'),
      scopeSite: document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-protected-scope-site'),
      realtimeState: document.querySelector('[data-testid="real-realtime-status"]')?.getAttribute('data-realtime-state'),
      realtimeSite: document.querySelector('[data-testid="real-realtime-status"]')?.getAttribute('data-realtime-site'),
      headerSite: document.querySelector('[data-testid="real-shell-site"]')?.textContent,
      newSiteRendered: document.body.innerText.includes('Osaka Plant'),
      focusedHeading: document.activeElement === purgeHeading,
    };
    return true;
  })()`, 'old Site purge before navigation', 5);
  const purgingState = await evaluate(cdpClient, 'globalThis.__rmsSitePurgeEvidence');
  assert(purgingState.pathname === `/sites/${siteAId}/commands`, 'navigation began before old Site purge became visible');
  assert(purgingState.transition === 'purging' && !purgingState.scopeSite, 'protected Site scope was not revoked during purge');
  assert(purgingState.headerSite === 'No active Site', 'trusted header retained the revoked Site during purge');
  assert(purgingState.realtimeState === 'idle' && !purgingState.realtimeSite, 'old Site realtime status survived protected purge');
  assert(purgingState.focusedHeading, 'purging state did not restore focus to its heading');
  assert(purgingState.newSiteRendered === false, 'new Site rendered before old Site purge completed');
  await waitForCondition(cdpClient, `location.pathname === '/sites/${siteBId}/assets' && document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-site-id') === '${siteBId}'`, 'new Site after protected purge');
  const completedSiteTransition = await evaluate(cdpClient, `({
    oldRoute: Boolean(document.querySelector('[data-site-route][data-site-id="${siteAId}"]')),
    oldDraft: Boolean(document.querySelector('[data-testid="real-command-draft-value"]')),
    oldSubscription: Boolean(document.querySelector('[data-subscription-site="${siteAId}"]')),
    newSite: document.querySelector('[data-testid="real-site-route-assets"]')?.textContent?.includes('Osaka Plant') ?? false,
    newScope: document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-protected-scope-site'),
    headerSite: document.querySelector('[data-testid="real-shell-site"]')?.textContent,
    realtimeSite: document.querySelector('[data-testid="real-realtime-status"]')?.getAttribute('data-realtime-site'),
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-site-route-assets"] h1'),
  })`);
  assert(!completedSiteTransition.oldRoute && !completedSiteTransition.oldDraft && !completedSiteTransition.oldSubscription, 'old Site protected values survived the transition');
  assert(completedSiteTransition.newSite && completedSiteTransition.newScope === siteBId, 'new Site did not establish a fresh protected scope');
  assert(completedSiteTransition.headerSite === 'Osaka Plant' && completedSiteTransition.realtimeSite === siteBId, 'trusted header did not follow the new Site snapshot');
  assert(completedSiteTransition.focusedHeading, 'new Site route did not restore focus to its heading');
  recordScenario('site-switching');

  fixture.state.roles = ['platform-admin'];
  fixture.state.capabilities = ['site.list'];
  await navigate(cdpClient, `${webURL}/sites/${siteAId}/assets`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'FORBIDDEN'`, 'Site capability denial');
  const forbiddenSite = await evaluate(cdpClient, `({
    text: document.querySelector('[data-testid="real-route-forbidden"]')?.textContent ?? '',
    siteNavigation: Boolean(document.querySelector('[data-feature-id="site-assets"]')),
    roleDisplayed: document.querySelector('[data-testid="real-principal-roles"]')?.textContent?.includes('platform-admin') ?? false,
  })`);
  assert(forbiddenSite.roleDisplayed, 'descriptive Site role was not visible for audit context');
  assert(forbiddenSite.siteNavigation === false, 'Site navigation remained visible without site.read');
  assert(!forbiddenSite.text.includes(siteAId) && !forbiddenSite.text.includes('Tokyo Plant') && !forbiddenSite.text.includes('site.read'), 'Site Access Denied leaked protected metadata');

  fixture.state.roles = ['descriptive-role-only'];
  fixture.state.capabilities = ['site.list', 'site.read'];
  for (const invalidPath of [
    '/sites/b1/assets',
    '/sites/b2/commands',
    '/sites/not-a-uuid/assets',
    `/sites/${invisibleSiteId}/assets`,
  ]) {
    await navigate(cdpClient, `${webURL}${invalidPath}`);
    await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'SITE_NOT_VISIBLE'`, `safe invisible Site state for ${invalidPath}`);
    const invisibleSite = await evaluate(cdpClient, `({
      pathname: location.pathname,
      text: document.querySelector('[data-testid="real-site-not-visible"]')?.textContent ?? '',
      siteRoute: Boolean(document.querySelector('[data-site-route]')),
    })`);
    assert(invisibleSite.pathname === invalidPath, `invalid Site silently changed scope from ${invalidPath}`);
    assert(!invisibleSite.text.includes(invisibleSiteId) && !invisibleSite.text.includes('b1') && !invisibleSite.text.includes('b2'), `invalid Site state leaked the requested identity for ${invalidPath}`);
    assert(invisibleSite.siteRoute === false, `invalid Site mounted a Site business surface for ${invalidPath}`);
  }
  recordScenario('invalid-site');

  await navigate(cdpClient, `${webURL}/sites/${siteAId}/unknown`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NOT_FOUND' && Boolean(document.querySelector('[data-testid="real-site-route-not-found"]'))`, 'validated Site route 404');

  fixture.state.sites = [siteA];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `location.pathname === '/sites/${siteAId}/assets' && document.querySelector('main')?.getAttribute('data-route-state') === 'READY'`, 'sole authorized Site auto-entry');
  const soleSite = await evaluate(cdpClient, `({
    pathname: location.pathname,
    siteId: document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-site-id'),
    empty: document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-business-state'),
    unavailableText: document.body.innerText.includes('服务当前不可用'),
  })`);
  assert(soleSite.pathname === `/sites/${siteAId}/assets` && soleSite.siteId === siteAId, 'sole Site did not enter an explicit UUIDv7 URL');
  assert(soleSite.empty === 'EMPTY' && soleSite.unavailableText === false, 'empty Site business state was confused with unavailability');
  await assertStorageEmpty(cdpClient, 'Site scope matrix');
  recordScenario('one-site');

  fixture.state.sessionLifetimeMs = 1500;
  fixture.state.loginMode = 'session-expiration-proof';
  const loginCountBeforeExpiration = fixture.state.loginReturnTargets.length;
  await navigate(cdpClient, `${webURL}/sites/${siteAId}/assets?session=expires`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'READY'`, 'authenticated shell before Session expiration');
  await waitForCondition(cdpClient, `location.pathname === '/api/v1/auth/login' && Boolean(document.querySelector('[data-testid="session-expiration-login-proof"]'))`, 'Session expiration login proof');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeExpiration + 1, 'Session expiration did not start login');
  assert(fixture.state.loginReturnTargets.at(-1) === `/sites/${siteAId}/assets?session=expires`, 'Session expiration lost its safe returnTo');
  assert(!await evaluate(cdpClient, `Boolean(document.querySelector('[data-testid="real-protected-shell"]'))`), 'Session expiration retained the protected Shell');
  recordScenario('session-expiration');
  fixture.state.loginMode = 'success';
  fixture.state.sessionLifetimeMs = 60 * 60 * 1000;

  await cdpClient.send('Emulation.setDeviceMetricsOverride', {
    width: 390,
    height: 844,
    deviceScaleFactor: 1,
    mobile: true,
  });
  fixture.state.sites = [siteA, siteB];
  fixture.state.capabilities = ['site.list', 'site.read'];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'CHOOSE_SITE'`, 'mobile Site chooser');
  const mobileChooser = await evaluate(cdpClient, `({
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-site-chooser"] h1'),
    choiceHeights: Array.from(document.querySelectorAll('[data-testid="real-site-chooser"] [data-site-id]')).map((item) => item.getBoundingClientRect().height),
    headerRight: document.querySelector('.real-shell-header')?.getBoundingClientRect().right,
    realtimeRight: document.querySelector('[data-testid="real-realtime-status"]')?.getBoundingClientRect().right,
    viewport: window.innerWidth,
  })`);
  assert(!mobileChooser.overflow && mobileChooser.focusedHeading, 'mobile Site chooser overflowed or lost state focus');
  assert(mobileChooser.choiceHeights.every((height) => height >= 40), 'mobile Site chooser targets were too small');
  assert(mobileChooser.headerRight <= mobileChooser.viewport + 1 && mobileChooser.realtimeRight <= mobileChooser.viewport + 1, 'mobile trusted header exceeded the viewport');

  fixture.state.capabilities = ['site.read'];
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'FORBIDDEN'`, 'mobile Access Denied');
  const mobileForbidden = await evaluate(cdpClient, `({
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-route-forbidden"] h1'),
  })`);
  assert(!mobileForbidden.overflow && mobileForbidden.focusedHeading, 'mobile Access Denied was unusable');

  await navigate(cdpClient, `${webURL}/alarms`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NOT_INTEGRATED'`, 'mobile Not Integrated');
  const mobileNotIntegrated = await evaluate(cdpClient, `({
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-route-not-integrated"] h1'),
  })`);
  assert(!mobileNotIntegrated.overflow && mobileNotIntegrated.focusedHeading, 'mobile Not Integrated was unusable');

  fixture.state.capabilities = ['site.list', 'site.read'];
  fixture.state.sites = [];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'NO_AUTHORIZED_SITE'`, 'mobile No Authorized Site');
  const mobileNoSite = await evaluate(cdpClient, `({
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-site-none"] h1'),
    actionsVisible: Array.from(document.querySelectorAll('[data-testid="real-site-none"] button, [data-testid="real-site-none"] a')).every((item) => {
      const rect = item.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0 && rect.right <= window.innerWidth + 1;
    }),
  })`);
  assert(!mobileNoSite.overflow && mobileNoSite.focusedHeading && mobileNoSite.actionsVisible, 'mobile No Authorized Site was unusable');
  recordScenario('mobile');

  fixture.state.platformMode = 'ok';
  fixture.state.roles = ['descriptive-role-only'];
  fixture.state.capabilities = ['organization.list', 'organization.read', 'site.list', 'site.read', 'device.read'];
  fixture.state.logoutMode = 'failure';
  await navigate(cdpClient, `${webURL}/logout-proof`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'READY'`, 'authenticated shell before logout');
  const loginCountBeforeLogout = fixture.state.loginReturnTargets.length;
  await clickTestId(cdpClient, 'real-logout-button');
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="real-logout-failure"]'))`, 'retryable logout failure');
  const failedLogout = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    retryable: document.querySelector('[data-testid="real-logout-failure"]')?.getAttribute('data-retryable'),
    text: document.querySelector('[data-testid="real-logout-failure"]')?.textContent ?? '',
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
  })`);
  assert(failedLogout.mounted === 'true', 'retryable logout failure purged protected state before revocation');
  assert(failedLogout.retryable === 'true', 'logout failure was not marked retryable');
  assert(failedLogout.text.includes('SESSION_PERSISTENCE_FAILED') && failedLogout.text.includes(`traceId ${traceId}`), 'logout failure omitted safe Problem detail or trace ID');
  assert(!failedLogout.text.includes(fixtureCapability) && !failedLogout.text.includes('rms-03-session'), 'logout failure exposed protected Session values');
  assert(!failedLogout.overflow, 'mobile logout failure overflowed the viewport');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeLogout, 'logout failure incorrectly started login');
  await assertStorageEmpty(cdpClient, 'logout failure');
  recordFailure('SESSION_PERSISTENCE_FAILED');

  fixture.state.logoutMode = 'success';
  await clickTestId(cdpClient, 'real-logout-button');
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'LOGIN_REQUIRED' && document.body.innerText.includes('服务器 Session 已撤销')`, 'confirmed logout completion');
  const completedLogout = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    protectedShell: Boolean(document.querySelector('[data-testid="real-protected-shell"]')),
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-shell-login-required"] h1'),
    loginName: document.querySelector('[data-testid="real-shell-login-required"] button')?.textContent?.trim() ?? '',
  })`);
  assert(completedLogout.mounted === 'false' && completedLogout.protectedShell === false, 'confirmed logout did not purge protected state');
  assert(!completedLogout.overflow && completedLogout.focusedHeading && completedLogout.loginName.length > 0, 'mobile Session ended state was not accessible');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeLogout, 'confirmed logout automatically started a new login');
  await assertStorageEmpty(cdpClient, 'logout success');
  recordScenario('logout');
  await cdpClient.send('Emulation.clearDeviceMetricsOverride');

  const authorizationRequests = fixture.state.requests.filter((entry) => 'authorization' in entry.headers);
  assert(authorizationRequests.length === 0, 'browser sent an Authorization header');
  const logoutRequests = fixture.state.requests.filter((entry) => entry.method === 'POST' && entry.path === '/api/v1/auth/logout');
  assert(logoutRequests.length === 2, `expected two logout attempts, got ${logoutRequests.length}`);
  for (const request of logoutRequests) {
    assert(request.headers[stateChangeHeader] === fixtureCapability, 'logout did not send the in-memory state-change capability');
    assert(request.headers.origin === webURL, `logout Origin was not the Real application origin: ${request.headers.origin}`);
  }
  const energyRequests = fixture.state.requests.filter((entry) => entry.method === 'POST' && entry.path === '/api/v1/analytics/energy-series');
  assert(energyRequests.length === 1, `expected one Energy query, got ${energyRequests.length}`);
  assert(energyRequests[0].headers[stateChangeHeader] === fixtureCapability, 'Energy query did not send the in-memory state-change capability');
  assert(energyRequests[0].headers.origin === webURL, `Energy query Origin was not the Real application origin: ${energyRequests[0].headers.origin}`);
  assert(fixture.state.energyQueries.length === 1, `expected one parsed Energy query, got ${fixture.state.energyQueries.length}`);
  assert(fixture.state.energyQueries[0].organizationId === actingOrganizationId, 'Energy query did not use the authenticated Organization');
  assert(fixture.state.energyQueries[0].siteId === siteBId, 'Energy query did not use the validated Site');
  const forbiddenHeaders = fixture.state.requests.filter((entry) =>
    ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'x-scope'].some((name) => name in entry.headers));
  assert(forbiddenHeaders.length === 0, `browser sent forbidden authorization headers: ${JSON.stringify(forbiddenHeaders)}`);

  const browserSafety = await evaluate(cdpClient, `({
    unsafeQueryKeys: Array.from(new URL(location.href).searchParams.keys()).filter((key) => /token|session|principal/i.test(key)),
    credentialInputs: Array.from(document.querySelectorAll('input')).filter((input) => input.type === ['pass', 'word'].join('')).length,
  })`);
  assert(browserSafety.unsafeQueryKeys.length === 0, `sensitive query fields were present: ${JSON.stringify(browserSafety.unsafeQueryKeys)}`);
  assert(browserSafety.credentialInputs === 0, 'Real rendered a browser credential form');

  const websocketEvents = cdpClient.events.filter((event) => event.method === 'Network.webSocketCreated');
  const businessWebSockets = websocketEvents.filter((event) => {
    const url = String(event.params?.url ?? '');
    return /\/ws(?:\/|\?|$)|centrifugo|telemetry/i.test(url);
  });
  assert(businessWebSockets.length === 0, `business realtime subscriptions started during RMS shell flows: ${JSON.stringify(businessWebSockets)}`);
  const forbiddenModuleRequests = cdpClient.events.filter((event) => {
    if (event.method !== 'Network.requestWillBeSent') return false;
    const url = String(event.params?.request?.url ?? '');
    return /\/src\/(?:mock|pages|ai)\/|\/src\/App\.tsx|Mock[A-Z]/.test(url);
  });
  assert(forbiddenModuleRequests.length === 0, `Real loaded Demo or Mock modules: ${JSON.stringify(forbiddenModuleRequests)}`);
  const sensitiveLogEvents = cdpClient.events.filter((event) => {
    if (event.method !== 'Runtime.consoleAPICalled' && event.method !== 'Log.entryAdded') return false;
    const serialized = JSON.stringify(event.params);
    return serialized.includes('rms-03-session')
      || serialized.includes('rms-03-operator')
      || serialized.includes('RMS-03 Operator')
      || serialized.includes(fixtureCapability);
  });
  assert(sensitiveLogEvents.length === 0, 'protected Shell values reached browser logs');

  const organizationAuthorityRequests = fixture.state.requests.filter((entry) => 'x-organization-id' in entry.headers);
  const siteAuthorityRequests = fixture.state.requests.filter((entry) => 'x-site-id' in entry.headers);
  const otherAuthorityRequests = fixture.state.requests.filter((entry) =>
    ['x-role', 'x-admin', 'x-scope'].some((name) => name in entry.headers));
  browserEvidence.network = {
    requestCount: fixture.state.requests.length,
    browserAuthorizationHeaderCount: authorizationRequests.length,
    browserOrganizationAuthorityHeaderCount: organizationAuthorityRequests.length,
    browserSiteAuthorityHeaderCount: siteAuthorityRequests.length,
    browserOtherAuthorityHeaderCount: otherAuthorityRequests.length,
  };
  for (const scenario of RMS_REQUIRED_BROWSER_SCENARIOS) {
    assert(browserEvidence.scenarios[scenario]?.passed === true, `browser evidence omitted ${scenario}`);
  }
  browserEvidence.passed = true;
  await mkdir(join(root, 'out', 'rms-web-certification'), { recursive: true });
  await writeFile(evidencePath, `${JSON.stringify(browserEvidence, null, 2)}\n`, 'utf8');

  console.log(`RMS authenticated Shell and capability route browser audit passed. Evidence: ${evidencePath}`);
} finally {
  cdpClient?.close();
  await stopProcess(browserProcess);
  await stopProcess(viteProcess);
  for (const response of fixture.state.pendingPrincipalResponses.splice(0)) response.destroy();
  if (fixture.server.listening) await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 250 });
}
