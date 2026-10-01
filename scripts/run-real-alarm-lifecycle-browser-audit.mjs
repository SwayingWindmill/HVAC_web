import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createHTTPServer } from 'node:http';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer as createViteServer } from 'vite';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/real-alarms');
const outputRoot = resolve(root, 'out/real-alarm-lifecycle-certification');
const profileDir = join(tmpdir(), `real-alarm-lifecycle-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const tenantId = '01910000-0000-7000-8000-000000000001';
const siteAId = '01910000-0001-7000-8000-000000000001';
const siteBId = '01910000-0002-7000-8000-000000000002';
const alarmAId = '01910000-1000-7000-8000-000000000001';
const alarmBId = '01910000-1000-7000-8000-000000000003';
const deviceAId = '01910000-2000-7000-8000-000000000001';
const deviceBId = '01910000-2000-7000-8000-000000000002';

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

function timelineEntry({
  operation = 'PUBLISH',
  reason = `ALARM_${operation}`,
  occurredAt = '2026-07-31T09:00:00Z',
  version = 1,
  severity = 'CRITICAL',
  assigneeId,
  suppression,
  actorId = operation === 'PUBLISH' ? 'alarm-evaluator' : 'principal:alarm-operator',
} = {}) {
  return {
    operation,
    condition: 'ACTIVE',
    reason,
    actorType: operation === 'PUBLISH' ? 'WORKLOAD' : 'PRINCIPAL',
    actorId,
    ...(assigneeId ? { assigneeId } : {}),
    ...(suppression ? { suppression } : {}),
    currentSeverity: severity,
    policyRevision: 'alarm-policy-1',
    correlationId: `lifecycle-audit-${version}-${operation.toLowerCase()}`,
    occurredAt,
    version,
  };
}

function createAlarm({ alarmId, siteId, deviceId, title, severity = 'CRITICAL', fingerprint = 'a'.repeat(64) }) {
  const firstOccurredAt = '2026-07-31T09:00:00Z';
  return {
    schemaVersion: 2,
    alarmId,
    tenantId,
    siteId,
    deviceId,
    alarmType: 'SUPPLY_TEMPERATURE_DRIFT',
    fingerprint,
    incidentCorrelationId: alarmId,
    sourceType: 'DEVICE_RULE',
    sourceReference: `rule:${siteId}:supply-temperature`,
    ruleRevision: 'alarm-policy-1',
    title,
    summary: 'Authoritative Alarm condition remains active while handling facts change.',
    condition: 'ACTIVE',
    currentSeverity: severity,
    peakSeverity: severity,
    occurrenceCount: 3,
    firstOccurredAt,
    lastOccurredAt: '2026-07-31T09:15:00Z',
    evidence: [{ kind: 'telemetry-snapshot', reference: `snapshot:${alarmId.slice(-3)}`, capturedAt: '2026-07-31T09:15:00Z' }],
    links: [{ kind: 'DEVICE', targetId: deviceId }],
    timeline: [timelineEntry({ severity })],
    version: 1,
    createdAt: firstOccurredAt,
    updatedAt: '2026-07-31T09:15:00Z',
  };
}

const alarmsBySite = new Map([
  [siteAId, [createAlarm({ alarmId: alarmAId, siteId: siteAId, deviceId: deviceAId, title: 'Tokyo supply temperature drift' })]],
  [siteBId, [createAlarm({ alarmId: alarmBId, siteId: siteBId, deviceId: deviceBId, title: 'Osaka condenser approach', severity: 'WARNING', fingerprint: 'b'.repeat(64) })]],
]);

function clone(value) {
  return structuredClone(value);
}

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

function json(response, status, payload) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'private, no-store',
  });
  response.end(JSON.stringify(payload));
}

async function requestJson(request) {
  let raw = '';
  for await (const chunk of request) raw += chunk;
  return raw ? JSON.parse(raw) : {};
}

function findAlarm(alarmId) {
  return [...alarmsBySite.values()].flat().find((alarm) => alarm.alarmId === alarmId) ?? null;
}

function nextOccurredAt(alarm) {
  return new Date(Math.max(Date.parse(alarm.updatedAt) + 60_000, Date.now())).toISOString();
}

function appendTimeline(alarm, { operation, reason, assigneeId, suppression }) {
  const occurredAt = nextOccurredAt(alarm);
  alarm.version += 1;
  alarm.updatedAt = occurredAt;
  alarm.timeline.push(timelineEntry({
    operation,
    reason,
    occurredAt,
    version: alarm.version,
    severity: alarm.currentSeverity,
    assigneeId,
    suppression,
  }));
}

function createGatewayFixture() {
  const requests = [];
  let requestSequence = 0;
  let forceSuppressionConflict = false;
  const server = createHTTPServer(async (request, response) => {
    try {
      const url = new URL(request.url ?? '/', 'http://fixture.local');
      const record = {
        method: request.method ?? 'GET',
        path: url.pathname,
        query: url.search,
        body: null,
        status: 0,
        idempotencyKey: request.headers['idempotency-key'] ?? null,
      };
      requests.push(record);

      if (request.method === 'GET' && url.pathname === '/api/v1/alarms') {
        const siteId = url.searchParams.get('siteId');
        const condition = url.searchParams.get('condition');
        const severity = url.searchParams.get('severity');
        const limit = Number(url.searchParams.get('limit') ?? '50');
        const items = (alarmsBySite.get(siteId) ?? [])
          .filter((alarm) => !condition || alarm.condition === condition)
          .filter((alarm) => !severity || alarm.currentSeverity === severity)
          .slice(0, limit)
          .map(clone);
        record.status = 200;
        requestSequence += 1;
        json(response, 200, {
          data: items,
          meta: { requestId: `lifecycle-list-${requestSequence}`, limit, nextCursor: null, hasMore: false },
        });
        return;
      }

      const detailMatch = url.pathname.match(/^\/api\/v1\/alarms\/([^/]+)$/);
      if (detailMatch && request.method === 'GET') {
        const alarm = findAlarm(decodeURIComponent(detailMatch[1]));
        if (!alarm) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Alarm not found.'));
          return;
        }
        record.status = 200;
        requestSequence += 1;
        json(response, 200, { data: clone(alarm), meta: { requestId: `lifecycle-detail-${requestSequence}` } });
        return;
      }

      const acknowledgeMatch = url.pathname.match(/^\/api\/v1\/alarms\/([^/]+)\/ack$/);
      if (acknowledgeMatch && request.method === 'POST') {
        const alarm = findAlarm(decodeURIComponent(acknowledgeMatch[1]));
        if (!alarm) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Alarm not found.'));
          return;
        }
        const body = await requestJson(request);
        record.body = body;
        const occurredAt = nextOccurredAt(alarm);
        alarm.acknowledgement = {
          acknowledgedAt: occurredAt,
          acknowledgedBy: 'principal:alarm-operator',
          ...(String(body.comment ?? '').trim() ? { comment: String(body.comment).trim() } : {}),
        };
        appendTimeline(alarm, { operation: 'ACKNOWLEDGE', reason: String(body.comment ?? '').trim() || 'operator acknowledged alarm' });
        record.status = 200;
        requestSequence += 1;
        json(response, 200, { data: clone(alarm), meta: { requestId: `lifecycle-ack-${requestSequence}` } });
        return;
      }

      const assignMatch = url.pathname.match(/^\/api\/v1\/alarms\/([^/]+)\/assign$/);
      if (assignMatch && request.method === 'POST') {
        const alarm = findAlarm(decodeURIComponent(assignMatch[1]));
        if (!alarm) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Alarm not found.'));
          return;
        }
        const body = await requestJson(request);
        record.body = body;
        if (body.expectedVersion !== alarm.version) {
          record.status = 409;
          json(response, 409, problem(409, 'ALARM_VERSION_CONFLICT', 'Alarm changed before assignment.'));
          return;
        }
        alarm.assigneeId = String(body.assigneeId).trim();
        appendTimeline(alarm, { operation: 'ASSIGN', reason: String(body.reason).trim(), assigneeId: alarm.assigneeId });
        record.status = 200;
        requestSequence += 1;
        json(response, 200, { data: clone(alarm), meta: { requestId: `lifecycle-assign-${requestSequence}` } });
        return;
      }

      const localMatch = url.pathname.match(/^\/api\/v1\/local\/sites\/([^/]+)\/alarms\/([^/:]+):(unassign|suppress|unsuppress)$/);
      if (localMatch && request.method === 'POST') {
        const siteId = decodeURIComponent(localMatch[1]);
        const alarmId = decodeURIComponent(localMatch[2]);
        const operation = localMatch[3];
        const alarm = findAlarm(alarmId);
        if (!alarm || alarm.siteId !== siteId) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Alarm not found.'));
          return;
        }
        const body = await requestJson(request);
        record.body = body;
        if (operation === 'suppress' && forceSuppressionConflict) {
          forceSuppressionConflict = false;
          record.status = 409;
          json(response, 409, problem(409, 'ALARM_VERSION_CONFLICT', 'Alarm changed before this lifecycle transition.'));
          return;
        }
        if (body.expectedVersion !== alarm.version) {
          record.status = 409;
          json(response, 409, problem(409, 'ALARM_VERSION_CONFLICT', 'Alarm changed before this lifecycle transition.'));
          return;
        }
        if (!record.idempotencyKey || !String(body.reason ?? '').trim()) {
          record.status = 400;
          json(response, 400, problem(400, 'INVALID_ARGUMENT', 'Lifecycle request is invalid.'));
          return;
        }

        if (operation === 'unassign') {
          delete alarm.assigneeId;
          appendTimeline(alarm, { operation: 'UNASSIGN', reason: String(body.reason).trim() });
        } else if (operation === 'suppress') {
          const startsAt = nextOccurredAt(alarm);
          const suppression = {
            startsAt,
            expiresAt: body.suppressedUntil,
            reason: String(body.reason).trim(),
            actorId: 'principal:alarm-operator',
            policyRevision: 'alarm-policy-1',
          };
          alarm.suppression = suppression;
          appendTimeline(alarm, { operation: 'SUPPRESS', reason: suppression.reason, suppression });
        } else {
          delete alarm.suppression;
          appendTimeline(alarm, { operation: 'UNSUPPRESS', reason: String(body.reason).trim() });
        }
        record.status = 200;
        json(response, 200, clone(alarm));
        return;
      }

      record.status = 404;
      json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Route not found.'));
    } catch (error) {
      json(response, 500, problem(500, 'FIXTURE_FAILURE', error instanceof Error ? error.message : String(error), true));
    }
  });

  return {
    server,
    requests,
    forceConflictOnce() { forceSuppressionConflict = true; },
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
  const diagnostic = await evaluate(client, `({ url: location.href, text: document.body?.innerText?.slice(0, 7000) ?? '', html: document.body?.innerHTML?.slice(0, 5000) ?? '' })`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready; last=${JSON.stringify(last)} diagnostic=${JSON.stringify(diagnostic)}`);
}

async function setControlValue(client, testId, value) {
  return evaluate(client, `(() => {
    const node = document.querySelector('[data-testid="${testId}"]');
    if (!(node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement)) return false;
    const prototype = node instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(node, ${JSON.stringify(value)});
    node.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`);
}

async function clickControl(client, testId) {
  return evaluate(client, `(() => {
    const node = document.querySelector('[data-testid="${testId}"]');
    if (!(node instanceof HTMLElement) || (node instanceof HTMLButtonElement && node.disabled)) return false;
    node.click();
    return true;
  })()`);
}

async function clickText(client, selector, text) {
  return evaluate(client, `(() => {
    const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)})).find((candidate) => candidate.textContent?.includes(${JSON.stringify(text)}));
    if (!(node instanceof HTMLElement)) return false;
    node.click();
    return true;
  })()`);
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');
}

const browserPath = resolveLinuxBrowserExecutable();
const browserProfileDir = profileDir;

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

  process.env.VITE_S4_LOCAL_ALARMS = 'true';
  viteServer = await createViteServer({
    root: fixtureRoot,
    configFile: false,
    logLevel: 'error',
    define: {},
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: { host: '127.0.0.1', port: 0, strictPort: false, proxy: { '/api': { target: gatewayURL, changeOrigin: true } } },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Vite fixture server has no address');
  const webURL = `http://127.0.0.1:${viteAddress.port}`;

  browserProcess = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--disable-extensions', '--disable-sync', '--disable-background-networking',
    '--no-sandbox', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--remote-debugging-address=0.0.0.0',
    '--window-size=1440,1000', `--remote-debugging-port=${debugPort ?? 0}`, `--user-data-dir=${browserProfileDir}`, 'about:blank',
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
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-business-state') === 'READY' && document.body.innerText.includes('Tokyo supply temperature drift')`,
    'authoritative active Alarm list',
  );
  assert(await clickText(cdpClient, '[data-testid="alarm-triage-ledger"] button', 'Tokyo supply temperature drift'), 'Alarm detail control was unavailable');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="alarm-context-inspector"]')) && Boolean(document.querySelector('[data-testid="real-alarm-local-lifecycle"]')) && Boolean(document.querySelector('[data-testid="real-alarm-reason"]'))`,
    'local Alarm lifecycle workbench',
  );

  const forbiddenLifecycle = await evaluate(cdpClient, `Array.from(document.querySelectorAll('[data-testid="real-alarm-local-lifecycle"] button')).map((node) => node.textContent ?? '').filter((text) => text.includes('关闭') || text.includes('重开') || text.includes('恢复告警'))`);
  assert(forbiddenLifecycle.length === 0, `Local lifecycle exposed unsupported Close/Reopen/Manual-Recovery actions: ${JSON.stringify(forbiddenLifecycle)}`);
  assertions.push('no-close-reopen-or-manual-recovery-control');

  assert(await clickControl(cdpClient, 'real-alarm-acknowledge'), 'Alarm ACK control was unavailable');
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="real-alarm-ack-dialog"]'))`, 'Alarm ACK dialog');
  assert(await setControlValue(cdpClient, 'real-alarm-ack-comment', 'browser acknowledgement'), 'Alarm ACK comment was unavailable');
  assert(await clickText(cdpClient, '[data-testid="real-alarm-ack-dialog"] button', '确认告警'), 'Alarm ACK confirmation was unavailable');
  await waitForCondition(cdpClient, `!document.querySelector('[data-testid="real-alarm-ack-dialog"]') && document.querySelector('[data-testid="alarm-context-inspector"]')?.textContent?.includes('已确认')`, 'Alarm ACK projection');
  assert(findAlarm(alarmAId).condition === 'ACTIVE' && findAlarm(alarmAId).acknowledgement, 'ACK changed physical condition or failed to record acknowledgement');
  assertions.push('acknowledgement-is-orthogonal-to-active-condition');

  assert(await setControlValue(cdpClient, 'real-alarm-reason', 'browser assignment'), 'Alarm assignment reason control was unavailable');
  assert(await setControlValue(cdpClient, 'real-alarm-assignee', 'principal:operator-2'), 'Alarm assignee control was unavailable');
  assert(await evaluate(cdpClient, `globalThis.__REAL_ALARMS_AUDIT__.draftDirty()`), 'Alarm lifecycle draft was not protected');
  assert(await clickControl(cdpClient, 'real-alarm-assign'), 'Alarm assign control was unavailable');
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="alarm-handling-facts"]')?.textContent?.includes('已指派')`, 'Alarm assignment projection');
  assert(!await evaluate(cdpClient, `globalThis.__REAL_ALARMS_AUDIT__.draftDirty()`), 'Alarm lifecycle draft was not cleared after assignment');
  assert(findAlarm(alarmAId).assigneeId === 'principal:operator-2' && findAlarm(alarmAId).condition === 'ACTIVE', 'Assignment changed physical condition or lost assignee');
  assertions.push('assign-preserves-active-condition-and-draft-guard');

  assert(await setControlValue(cdpClient, 'real-alarm-reason', 'browser unassignment'), 'Alarm unassignment reason was unavailable');
  assert(await clickControl(cdpClient, 'real-alarm-unassign'), 'Alarm unassign control was unavailable');
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="alarm-handling-facts"]')?.textContent?.includes('未指派')`, 'Alarm unassignment projection');
  assert(!findAlarm(alarmAId).assigneeId && findAlarm(alarmAId).condition === 'ACTIVE', 'Unassign changed physical condition or failed to remove owner');
  assertions.push('unassign-preserves-active-condition');

  fixture.forceConflictOnce();
  assert(await setControlValue(cdpClient, 'real-alarm-reason', 'browser suppression retry'), 'Alarm suppression reason was unavailable');
  assert(await clickControl(cdpClient, 'real-alarm-suppress'), 'Alarm suppress control was unavailable for conflict');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="real-alarm-mutation-error"]')) && document.querySelector('[data-testid="real-alarm-mutation-error"]')?.textContent?.includes('changed before this lifecycle transition')`,
    'Alarm suppression version conflict',
  );
  assert(await clickControl(cdpClient, 'real-alarm-suppress'), 'Alarm suppress retry control was unavailable');
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="alarm-context-inspector"]')?.textContent?.includes('抑制中')`, 'Alarm suppression projection');

  const suppressAttempts = fixture.requests.filter((entry) => entry.method === 'POST' && entry.path.endsWith(':suppress') && entry.body?.reason === 'browser suppression retry');
  assert(suppressAttempts.length === 2, 'Alarm suppression retry did not issue exactly two attempts');
  assert(suppressAttempts[0].status === 409 && suppressAttempts[1].status === 200, 'Alarm suppression retry status evidence is invalid');
  assert(suppressAttempts[0].idempotencyKey === suppressAttempts[1].idempotencyKey, 'Alarm suppression retry did not preserve Idempotency-Key');
  assert(suppressAttempts[0].body?.suppressedUntil === suppressAttempts[1].body?.suppressedUntil, 'Alarm suppression retry did not preserve the absolute suppression deadline');
  assert(findAlarm(alarmAId).condition === 'ACTIVE' && findAlarm(alarmAId).suppression, 'Suppression changed physical condition or failed to record suppression');
  assertions.push('version-conflict-refetch-stable-suppression-payload-and-idempotency');

  assert(await setControlValue(cdpClient, 'real-alarm-reason', 'browser unsuppression'), 'Alarm unsuppression reason was unavailable');
  assert(await clickControl(cdpClient, 'real-alarm-unsuppress'), 'Alarm unsuppress control was unavailable');
  await waitForCondition(cdpClient, `!document.querySelector('[data-testid="alarm-context-inspector"]')?.textContent?.includes('抑制中')`, 'Alarm unsuppression projection');
  assert(!findAlarm(alarmAId).suppression && findAlarm(alarmAId).condition === 'ACTIVE', 'Unsuppress changed physical condition or failed to clear suppression');
  assertions.push('unsuppress-preserves-active-condition');

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

  const localWrites = fixture.requests.filter((entry) => entry.method === 'POST' && entry.path.includes('/api/v1/local/sites/'));
  assert(localWrites.every((entry) => /:(unassign|suppress|unsuppress)$/.test(entry.path)), `Local lifecycle seam received unsupported operation: ${JSON.stringify(localWrites.map((entry) => entry.path))}`);
  assert(!localWrites.some((entry) => /:(close|reopen|clear)$/.test(entry.path)), 'Local lifecycle seam issued a fabricated close/reopen/clear write');
  assertions.push('local-seam-only-permitted-orthogonal-lifecycle-operations');

  const browserErrors = cdpClient.events
    .filter((event) => event.method === 'Runtime.exceptionThrown'
      || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error' && event.params?.entry?.source === 'javascript'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.entry?.text ?? event.method);
  assert(browserErrors.length === 0, `Browser emitted errors: ${browserErrors.join(' | ')}`);
  assertions.push('browser-console-and-runtime-clean');

  conclusion = 'passed';
  const evidence = {
    schemaVersion: 3,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    network: { requests: fixture.requests },
    finalAlarm: clone(findAlarm(alarmAId)),
    safety: {
      physicalConditionStayedActive: findAlarm(alarmAId).condition === 'ACTIVE',
      closeReopenControls: false,
      suppressionRetryPreservedIdempotency: true,
      crossSitePurge: true,
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
    console.warn(`Alarm lifecycle browser profile cleanup was deferred: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (conclusion !== 'passed') {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({ schemaVersion: 3, passed: false, generatedAt: new Date().toISOString(), assertions, network: { requests: fixture.requests } }, null, 2));
  }
}
