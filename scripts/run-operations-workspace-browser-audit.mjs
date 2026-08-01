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
const fixtureRoot = resolve(root, 'scripts/fixtures/operations-reconnect');
const outputRoot = resolve(root, 'out/operations-reconnect-certification');
const profileDir = join(tmpdir(), `operations-reconnect-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const organizationId = '01910000-0000-7000-8000-000000000001';
const siteId = '01910000-0001-7000-8000-000000000001';
const investigationId = 'investigation-browser-001';
const hiddenInvestigationId = 'hidden-investigation';

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

function event(id, type, payload) {
  return `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify({ type, ...payload })}\n\n`;
}

const plan = {
  schemaVersion: 1,
  id: 'site-night-energy-investigation',
  label: 'Site night-energy investigation',
  completedSteps: 4,
  totalSteps: 4,
  progressPercent: 100,
  steps: [
    { id: 'READ_SITE_CONTEXT', label: 'Read authoritative Site context', status: 'COMPLETED' },
    { id: 'READ_ENERGY_SERIES', label: 'Read authoritative night-energy periods', status: 'COMPLETED' },
    { id: 'ANALYZE', label: 'Run deterministic night-energy analysis', status: 'COMPLETED' },
    { id: 'COMMIT_RESULT', label: 'Commit Evidence, Analysis and Finding', status: 'COMPLETED' },
  ],
};
const stableActivity = {
  recordId: 'receipt-stable',
  logicalTool: 'registry.getSite',
  owner: 'registry',
  resultCategory: 'SUCCEEDED',
  startedAt: 1,
  completedAt: 2,
};
const finalActivity = {
  recordId: 'receipt-final',
  logicalTool: 'analytics.getEnergySeries',
  owner: 'telemetry-query-service',
  resultCategory: 'SUCCEEDED',
  startedAt: 3,
  completedAt: 4,
};

function investigation(revision, status) {
  return {
    schemaVersion: 1,
    id: investigationId,
    scope: { organizationId, siteId, equipmentId: null, deviceId: null },
    status,
    revision,
    createdAt: 1,
    activeRun: status === 'RUNNING' ? { id: 'active-run', status: 'ACTIVE', startedAt: 1 } : null,
    outcome: status === 'COMPLETED' ? 'SUPPORTED_SITE_FINDING' : null,
    evidence: status === 'COMPLETED' ? [{
      schemaVersion: 1,
      recordType: 'EVIDENCE',
      id: 'evidence-final',
      investigationId,
      recordedAt: 5,
      statement: 'Committed browser recovery evidence.',
    }] : [],
    analysisReferences: [],
    findings: status === 'COMPLETED' ? [{
      schemaVersion: 1,
      recordType: 'FINDING',
      id: 'finding-final',
      investigationId,
      recordedAt: 6,
      statement: 'Recovered Investigation reached the committed finding.',
    }] : [],
  };
}

function stream(revision, status, activities) {
  const runId = `${investigationId}:projection:${revision}`;
  const frames = [
    event(`${revision}:0`, 'RUN_STARTED', { threadId: investigationId, runId }),
    event(`${revision}:1`, 'STATE_SNAPSHOT', {
      snapshot: {
        schemaVersion: 'operations-investigation-ui/v1',
        investigation: investigation(revision, status),
        plan,
        toolActivities: activities,
      },
    }),
  ];
  activities.forEach((activity, index) => {
    const sequence = 2 + index * 3;
    frames.push(
      event(`${revision}:${sequence}`, 'TOOL_CALL_START', {
        toolCallId: activity.recordId,
        toolCallName: activity.logicalTool,
      }),
      event(`${revision}:${sequence + 1}`, 'TOOL_CALL_ARGS', {
        toolCallId: activity.recordId,
        delta: JSON.stringify(activity),
      }),
      event(`${revision}:${sequence + 2}`, 'TOOL_CALL_END', { toolCallId: activity.recordId }),
    );
  });
  const latest = 2 + activities.length * 3;
  frames.push(event(`${revision}:${latest}`, 'RUN_FINISHED', {
    threadId: investigationId,
    runId,
    outcome: { type: 'success' },
  }));
  return { body: frames.join(''), latest: `${revision}:${latest}` };
}

function problem(response, status, code, detail, retryable) {
  response.writeHead(status, {
    'content-type': 'application/problem+json',
    'cache-control': 'private, no-store',
  });
  response.end(JSON.stringify({
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    code,
    retryable,
  }));
}

function createGatewayFixture() {
  const requests = [];
  let eventRequestCount = 0;
  let hiddenRequestCount = 0;
  const eventPrefix = `/api/v1/sites/${siteId}/operations/investigations/`;
  const eventSuffix = '/events';
  const server = createHTTPServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    if (request.method !== 'GET'
      || !url.pathname.startsWith(eventPrefix)
      || !url.pathname.endsWith(eventSuffix)) {
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'Route not found.', false);
      return;
    }
    const encodedInvestigationId = url.pathname.slice(eventPrefix.length, -eventSuffix.length);
    const requestedInvestigationId = decodeURIComponent(encodedInvestigationId);
    if (requestedInvestigationId !== investigationId) {
      hiddenRequestCount += 1;
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Investigation is not visible.', false);
      return;
    }
    eventRequestCount += 1;
    const recoveryPosition = request.headers['last-event-id'] ?? null;
    requests.push({ requestNumber: eventRequestCount, recoveryPosition });
    if (eventRequestCount === 2) {
      problem(response, 502, 'OPERATIONS_AGENT_BAD_GATEWAY', 'Synthetic network interruption.', true);
      return;
    }
    const current = eventRequestCount === 1
      ? stream(1, 'RUNNING', [stableActivity])
      : stream(2, 'COMPLETED', [stableActivity, finalActivity]);
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-store, no-transform',
      'x-operations-recovery-mode': 'FULL_SNAPSHOT',
      'x-operations-recovery-reason': eventRequestCount === 1 ? 'INITIAL' : 'EXPIRED',
      'x-operations-snapshot-position': eventRequestCount === 1 ? '1:1' : '2:1',
      'x-operations-latest-position': current.latest,
    });
    response.end(current.body);
  });
  return {
    server,
    requests,
    hiddenRequests: () => hiddenRequestCount,
  };
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
  const response = await client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'Browser evaluation failed');
  }
  return response.result.value;
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped && process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else if (!stopped) {
    child.kill('SIGKILL');
  }
}

const browserCandidates = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium-browser',
  '/usr/bin/chromium',
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

try {
  await mkdir(profileDir, { recursive: true });
  await mkdir(outputRoot, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });
  viteServer = await createViteServer({
    root: fixtureRoot,
    configFile: false,
    logLevel: 'error',
    define: { __HVAC_WEB_BUILD_TARGET__: JSON.stringify('real') },
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '127.0.0.1',
      port: 0,
      strictPort: false,
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Vite fixture server has no address');
  const viteOrigin = `http://127.0.0.1:${viteAddress.port}`;
  const webURL = `${viteOrigin}/?investigation=${investigationId}`;

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
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break;
    } catch {}
    if (attempt === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Log.enable');
  await cdpClient.send('Page.enable');
  await cdpClient.send('Page.navigate', { url: webURL });

  let sawRetrying = false;
  let terminal = false;
  let lastState = null;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    const state = await evaluate(cdpClient, `({
      href: location.href,
      readyState: document.readyState,
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status') ?? null,
      investigation: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      text: document.body?.innerText ?? '',
      html: document.body?.innerHTML?.slice(0, 4000) ?? '',
    })`).catch((error) => ({ error: String(error) }));
    lastState = state;
    if (state?.connection === 'RETRYING') sawRetrying = true;
    if (state?.connection === 'TERMINAL'
      && state?.investigation === 'COMPLETED'
      && state.text.includes('Revision 2')
      && state.text.includes('Recovered Investigation reached the committed finding.')) {
      terminal = true;
      break;
    }
    await pause(100);
  }
  assert(
    terminal,
    `Operations reconnect browser audit did not reach the committed terminal snapshot; last=${JSON.stringify(lastState)} requests=${JSON.stringify(fixture.requests)} events=${JSON.stringify(cdpClient.events.slice(-20))}`,
  );
  assert(sawRetrying, 'Operations reconnect browser audit did not expose a stable retrying state');
  assertions.push('retryable-interruption-visible-and-recovered');

  const finalState = await evaluate(cdpClient, `({
    connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status'),
    investigation: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status'),
    toolCount: document.querySelectorAll('.operations-tools li').length,
    text: document.body.innerText,
    protectedResourceId: globalThis.__OPERATIONS_RECONNECT_AUDIT__?.protectedResourceId(),
  })`);
  assert(finalState.connection === 'TERMINAL' && finalState.investigation === 'COMPLETED', 'terminal UI state is unstable');
  assert(finalState.toolCount === 2, 'committed Tool activity was duplicated or lost');
  assert(finalState.text.includes('Committed browser recovery evidence.'), 'committed Evidence was not restored');
  assert(finalState.protectedResourceId === `operations-investigation:${siteId}:${investigationId}`, 'protected resource was not registered');
  assertions.push('authoritative-snapshot-and-durable-records');

  assert(fixture.requests.length === 3, `expected exactly three authorized event requests, got ${fixture.requests.length}`);
  assert(fixture.requests[0].recoveryPosition === null, 'initial connection unexpectedly supplied a recovery position');
  assert(fixture.requests[1].recoveryPosition === '1:5' && fixture.requests[2].recoveryPosition === '1:5', 'reconnect did not retain the stable last position');
  assertions.push('stable-last-event-id-across-retry');

  await evaluate(cdpClient, 'globalThis.__OPERATIONS_RECONNECT_AUDIT__.purge()');
  for (let attempt = 0; attempt < 50; attempt += 1) {
    const purged = await evaluate(cdpClient, `!document.querySelector('.operations-workspace')`);
    if (purged) break;
    if (attempt === 49) throw new Error('protected resource purge did not remove the Investigation projection');
    await pause(50);
  }
  assertions.push('protected-resource-purge');

  await cdpClient.send('Page.navigate', {
    url: `${viteOrigin}/?investigation=${hiddenInvestigationId}`,
  });
  let nondiscoverableVisible = false;
  let hiddenDiagnostic = null;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const state = await evaluate(cdpClient, `({
      text: document.body?.innerText ?? '',
      alert: document.querySelector('[role="alert"]')?.innerText ?? '',
    })`).catch((error) => ({ error: String(error) }));
    hiddenDiagnostic = state;
    if (state.alert.includes('Investigation not visible')) {
      nondiscoverableVisible = true;
      break;
    }
    await pause(100);
  }
  assert(
    nondiscoverableVisible,
    `nondiscoverable UX did not stabilize: ${JSON.stringify(hiddenDiagnostic)}`,
  );
  await pause(1000);
  assert(fixture.hiddenRequests() === 1, `nondiscoverable Investigation retried ${fixture.hiddenRequests()} times`);
  assertions.push('nondiscoverable-stable-no-retry');

  conclusion = 'passed';
  const evidence = {
    schemaVersion: 1,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    requests: fixture.requests,
    hiddenRequestCount: fixture.hiddenRequests(),
    finalState: {
      connection: finalState.connection,
      investigation: finalState.investigation,
      toolCount: finalState.toolCount,
    },
    safety: {
      productionTrafficPercent: 0,
      localReadOnly: true,
      duplicateDurableRecords: false,
      businessWrites: 0,
    },
  };
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify(evidence, null, 2));
  console.log(`Operations reconnect browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
} finally {
  cdpClient?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  try {
    await rm(profileDir, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 });
  } catch (error) {
    console.warn(`Operations reconnect browser profile cleanup was deferred: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (conclusion !== 'passed') {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({
      schemaVersion: 1,
      passed: false,
      generatedAt: new Date().toISOString(),
      assertions,
      requests: fixture.requests,
      hiddenRequestCount: fixture.hiddenRequests(),
    }, null, 2));
  }
}
