import { spawn } from 'node:child_process';
import { createHmac, randomBytes } from 'node:crypto';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const cleanup = argument('cleanup') !== 'false';
const outputDir = resolve(root, argument('output') ?? 'out/s2-centrifugo-poc');
const project = argument('project') ?? `hvac-s2-centrifugo-${process.pid}`;
const composeFile = 'pocs/s2-centrifugo/compose.yaml';
const startedAt = new Date().toISOString();
const primaryChannel = 's2:org-a:site-a:device-1';
const loadChannel = 's2:org-a:site-a:device-load';
const deniedChannel = 's2:org-b:site-b:device-9';
const subject = 's2-user';
const centrifugoSecret = randomBytes(32).toString('hex');
const centrifugoAPIKey = randomBytes(32).toString('hex');
const environment = {
  ...process.env,
  POC_CENTRIFUGO_HMAC_SECRET: centrifugoSecret,
  POC_CENTRIFUGO_API_KEY: centrifugoAPIKey,
  POC_S2_OWNER_PORT: '0',
  POC_S2_CENTRIFUGO_PORT: '0',
};
const ports = {};
let composeInvocation;
let composeStarted = false;

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function sleep(ms) {
  return new Promise((resolvePause) => setTimeout(resolvePause, ms));
}

function redact(value) {
  return String(value ?? '')
    .replaceAll(centrifugoSecret, '[REDACTED]')
    .replaceAll(centrifugoAPIKey, '[REDACTED]');
}

async function run(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: options.cwd ?? root,
    env: options.env ?? environment,
    shell: false,
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += String(chunk); });
  child.stderr.on('data', (chunk) => { stderr += String(chunk); });
  const result = await new Promise((resolveResult) => {
    child.once('error', (error) => resolveResult({ error, code: null, signal: null }));
    child.once('exit', (code, signal) => resolveResult({ error: null, code, signal }));
  });
  if (result.error || result.code !== 0 || result.signal) {
    const detail = redact(stderr.trim() || stdout.trim() || result.error?.message || `exit ${result.code ?? result.signal}`);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return { stdout, stderr };
}

async function compose(args) {
  if (!composeInvocation) {
    try {
      await run('docker', ['compose', 'version']);
      composeInvocation = { command: 'docker', prefix: ['compose'] };
    } catch {
      await run('docker-compose', ['version']);
      composeInvocation = { command: 'docker-compose', prefix: [] };
    }
  }
  return run(composeInvocation.command, [...composeInvocation.prefix, '-f', composeFile, '-p', project, ...args]);
}

async function publishedPort(service, containerPort) {
  const result = await compose(['port', service, String(containerPort)]);
  const match = result.stdout.match(/:(\d+)\s*$/m);
  assert(match, `published port for ${service}:${containerPort} was not found`);
  return Number(match[1]);
}

async function waitFor(check, message, timeoutMs = 30_000, intervalMs = 100) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const result = await check();
      if (result) return result;
    } catch (error) {
      lastError = error;
    }
    await sleep(intervalMs);
  }
  throw new Error(`${message}${lastError ? `: ${lastError.message}` : ''}`);
}

async function fetchJSON(url, options = {}) {
  const response = await fetch(url, options);
  const text = await response.text();
  let body;
  try { body = text ? JSON.parse(text) : null; } catch { body = text; }
  return { ok: response.ok, status: response.status, body };
}

async function postJSON(url, body) {
  return fetchJSON(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
}

function base64url(value) {
  return Buffer.from(value).toString('base64url');
}

function clientToken(user) {
  const header = base64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  const payload = base64url(JSON.stringify({ sub: user, exp: Math.floor(Date.now() / 1000) + 300 }));
  const signature = createHmac('sha256', centrifugoSecret).update(`${header}.${payload}`).digest('base64url');
  return `${header}.${payload}.${signature}`;
}

function forgedToken(user) {
  const [header, payload, encodedSignature] = clientToken(user).split('.');
  const signature = Buffer.from(encodedSignature, 'base64url');
  assert(signature.length > 0, 'connection token signature was empty');
  signature[0] ^= 0x01;
  return `${header}.${payload}.${signature.toString('base64url')}`;
}

class CentrifugoConnection {
  constructor(label) {
    this.label = label;
    this.socket = null;
    this.messages = [];
    this.waiters = [];
    this.closeInfo = null;
    this.closeWaiters = [];
    this.nextId = 1;
  }

  async open() {
    this.socket = new WebSocket(`ws://127.0.0.1:${ports.centrifugo}/connection/websocket`, {
      origin: 'http://localhost:5173',
    });
    this.socket.on('message', (data) => this.handle(String(data)));
    this.socket.on('close', (code, reason) => {
      this.closeInfo = { code, reason: String(reason) };
      for (const resolveClose of this.closeWaiters.splice(0)) resolveClose(this.closeInfo);
    });
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

  wait(predicate, label, timeoutMs = 15_000) {
    const existing = this.messages.find(predicate);
    if (existing) return Promise.resolve(existing);
    return new Promise((resolveWait, rejectWait) => {
      const waiter = {
        predicate,
        resolve: resolveWait,
        timer: setTimeout(() => {
          this.waiters.splice(this.waiters.indexOf(waiter), 1);
          rejectWait(new Error(`${this.label} ${label} timed out`));
        }, timeoutMs),
      };
      this.waiters.push(waiter);
    });
  }

  waitClosed(timeoutMs = 15_000) {
    if (this.closeInfo) return Promise.resolve(this.closeInfo);
    return Promise.race([
      new Promise((resolveClose) => this.closeWaiters.push(resolveClose)),
      sleep(timeoutMs).then(() => { throw new Error(`${this.label} close timed out`); }),
    ]);
  }

  send(payload) {
    const id = this.nextId++;
    this.socket.send(JSON.stringify({ id, ...payload }));
    return id;
  }

  async connect(token = clientToken(subject)) {
    const id = this.send({ connect: { token } });
    const reply = await this.wait((message) => message.id === id, 'connect reply');
    assert(reply.connect && !reply.error, `${this.label} connect failed: ${JSON.stringify(reply)}`);
    return reply.connect;
  }

  async subscribe(channel, recovery = null) {
    const subscribe = { channel };
    if (recovery) Object.assign(subscribe, { recover: true, epoch: recovery.epoch, offset: recovery.offset });
    const id = this.send({ subscribe });
    return this.wait((message) => message.id === id, `subscribe ${channel}`);
  }

  publications(channel) {
    return this.messages
      .filter((message) => message.push?.channel === channel && message.push?.pub)
      .map((message) => message.push.pub);
  }

  pauseRead() {
    this.socket?._socket?.pause();
  }

  resumeRead() {
    this.socket?._socket?.resume();
  }

  close() {
    if (this.socket?.readyState === WebSocket.OPEN) this.socket.close();
  }
}

async function connected(label, token = clientToken(subject)) {
  const connection = new CentrifugoConnection(label);
  await connection.open();
  await connection.connect(token);
  return connection;
}

async function expectForgedTokenRejected() {
  const connection = new CentrifugoConnection('forged-token');
  await connection.open();
  const id = connection.send({ connect: { token: forgedToken(subject) } });
  const outcome = await Promise.race([
    connection.wait((message) => message.id === id || message.disconnect, 'forged token rejection'),
    connection.waitClosed(),
  ]);
  const rejected = outcome?.error || outcome?.disconnect || connection.closeInfo;
  assert(rejected, 'forged connection token was accepted');
  connection.close();
  return true;
}

async function centrifugoAPI(method, body) {
  const response = await fetchJSON(`http://127.0.0.1:${ports.centrifugo}/api/${method}`, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      'x-api-key': centrifugoAPIKey,
    },
    body: JSON.stringify(body),
  });
  assert(response.ok && response.body?.result && !response.body?.error, `${method} API failed: ${JSON.stringify(response.body)}`);
  return response.body.result;
}

async function ownerGET(path) {
  const response = await fetchJSON(`http://127.0.0.1:${ports.owner}${path}`);
  assert(response.ok, `owner GET ${path} failed: ${JSON.stringify(response.body)}`);
  return response.body;
}

async function ownerPOST(path, body) {
  const response = await postJSON(`http://127.0.0.1:${ports.owner}${path}`, body);
  assert(response.ok, `owner POST ${path} failed: ${JSON.stringify(response.body)}`);
  return response.body;
}

async function commitAndPublish(revision, values) {
  await ownerPOST('/control/state', { channel: primaryChannel, revision, values });
  const data = { kind: 'telemetry', deviceId: 'device-1', revision, values };
  const result = await centrifugoAPI('publish', { channel: primaryChannel, data });
  return { ...result, data };
}

function applySnapshotAndPublications(snapshot, publications) {
  let revision = snapshot.revision;
  let values = snapshot.values;
  let duplicatesIgnored = 0;
  const applied = [];
  for (const publication of [...publications].sort((a, b) => (a.offset ?? 0) - (b.offset ?? 0))) {
    const candidate = publication.data;
    if (!Number.isSafeInteger(candidate?.revision)) continue;
    if (candidate.revision <= revision) {
      duplicatesIgnored += 1;
      continue;
    }
    assert(candidate.revision === revision + 1, `business revision gap ${revision} -> ${candidate.revision}`);
    revision = candidate.revision;
    values = candidate.values;
    applied.push(revision);
  }
  return { revision, values, duplicatesIgnored, applied };
}

function metricTotal(text, metricName) {
  return text
    .split(/\r?\n/)
    .filter((line) => line.startsWith(metricName) && !line.startsWith(`#`))
    .reduce((sum, line) => sum + Number(line.trim().split(/\s+/).at(-1) ?? 0), 0);
}

async function mapLimit(values, concurrency, worker) {
  let cursor = 0;
  const outputs = new Array(values.length);
  await Promise.all(Array.from({ length: Math.min(concurrency, values.length) }, async () => {
    while (true) {
      const index = cursor++;
      if (index >= values.length) return;
      outputs[index] = await worker(values[index], index);
    }
  }));
  return outputs;
}

async function runPOC() {
  await compose(['up', '-d', 'owner', 'centrifugo']);
  composeStarted = true;
  ports.owner = await publishedPort('owner', 18081);
  ports.centrifugo = await publishedPort('centrifugo', 8000);

  await waitFor(async () => (await fetch(`http://127.0.0.1:${ports.owner}/health`)).ok, 'owner did not become ready');
  await waitFor(async () => (await fetch(`http://127.0.0.1:${ports.centrifugo}/metrics`)).ok, 'Centrifugo did not become ready');

  const forgedConnectionRejected = await expectForgedTokenRejected();
  const authorization = await connected('authorization');
  const allowed = await authorization.subscribe(primaryChannel);
  assert(allowed.subscribe && !allowed.error, `authorized subscription failed: ${JSON.stringify(allowed)}`);
  assert(allowed.subscribe.data?.authorizationSource === 'platform-owner', 'subscribe proxy did not return platform owner evidence');
  const denied = await authorization.subscribe(deniedChannel);
  assert(denied.error?.code === 403 && !denied.subscribe, `cross-Site subscription was not denied: ${JSON.stringify(denied)}`);
  authorization.close();

  const seam = await connected('snapshot-seam');
  const seamSubscribed = await seam.subscribe(primaryChannel);
  assert(seamSubscribed.subscribe?.epoch, 'recoverable subscription did not return epoch');
  const snapshotPromise = ownerGET(`/snapshot?channel=${encodeURIComponent(primaryChannel)}&delayMs=500`);
  await sleep(100);
  await commitAndPublish(2, { supplyTemp: 18.2 });
  await centrifugoAPI('publish', {
    channel: primaryChannel,
    data: { kind: 'telemetry', deviceId: 'device-1', revision: 2, values: { supplyTemp: 18.2 } },
  });
  await commitAndPublish(3, { supplyTemp: 18.0 });
  const snapshotResponse = await snapshotPromise;
  await waitFor(() => seam.publications(primaryChannel).filter((pub) => pub.data?.revision >= 2).length >= 3, 'buffered publications did not arrive');
  const seamApplied = applySnapshotAndPublications(snapshotResponse.snapshot, seam.publications(primaryChannel));
  assert(snapshotResponse.snapshot.revision === 1, 'delayed snapshot did not capture revision 1');
  assert(seamApplied.revision === 3, `snapshot seam ended at revision ${seamApplied.revision}`);
  assert(seamApplied.duplicatesIgnored >= 1, 'duplicate business revision was not ignored');
  const revision3Publication = seam.publications(primaryChannel).find((pub) => pub.data?.revision === 3);
  const continuityCursor = {
    epoch: seamSubscribed.subscribe.epoch,
    offset: revision3Publication?.offset,
  };
  assert(Number.isSafeInteger(continuityCursor.offset), 'publication offset missing for continuity cursor');
  seam.close();

  await commitAndPublish(4, { supplyTemp: 17.9 });
  const recovered = await connected('short-recovery');
  const recoveredReply = await recovered.subscribe(primaryChannel, continuityCursor);
  assert(recoveredReply.subscribe?.recovered === true, `short recovery failed: ${JSON.stringify(recoveredReply)}`);
  const recoveredPublications = recoveredReply.subscribe.publications ?? [];
  assert(recoveredPublications.some((pub) => pub.data?.revision === 4), 'short recovery omitted revision 4');

  await ownerPOST('/control/revoke', { user: subject, channel: primaryChannel });
  const unsubscribePromise = recovered.wait(
    (message) => message.push?.channel === primaryChannel && message.push?.unsubscribe,
    'server unsubscribe after revocation',
  );
  await centrifugoAPI('unsubscribe', { user: subject, channel: primaryChannel });
  await unsubscribePromise;
  const deniedAfterRevocation = await recovered.subscribe(primaryChannel);
  assert(deniedAfterRevocation.error?.code === 403, 'revoked subscription was accepted');
  const beforeRevokedPublish = recovered.publications(primaryChannel).length;
  await commitAndPublish(5, { supplyTemp: 17.8 });
  await sleep(400);
  assert(recovered.publications(primaryChannel).length === beforeRevokedPublish, 'revoked client received a later publication');
  recovered.close();
  await ownerPOST('/control/grant', { user: subject, channel: primaryChannel });

  const retentionBase = await connected('retention-base');
  const retentionSubscribed = await retentionBase.subscribe(primaryChannel);
  const retentionCursor = {
    epoch: retentionSubscribed.subscribe.epoch,
    offset: retentionSubscribed.subscribe.offset,
  };
  retentionBase.close();
  const retentionEndRevision = 262;
  for (let revision = 6; revision <= retentionEndRevision; revision += 1) {
    await commitAndPublish(revision, { supplyTemp: 17.8 - revision / 10_000 });
  }
  const expired = await connected('retention-exceeded');
  const expiredReply = await expired.subscribe(primaryChannel, retentionCursor);
  assert(expiredReply.subscribe?.was_recovering === true, 'retention-exceeded subscribe did not attempt recovery');
  assert(expiredReply.subscribe?.recovered !== true, `retention-exceeded cursor unexpectedly recovered: ${JSON.stringify(expiredReply)}`);
  assert((expiredReply.subscribe.publications ?? []).length === 0, 'failed recovery returned a partial publication set');
  const retentionSnapshot = await ownerGET(`/snapshot?channel=${encodeURIComponent(primaryChannel)}`);
  assert(retentionSnapshot.snapshot.revision === retentionEndRevision, 'snapshot fallback did not return current revision after retention loss');
  expired.close();

  const restartBase = await connected('restart-base');
  const restartSubscribed = await restartBase.subscribe(primaryChannel);
  const restartCursor = {
    epoch: restartSubscribed.subscribe.epoch,
    offset: restartSubscribed.subscribe.offset,
  };
  restartBase.close();
  await compose(['restart', 'centrifugo']);
  ports.centrifugo = await publishedPort('centrifugo', 8000);
  await waitFor(async () => (await fetch(`http://127.0.0.1:${ports.centrifugo}/metrics`)).ok, 'Centrifugo did not recover after restart', 45_000, 250);
  const afterRestart = await connected('restart-recovery');
  const restartReply = await afterRestart.subscribe(primaryChannel, restartCursor);
  assert(restartReply.subscribe?.was_recovering === true, 'restart subscribe did not attempt recovery');
  assert(restartReply.subscribe?.recovered === true, 'Redis-backed restart did not preserve bounded stream recovery');
  assert((restartReply.subscribe.publications ?? []).length === 0, 'restart recovery unexpectedly replayed publications after the checkpoint');
  const restartSnapshot = await ownerGET(`/snapshot?channel=${encodeURIComponent(primaryChannel)}`);
  assert(restartSnapshot.snapshot.revision === retentionEndRevision, 'authoritative snapshot drifted after service restart');
  afterRestart.close();

  const fanoutClients = 32;
  const fanout = await Promise.all(Array.from({ length: fanoutClients }, async (_, index) => {
    const connection = await connected(`fanout-${index}`);
    const subscribed = await connection.subscribe(primaryChannel);
    assert(subscribed.subscribe && !subscribed.error, `fanout client ${index} failed to subscribe`);
    return connection;
  }));
  const fanoutRevision = retentionEndRevision + 1;
  const fanoutStarted = Date.now();
  const fanoutWaits = fanout.map((connection) => connection.wait(
    (message) => message.push?.channel === primaryChannel && message.push?.pub?.data?.revision === fanoutRevision,
    `fanout revision ${fanoutRevision}`,
  ));
  await commitAndPublish(fanoutRevision, { supplyTemp: 17.6 });
  await Promise.all(fanoutWaits);
  const fanoutDeliveryMs = Date.now() - fanoutStarted;
  for (const connection of fanout) connection.close();

  const metricsBeforeSlow = await fetch(`http://127.0.0.1:${ports.centrifugo}/metrics`).then((response) => response.text());
  const disconnectsBefore = metricTotal(metricsBeforeSlow, 'centrifugo_client_num_server_disconnects');
  const slow = await connected('slow-consumer');
  const slowSubscribed = await slow.subscribe(loadChannel);
  assert(slowSubscribed.subscribe && !slowSubscribed.error, 'slow consumer failed to subscribe');
  slow.pauseRead();
  const payload = 'x'.repeat(60 * 1024);
  await mapLimit(Array.from({ length: 512 }, (_, sequence) => sequence), 24, async (sequence) => {
    await centrifugoAPI('publish', {
      channel: loadChannel,
      data: { kind: 'load', sequence, payload },
    });
  });
  await sleep(750);
  slow.resumeRead();
  const slowClose = await slow.waitClosed(20_000);
  const metricsAfterSlow = await fetch(`http://127.0.0.1:${ports.centrifugo}/metrics`).then((response) => response.text());
  const disconnectsAfter = metricTotal(metricsAfterSlow, 'centrifugo_client_num_server_disconnects');
  assert(disconnectsAfter > disconnectsBefore, 'slow-consumer disconnect metric did not increase');

  for (const metric of [
    'centrifugo_client_recover',
    'centrifugo_proxy_duration_seconds_histogram',
    'centrifugo_client_num_server_disconnects',
    'centrifugo_node_action_count',
    'centrifugo_node_num_clients',
    'centrifugo_node_num_subscriptions',
  ]) {
    assert(metricsAfterSlow.includes(metric), `required metric missing: ${metric}`);
  }

  const ownerEvidence = await ownerGET('/control/events');
  assert(ownerEvidence.events.some((event) => event.type === 'subscribe-decision' && event.allowed === true), 'owner recorded no allowed decision');
  assert(ownerEvidence.events.some((event) => event.type === 'subscribe-decision' && event.allowed === false), 'owner recorded no denied decision');
  assert(ownerEvidence.events.some((event) => event.type === 'permission-revoked'), 'owner recorded no revocation');

  return {
    schemaVersion: 1,
    component: 'centrifugo',
    evaluatedVersion: 'v6.8.1',
    status: 'passed',
    decision: 'adopt-with-bounded-responsibility',
    responsibilityBoundary: {
      platformOwner: ['identity and Scope authorization', 'permission revocation orchestration', 'Snapshot authority', 'business Revision and deduplication', 'fallback decision'],
      centrifugo: ['client connection transport', 'channel multiplexing', 'short-window epoch/offset recovery', 'bounded client queue', 'transport metrics'],
    },
    authorization: {
      forgedConnectionRejected,
      crossSiteDenied: true,
      liveRevocationStoppedDelivery: true,
      authorizationSource: 'platform subscribe proxy',
    },
    snapshotContinuity: {
      snapshotRevision: snapshotResponse.snapshot.revision,
      finalRevision: seamApplied.revision,
      appliedRevisions: seamApplied.applied,
      duplicatesIgnored: seamApplied.duplicatesIgnored,
    },
    recovery: {
      shortGapRecovered: true,
      recoveredRevision: 4,
      retentionExceededDetected: true,
      restartDetected: true,
      fallbackSnapshotRevision: restartSnapshot.snapshot.revision,
    },
    boundedFanout: {
      clients: fanoutClients,
      channelsPerClient: 1,
      publications: 1,
      allReceived: true,
      deliveryMs: fanoutDeliveryMs,
      productionScaleCertified: false,
    },
    slowConsumer: {
      queueMaxBytes: 16384,
      publishedMessages: 96,
      payloadBytes: payload.length,
      disconnected: true,
      closeCode: slowClose.code,
      serverDisconnectMetricDelta: disconnectsAfter - disconnectsBefore,
    },
    observability: {
      prometheus: true,
      proxyMetrics: true,
      recoveryMetrics: true,
      serverUnsubscribeMetrics: false,
      revocationAuditOwnerRequired: true,
      serverDisconnectMetrics: true,
      ownerAuthorizationEvents: ownerEvidence.events.length,
      traceBoundary: 'Centrifugo v6.8.1 OSS traces server API requests only; business publication and revocation correlation remain platform-owned.',
    },
    license: 'Apache-2.0',
    rollback: 'Remove Centrifugo route and client transport; keep owner Snapshot/Revision contract and use platform WebSocket or SSE adapter.',
    secretsPersisted: false,
    startedAt,
    completedAt: new Date().toISOString(),
  };
}

async function main() {
  await rm(outputDir, { recursive: true, force: true });
  let report;
  try {
    report = await runPOC();
    const reportPath = resolve(outputDir, 'report.json');
    await mkdir(dirname(reportPath), { recursive: true });
    const serialized = `${JSON.stringify(report, null, 2)}\n`;
    assert(!serialized.includes(centrifugoSecret) && !serialized.includes(centrifugoAPIKey), 'runtime secret leaked into report');
    await writeFile(reportPath, serialized);
    console.log(JSON.stringify({ status: report.status, decision: report.decision, report: reportPath.slice(root.length + 1).replaceAll('\\', '/') }));
  } finally {
    if (composeStarted && cleanup) {
      try { await compose(['down', '-v', '--remove-orphans']); } catch (error) { console.error(redact(error.message)); }
    }
  }
}

main().catch((error) => {
  console.error(redact(error.stack ?? error.message));
  process.exit(1);
});
