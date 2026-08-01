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
const outputRoot = resolve(root, 'out/real-alarm-lifecycle-certification');
const profileDir = join(tmpdir(), `real-alarm-lifecycle-browser-${process.pid}`);
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

function publishTransition() {
  return {
    toStatus: 'OPEN',
    operation: 'PUBLISH',
    reason: 'ALARM_PUBLISHED',
    actorType: 'WORKLOAD',
    occurredAt: '2026-07-31T09:00:00Z',
    version: 1,
  };
}

function lifecycleTransition({
  fromStatus,
  toStatus,
  operation,
  reason,
  occurredAt,
  version,
  assigneeId,
  suppressedUntil,
  correlationId,
}) {
  return {
    fromStatus,
    toStatus,
    operation,
    reason,
    actorType: 'PRINCIPAL',
    actorId: 'principal:alarm-operator',
    ...(assigneeId ? { assigneeId } : {}),
    ...(suppressedUntil ? { suppressedUntil } : {}),
    policyRevision: 'alarm-policy-1',
    correlationId,
    occurredAt,
    version,
  };
}

function alarm({
  alarmId,
  siteId,
  title,
  summary,
  severity,
  status,
  occurrenceCount,
  sourceReference,
  deviceId,
  lastOccurredAt,
}) {
  const transitions = [publishTransition()];
  if (status !== 'OPEN') {
    const operation = status === 'ACKNOWLEDGED' ? 'ACKNOWLEDGE' : 'CLOSE';
    transitions.push(lifecycleTransition({
      fromStatus: 'OPEN',
      toStatus: status,
      operation,
      reason: `ALARM_${operation}`,
      occurredAt: lastOccurredAt,
      version: 2,
      correlationId: `fixture-${alarmId}-2`,
    }));
  }
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
    firstOccurredAt: '2026-07-31T09:00:00Z',
    lastOccurredAt,
    evidence: [{ kind: 'telemetry-snapshot', reference: `snapshot:${alarmId.slice(-3)}`, capturedAt: lastOccurredAt }],
    transitions,
    version: transitions.at(-1).version,
    createdAt: '2026-07-31T09:00:00Z',
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

function json(response, status, payload, headers = {}) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'private, no-store',
    ...headers,
  });
  response.end(JSON.stringify(payload));
}

async function readJSONBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function clone(value) {
  return structuredClone(value);
}

function nextMutationInstant(current) {
  return new Date(Math.max(Date.parse(current.updatedAt) + 60_000, Date.now())).toISOString();
}

function applyLifecycle(current, suffix, input, idempotencyKey) {
  const operation = {
    acknowledge: 'ACKNOWLEDGE',
    assign: 'ASSIGN',
    unassign: 'UNASSIGN',
    suppress: 'SUPPRESS',
    unsuppress: 'UNSUPPRESS',
    close: 'CLOSE',
    reopen: 'REOPEN',
  }[suffix];
  const fromStatus = current.status;
  let toStatus = fromStatus;
  let assigneeId;
  let suppressedUntil;
  switch (operation) {
    case 'ACKNOWLEDGE':
      if (fromStatus !== 'OPEN') throw new Error('invalid transition');
      toStatus = 'ACKNOWLEDGED';
      break;
    case 'ASSIGN':
      if (fromStatus === 'CLOSED' || !input.assigneeId) throw new Error('invalid transition');
      current.assigneeId = input.assigneeId;
      assigneeId = input.assigneeId;
      break;
    case 'UNASSIGN':
      if (fromStatus === 'CLOSED' || !current.assigneeId) throw new Error('invalid transition');
      delete current.assigneeId;
      break;
    case 'SUPPRESS':
      if (!['OPEN', 'ACKNOWLEDGED'].includes(fromStatus) || !input.suppressedUntil) throw new Error('invalid transition');
      toStatus = 'SUPPRESSED';
      current.suppressedUntil = input.suppressedUntil;
      suppressedUntil = input.suppressedUntil;
      break;
    case 'UNSUPPRESS': {
      if (fromStatus !== 'SUPPRESSED') throw new Error('invalid transition');
      const suppression = [...current.transitions].reverse().find((transition) => transition.operation === 'SUPPRESS');
      if (!suppression || !['OPEN', 'ACKNOWLEDGED'].includes(suppression.fromStatus)) throw new Error('invalid transition');
      toStatus = suppression.fromStatus;
      delete current.suppressedUntil;
      break;
    }
    case 'CLOSE':
      if (fromStatus === 'CLOSED') throw new Error('invalid transition');
      toStatus = 'CLOSED';
      delete current.suppressedUntil;
      break;
    case 'REOPEN':
      if (fromStatus !== 'CLOSED') throw new Error('invalid transition');
      toStatus = 'OPEN';
      delete current.suppressedUntil;
      break;
  }
  const occurredAt = nextMutationInstant(current);
  current.status = toStatus;
  current.version += 1;
  current.updatedAt = occurredAt;
  current.transitions.push(lifecycleTransition({
    fromStatus,
    toStatus,
    operation,
    reason: input.reason,
    occurredAt,
    version: current.version,
    assigneeId,
    suppressedUntil,
    correlationId: idempotencyKey,
  }));
  return current;
}

function createGatewayFixture() {
  const requests = [];
  const idempotency = new Map();
  let forceVersionConflict = false;
  const server = createHTTPServer(async (request, response) => {
    try {
      const url = new URL(request.url ?? '/', 'http://fixture.local');
      const record = {
        method: request.method ?? 'GET',
        path: url.pathname,
        query: url.search,
        csrf: request.headers['x-csrf-token'] ?? null,
        idempotencyKey: request.headers['idempotency-key'] ?? null,
        body: null,
        status: 0,
      };
      requests.push(record);

      const mutationMatch = url.pathname.match(/^\/api\/v1\/local\/sites\/([^/]+)\/alarms\/([^/:]+):(acknowledge|assign|unassign|suppress|unsuppress|close|reopen)$/);
      if (mutationMatch && request.method === 'POST') {
        const [, siteId, alarmId, suffix] = mutationMatch;
        const input = await readJSONBody(request);
        record.body = input;
        const item = (alarmsBySite.get(siteId) ?? []).find((entry) => entry.alarmId === alarmId);
        if (!item) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The Alarm resource is not visible.'));
          return;
        }
        if (record.csrf !== 'fixture-capability' || typeof record.idempotencyKey !== 'string') {
          record.status = 403;
          json(response, 403, problem(403, 'ALARM_ACCESS_DENIED', 'Lifecycle authorization is missing.'));
          return;
        }
        const bindingKey = `${siteId}|${alarmId}|${record.idempotencyKey}`;
        const digest = JSON.stringify({ suffix, input });
        const bound = idempotency.get(bindingKey);
        if (bound) {
          if (bound.digest !== digest) {
            record.status = 409;
            json(response, 409, problem(409, 'ALARM_IDEMPOTENCY_CONFLICT', 'The Idempotency-Key is bound to another payload.'));
            return;
          }
          record.status = 200;
          json(response, 200, bound.response, { 'Idempotent-Replay': 'true' });
          return;
        }
        if (forceVersionConflict) {
          forceVersionConflict = false;
          record.status = 409;
          json(response, 409, problem(409, 'ALARM_VERSION_CONFLICT', 'The Alarm changed before this lifecycle transition was committed.'));
          return;
        }
        if (input.expectedVersion !== item.version) {
          record.status = 409;
          json(response, 409, problem(409, 'ALARM_VERSION_CONFLICT', 'The Alarm changed before this lifecycle transition was committed.'));
          return;
        }
        try {
          const updated = applyLifecycle(item, suffix, input, record.idempotencyKey);
          const snapshot = clone(updated);
          idempotency.set(bindingKey, { digest, response: snapshot });
          record.status = 200;
          json(response, 200, snapshot);
        } catch {
          record.status = 422;
          json(response, 422, problem(422, 'ALARM_TRANSITION_INVALID', 'The lifecycle transition is invalid.'));
        }
        return;
      }

      const detailMatch = url.pathname.match(/^\/api\/v1\/local\/sites\/([^/]+)\/alarms\/([^/]+)$/);
      if (detailMatch && request.method === 'GET') {
        const [, siteId, alarmId] = detailMatch;
        const item = (alarmsBySite.get(siteId) ?? []).find((entry) => entry.alarmId === alarmId);
        record.status = item ? 200 : 404;
        json(response, item ? 200 : 404, item ? clone(item) : problem(404, 'RESOURCE_NOT_FOUND', 'The Alarm resource is not visible.'));
        return;
      }

      const listMatch = url.pathname.match(/^\/api\/v1\/local\/sites\/([^/]+)\/alarms$/);
      if (listMatch && request.method === 'GET') {
        const siteId = listMatch[1];
        const status = url.searchParams.get('status');
        const severity = url.searchParams.get('severity');
        const limit = Number(url.searchParams.get('limit') ?? '50');
        if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
          record.status = 400;
          json(response, 400, problem(400, 'ALARM_FILTER_INVALID', 'The Alarm list limit is invalid.'));
          return;
        }
        const items = (alarmsBySite.get(siteId) ?? [])
          .filter((entry) => !status || entry.status === status)
          .filter((entry) => !severity || entry.severity === severity)
          .slice(0, limit)
          .map(clone);
        record.status = 200;
        json(response, 200, { schemaVersion: 1, items, nextCursor: null, hasMore: false });
        return;
      }
      record.status = 404;
      json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Route not found.'));
    } catch (error) {
      json(response, 500, problem(500, 'FIXTURE_FAILURE', String(error), true));
    }
  });
  return {
    server,
    requests,
    forceConflictOnce() {
      forceVersionConflict = true;
    },
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
  const response = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'Browser evaluation failed');
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

async function setControlValue(client, testId, value) {
  return evaluate(client, `(() => {
    const node = document.querySelector('[data-testid="${testId}"]');
    if (!(node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement || node instanceof HTMLSelectElement)) return false;
    const prototype = node instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : node instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(node, ${JSON.stringify(value)});
    node.dispatchEvent(new Event(node instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }));
    return true;
  })()`);
}

async function clickControl(client, testId) {
  return evaluate(client, `(() => {
    const node = document.querySelector('[data-testid="${testId}"]');
    if (!(node instanceof HTMLButtonElement) || node.disabled) return false;
    node.click();
    return true;
  })()`);
}

async function submitLifecycle(client, operationTestId, reason, expectedStatus, expectedVersion) {
  assert(await setControlValue(client, 'real-alarm-reason', reason), 'Alarm reason control was unavailable');
  assert(await evaluate(client, `globalThis.__REAL_ALARMS_AUDIT__.draftDirty()`), 'Alarm lifecycle draft was not protected');
  assert(await clickControl(client, operationTestId), `${operationTestId} was unavailable`);
  await waitForCondition(
    client,
    `document.querySelector('[data-testid="real-alarm-detail"]')?.getAttribute('data-alarm-status') === '${expectedStatus}' && document.querySelector('[data-testid="real-alarm-detail"]')?.getAttribute('data-alarm-version') === '${expectedVersion}'`,
    `${operationTestId} lifecycle result`,
  );
  assert(!(await evaluate(client, `globalThis.__REAL_ALARMS_AUDIT__.draftDirty()`)), 'Alarm lifecycle draft was not cleared after success');
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
  })`);
  assert(initial.state === 'READY' && initial.siteId === siteAId, 'Site A Alarm scope was not ready');
  assert(!initial.text.includes('冷冻机房') && !initial.text.includes('温度过高'), 'Real Alarm UI displayed Demo Alarm content');
  assertions.push('site-a-authoritative-list-no-demo-contamination');

  assert(await evaluate(cdpClient, `(() => {
    const button = Array.from(document.querySelectorAll('.real-alarms__list button')).find((candidate) => candidate.textContent?.includes('Tokyo supply temperature drift'));
    if (!button) return false;
    button.click();
    return true;
  })()`), 'Site A Alarm detail control was unavailable');
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarm-detail"]')?.getAttribute('data-alarm-status') === 'OPEN' && document.body.innerText.includes('rule:tokyo-supply-temperature-drift:v4') && document.body.innerText.includes('ALARM_PUBLISHED')`,
    'authoritative Alarm detail',
  );
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="real-alarm-local-lifecycle"]')) && Boolean(document.querySelector('[data-testid="real-alarm-reason"]'))`,
    'local Alarm lifecycle workbench',
  );

  await submitLifecycle(cdpClient, 'real-alarm-acknowledge', 'browser acknowledgement', 'ACKNOWLEDGED', 2);
  assert(await setControlValue(cdpClient, 'real-alarm-assignee', 'principal:operator-2'), 'Alarm assignee control was unavailable');
  await submitLifecycle(cdpClient, 'real-alarm-assign', 'browser assignment', 'ACKNOWLEDGED', 3);
  await waitForCondition(cdpClient, `document.body.innerText.includes('principal:operator-2')`, 'Alarm assignment projection');
  fixture.forceConflictOnce();
  assert(await setControlValue(cdpClient, 'real-alarm-reason', 'browser suppression retry'), 'Alarm suppression reason was unavailable');
  assert(await clickControl(cdpClient, 'real-alarm-suppress'), 'Alarm suppress control was unavailable for conflict');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="real-alarm-mutation-error"]')) && document.body.innerText.includes('changed before this lifecycle transition')`,
    'Alarm suppression version conflict',
  );
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarm-detail"]')?.getAttribute('data-alarm-version') === '3'`,
    'Alarm suppression conflict refetch',
  );
  assert(await clickControl(cdpClient, 'real-alarm-suppress'), 'Alarm suppress retry control was unavailable');
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarm-detail"]')?.getAttribute('data-alarm-status') === 'SUPPRESSED' && document.querySelector('[data-testid="real-alarm-detail"]')?.getAttribute('data-alarm-version') === '4'`,
    'Alarm suppress retry result',
  );
  const suppressAttempts = fixture.requests.filter((entry) => entry.method === 'POST' && entry.path.endsWith(':suppress') && entry.body?.reason === 'browser suppression retry');
  assert(suppressAttempts.length === 2, 'Alarm suppression retry did not issue exactly two attempts');
  assert(suppressAttempts[0].status === 409 && suppressAttempts[1].status === 200, 'Alarm suppression retry status evidence is invalid');
  assert(suppressAttempts[0].idempotencyKey === suppressAttempts[1].idempotencyKey, 'Alarm suppression retry did not preserve Idempotency-Key');
  assert(suppressAttempts[0].body?.suppressedUntil === suppressAttempts[1].body?.suppressedUntil, 'Alarm suppression retry did not preserve the absolute suppression deadline');
  assertions.push('version-conflict-refetch-stable-suppression-payload-and-idempotency');

  await submitLifecycle(cdpClient, 'real-alarm-unsuppress', 'browser unsuppression', 'ACKNOWLEDGED', 5);
  await submitLifecycle(cdpClient, 'real-alarm-close', 'browser close', 'CLOSED', 6);
  await submitLifecycle(cdpClient, 'real-alarm-reopen', 'browser reopen', 'OPEN', 7);
  await submitLifecycle(cdpClient, 'real-alarm-close', 'browser final close', 'CLOSED', 8);
  assertions.push('acknowledge-assign-suppress-unsuppress-close-reopen-close');

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
    draftDirty: globalThis.__REAL_ALARMS_AUDIT__.draftDirty(),
  })`);
  assert(afterSwitch.siteId === siteBId, 'Alarm fixture did not switch to Site B');
  assert(!JSON.stringify(afterSwitch.cacheKeys).includes(siteAId), 'old Site Alarm cache survived Site transition');
  assert(!afterSwitch.text.includes('Tokyo supply temperature drift'), 'old Site Alarm content survived Site transition');
  assert(afterSwitch.draftDirty === false, 'old Site Alarm lifecycle draft survived Site transition');
  assertions.push('cross-site-cache-view-and-draft-purge');

  const alarmRequests = fixture.requests.filter((entry) => entry.path.includes('/alarms'));
  const mutationRequests = alarmRequests.filter((entry) => entry.method === 'POST');
  assert(mutationRequests.length >= 8, 'Alarm browser audit did not exercise lifecycle writes');
  assert(mutationRequests.every((entry) => entry.csrf === 'fixture-capability'), 'Alarm lifecycle request omitted CSRF capability');
  assert(mutationRequests.every((entry) => typeof entry.idempotencyKey === 'string' && entry.idempotencyKey.startsWith('real-alarm-')), 'Alarm lifecycle request omitted stable Idempotency-Key');
  assert(mutationRequests.every((entry) => Number.isInteger(entry.body?.expectedVersion) && entry.body.expectedVersion > 0), 'Alarm lifecycle request omitted expected version');
  assert(alarmRequests.every((entry) => entry.path.startsWith('/api/v1/local/sites/')), 'Alarm browser audit bypassed the local Site-scoped seam');
  assert(fixture.requests.every((entry) => !entry.path.includes('/telemetry/')), 'Alarm browser audit used Telemetry as an Alarm source');
  assertions.push('csrf-idempotency-expected-version-no-telemetry-inference');

  stateEvidence.lifecycle = {
    finalStatus: 'CLOSED',
    finalVersion: 8,
    operations: ['ACKNOWLEDGE', 'ASSIGN', 'SUPPRESS', 'UNSUPPRESS', 'CLOSE', 'REOPEN', 'CLOSE'],
    conflictRetried: true,
  };
  conclusion = 'passed';
  const evidence = {
    schemaVersion: 2,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    stateEvidence,
    network: { requests: fixture.requests },
    safety: {
      productionTrafficPercent: 0,
      localLifecycle: true,
      optimisticConcurrency: true,
      idempotentRetry: true,
      telemetryInference: false,
      demoContamination: false,
    },
  };
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify(evidence, null, 2));
  console.log(`Real Alarm lifecycle browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
} finally {
  cdpClient?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  try {
    await rm(profileDir, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 });
  } catch (error) {
    console.warn(`Real Alarm browser profile cleanup was deferred: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (conclusion !== 'passed') {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({ schemaVersion: 2, passed: false, generatedAt: new Date().toISOString(), assertions, stateEvidence, network: { requests: fixture.requests } }, null, 2));
  }
}
