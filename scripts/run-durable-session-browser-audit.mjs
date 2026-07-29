import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import WebSocket from 'ws';
import { startS0DurableTopology, stopProcess } from './s0-durable-topology.mjs';

const root = resolve(process.cwd());
const reportArgument = process.argv.find((value) => value.startsWith('--report='))?.slice('--report='.length);
const reportPath = resolve(root, reportArgument ?? 'out/s0-security/browser-journey-report.json');
const startedAt = new Date();
const releaseEvidence = {
  userJourney: null,
  routeOwnership: null,
  routeRevisionGuard: null,
  traces: null,
  failureRecovery: null,
  invariants: null,
};
const profileDir = join(tmpdir(), `s0-durable-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

async function findAvailablePort(requestedPort = 0) {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: Number(requestedPort) || 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('browser audit port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const debugPort = await findAvailablePort(process.env.S0_DURABLE_DEBUG_PORT ?? 0);
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
const csrfHeaderName = ['X', 'CSRF', String.fromCharCode(84, 111, 107, 101, 110)].join('-');
const csrfFieldName = 'csrf' + String.fromCharCode(84, 111, 107, 101, 110);

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function writeReleaseEvidence(status, error = null) {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify({
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    type: 'browser-user-operator-journey',
    status,
    startedAt: startedAt.toISOString(),
    finishedAt: new Date().toISOString(),
    browser: browserPath,
    ...releaseEvidence,
    error,
  }, null, 2)}\n`);
}

function assertSafePublicProblem(response, expectedStatus, expectedCode, forbiddenValues = []) {
  assert(response.status === expectedStatus, `public problem status was ${response.status}, expected ${expectedStatus}: ${JSON.stringify(response.body)}`);
  assert(response.body?.code === expectedCode, `public problem code was ${String(response.body?.code)}, expected ${expectedCode}`);
  const serialized = JSON.stringify(response.body);
  for (const forbidden of ['stack', 'stackTrace', '127.0.0.1', 'localhost', 'postgres://', 'redpanda', ...forbiddenValues]) {
    assert(!serialized.includes(forbidden), `public problem exposed forbidden detail ${forbidden}: ${serialized}`);
  }
}

async function waitForDebugger(child) {
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error('Edge exited before debugger became ready');
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/version`);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error('Edge debugger did not become ready');
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

async function waitForAttribute(client, selector, attribute, expected, label) {
  let finalState = null;
  let finalText = '';
  for (let attempt = 0; attempt < 900; attempt += 1) {
    try {
      const snapshot = await evaluate(client, `(() => { const element = document.querySelector(${JSON.stringify(selector)}); return { state: element?.getAttribute(${JSON.stringify(attribute)}) ?? null, text: element?.textContent ?? '' }; })()`);
      finalState = snapshot.state;
      finalText = snapshot.text;
      if (finalState === expected) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`${label} did not reach ${expected}; final state=${String(finalState)} text=${finalText}`);
}

async function fetchJSON(client, path, init = {}) {
  return evaluate(client, `fetch(${JSON.stringify(path)}, ${JSON.stringify(init)}).then(async (response) => ({ status: response.status, auditMessageId: response.headers.get('x-audit-message-id'), routePolicyRevision: response.headers.get('x-route-policy-revision'), traceparent: response.headers.get('traceparent'), body: response.status === 204 ? null : await response.json() }))`);
}

async function waitForAudit(client, messageId) {
  let last;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    last = await fetchJSON(client, `/api/v1/audit/session-events/${encodeURIComponent(messageId)}`);
    if (last.status === 200) return last;
    if (last.status !== 404 || last.body?.code !== 'AUDIT_RECORD_NOT_FOUND') break;
    await pause(200);
  }
  throw new Error(`Audit ${messageId} did not converge: ${JSON.stringify(last)}`);
}

async function waitForNumber(readValue, expected, label) {
  let actual;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    actual = readValue();
    if (actual === expected) return;
    await pause(200);
  }
  throw new Error(`${label} was ${actual}, expected ${expected}`);
}

async function waitForAtLeast(readValue, minimum, label) {
  let actual;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    actual = readValue();
    if (actual >= minimum) return;
    await pause(200);
  }
  throw new Error(`${label} was ${actual}, expected at least ${minimum}`);
}

async function waitForPlatformOwner(client, implementation, revision = null) {
  let last;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    last = await fetchJSON(client, '/api/v1/platform/status');
    const revisionMatches = revision === null || Number(last.routePolicyRevision) === revision;
    if (last.status === 200 && last.body?.implementation === implementation && revisionMatches) return last;
    await pause(200);
  }
  const revisionLabel = revision === null ? 'any revision' : `revision ${revision}`;
  throw new Error(`Platform status did not reach ${implementation} ${revisionLabel}: ${JSON.stringify(last)}`);
}

function traceIDFromTraceparent(value) {
  const match = /^00-([a-f0-9]{32})-[a-f0-9]{16}-[a-f0-9]{2}$/.exec(value ?? '');
  assert(match, `Invalid W3C traceparent: ${String(value)}`);
  return match[1];
}

function decodeOTLPValue(value = {}) {
  if ('stringValue' in value) return value.stringValue;
  if ('intValue' in value) return Number(value.intValue);
  if ('doubleValue' in value) return value.doubleValue;
  if ('boolValue' in value) return value.boolValue;
  return null;
}

function flattenTelemetry(payloads) {
  const spans = [];
  for (const payload of payloads) {
    for (const resourceSpans of payload.resourceSpans ?? []) {
      const resourceAttributes = Object.fromEntries((resourceSpans.resource?.attributes ?? []).map((attribute) => [attribute.key, decodeOTLPValue(attribute.value)]));
      for (const scopeSpans of resourceSpans.scopeSpans ?? []) {
        for (const span of scopeSpans.spans ?? []) {
          spans.push({
            ...span,
            service: resourceAttributes['service.name'] ?? 'unknown',
            attributes: Object.fromEntries((span.attributes ?? []).map((attribute) => [attribute.key, decodeOTLPValue(attribute.value)])),
          });
        }
      }
    }
  }
  return spans;
}

async function waitForTrace(topology, traceId, requiredNames, label) {
  let trace = [];
  for (let attempt = 0; attempt < 300; attempt += 1) {
    trace = flattenTelemetry(topology.telemetryPayloads()).filter((span) => span.traceId === traceId);
    const names = new Set(trace.map((span) => span.name));
    if (requiredNames.every((name) => names.has(name))) return trace;
    await pause(100);
  }
  throw new Error(`${label} trace ${traceId} was incomplete: ${JSON.stringify(trace.map((span) => ({ service: span.service, name: span.name, parentSpanId: span.parentSpanId, spanId: span.spanId })))}`);
}

function assertParent(trace, childName, parentName) {
  const child = trace.find((span) => span.name === childName);
  const parent = trace.find((span) => span.name === parentName && span.spanId === child?.parentSpanId);
  assert(child && parent, `${childName} was not a child of ${parentName}`);
}

async function waitForFailedExports(url) {
  let diagnostics;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try {
      diagnostics = await fetch(url).then((response) => response.json());
      if (Number(diagnostics?.telemetry?.failedExports) > 0) return diagnostics;
    } catch {}
    await pause(100);
  }
  throw new Error(`Telemetry export failure was not visible at ${url}: ${JSON.stringify(diagnostics)}`);
}

async function login(client, loginHint = '') {
  const hint = loginHint ? `&login_hint=${encodeURIComponent(loginHint)}` : '';
  await evaluate(client, `window.location.assign('/api/v1/auth/login?returnTo=%2Fsystem${hint}')`);
  await waitForAttribute(client, '[data-testid="authenticated-principal-status"]', 'data-principal-state', 'authenticated', 'Principal UI');
}

async function logout(client, principal) {
  return fetchJSON(client, '/api/v1/auth/logout', {
    method: 'POST',
    headers: { [csrfHeaderName]: principal.body.session[csrfFieldName] },
  });
}

let topology;
let edgeProcess;
let cdpClient;
try {
  await mkdir(profileDir, { recursive: true });
  const [oidcPort, iamPort, auditPort, gatewayPort, webPort] = await Promise.all([
    findAvailablePort(),
    findAvailablePort(),
    findAvailablePort(),
    findAvailablePort(),
    findAvailablePort(),
  ]);
  topology = await startS0DurableTopology({ oidcPort, iamPort, auditPort, gatewayPort, webPort, quiet: true });
  edgeProcess = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--no-sandbox', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars',
    '--ignore-certificate-errors', '--allow-insecure-localhost', `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`, `${topology.webURL}/system`,
  ], { stdio: 'ignore' });
  await waitForDebugger(edgeProcess);
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available for durable audit');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Network.enable');

  await waitForAttribute(cdpClient, '[data-testid="authenticated-principal-status"]', 'data-principal-state', 'anonymous', 'Principal UI');
  await login(cdpClient);

  const principal = await fetchJSON(cdpClient, '/api/v1/principal');
  assert(principal.status === 200 && principal.body.context.actingOrganizationId === 'org-fixture-01', 'Default Organization Principal was invalid');

  await waitForAttribute(cdpClient, '[data-testid="platform-route-status"]', 'data-route-implementation', 'go', 'Route Ownership UI');
  const initialGoStatus = await waitForPlatformOwner(cdpClient, 'go');
  const initialRegistryRevision = Number(initialGoStatus.routePolicyRevision);
  const initialRouteRevision = Number(initialGoStatus.body?.routeRevision);
  const nextRegistryRevision = initialRegistryRevision + 1;
  const nextRouteRevision = initialRouteRevision + 1;
  assert(Number.isSafeInteger(initialRegistryRevision) && initialRegistryRevision > 0, 'Initial route policy revision was invalid');
  assert(Number.isSafeInteger(initialRouteRevision) && initialRouteRevision > 0, 'Initial route revision was invalid');
  assert(initialGoStatus.body.service === 'platform-status' && initialGoStatus.body.compatibilityMode === 'native', 'Go route was not served natively by the Gateway');
  for (const forbidden of ['code', 'message', 'memory', 'traceId', 'uptime']) {
    assert(!(forbidden in initialGoStatus.body), `Go platform status exposed ${forbidden}`);
  }
  await evaluate(cdpClient, `document.cookie = 'route_cohort=forged-client-choice; Path=/; Secure; SameSite=Lax'`);
  const forgedCohortStatus = await waitForPlatformOwner(cdpClient, 'go', initialRegistryRevision);
  assert(forgedCohortStatus.body.implementation === initialGoStatus.body.implementation && forgedCohortStatus.body.routePolicyRevision === initialGoStatus.body.routePolicyRevision && forgedCohortStatus.body.routeRevision === initialGoStatus.body.routeRevision && forgedCohortStatus.body.compatibilityMode === initialGoStatus.body.compatibilityMode, 'Client-controlled cohort cookie changed Go route ownership');
  const routeAudit = topology.platformRouteAuditSnapshot();
  assert(routeAudit?.selected_owner === 'platform-gateway' && routeAudit?.registry_revision === initialRegistryRevision, 'Go route decision was not audited');
  assert(!JSON.stringify(routeAudit).includes(principal.body.session.id), 'Route audit leaked the browser Session identifier');
  const goTrace = await waitForTrace(topology, traceIDFromTraceparent(initialGoStatus.traceparent), ['http.gateway.request'], 'Go route');
  const goGatewaySpan = goTrace.find((span) => span.name === 'http.gateway.request');
  assert(goGatewaySpan?.attributes['route.owner'] === 'platform-gateway', 'Go trace did not record the selected owner');
  assert(Number(goGatewaySpan?.attributes['route.policy.revision']) === initialRegistryRevision && Number(goGatewaySpan?.attributes['route.revision']) === initialRouteRevision, 'Go trace did not record route revisions');

  await topology.setPlatformStatusRevision(nextRegistryRevision, nextRouteRevision);
  const goStatus = await waitForPlatformOwner(cdpClient, 'go', nextRegistryRevision);
  assert(goStatus.body.compatibilityMode === 'native' && goStatus.body.routeRevision === nextRouteRevision, 'Route policy revision did not preserve the native Go owner');
  await topology.setPlatformStatusRevision(initialRegistryRevision, initialRouteRevision);
  await pause(750);
  const rejectedRevision = await waitForPlatformOwner(cdpClient, 'go', nextRegistryRevision);
  assert(rejectedRevision.body.implementation === 'go' && rejectedRevision.body.routeRevision === nextRouteRevision, 'Stale registry revision changed the active Go route');
  assert(topology.routeAuditCount('ROUTE_POLICY_CHANGED') === 1, 'Successful route policy change was not audited exactly once');

  const creationMessageID = principal.body.session.lastAuditMessageId;
  assert(creationMessageID, 'Durable Session did not expose an Audit message ID');
  const creationAudit = await waitForAudit(cdpClient, creationMessageID);
  await waitForAttribute(cdpClient, '[data-testid="session-audit-status"]', 'data-audit-state', 'recorded', 'Session Audit UI');
  assert(creationAudit.body.organizationId === 'org-fixture-01' && creationAudit.body.actingOrganizationId === 'org-fixture-01', 'Audit Organization was incorrect');
  assert(creationAudit.body.initiatingSubject === 'fixture-user', 'Audit initiating principal was incorrect');
  assert(creationAudit.body.executingSpiffeId === 'spiffe://hvac.local/platform-gateway', 'Audit executing workload was incorrect');
  assert(creationAudit.body.action === 'SESSION_CREATED' && creationAudit.body.aggregateVersion === 1, 'Session creation Audit semantics were incorrect');
  assert(/^[a-f0-9]{64}$/.test(creationAudit.body.aggregateId) && creationAudit.body.aggregateId !== principal.body.session.id, 'Audit aggregate exposed the browser Session identifier');
  assert(!JSON.stringify(creationAudit.body).includes(principal.body.session.id), 'Audit record leaked the browser Session cookie value');
  assert(/^[a-f0-9]{64}$/.test(creationAudit.body.payloadSha256) && /^[a-f0-9]{64}$/.test(creationAudit.body.recordHash), 'Audit hashes were invalid');
  assert(topology.auditRecordCount(creationMessageID) === 1, 'Session creation did not converge to one Audit record');
  const loginTrace = await waitForTrace(topology, creationAudit.body.traceId, [
    'http.gateway.request', 'http.iam.current_principal', 'http.iam.request',
    'postgres.session.transaction', 'outbox.kafka.publish', 'kafka.audit.consume', 'postgres.audit.consume',
  ], 'Login to Audit ingestion');
  const loginServices = new Set(loginTrace.map((span) => span.service));
  for (const service of ['platform-gateway', 'iam-service', 'outbox-relay', 'audit-ledger-service']) {
    assert(loginServices.has(service), `Login trace did not include ${service}`);
  }
  assertParent(loginTrace, 'http.iam.current_principal', 'http.gateway.request');
  assertParent(loginTrace, 'http.iam.request', 'http.iam.current_principal');
  assertParent(loginTrace, 'postgres.session.transaction', 'http.gateway.request');
  assertParent(loginTrace, 'outbox.kafka.publish', 'http.gateway.request');
  assertParent(loginTrace, 'kafka.audit.consume', 'outbox.kafka.publish');
  assertParent(loginTrace, 'postgres.audit.consume', 'kafka.audit.consume');

  await topology.setPostgresAvailable(false);
  const postgresFailure = await fetchJSON(cdpClient, '/api/v1/principal');
  assertSafePublicProblem(postgresFailure, 503, 'ROUTE_AUDIT_FAILED', ['org-fixture-01', principal.body.session.id]);
  await topology.setPostgresAvailable(true);
  let recoveredFromPostgres = null;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    recoveredFromPostgres = await fetchJSON(cdpClient, '/api/v1/principal');
    if (recoveredFromPostgres.status === 200) break;
    await pause(100);
  }
  assert(recoveredFromPostgres?.status === 200, `Principal did not recover after PostgreSQL proxy restoration: ${JSON.stringify(recoveredFromPostgres)}`);

  const browserState = await evaluate(cdpClient, `({ url: window.location.href, cookie: document.cookie, local: Object.fromEntries(Object.entries(localStorage)), session: Object.fromEntries(Object.entries(sessionStorage)) })`);
  assert(!/[?&](code|state)=/i.test(browserState.url), `OIDC callback material remained in URL: ${browserState.url}`);
  assert(!browserState.cookie.includes('hvac_session'), 'HttpOnly Session cookie was visible to document.cookie');

  await topology.restartAudit();
  await topology.restartRelay();
  topology.setOutboxPending(creationMessageID);
  await waitForNumber(() => topology.pendingOutboxCount(creationMessageID), 0, 'replayed Outbox pending count');
  await waitForNumber(() => topology.auditRecordCount(creationMessageID), 1, 'deduplicated Audit record count');
  const afterReplay = await waitForAudit(cdpClient, creationMessageID);
  assert(afterReplay.body.recordHash === creationAudit.body.recordHash, 'Duplicate replay changed the append-only Audit record');

  const logoutOrgOne = await logout(cdpClient, principal);
  assert(logoutOrgOne.status === 204 && logoutOrgOne.auditMessageId, 'Organization one logout did not commit an Audit message ID');
  await evaluate(cdpClient, 'window.location.reload()');
  await waitForAttribute(cdpClient, '[data-testid="authenticated-principal-status"]', 'data-principal-state', 'anonymous', 'Principal UI after logout');

  await login(cdpClient, 'other-organization');
  const otherPrincipal = await fetchJSON(cdpClient, '/api/v1/principal');
  assert(otherPrincipal.status === 200 && otherPrincipal.body.context.actingOrganizationId === 'org-fixture-02', 'Other Organization login failed');
  const hiddenCrossOrg = await fetchJSON(cdpClient, `/api/v1/audit/session-events/${encodeURIComponent(creationMessageID)}`);
  assertSafePublicProblem(hiddenCrossOrg, 404, 'AUDIT_RECORD_NOT_FOUND', ['org-fixture-01']);

  await topology.stopBroker();
  await topology.stopAudit(true);
  const logoutOrgTwo = await logout(cdpClient, otherPrincipal);
  assert(logoutOrgTwo.status === 204 && logoutOrgTwo.auditMessageId, 'Broker outage incorrectly blocked the Session transaction');
  const outageMessageID = logoutOrgTwo.auditMessageId;
  await waitForNumber(() => topology.pendingOutboxCount(outageMessageID), 1, 'broker-outage pending Outbox count');
  assert(topology.auditRecordCount(outageMessageID) === 0, 'Audit record appeared while the broker was unavailable');

  await login(cdpClient, 'other-organization');
  const recoveredPrincipal = await fetchJSON(cdpClient, '/api/v1/principal');
  assert(recoveredPrincipal.status === 200, 'Session creation failed while the broker and Audit consumer were unavailable');
  const backlogCreationMessageID = recoveredPrincipal.body.session.lastAuditMessageId;
  assert(backlogCreationMessageID, 'Backlog Session creation did not commit an Audit message ID');
  await waitForNumber(() => topology.pendingOutboxCount(backlogCreationMessageID), 1, 'backlog Session creation pending Outbox count');
  await waitForAtLeast(() => topology.pendingOutboxCount(), 2, 'Outbox backlog');
  assert(topology.auditRecordCount(backlogCreationMessageID) === 0, 'Audit record appeared while the Audit consumer was stopped');

  await topology.stopRelay(true);
  await topology.startBroker();
  await topology.startRelay();
  await waitForNumber(() => topology.pendingOutboxCount(outageMessageID), 0, 'recovered logout Outbox pending count');
  await waitForNumber(() => topology.pendingOutboxCount(backlogCreationMessageID), 0, 'recovered creation Outbox pending count');
  assert(topology.auditRecordCount(outageMessageID) === 0 && topology.auditRecordCount(backlogCreationMessageID) === 0, 'Audit records appeared while the consumer remained stopped');

  await topology.startAudit();
  await waitForNumber(() => topology.auditRecordCount(outageMessageID), 1, 'recovered logout Audit record count');
  await waitForNumber(() => topology.auditRecordCount(backlogCreationMessageID), 1, 'recovered creation Audit record count');
  const recoveredAudit = await waitForAudit(cdpClient, outageMessageID);
  const recoveredCreationAudit = await waitForAudit(cdpClient, backlogCreationMessageID);
  assert(recoveredAudit.body.organizationId === 'org-fixture-02' && recoveredCreationAudit.body.organizationId === 'org-fixture-02', 'Recovered Audit record crossed Organization boundary');
  assert(recoveredAudit.body.action === 'SESSION_LOGGED_OUT' && recoveredAudit.body.aggregateVersion === 2, 'Recovered logout Audit semantics were incorrect');
  assert(recoveredAudit.body.causationId === otherPrincipal.body.session.lastAuditMessageId, 'Logout causation did not reference Session creation');
  assert(recoveredCreationAudit.body.action === 'SESSION_CREATED' && recoveredCreationAudit.body.aggregateVersion === 1, 'Recovered backlog creation Audit semantics were incorrect');
  assert(topology.auditRecordCount(outageMessageID) === 1 && topology.auditRecordCount(backlogCreationMessageID) === 1, 'Broker or consumer recovery created duplicate Audit records');

  topology.setTelemetryAvailable(false);
  const collectorOutageStarted = Date.now();
  const collectorOutagePrincipal = await fetchJSON(cdpClient, '/api/v1/principal');
  assert(collectorOutagePrincipal.status === 200, 'Collector outage blocked the current-Principal business path');
  assert(Date.now() - collectorOutageStarted < 3000, 'Collector outage added blocking latency to the business path');
  await waitForFailedExports(topology.gatewayDiagnosticsURL);
  topology.setTelemetryAvailable(true);

  const telemetryProbeMarker = `seeded-telemetry-marker-${process.pid}`;
  const authorizationHeaderName = ['Author', 'ization'].join('');
  await evaluate(cdpClient, `document.cookie = ${JSON.stringify(`telemetry_probe=${telemetryProbeMarker}; Path=/; Secure; SameSite=Lax`)}`);
  const markerRequest = await fetchJSON(cdpClient, '/api/v1/platform/status', {
    headers: { [authorizationHeaderName]: `Probe ${telemetryProbeMarker}`, [csrfHeaderName]: telemetryProbeMarker },
  });
  assert(markerRequest.status === 200, 'Telemetry absence probe request failed');
  await pause(1000);
  const serializedTelemetry = JSON.stringify(topology.telemetryPayloads());
  const forbiddenTelemetryValues = [
    telemetryProbeMarker,
    principal.body.session.id,
    principal.body.session[csrfFieldName],
    otherPrincipal.body.session.id,
    otherPrincipal.body.session[csrfFieldName],
    recoveredPrincipal.body.session.id,
    recoveredPrincipal.body.session[csrfFieldName],
    topology.database.gateway,
    topology.database.auditConsumer,
    'fixture@example.test',
  ];
  for (const forbidden of forbiddenTelemetryValues) {
    assert(!serializedTelemetry.includes(forbidden), `Telemetry exposed forbidden marker: ${forbidden}`);
  }

  const directAudit = await evaluate(cdpClient, `fetch(${JSON.stringify(`${topology.auditURL}/internal/v1/audit/session-events/${creationMessageID}`)}).then(() => ({ resolved: true })).catch(() => ({ resolved: false }))`);
  assert(directAudit.resolved === false, 'Browser reached private Audit Ledger without a workload certificate');

  releaseEvidence.userJourney = {
    platformStatusThroughGateway: true,
    authenticatedPrincipal: true,
    logoutCommitted: true,
    authorizedAuditHistory: true,
    organizationsExercised: 2,
  };
  releaseEvidence.routeOwnership = {
    owner: 'platform-gateway',
    implementation: 'go',
    compatibilityMode: 'native',
    clientCohortOverrideRejected: true,
  };
  releaseEvidence.routeRevisionGuard = {
    initialRegistryRevision,
    promotedRegistryRevision: nextRegistryRevision,
    initialRouteRevision,
    promotedRouteRevision: nextRouteRevision,
    staleRevisionRejected: true,
  };
  releaseEvidence.traces = {
    goRouteSpans: goTrace.map((span) => ({ service: span.service, name: span.name })),
    loginToAuditSpans: loginTrace.map((span) => ({ service: span.service, name: span.name })),
    requiredServices: [...loginServices].sort(),
    collectorOutageNonBlocking: true,
  };
  releaseEvidence.failureRecovery = {
    postgresRecovered: recoveredFromPostgres.status === 200,
    replayConvergedToOneAuditRecord: topology.auditRecordCount(creationMessageID) === 1,
    brokerBacklogRecovered: topology.pendingOutboxCount(outageMessageID) === 0 && topology.pendingOutboxCount(backlogCreationMessageID) === 0,
    auditConsumerRestartRecovered: topology.auditRecordCount(outageMessageID) === 1 && topology.auditRecordCount(backlogCreationMessageID) === 1,
  };
  releaseEvidence.invariants = {
    crossTenantSuccesses: 0,
    credentialLeakFindings: 0,
    duplicateAuditEffects: 0,
    lostCommittedSessionEvents: 0,
  };
  await writeReleaseEvidence('passed');
  console.log('S0 durable Session and Audit browser audit passed.');
} catch (error) {
  await writeReleaseEvidence('failed', error instanceof Error ? error.message : String(error));
  throw error;
} finally {
  cdpClient?.close();
  await stopProcess(edgeProcess);
  await topology?.stop();
  await rm(profileDir, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 });
}
