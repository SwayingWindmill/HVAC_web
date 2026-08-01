import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createTCPServer } from 'node:net';
import { createRequire } from 'node:module';
import { rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const requireFromScript = createRequire(import.meta.url);
const vitePackagePath = requireFromScript.resolve('vite/package.json');
const viteBinPath = resolve(vitePackagePath, '../bin/vite.js');
const profileDir = join(tmpdir(), `rms-02-principal-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const instant = '2099-07-28T03:00:00.000Z';
const actingOrganizationId = '01900000-0000-7000-8000-000000000001';
const policyRevision = 'rms-policy:7';
const capabilities = ['organization.list', 'site.read', 'device.read', 'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read'];

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

function json(response, status, payload) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'no-store',
    'x-request-id': `rms-02-${Date.now()}`,
    traceparent: '00-0123456789abcdef0123456789abcdef-0123456789abcdef-01',
  });
  response.end(JSON.stringify(payload));
}

function problem(path) {
  return {
    type: 'https://errors.hvac.local/resource-not-found',
    title: 'Resource not found',
    status: 404,
    detail: 'The requested audit fixture resource was not found.',
    instance: path,
    code: 'RESOURCE_NOT_FOUND',
    traceId: '0123456789abcdef0123456789abcdef',
    retryable: false,
  };
}

function principalResponse() {
  const principal = {
    subject: 'rms-02-browser',
    issuer: 'https://identity.hvac.local',
    displayName: 'RMS-02 Browser',
    email: ['rms-02-browser', 'example.invalid'].join('@'),
    roles: ['descriptive-role-only'],
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
      policyRevision: 'gateway-delegation:4',
      delegationExpiresAt: instant,
    },
    authorization: {
      capabilitySetVersion: 4,
      policyRevision,
      capabilities,
    },
    session: {
      id: 'rms-02-session',
      expiresAt: instant,
      csrfToken: ['rms', '02', 'csrf', String(process.pid)].join('-'),
      revocationObjectiveMs: 1000,
      lastAuditMessageId: 'rms-02-audit',
    },
  };
}

function createGatewayFixture() {
  const requests = [];
  const server = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    requests.push({ method: request.method, path: url.pathname });

    if (url.pathname === '/api/v1/principal' && request.method === 'GET') {
      json(response, 200, principalResponse());
      return;
    }
    if (url.pathname === '/api/v1/health' && request.method === 'GET') {
      json(response, 200, { status: 'ok', service: 'platform-gateway', checkedAt: instant });
      return;
    }
    if (url.pathname === '/api/v1/version' && request.method === 'GET') {
      json(response, 200, { service: 'platform-gateway', version: 'rms-02', commit: 'local', builtAt: instant });
      return;
    }
    if (url.pathname === '/api/v1/platform/status' && request.method === 'GET') {
      json(response, 200, {
        status: 'ok', service: 'platform-status', implementation: 'go', version: 'rms-02', checkedAt: instant,
        routePolicyRevision: 1, routeRevision: 1, compatibilityMode: 'native',
      });
      return;
    }
    if (url.pathname === '/api/v1/audit/session-events/rms-02-audit' && request.method === 'GET') {
      json(response, 200, {
        ledgerSequence: 1,
        messageId: 'rms-02-audit',
        schemaVersion: 1,
        organizationId: actingOrganizationId,
        aggregateType: 'bff-session',
        aggregateId: 'rms-02-session',
        aggregateVersion: 1,
        occurredAt: instant,
        initiatingSubject: 'rms-02-browser',
        initiatingIssuer: 'https://identity.hvac.local',
        executingService: 'platform-gateway',
        executingSpiffeId: 'spiffe://hvac.local/platform-gateway',
        actingOrganizationId,
        action: 'SESSION_CREATED',
        result: 'SUCCEEDED',
        policyRevision,
        correlationId: 'rms-02-correlation',
        causationId: 'rms-02-causation',
        traceId: '0123456789abcdef0123456789abcdef',
        payloadSha256: '0'.repeat(64),
        previousRecordHash: '1'.repeat(64),
        recordHash: '2'.repeat(64),
        recordedAt: instant,
      });
      return;
    }
    json(response, 404, problem(url.pathname));
  });
  return { server, requests };
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

async function waitForCondition(client, expression, label, attempts = 300) {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      const value = await evaluate(client, expression);
      if (value) return value;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({
    url: location.href,
    readyState: document.readyState,
    text: document.body?.innerText?.slice(0, 4000) ?? '',
    html: document.body?.innerHTML?.slice(0, 4000) ?? '',
    rootHtml: document.getElementById('root')?.innerHTML?.slice(0, 4000) ?? null,
  })`);
  diagnostic.events = client.events.slice(-30);
  throw new Error(`${label} did not become ready: ${JSON.stringify(diagnostic)}`);
}

async function navigate(client, url) {
  const result = await client.send('Page.navigate', { url });
  if (result.errorText) throw new Error(`browser navigation failed for ${url}: ${result.errorText}`);
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
    viteBinPath,
    'apps/hvac-web',
    '--config', 'apps/hvac-web/vite.real.config.ts',
    '--host', '127.0.0.1',
    '--port', String(webPort),
    '--strictPort',
  ], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      HVAC_WEB_BUILD_ID: 'rms-02-browser',
      HVAC_WEB_GATEWAY_BASE_PATH: '/api/v1',
      HVAC_WEB_REALTIME_PROTOCOL: 'centrifugo-v1',
      HVAC_WEB_AUDIT_DISABLE_HMR: 'true',
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayURL,
    },
  });
  await waitForHTTP(`${webURL}/system?tab=overview`, viteProcess, 'Vite RMS-02 server');

  browserProcess = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--disable-extensions',
    '--disable-component-extensions-with-background-pages',
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

  const principalReady = `document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-policy-revision') === '${policyRevision}' && document.querySelector('[data-testid="real-shell-principal"]')?.textContent?.includes('RMS-02 Browser')`;
  await navigate(cdpClient, `${webURL}/system?tab=overview`);
  try {
    await waitForCondition(cdpClient, principalReady, 'authenticated Principal diagnostic', 100);
  } catch (firstError) {
    const documentState = await evaluate(cdpClient, `({
      text: document.body?.innerText ?? '',
      rootHtml: document.getElementById('root')?.innerHTML ?? null,
    })`);
    const applicationRootBlank = documentState.text.trim() === '' && documentState.rootHtml === '';
    if (!applicationRootBlank) throw firstError;
    await cdpClient.send('Page.reload', { ignoreCache: true });
    try {
      await waitForCondition(cdpClient, principalReady, 'authenticated Principal diagnostic after cold-start reload');
    } catch (secondError) {
      const firstMessage = firstError instanceof Error ? firstError.message : String(firstError);
      const secondMessage = secondError instanceof Error ? secondError.message : String(secondError);
      throw new Error(`Principal UI remained blank after one cold-start reload. First failure: ${firstMessage}. Second failure: ${secondMessage}`);
    }
  }
  const diagnostic = await evaluate(cdpClient, `(() => {
    const shell = document.querySelector('[data-testid="real-protected-shell"]');
    return {
      policyRevision: shell?.getAttribute('data-policy-revision') ?? null,
      capabilityCount: shell?.getAttribute('data-capability-count') ?? null,
      principal: document.querySelector('[data-testid="real-shell-principal"]')?.textContent ?? '',
      roles: document.querySelector('[data-testid="real-principal-roles"]')?.textContent ?? '',
      routeState: document.querySelector('[data-testid="real-route-forbidden"]')?.getAttribute('data-route-state') ?? null,
      text: document.body?.innerText ?? '',
    };
  })()`);
  assert(diagnostic.policyRevision === policyRevision, `policy revision was not rendered: ${JSON.stringify(diagnostic)}`);
  assert(diagnostic.capabilityCount === String(capabilities.length), `capability count was not rendered: ${JSON.stringify(diagnostic)}`);
  assert(diagnostic.principal.includes('RMS-02 Browser'), `Principal identity was not rendered: ${JSON.stringify(diagnostic)}`);
  assert(diagnostic.roles.includes('descriptive-role-only'), 'descriptive role context was not rendered');
  assert(diagnostic.routeState === 'FORBIDDEN', `missing organization.read did not fail closed: ${JSON.stringify(diagnostic)}`);
  assert(!diagnostic.text.includes('organization.read'), 'the UI invented a capability from the descriptive role');
  assert(fixture.requests.some((entry) => entry.method === 'GET' && entry.path === '/api/v1/principal'), 'the browser did not read the current Principal resource');

  console.log('RMS-02 Principal capability browser audit passed.');
} finally {
  cdpClient?.close();
  await stopProcess(browserProcess);
  await stopProcess(viteProcess);
  if (fixture.server.listening) await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 250 });
}
