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
const unableInvestigationId = 'investigation-browser-unable';
const hiddenInvestigationId = 'hidden-investigation';
const digest = (character) => `sha256:${character.repeat(64)}`;

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

function plan(status) {
  const terminal = status !== 'RUNNING';
  return {
    schemaVersion: 1,
    id: 'site-night-energy-investigation',
    label: 'Site night-energy investigation',
    completedSteps: terminal ? 4 : 2,
    totalSteps: 4,
    progressPercent: terminal ? 100 : 50,
    steps: [
      { id: 'READ_SITE_CONTEXT', label: 'Read authoritative Site context', status: 'COMPLETED' },
      { id: 'READ_ENERGY_SERIES', label: 'Read authoritative night-energy periods', status: 'COMPLETED' },
      { id: 'ANALYZE', label: 'Run deterministic night-energy analysis', status: terminal ? 'COMPLETED' : 'IN_PROGRESS' },
      { id: 'COMMIT_RESULT', label: 'Commit Evidence, Analysis and Finding', status: terminal ? 'COMPLETED' : 'PENDING' },
    ],
  };
}

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

function scope() {
  return { organizationId, siteId, equipmentId: null, deviceId: null };
}

function evidenceSource({ owner, requestId, registryRevision = null, datasetRevision = null, partial = false, quality = 'GOOD' }) {
  return {
    owner,
    scope: scope(),
    requestId,
    registryRevision,
    datasetRevision,
    watermark: {
      data: datasetRevision ? '2026-08-01T00:00:00.000Z' : null,
      aggregate: datasetRevision ? '2026-08-01T00:05:00.000Z' : null,
    },
    partial,
    quality: { classification: quality, valid: quality === 'GOOD' ? 8 : 4, suspect: quality === 'GOOD' ? 0 : 4, invalid: 0 },
    capturedAt: 5,
    evaluatedAt: 6,
    provenanceDigest: digest(owner === 'registry' ? 'a' : 'b'),
  };
}

function supportedRecords(id) {
  const evidenceId = `${id}:evidence:comparison`;
  const analysisId = `${id}:analysis:comparison`;
  return {
    evidence: [{
      schemaVersion: 1,
      recordType: 'EVIDENCE',
      id: evidenceId,
      investigationId: id,
      recordedAt: 7,
      evidenceKind: 'SITE_ENERGY_PERIOD_COMPARISON',
      classification: 'ALGORITHM_RESULT',
      statement: 'Committed browser recovery evidence confirms a Site night-energy increase.',
      analysisReferenceDigest: digest('c'),
      sources: [
        evidenceSource({ owner: 'registry', requestId: 'request-registry', registryRevision: 'registry-r17' }),
        evidenceSource({ owner: 'telemetry-query-service', requestId: 'request-energy', datasetRevision: 'dataset-r42' }),
      ],
    }],
    analysisReferences: [{
      schemaVersion: 1,
      recordType: 'ANALYSIS_REFERENCE',
      id: analysisId,
      investigationId: id,
      recordedAt: 8,
      analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
      authority: 'DETERMINISTIC_ALGORITHM',
      algorithmVersion: 'night-energy-v1',
      policyVersion: 'quality-policy-v1',
      inputEvidenceIds: [evidenceId],
      parameterDigest: digest('d'),
      resultDigest: digest('e'),
      executedAt: 8,
      outcome: 'SUPPORTED_SITE_FINDING',
    }],
    findings: [{
      schemaVersion: 1,
      recordType: 'FINDING',
      id: `${id}:finding:site`,
      investigationId: id,
      recordedAt: 9,
      findingKind: 'SITE_NIGHT_ENERGY_INCREASE',
      classification: 'INFERENCE',
      statement: 'Recovered Investigation reached the committed Site finding.',
      evidenceIds: [evidenceId],
      analysisReferenceIds: [analysisId],
      conclusion: {
        status: 'SUPPORTED',
        scope: 'SITE',
        organizationId,
        siteId,
      },
    }],
  };
}

function unableRecords(id) {
  const evidenceId = `${id}:evidence:readiness`;
  const analysisId = `${id}:analysis:readiness`;
  const targetPeriod = {
    localDate: '2026-07-31',
    from: '2026-07-31T00:00:00+09:00',
    to: '2026-07-31T08:00:00+09:00',
    expectedBuckets: 8,
  };
  const baselinePeriod = {
    localDate: '2026-07-24',
    from: '2026-07-24T00:00:00+09:00',
    to: '2026-07-24T08:00:00+09:00',
    expectedBuckets: 8,
  };
  return {
    evidence: [{
      schemaVersion: 1,
      recordType: 'EVIDENCE',
      id: evidenceId,
      investigationId: id,
      recordedAt: 10,
      evidenceKind: 'SITE_ENERGY_SERIES_READINESS_ASSESSED',
      classification: 'FACT',
      statement: 'Site energy is available, but Equipment attribution evidence is absent.',
      analysisReferenceDigest: null,
      sources: [
        evidenceSource({ owner: 'telemetry-query-service', requestId: 'request-unable-energy', datasetRevision: 'dataset-r43', partial: true, quality: 'UNCERTAIN' }),
      ],
    }],
    analysisReferences: [{
      schemaVersion: 1,
      recordType: 'ANALYSIS_REFERENCE',
      id: analysisId,
      investigationId: id,
      recordedAt: 11,
      analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
      authority: 'DETERMINISTIC_ALGORITHM',
      algorithmVersion: 'night-energy-v1',
      policyVersion: 'quality-policy-v1',
      inputEvidenceIds: [evidenceId],
      parameterDigest: digest('f'),
      resultDigest: digest('1'),
      executedAt: 11,
      outcome: 'UNABLE_TO_CONCLUDE',
    }],
    findings: [{
      schemaVersion: 1,
      recordType: 'FINDING',
      id: `${id}:finding:unable`,
      investigationId: id,
      recordedAt: 12,
      findingKind: 'UNABLE_TO_CONCLUDE',
      classification: 'INFERENCE',
      statement: 'The Site Investigation cannot produce an Equipment root-cause conclusion.',
      evidenceIds: [evidenceId],
      analysisReferenceIds: [analysisId],
      conclusion: {
        status: 'UNABLE_TO_CONCLUDE',
        scope: 'EQUIPMENT',
        reasonCode: 'EQUIPMENT_ATTRIBUTION_EVIDENCE_MISSING',
        detail: 'Required Equipment binding and period comparison evidence is not available.',
        requiredNext: [{
          status: 'REQUIRED_NEXT',
          kind: 'EQUIPMENT_ENERGY_BINDINGS',
          owner: 'registry',
          capability: 'registry.getEquipmentEnergyBindings',
          organizationId,
          siteId,
          equipmentIds: ['equipment-ahu-01'],
          targetPeriod,
          baselinePeriod,
          requiredMetadata: ['BUSINESS_REVISION', 'QUALITY', 'CAPTURED_AT', 'PAYLOAD_DIGEST'],
        }, {
          status: 'REQUIRED_NEXT',
          kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON',
          owner: 'telemetry-query-service',
          capability: 'analytics.energy.getEquipmentSeries',
          organizationId,
          siteId,
          equipmentIds: ['equipment-ahu-01'],
          targetPeriod,
          baselinePeriod,
          requiredMetadata: ['DATASET_REVISION', 'WATERMARK', 'PARTIAL', 'QUALITY', 'CAPTURED_AT', 'PAYLOAD_DIGEST'],
        }],
      },
    }],
  };
}

function toolReceipts(id, activities) {
  return activities.map((activity, index) => ({
    schemaVersion: 1,
    recordType: 'TOOL_EXECUTION_RECEIPT',
    id: activity.recordId,
    investigationId: id,
    recordedAt: activity.completedAt,
    logicalTool: activity.logicalTool,
    owner: activity.owner,
    requestId: `${activity.recordId}:request`,
    attemptId: `${activity.recordId}:attempt`,
    runId: `${id}:run`,
    stepId: index === 0 ? 'READ_SITE_CONTEXT' : 'READ_ENERGY_SERIES',
    startedAt: activity.startedAt,
    completedAt: activity.completedAt,
    resultCategory: activity.resultCategory,
    metadata: activity.owner === 'registry'
      ? { registryRevision: 'registry-r17', quality: 'GOOD' }
      : { datasetRevision: 'dataset-r42', watermark: '2026-08-01T00:05:00.000Z', partial: false, quality: 'GOOD' },
  }));
}

function investigation(id, revision, status, outcome, activities = []) {
  const records = outcome === 'SUPPORTED_SITE_FINDING'
    ? supportedRecords(id)
    : outcome === 'UNABLE_TO_CONCLUDE'
      ? unableRecords(id)
      : { evidence: [], analysisReferences: [], findings: [] };
  return {
    schemaVersion: 1,
    id,
    scope: scope(),
    status,
    revision,
    createdAt: id === unableInvestigationId ? 2 : 1,
    activeRun: status === 'RUNNING' ? { id: `${id}:active-run`, status: 'ACTIVE', startedAt: 1 } : null,
    outcome,
    ...records,
    toolReceipts: toolReceipts(id, activities),
  };
}

function stream(id, revision, status, outcome, activities) {
  const runId = `${id}:projection:${revision}`;
  const view = investigation(id, revision, status, outcome, activities);
  const { toolReceipts: _toolReceipts, ...projection } = view;
  const frames = [
    event(`${revision}:0`, 'RUN_STARTED', { threadId: id, runId }),
    event(`${revision}:1`, 'STATE_SNAPSHOT', {
      snapshot: {
        schemaVersion: 'operations-investigation-ui/v1',
        investigation: projection,
        plan: plan(status),
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
    threadId: id,
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

function json(response, body, status = 200) {
  response.writeHead(status, {
    'content-type': 'application/json',
    'cache-control': 'private, no-store',
  });
  response.end(JSON.stringify(body));
}

function summary(view) {
  return {
    schemaVersion: 1,
    id: view.id,
    scope: view.scope,
    status: view.status,
    revision: view.revision,
    createdAt: view.createdAt,
    outcome: view.outcome,
    evidenceCount: view.evidence.length,
    analysisReferenceCount: view.analysisReferences.length,
    findingCount: view.findings.length,
    toolReceiptCount: view.toolReceipts.length,
  };
}

function createGatewayFixture() {
  const requests = [];
  let supportedEventRequestCount = 0;
  let hiddenRequestCount = 0;
  let listRequestCount = 0;
  const collectionPath = `/api/v1/sites/${siteId}/operations/investigations`;
  const itemPrefix = `${collectionPath}/`;
  const currentSupported = () => supportedEventRequestCount >= 3
    ? investigation(investigationId, 2, 'COMPLETED', 'SUPPORTED_SITE_FINDING', [stableActivity, finalActivity])
    : investigation(investigationId, 1, 'RUNNING', null, [stableActivity]);
  const unable = investigation(unableInvestigationId, 5, 'COMPLETED', 'UNABLE_TO_CONCLUDE', [stableActivity]);

  const server = createHTTPServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    if (request.method !== 'GET') {
      problem(response, 405, 'METHOD_NOT_ALLOWED', 'Fixture accepts GET only.', false);
      return;
    }
    if (url.pathname === collectionPath) {
      listRequestCount += 1;
      json(response, {
        schemaVersion: 1,
        investigations: [summary(unable), summary(currentSupported())]
          .sort((left, right) => right.createdAt - left.createdAt || right.id.localeCompare(left.id)),
      });
      return;
    }
    if (!url.pathname.startsWith(itemPrefix)) {
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'Route not found.', false);
      return;
    }
    const relative = url.pathname.slice(itemPrefix.length);
    const isEvents = relative.endsWith('/events');
    const encodedId = isEvents ? relative.slice(0, -'/events'.length) : relative;
    const requestedId = decodeURIComponent(encodedId);
    if (requestedId !== investigationId && requestedId !== unableInvestigationId) {
      hiddenRequestCount += 1;
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Investigation is not visible.', false);
      return;
    }
    if (!isEvents) {
      json(response, requestedId === investigationId ? currentSupported() : unable);
      return;
    }

    if (requestedId === unableInvestigationId) {
      const current = stream(unableInvestigationId, 5, 'COMPLETED', 'UNABLE_TO_CONCLUDE', [stableActivity]);
      response.writeHead(200, {
        'content-type': 'text/event-stream; charset=utf-8',
        'cache-control': 'no-store, no-transform',
        'x-operations-recovery-mode': 'FULL_SNAPSHOT',
        'x-operations-recovery-reason': 'INITIAL',
        'x-operations-snapshot-position': '5:1',
        'x-operations-latest-position': current.latest,
      });
      response.end(current.body);
      return;
    }

    supportedEventRequestCount += 1;
    const recoveryPosition = request.headers['last-event-id'] ?? null;
    requests.push({ requestNumber: supportedEventRequestCount, recoveryPosition });
    if (supportedEventRequestCount === 2) {
      problem(response, 502, 'OPERATIONS_AGENT_BAD_GATEWAY', 'Synthetic network interruption.', true);
      return;
    }
    const current = supportedEventRequestCount === 1
      ? stream(investigationId, 1, 'RUNNING', null, [stableActivity])
      : stream(investigationId, 2, 'COMPLETED', 'SUPPORTED_SITE_FINDING', [stableActivity, finalActivity]);
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-store, no-transform',
      'x-operations-recovery-mode': 'FULL_SNAPSHOT',
      'x-operations-recovery-reason': supportedEventRequestCount === 1 ? 'INITIAL' : 'EXPIRED',
      'x-operations-snapshot-position': supportedEventRequestCount === 1 ? '1:1' : '2:1',
      'x-operations-latest-position': current.latest,
    });
    response.end(current.body);
  });
  return {
    server,
    requests,
    hiddenRequests: () => hiddenRequestCount,
    listRequests: () => listRequestCount,
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
      outcome: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-outcome') ?? null,
      text: document.body?.innerText ?? '',
    })`).catch((error) => ({ error: String(error) }));
    lastState = state;
    if (state?.connection === 'RETRYING') sawRetrying = true;
    if (state?.connection === 'TERMINAL'
      && state?.investigation === 'COMPLETED'
      && state?.outcome === 'SUPPORTED_SITE_FINDING'
      && state.text.includes('Revision 2')
      && state.text.includes('Recovered Investigation reached the committed Site finding.')) {
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

  await evaluate(cdpClient, `document.querySelectorAll('.operations-record-card details').forEach((details) => { details.open = true; })`);
  const supportedState = await evaluate(cdpClient, `({
    connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status'),
    investigation: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status'),
    toolCount: document.querySelectorAll('.operations-tools > li').length,
    evidenceCount: document.querySelectorAll('[data-record-type="EVIDENCE"]').length,
    analysisCount: document.querySelectorAll('[data-record-type="ANALYSIS_REFERENCE"]').length,
    findingCount: document.querySelectorAll('[data-record-type="FINDING"]').length,
    listCount: document.querySelectorAll('.operations-list-item').length,
    text: document.body.textContent ?? '',
    protectedResourceId: globalThis.__OPERATIONS_RECONNECT_AUDIT__?.protectedResourceId(),
  })`);
  assert(supportedState.connection === 'TERMINAL' && supportedState.investigation === 'COMPLETED', 'terminal UI state is unstable');
  assert(supportedState.toolCount === 2, 'committed Tool receipts were duplicated or lost');
  assert(supportedState.evidenceCount === 1 && supportedState.analysisCount === 1 && supportedState.findingCount === 1, 'typed committed records were duplicated or lost');
  assert(supportedState.listCount === 2, 'Site Investigation list did not expose both authorized records');
  for (const requiredText of [
    'dataset-r42',
    'Data watermark',
    'GOOD',
    'Partial',
    'DETERMINISTIC_ALGORITHM',
    'Site-only conclusion',
    '不构成 Equipment root cause',
  ]) {
    assert(supportedState.text.includes(requiredText), `supported Workspace omitted ${requiredText}`);
  }
  assert(supportedState.protectedResourceId === `operations-investigation:${siteId}:${investigationId}`, 'protected resource was not registered');
  assertions.push('typed-provenance-and-site-only-finding');

  assert(fixture.requests.length === 3, `expected exactly three authorized event requests, got ${fixture.requests.length}`);
  assert(fixture.requests[0].recoveryPosition === null, 'initial connection unexpectedly supplied a recovery position');
  assert(fixture.requests[1].recoveryPosition === '1:5' && fixture.requests[2].recoveryPosition === '1:5', 'reconnect did not retain the stable last position');
  assertions.push('stable-last-event-id-across-retry');

  const openedUnable = await evaluate(cdpClient, `(() => {
    const button = [...document.querySelectorAll('.operations-list-item')]
      .find((candidate) => candidate.textContent.includes(${JSON.stringify(unableInvestigationId)}));
    if (!button) return false;
    button.click();
    return true;
  })()`);
  assert(openedUnable, 'unable-to-conclude Investigation was not navigable from the Site list');

  let unableVisible = false;
  let unableState = null;
  for (let attempt = 0; attempt < 150; attempt += 1) {
    const state = await evaluate(cdpClient, `({
      href: location.href,
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status') ?? null,
      outcome: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-outcome') ?? null,
      requiredNextCount: document.querySelectorAll('.operations-required-next-card').length,
      text: document.body?.innerText ?? '',
    })`).catch((error) => ({ error: String(error) }));
    unableState = state;
    if (state.connection === 'TERMINAL'
      && state.outcome === 'UNABLE_TO_CONCLUDE'
      && state.requiredNextCount === 2
      && state.href.includes(`investigation=${unableInvestigationId}`)) {
      unableVisible = true;
      break;
    }
    await pause(100);
  }
  assert(unableVisible, `unable-to-conclude Workspace did not stabilize: ${JSON.stringify(unableState)}`);
  for (const requiredText of [
    'UNABLE TO CONCLUDE',
    'EQUIPMENT_ATTRIBUTION_EVIDENCE_MISSING',
    'registry.getEquipmentEnergyBindings',
    'analytics.energy.getEquipmentSeries',
    'BUSINESS_REVISION',
    'DATASET_REVISION',
    'WATERMARK',
    'PARTIAL',
  ]) {
    assert(unableState.text.includes(requiredText), `unable Workspace omitted ${requiredText}`);
  }
  assertions.push('unable-to-conclude-required-next');

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
  assert(nondiscoverableVisible, `nondiscoverable UX did not stabilize: ${JSON.stringify(hiddenDiagnostic)}`);
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
    listRequestCount: fixture.listRequests(),
    hiddenRequestCount: fixture.hiddenRequests(),
    finalState: {
      connection: supportedState.connection,
      investigation: supportedState.investigation,
      toolCount: supportedState.toolCount,
      evidenceCount: supportedState.evidenceCount,
      analysisCount: supportedState.analysisCount,
      findingCount: supportedState.findingCount,
      requiredNextCount: unableState.requiredNextCount,
    },
    safety: {
      productionTrafficPercent: 0,
      localReadOnly: true,
      duplicateDurableRecords: false,
      businessWrites: 0,
      rawPointsRendered: false,
      equipmentRootCauseClaimed: false,
    },
  };
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify(evidence, null, 2));
  console.log(`Operations Investigation Workspace browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
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
      listRequestCount: fixture.listRequests(),
      hiddenRequestCount: fixture.hiddenRequests(),
    }, null, 2));
  }
}
