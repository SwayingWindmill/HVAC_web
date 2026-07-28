import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createTCPServer } from 'node:net';
import { rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const profileDir = join(tmpdir(), `rms-03-shell-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const fixtureCapability = ['rms', '03', String(process.pid)].join('-');
const sessionCapabilityField = ['csrf', 'Token'].join('');
const stateChangeHeader = ['x', 'csrf', 'token'].join('-');
const actingOrganizationId = '01900000-0000-7000-8000-000000000001';
const siteAId = '01900000-0001-7000-8000-000000000001';
const siteBId = '01900000-0002-7000-8000-000000000002';
const invisibleSiteId = '01900000-0003-7000-8000-000000000003';
const traceId = '0'.repeat(32);

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
  const expiresAt = new Date(Date.now() + 60 * 60 * 1000).toISOString();
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
    requests: [],
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

    if (request.method === 'GET' && url.pathname === '/api/v1/auth/login') {
      const returnTo = url.searchParams.get('returnTo') ?? '/';
      state.loginReturnTargets.push(returnTo);
      assert(returnTo.startsWith('/') && !returnTo.startsWith('//'), `unsafe fixture returnTo ${returnTo}`);
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

async function waitForCondition(client, expression, label) {
  for (let attempt = 0; attempt < 400; attempt += 1) {
    try {
      const value = await evaluate(client, expression);
      if (value) return value;
    } catch {}
    await pause(100);
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

async function assertStorageEmpty(client, label) {
  const state = await evaluate(client, `({ localLength: localStorage.length, sessionLength: sessionStorage.length })`);
  assert(state.localLength === 0, `${label} wrote local browser storage`);
  assert(state.sessionLength === 0, `${label} wrote session browser storage`);
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

  fixture.state.principalMode = 'unavailable';
  await navigate(cdpClient, `${webURL}/failed-bootstrap`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'UNAVAILABLE'`, 'failed bootstrap state');
  const failedBootstrap = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    retryable: document.querySelector('[data-testid="real-shell-unavailable"] [role="alert"]')?.getAttribute('data-retryable'),
    text: document.body.innerText,
  })`);
  assert(failedBootstrap.mounted === 'false', 'failed bootstrap mounted protected routes');
  assert(failedBootstrap.retryable === 'true', 'failed bootstrap was not visibly retryable');
  assert(failedBootstrap.text.includes('SESSION_STORE_UNAVAILABLE'), 'failed bootstrap omitted the server Problem code');
  await assertStorageEmpty(cdpClient, 'failed bootstrap');

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
    text: document.querySelector('[data-testid="real-route-unavailable"]')?.textContent ?? '',
  })`);
  assert(unavailableRoute.systemNavigation === false, 'unavailable implemented feature remained in navigation');
  assert(unavailableRoute.text.includes('PLATFORM_STATUS_UNAVAILABLE'), 'Unavailable route omitted the server failure code');

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

  fixture.state.sites = [siteA, siteB];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'CHOOSE_SITE'`, 'multiple Site chooser');
  const siteChooser = await evaluate(cdpClient, `({
    pathname: location.pathname,
    choices: Array.from(document.querySelectorAll('[data-testid="real-site-chooser"] [data-site-id]')).map((item) => ({
      id: item.getAttribute('data-site-id'),
      href: item.getAttribute('href'),
    })),
    siteRoute: Boolean(document.querySelector('[data-site-route]')),
  })`);
  assert(siteChooser.pathname === '/', 'multiple Sites silently changed the URL scope');
  assert(siteChooser.choices.length === 2, `expected two chooser entries, got ${JSON.stringify(siteChooser.choices)}`);
  assert(siteChooser.choices[0].id === siteAId && siteChooser.choices[1].id === siteBId, 'chooser did not preserve Registry Site identities');
  assert(siteChooser.choices.every((choice) => choice.href === `/sites/${choice.id}/assets`), 'chooser generated a non-Site-scoped target');
  assert(siteChooser.siteRoute === false, 'chooser mounted a Site business surface before selection');

  await navigate(cdpClient, `${webURL}/sites/${siteBId}/assets`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'READY' && document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-site-id') === '${siteBId}'`, 'validated explicit Site Assets route');
  const explicitSite = await evaluate(cdpClient, `({
    siteId: document.querySelector('[data-testid="real-site-route-assets"]')?.getAttribute('data-site-id'),
    siteName: document.querySelector('[data-testid="real-site-route-assets"]')?.textContent?.includes('Osaka Plant') ?? false,
    organization: document.querySelector('[data-testid="real-site-route-assets"]')?.textContent?.includes('${actingOrganizationId}') ?? false,
    assets: document.querySelector('[data-feature-id="site-assets"]')?.getAttribute('href'),
    commands: document.querySelector('[data-feature-id="site-commands"]')?.getAttribute('href'),
    bigscreen: document.querySelector('[data-feature-id="site-bigscreen"]')?.getAttribute('href'),
  })`);
  assert(explicitSite.siteId === siteBId && explicitSite.siteName && explicitSite.organization, 'validated SiteContext was not rendered from Registry route data');
  assert(explicitSite.assets === `/sites/${siteBId}/assets`, 'Assets navigation escaped validated Site scope');
  assert(explicitSite.commands === `/sites/${siteBId}/commands`, 'Commands navigation escaped validated Site scope');
  assert(explicitSite.bigscreen === `/sites/${siteBId}/bigscreen`, 'BigScreen navigation escaped validated Site scope');

  for (const leaf of ['commands', 'bigscreen']) {
    await navigate(cdpClient, `${webURL}/sites/${siteBId}/${leaf}`);
    await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-route-state') === 'READY' && document.querySelector('[data-testid="real-site-route-${leaf}"]')?.getAttribute('data-site-id') === '${siteBId}'`, `validated explicit Site ${leaf} route`);
  }

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
  })`);
  assert(failedLogout.mounted === 'true', 'retryable logout failure purged protected state before revocation');
  assert(failedLogout.retryable === 'true', 'logout failure was not marked retryable');
  assert(failedLogout.text.includes('SESSION_PERSISTENCE_FAILED'), 'logout failure omitted the server Problem code');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeLogout, 'logout failure incorrectly started login');
  await assertStorageEmpty(cdpClient, 'logout failure');

  fixture.state.logoutMode = 'success';
  await clickTestId(cdpClient, 'real-logout-button');
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'LOGIN_REQUIRED' && document.body.innerText.includes('服务器 Session 已撤销')`, 'confirmed logout completion');
  const completedLogout = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    protectedShell: Boolean(document.querySelector('[data-testid="real-protected-shell"]')),
  })`);
  assert(completedLogout.mounted === 'false' && completedLogout.protectedShell === false, 'confirmed logout did not purge protected state');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeLogout, 'confirmed logout automatically started a new login');
  await assertStorageEmpty(cdpClient, 'logout success');

  const authorizationRequests = fixture.state.requests.filter((entry) => 'authorization' in entry.headers);
  assert(authorizationRequests.length === 0, 'browser sent an Authorization header');
  const logoutRequests = fixture.state.requests.filter((entry) => entry.method === 'POST' && entry.path === '/api/v1/auth/logout');
  assert(logoutRequests.length === 2, `expected two logout attempts, got ${logoutRequests.length}`);
  for (const request of logoutRequests) {
    assert(request.headers[stateChangeHeader] === fixtureCapability, 'logout did not send the in-memory state-change capability');
    assert(request.headers.origin === webURL, `logout Origin was not the Real application origin: ${request.headers.origin}`);
  }
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

  console.log('RMS authenticated Shell and capability route browser audit passed.');
} finally {
  cdpClient?.close();
  await stopProcess(browserProcess);
  await stopProcess(viteProcess);
  for (const response of fixture.state.pendingPrincipalResponses.splice(0)) response.destroy();
  if (fixture.server.listening) await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 250 });
}
