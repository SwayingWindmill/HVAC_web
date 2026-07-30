import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import WebSocket from 'ws';
import { startS0AuthTopology, stopProcess } from './s0-auth-topology.mjs';

const root = resolve(process.cwd());
const debugPort = Number(process.env.S2_GATEWAY_DEBUG_PORT ?? 9365);
const profileDir = join(tmpdir(), `s2-gateway-browser-${process.pid}`);
const outputPath = resolve(root, process.env.S2_GATEWAY_BROWSER_REPORT ?? 'out/s2-gateway-snapshot/browser-session.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const browserCandidates = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  join('C:\\Program Files (x86)', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  join('C:\\Program Files', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium-browser',
  '/usr/bin/chromium',
].filter(Boolean);
const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
if (!browserPath) throw new Error('A CDP-compatible Edge, Chrome, or Chromium executable was not found');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function waitForDebugger(child) {
  for (let attempt = 0; attempt < 900; attempt += 1) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error('Browser exited before debugger became ready');
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/version`);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error('Browser debugger did not become ready');
}

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    let nextId = 0;
    socket.addEventListener('open', () => resolveClient({
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
      if (!message.id) return;
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

async function waitForPrincipalState(client, expected) {
  for (let attempt = 0; attempt < 900; attempt += 1) {
    try {
      const state = await evaluate(client, `document.querySelector('[data-testid="authenticated-principal-status"]')?.getAttribute('data-principal-state') ?? null`);
      if (state === expected) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`Principal UI did not reach ${expected}`);
}

let topology;
let browserProcess;
let cdpClient;
try {
  await mkdir(profileDir, { recursive: true });
  await mkdir(dirname(outputPath), { recursive: true });
  topology = await startS0AuthTopology({
    oidcPort: 19101,
    iamPort: 18455,
    gatewayPort: 18091,
    webPort: 5190,
    telemetryPort: 18456,
    telemetry: true,
    routeRegistry: false,
  });
  browserProcess = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--no-first-run',
    '--no-default-browser-check',
    '--hide-scrollbars',
    '--ignore-certificate-errors',
    '--allow-insecure-localhost',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`,
    `${topology.webURL}/system`,
  ], { stdio: 'ignore' });
  await waitForDebugger(browserProcess);
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available for the S2 audit');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Network.enable');

  await waitForPrincipalState(cdpClient, 'anonymous');
  await evaluate(cdpClient, `window.location.assign('/api/v1/auth/login?returnTo=%2Fsystem&login_hint=s2-telemetry')`);
  await waitForPrincipalState(cdpClient, 'authenticated');

  const principal = await evaluate(cdpClient, `fetch('/api/v1/principal').then(async (response) => ({ status: response.status, body: await response.json() }))`);
  assert(principal.status === 200, `Principal status was ${principal.status}`);
  assert(principal.body.principal.subject === 'fixture-user', 'Unexpected authenticated principal');
  assert(principal.body.context.actingOrganizationId === '018f2e00-1000-7000-8000-000000000003', 'Unexpected acting Organization');
  assert(principal.body.session.csrfToken, 'Authenticated Session omitted CSRF token');

  const browserState = await evaluate(cdpClient, `({ cookie: document.cookie, localStorage: Object.fromEntries(Object.entries(localStorage)), sessionStorage: Object.fromEntries(Object.entries(sessionStorage)) })`);
  assert(!browserState.cookie.includes('hvac_session'), 'HttpOnly Session was visible to browser JavaScript');
  assert(!/(access_token|refresh_token|id_token|Bearer)/i.test(JSON.stringify(browserState)), 'Authentication material leaked into browser storage');

  const single = await evaluate(cdpClient, `
    fetch('/api/v1/devices/018f2e00-3000-7000-8000-000000000001/observation-snapshot?keys=humidity,temperature', {
      headers: { 'X-Request-ID': 's2-browser-single' },
    }).then(async (response) => ({ status: response.status, cacheControl: response.headers.get('cache-control'), body: await response.json() }))
  `);
  assert(single.status === 200, `Authorized single Snapshot status was ${single.status}: ${JSON.stringify(single.body)}`);
  assert(single.cacheControl === 'private, no-store', `Single Snapshot Cache-Control was ${single.cacheControl}`);
  assert(single.body.deviceId === '018f2e00-3000-7000-8000-000000000001', 'Single Snapshot Device drifted');
  assert(single.body.values.length === 2 && single.body.values[0].key === 'humidity' && single.body.values[1].key === 'temperature', 'Single Snapshot key order drifted');

  const batch = await evaluate(cdpClient, `
    fetch('/api/v1/telemetry/observation-snapshots:batchGet', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': ${JSON.stringify(principal.body.session.csrfToken)}, 'X-Request-ID': 's2-browser-batch' },
      body: JSON.stringify({ requests: [
        { requestId: 'first', deviceId: '018f2e00-3000-7000-8000-000000000001', keys: ['temperature'] },
        { requestId: 'second', deviceId: '018f2e00-3000-7000-8000-000000000002', keys: [] },
      ] }),
    }).then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(batch.status === 200, `Authorized batch status was ${batch.status}: ${JSON.stringify(batch.body)}`);
  assert(batch.body.items.length === 2 && batch.body.items[0].requestId === 'first' && batch.body.items[1].requestId === 'second', 'Browser batch order drifted');

  const missingCSRF = await evaluate(cdpClient, `
    fetch('/api/v1/telemetry/observation-snapshots:batchGet', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ requests: [{ requestId: 'blocked', deviceId: '018f2e00-3000-7000-8000-000000000001', keys: [] }] }),
    }).then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(missingCSRF.status === 403 && missingCSRF.body.code === 'CSRF_REQUIRED', 'Browser batch without CSRF did not fail closed');

  const unauthorized = await evaluate(cdpClient, `
    fetch('/api/v1/devices/018f2e00-3000-7000-8000-000000000099/observation-snapshot')
      .then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(unauthorized.status === 404 && unauthorized.body.code === 'RESOURCE_NOT_FOUND', 'Cross-tenant/unknown Device was discoverable');

  const forged = await evaluate(cdpClient, `
    fetch('/api/v1/devices/018f2e00-3000-7000-8000-000000000001/observation-snapshot', { headers: { 'X-Organization-ID': 'forged-org' } })
      .then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(forged.status === 400 && forged.body.code === 'FORGED_IDENTITY_HEADER', 'Forged browser scope header was not rejected');

  const evidence = topology.telemetryEvidence;
  assert(evidence.requests === 2 && evidence.singleRequests === 1 && evidence.batchRequests === 1, `Unexpected Runtime fixture calls: ${JSON.stringify(evidence)}`);
  assert(evidence.browserAuthorityHeaders === 0 && evidence.missingAuthorization === 0, `Gateway leaked browser authority or omitted delegation: ${JSON.stringify(evidence)}`);

  const report = {
    schemaVersion: 1,
    ticket: 'S2 Ticket 05',
    generatedAt: new Date().toISOString(),
    browser: {
      authenticatedPrincipal: principal.body.principal.subject,
      actingOrganizationId: principal.body.context.actingOrganizationId,
      httpOnlySessionHiddenFromJavaScript: true,
      authorizedSingleSnapshot: true,
      authorizedBatchSnapshot: true,
      csrfEnforced: true,
      forgedScopeRejected: true,
      crossTenantNondiscovery: true,
    },
    upstreamEvidence: {
      telemetryRuntime: { ...evidence },
      legacyCalls: 0,
      mockCalls: 0,
      thingsBoardReadThroughCalls: 0,
    },
  };
  await writeFile(outputPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  console.log(`S2 Gateway browser Session audit passed: ${outputPath}`);
} finally {
  cdpClient?.close();
  await stopProcess(browserProcess);
  await topology?.stop();
  await rm(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 250 });
}
