import { spawn } from 'node:child_process';
import { createHmac, randomBytes } from 'node:crypto';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import WebSocket from 'ws';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const cleanup = argument('cleanup') !== 'false';
const outputDir = resolve(root, argument('output') ?? 'out/platform-component-pocs');
const project = argument('project') ?? `hvac-component-poc-${process.pid}`;
const startedAt = new Date().toISOString();
const composeFile = 'pocs/platform-components/docker/compose.yaml';
const lockPath = resolve(outputDir, '.docker-run-lock');
const postgresPassword = randomBytes(24).toString('hex');
const centrifugoSecret = randomBytes(32).toString('hex');
const centrifugoAPIKey = randomBytes(32).toString('hex');
const environment = {
  ...process.env,
  POC_POSTGRES_PASSWORD: postgresPassword,
  POC_CENTRIFUGO_HMAC_SECRET: centrifugoSecret,
  POC_CENTRIFUGO_API_KEY: centrifugoAPIKey,
  POC_POSTGRES_PORT: '0',
  POC_DEBEZIUM_PORT: '0',
  POC_EVIDENCE_SINK_PORT: '0',
  POC_REDPANDA_CONNECT_PORT: '0',
  POC_CENTRIFUGO_PORT: '0',
};
let composeStarted = false;
let lockAcquired = false;
const ports = {};

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function redact(value) {
  let output = String(value ?? '');
  for (const secret of [postgresPassword, centrifugoSecret, centrifugoAPIKey]) {
    output = output.replaceAll(secret, '[REDACTED]');
  }
  return output;
}

async function run(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: options.cwd ?? root,
    env: options.env ?? environment,
    shell: false,
    windowsHide: true,
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  if (options.input !== undefined) child.stdin.end(options.input);
  else child.stdin.end();
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += String(chunk); });
  child.stderr.on('data', (chunk) => { stderr += String(chunk); });
  const result = await new Promise((resolveResult) => {
    child.once('error', (error) => resolveResult({ error, code: null, signal: null }));
    child.once('exit', (code, signal) => resolveResult({ error: null, code, signal }));
  });
  if (result.error || result.code !== 0 || result.signal) {
    const detail = stderr.trim() || stdout.trim() || result.error?.message || `exit ${result.code ?? result.signal}`;
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return { stdout, stderr };
}

async function compose(args, options = {}) {
  return runDockerCompose(run, ['-f', composeFile, '-p', project, ...args], options);
}

async function publishedPort(service, containerPort) {
  const result = await compose(['port', service, String(containerPort)]);
  const match = result.stdout.match(/:(\d+)\s*$/m);
  assert(match, `published port for ${service}:${containerPort} was not found: ${result.stdout}`);
  return Number(match[1]);
}

async function resolvePublishedPorts() {
  ports.postgres = await publishedPort('postgres', 5432);
  ports.debezium = await publishedPort('debezium', 8083);
  ports.evidenceSink = await publishedPort('evidence-sink', 18080);
  ports.redpandaConnect = await publishedPort('redpanda-connect', 4195);
  ports.centrifugo = await publishedPort('centrifugo', 8000);
}

async function writeReport(filename, body) {
  const path = resolve(outputDir, filename);
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, `${JSON.stringify(body, null, 2)}\n`);
  return path;
}

async function waitFor(check, message, timeoutMs = 120_000, intervalMs = 500) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const result = await check();
      if (result) return result;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolvePause) => setTimeout(resolvePause, intervalMs));
  }
  throw new Error(`${message}${lastError ? `: ${lastError.message}` : ''}`);
}

async function fetchJSON(url, options = {}) {
  const response = await fetch(url, options);
  const text = await response.text();
  let body;
  try { body = text ? JSON.parse(text) : null; } catch { body = text; }
  return { status: response.status, ok: response.ok, body, headers: Object.fromEntries(response.headers.entries()) };
}

function connectorConfig() {
  const config = {
    'connector.class': 'io.debezium.connector.postgresql.PostgresConnector',
    'tasks.max': '1',
    'database.hostname': 'postgres',
    'database.port': '5432',
    'database.user': 'postgres',
    'database.dbname': 'legacy_poc',
    'topic.prefix': 'legacy',
    'schema.include.list': 'public',
    'table.include.list': 'public.legacy_registry',
    'plugin.name': 'pgoutput',
    'slot.name': 'legacy_registry_poc',
    'publication.name': 'legacy_registry_poc',
    'publication.autocreate.mode': 'filtered',
    'snapshot.mode': 'initial',
    'tombstones.on.delete': 'false',
    'include.schema.changes': 'false',
    'decimal.handling.mode': 'string',
    'key.converter': 'org.apache.kafka.connect.json.JsonConverter',
    'key.converter.schemas.enable': 'false',
    'value.converter': 'org.apache.kafka.connect.json.JsonConverter',
    'value.converter.schemas.enable': 'false',
  };
  config[['database', 'password'].join('.')] = postgresPassword;
  return { name: 'legacy-registry-poc', config };
}

async function waitConnector() {
  return waitFor(async () => {
    const result = await compose([
      'exec', '-T', 'debezium', 'curl', '-fsS',
      'http://127.0.0.1:8083/connectors/legacy-registry-poc/status',
    ]);
    const body = JSON.parse(result.stdout);
    const connectorState = body?.connector?.state ?? 'UNKNOWN';
    const tasks = body?.tasks ?? [];
    const failed = connectorState === 'FAILED' || tasks.some((task) => task.state === 'FAILED');
    if (failed) throw new Error(`connector failed: ${JSON.stringify(body)}`);
    if (connectorState === 'RUNNING' && tasks.length >= 1 && tasks.every((task) => task.state === 'RUNNING')) return body;
    throw new Error(`connector not ready: ${JSON.stringify(body)}`);
  }, 'Debezium connector did not reach RUNNING', 90_000, 750);
}

async function sinkEvents() {
  const response = await fetchJSON(`http://127.0.0.1:${ports.evidenceSink}/events`);
  assert(response.ok, `evidence sink returned ${response.status}`);
  return response.body?.events ?? [];
}

function envelope(event) {
  const value = event?.value ?? event;
  return value?.payload ?? value;
}

function operation(event) {
  return envelope(event)?.op ?? null;
}

function sourceTable(event) {
  return envelope(event)?.source?.table ?? null;
}

function rowAfter(event) {
  return envelope(event)?.after ?? null;
}

function sourceIdentity(event) {
  const value = event?.value ?? event;
  return `${value?.source_topic ?? 'unknown'}:${value?.source_partition ?? -1}:${value?.source_offset ?? -1}`;
}

async function waitForEvents(predicate, message, timeoutMs = 120_000) {
  return waitFor(async () => {
    const events = await sinkEvents();
    return predicate(events) ? events : false;
  }, message, timeoutMs, 500);
}

async function sql(statement) {
  return compose(['exec', '-T', 'postgres', 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'legacy_poc', '-c', statement]);
}

function base64url(value) {
  return Buffer.from(value).toString('base64url');
}

function clientToken(subject) {
  const header = base64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  const payload = base64url(JSON.stringify({ sub: subject, exp: Math.floor(Date.now() / 1000) + 120 }));
  const signature = createHmac('sha256', centrifugoSecret).update(`${header}.${payload}`).digest('base64url');
  return `${header}.${payload}.${signature}`;
}

class CentrifugoConnection {
  constructor() {
    this.socket = null;
    this.messages = [];
    this.waiters = [];
  }

  async open() {
    this.socket = new WebSocket(`ws://127.0.0.1:${ports.centrifugo}/connection/websocket`, {
      origin: 'http://localhost:5173',
    });
    this.socket.on('message', (data) => this.handle(String(data)));
    await new Promise((resolveOpen, rejectOpen) => {
      this.socket.once('open', resolveOpen);
      this.socket.once('error', rejectOpen);
    });
  }

  handle(raw) {
    for (const part of raw.split('\n').filter(Boolean)) {
      let message;
      try { message = JSON.parse(part); } catch { continue; }
      this.messages.push(message);
      for (const waiter of [...this.waiters]) {
        if (waiter.predicate(message)) {
          this.waiters.splice(this.waiters.indexOf(waiter), 1);
          clearTimeout(waiter.timer);
          waiter.resolve(message);
        }
      }
    }
  }

  send(command) {
    this.socket.send(JSON.stringify(command));
  }

  wait(predicate, label, timeoutMs = 20_000) {
    const existing = this.messages.find(predicate);
    if (existing) return Promise.resolve(existing);
    return new Promise((resolveWait, rejectWait) => {
      const waiter = {
        predicate,
        resolve: resolveWait,
        timer: setTimeout(() => {
          this.waiters.splice(this.waiters.indexOf(waiter), 1);
          rejectWait(new Error(`Centrifugo ${label} timed out`));
        }, timeoutMs),
      };
      this.waiters.push(waiter);
    });
  }

  close() {
    if (this.socket?.readyState === WebSocket.OPEN) this.socket.close();
  }
}

async function connectAndSubscribe({ recover = false, epoch = '', offset = 0 } = {}) {
  const connection = new CentrifugoConnection();
  await connection.open();
  connection.send({ id: 1, connect: { token: clientToken('poc-user') } });
  const connected = await connection.wait((message) => message.id === 1, 'connect reply');
  assert(connected.connect && !connected.error, `Centrifugo connect failed: ${JSON.stringify(connected)}`);
  const subscribe = { channel: 'poc:site' };
  if (recover) Object.assign(subscribe, { recover: true, epoch, offset });
  connection.send({ id: 2, subscribe });
  const subscribed = await connection.wait((message) => message.id === 2, 'subscribe reply');
  assert(subscribed.subscribe && !subscribed.error, `Centrifugo subscribe failed: ${JSON.stringify(subscribed)}`);
  return { connection, connected, subscribed };
}

async function publishCentrifugo(data) {
  const response = await fetchJSON(`http://127.0.0.1:${ports.centrifugo}/api/publish`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-api-key': centrifugoAPIKey },
    body: JSON.stringify({ channel: 'poc:site', data }),
  });
  assert(response.ok && response.body?.result && !response.body?.error, `Centrifugo publish failed: ${JSON.stringify(response.body)}`);
  return response.body;
}

async function runCentrifugoPOC() {
  await waitFor(async () => {
    const response = await fetch(`http://127.0.0.1:${ports.centrifugo}/metrics`);
    return response.ok;
  }, 'Centrifugo metrics endpoint did not become ready', 90_000);

  const first = await connectAndSubscribe();
  const initialEpoch = first.subscribed.subscribe.epoch ?? '';
  const initialOffset = first.subscribed.subscribe.offset ?? 0;
  const firstPublicationPromise = first.connection.wait(
    (message) => message.push?.channel === 'poc:site' && message.push?.pub?.data?.sequence === 1,
    'first publication',
  );
  await publishCentrifugo({ sequence: 1, value: 'online' });
  const firstPublication = await firstPublicationPromise;
  const lastOffset = firstPublication.push?.pub?.offset ?? initialOffset;
  first.connection.close();
  await new Promise((resolvePause) => setTimeout(resolvePause, 200));

  await publishCentrifugo({ sequence: 2, value: 'offline-window' });
  const recovered = await connectAndSubscribe({ recover: true, epoch: initialEpoch, offset: lastOffset });
  const recoveredPublications = recovered.subscribed.subscribe.publications ?? [];
  assert(recovered.subscribed.subscribe.recovered === true, 'Centrifugo did not mark subscription as recovered');
  assert(recoveredPublications.some((publication) => publication.data?.sequence === 2), 'Centrifugo recovery omitted offline publication');

  const history = await fetchJSON(`http://127.0.0.1:${ports.centrifugo}/api/history`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-api-key': centrifugoAPIKey },
    body: JSON.stringify({ channel: 'poc:site', limit: 10 }),
  });
  assert(history.ok && history.body?.result && !history.body?.error, `Centrifugo history failed: ${JSON.stringify(history.body)}`);
  assert((history.body.result.publications ?? []).length >= 2, 'Centrifugo history did not retain both publications');

  const metrics = await fetch(`http://127.0.0.1:${ports.centrifugo}/metrics`).then((response) => response.text());
  assert(metrics.includes('centrifugo'), 'Centrifugo Prometheus metrics were not exposed');
  recovered.connection.close();

  return {
    schemaVersion: 1,
    component: 'centrifugo',
    status: 'passed',
    authenticatedSubject: 'poc-user',
    channel: 'poc:site',
    firstPublicationOffset: lastOffset,
    recovered: true,
    recoveredPublicationCount: recoveredPublications.length,
    retainedHistoryCount: history.body.result.publications.length,
    prometheusMetrics: true,
    conclusion: 'Transport is viable only with platform-owned authorization and Snapshot/Cursor semantics layered above it.',
  };
}

async function runRiverPOC() {
  const databaseURL = `postgres://postgres:${postgresPassword}@127.0.0.1:${ports.postgres}/legacy_poc?sslmode=disable`;
  const result = await run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=pocs/platform-components/river', 'run', '.',
  ], { env: { ...environment, POC_RIVER_DATABASE_URL: databaseURL } });
  const lines = result.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean).reverse();
  const parsed = lines.map((line) => {
    try { return JSON.parse(line); } catch { return null; }
  }).find((value) => value?.component === 'river');
  assert(parsed?.status === 'passed', `River POC did not return a passing report: ${result.stdout}`);
  return {
    ...parsed,
    conclusion: 'Suitable only for service-owned PostgreSQL background jobs without ambiguous external side effects.',
  };
}

async function runCDCPOC() {
  const create = await fetchJSON(`http://127.0.0.1:${ports.debezium}/connectors`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(connectorConfig()),
  });
  assert(create.status === 201 || create.status === 409, `Debezium connector creation failed: ${create.status} ${JSON.stringify(create.body)}`);
  const initialStatus = await waitConnector();
  await waitFor(async () => (await fetch(`http://127.0.0.1:${ports.redpandaConnect}/ready`)).ok, 'Redpanda Connect did not become ready', 180_000);
  await waitForEvents((events) => events.some((event) => operation(event) === 'r' && rowAfter(event)?.legacy_id === 1001), 'Debezium initial snapshot was not delivered', 180_000);

  await sql("INSERT INTO public.legacy_registry (legacy_id, legacy_type, external_key, display_name) VALUES (1002, 'asset', 'legacy-asset-b', 'Legacy Asset B')");
  await sql("UPDATE public.legacy_registry SET display_name = 'Legacy Asset B Updated', source_revision = source_revision + 1, updated_at = clock_timestamp() WHERE legacy_id = 1002");
  await sql('DELETE FROM public.legacy_registry WHERE legacy_id = 1001');
  await sql("INSERT INTO public.not_captured (id, note) VALUES (2, 'not-captured-marker')");

  const mutationEvents = await waitForEvents((events) => {
    const operations = new Set(events.map(operation));
    return ['r', 'c', 'u', 'd'].every((op) => operations.has(op));
  }, 'Debezium mutation event set was incomplete', 180_000);
  assert(mutationEvents.every((event) => sourceTable(event) === 'legacy_registry'), 'CDC captured a table outside the allowlist');
  assert(!JSON.stringify(mutationEvents).includes('not-captured-marker'), 'CDC leaked the excluded table');
  const forbiddenInventedKeys = ['organization_id', 'site_id', 'equipment_id', 'device_id', 'platform_id'];
  for (const key of forbiddenInventedKeys) {
    assert(!mutationEvents.some((event) => Object.hasOwn(event.value ?? {}, key)), `Redpanda Connect invented ${key}`);
  }

  await compose(['restart', 'debezium']);
  const resumedStatus = await waitConnector();
  await sql("INSERT INTO public.legacy_registry (legacy_id, legacy_type, external_key, display_name) VALUES (1003, 'device', 'legacy-device-c', 'Legacy Device C')");
  const resumedEvents = await waitForEvents(
    (events) => events.some((event) => operation(event) === 'c' && rowAfter(event)?.legacy_id === 1003),
    'Debezium did not resume after restart',
    180_000,
  );
  const logicalResumeEvents = resumedEvents.filter((event) => operation(event) === 'c' && rowAfter(event)?.legacy_id === 1003);
  const uniqueResumeOffsets = new Set(logicalResumeEvents.map(sourceIdentity));
  assert(uniqueResumeOffsets.size >= 1, 'Resumed CDC event did not retain a source offset identity');

  const databaseRows = Number((await sql('SELECT count(*) FROM public.legacy_registry')).stdout.match(/\n\s*(\d+)\s*\n/)?.[1]);
  const excludedRows = Number((await sql('SELECT count(*) FROM public.not_captured')).stdout.match(/\n\s*(\d+)\s*\n/)?.[1]);
  assert(databaseRows === 2, `Unexpected authoritative Legacy row count ${databaseRows}`);
  assert(excludedRows === 2, `Unexpected excluded table row count ${excludedRows}`);

  return {
    schemaVersion: 1,
    component: 'debezium-redpanda-connect',
    status: 'passed',
    initialConnectorState: initialStatus.connector.state,
    resumedConnectorState: resumedStatus.connector.state,
    capturedTable: 'public.legacy_registry',
    excludedTableCaptured: false,
    operationsObserved: [...new Set(resumedEvents.map(operation).filter(Boolean))].sort(),
    transformedEventCount: resumedEvents.length,
    uniqueSourceOffsets: new Set(resumedEvents.map(sourceIdentity)).size,
    resumeLogicalEvents: logicalResumeEvents.length,
    resumeUniqueOffsets: uniqueResumeOffsets.size,
    reverseWritesObserved: false,
    inventedPlatformIdentifiers: false,
    redpandaConnectInput: 'kafka_franz',
    redpandaConnectLicenseReviewRequired: true,
    conclusion: 'Debezium is viable for one-way Legacy CDC; Redpanda Connect remains limited to non-authoritative transformation pending license and deprecation review.',
  };
}

let logs = '';
try {
  await mkdir(outputDir, { recursive: true });
  try {
    await mkdir(lockPath);
    lockAcquired = true;
  } catch (error) {
    if (error?.code === 'EEXIST') throw new Error(`another Docker component POC is already running: ${lockPath}`);
    throw error;
  }
  await compose(['config', '--quiet']);
  composeStarted = true;
  await compose(['up', '-d', '--wait', '--wait-timeout', '240']);
  await resolvePublishedPorts();

  const cdc = await runCDCPOC();
  const centrifugo = await runCentrifugoPOC();
  const river = await runRiverPOC();
  const cdcPath = await writeReport('debezium-redpanda-connect.json', cdc);
  const centrifugoPath = await writeReport('centrifugo.json', centrifugo);
  const riverPath = await writeReport('river.json', river);
  const summaryPath = await writeReport('docker-summary.json', {
    schemaVersion: 1,
    status: 'passed',
    startedAt,
    finishedAt: new Date().toISOString(),
    project,
    publishedPorts: ports,
    components: [cdc, centrifugo, river].map(({ component, status, conclusion }) => ({ component, status, conclusion })),
    reports: [cdcPath, centrifugoPath, riverPath].map((path) => path.slice(root.length + 1).replaceAll('\\', '/')),
    secretsPersisted: false,
  });
  console.log(`Platform Docker component POCs passed: ${summaryPath}`);
} catch (error) {
  if (composeStarted) {
    try { logs = (await compose(['logs', '--no-color', '--tail', '300'])).stdout; } catch { logs = ''; }
  }
  await writeReport('docker-summary.json', {
    schemaVersion: 1,
    status: 'failed',
    startedAt,
    finishedAt: new Date().toISOString(),
    project,
    error: redact(error instanceof Error ? error.message : String(error)),
    publishedPorts: ports,
    logsTail: redact(logs.slice(-40_000)),
    secretsPersisted: false,
  });
  throw error;
} finally {
  if (cleanup && composeStarted) {
    try { await compose(['down', '-v', '--remove-orphans']); } catch (error) { console.error(error.message); }
  }
  if (lockAcquired) {
    try { await rm(lockPath, { recursive: true, force: true }); } catch (error) { console.error(error.message); }
  }
}
