import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { createServer as createHTTPSServer } from 'node:https';
import { dirname, join, resolve } from 'node:path';
import { pullDockerImageWithRetry, runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const outputDir = resolve(root, process.env.S2_MQTT_REPORT_DIR ?? 'out/s2-mqtt-integration');
const pkiDir = join(outputDir, 'pki');
const queueDir = join(outputDir, 'gateway-queue');
const binDir = join(outputDir, 'bin');
const reportPath = join(outputDir, 'integration.json');
const composePath = resolve(root, 'infra/telemetry/mqtt/compose.yaml');
const projectName = `hvac-s2-mqtt-${process.pid}`;
const tenantId = '018f3d00-0000-7000-8000-000000000001';
const siteId = '018f3e00-1000-7000-8000-000000000001';
const integrationInstanceId = '018f3e00-0000-7000-8000-000000000101';
const gatewayId = 'EG8200-COMMERCIAL-001';
const expectedDeviceIds = new Set([
  '018f3e00-4000-7000-8000-000000000001',
  '018f3e00-4000-7000-8000-000000000002',
  '018f3e00-4000-7000-8000-000000000003',
  '018f3e00-4000-7000-8000-000000000004',
  '018f3e00-4000-7000-8000-000000000005',
  '018f3e00-4000-7000-8000-000000000006',
  '018f3e00-4000-7000-8000-000000000007',
]);
const plantConfigPath = resolve(root, 'tools/eg8200-simulator/configs/central-plant.local.json');
const plantConfigDocument = JSON.parse(await readFile(plantConfigPath, 'utf8'));
const expectedPointCount = Number(plantConfigDocument.points?.length ?? 0);
if (!Number.isInteger(expectedPointCount) || expectedPointCount < 1) throw new Error('central plant MQTT fixture has no telemetry points');
const uuidV7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const children = new Set();
let s2Server;

const report = {
  schemaVersion: 1,
  capability: 's2-mqtt-telemetry',
  status: 'failed',
  startedAt: new Date().toISOString(),
  brokerImage: 'eclipse-mosquitto:2.1.2-alpine@sha256:6f8d8a947c506f8a2290ec65cd4bd2bc7cb4d43fb5f6271f861cb013e2ef9797',
  assertions: {},
};

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function start(command, args, options = {}) {
  const child = spawn(command, args, { cwd: root, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true, ...options });
  children.add(child);
  child.once('exit', () => children.delete(child));
  return child;
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('MQTT integration port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

async function waitForHTTP(url, expectedStatus = 200, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = '';
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.status === expectedStatus) return;
      lastError = `status ${response.status}`;
    } catch (error) {
      lastError = String(error);
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 200));
  }
  throw new Error(`timed out waiting for ${url}: ${lastError}`);
}

async function waitFor(predicate, timeoutMs, description) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error(`timed out waiting for ${description}`);
}

async function readPrometheusMetric(url, name) {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`metrics request failed for ${url}: ${response.status}`);
  const body = await response.text();
  const line = body.split('\n').find((candidate) => candidate.startsWith(`${name} `));
  if (!line) throw new Error(`metric ${name} was not found at ${url}`);
  const value = Number(line.slice(name.length).trim());
  if (!Number.isFinite(value)) throw new Error(`metric ${name} is not numeric at ${url}: ${line}`);
  return value;
}

function compose(args, env) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env });
}

async function stopChild(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  await Promise.race([once(child, 'exit'), new Promise((resolveWait) => setTimeout(resolveWait, 5000))]);
  if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
}

try {
  const dockerPathProbe = spawnSync('which', ['docker'], { cwd: root, encoding: 'utf8', windowsHide: true });
  const dockerPath = String(dockerPathProbe.stdout ?? '').trim();
  if (dockerPathProbe.status !== 0 || !dockerPath.startsWith('/') || dockerPath.startsWith('/mnt/')) {
    throw new Error(`Linux docker CLI is required for the MQTT integration gate; resolved=${dockerPath || 'missing'}`);
  }
  if (spawnSync(dockerPath, ['version'], { stdio: 'ignore', windowsHide: true }).status !== 0) {
    throw new Error('Linux docker daemon is required for the MQTT integration gate');
  }
  await rm(outputDir, { recursive: true, force: true });
  await mkdir(binDir, { recursive: true });
  await mkdir(queueDir, { recursive: true });

  const pkiGenerator = join(binDir, 'generate-central-plant-pki');
  const adapterBinary = join(binDir, 'mqtt-telemetry-adapter');
  const publisherBinary = join(binDir, 'eg8200-mqtt-publisher');
  run(process.execPath, ['scripts/run-go.mjs', 'build', '-o', pkiGenerator, './tools/s0-auth-fixture/cmd/generate-central-plant-pki']);
  run(process.execPath, ['scripts/run-go.mjs', 'build', '-o', adapterBinary, './cmd/iot-service']);
  run(process.execPath, ['scripts/run-go.mjs', 'build', '-o', publisherBinary, './tools/eg8200-simulator/cmd/eg8200-mqtt-publisher']);
  run(pkiGenerator, [pkiDir]);

  const [mqttPort, s2Port, diagnosticsPort, publisherDiagnosticsPort] = await Promise.all([findAvailablePort(), findAvailablePort(), findAvailablePort(), findAvailablePort()]);
  const observations = [];
  let sourceIdentityVerified = false;
  let transientFailures = 0;
  let failNextObservation = true;
  s2Server = createHTTPSServer({
    key: await readFile(join(pkiDir, 'telemetry-key.pem')),
    cert: await readFile(join(pkiDir, 'telemetry-cert.pem')),
    ca: await readFile(join(pkiDir, 'ca.pem')),
    minVersion: 'TLSv1.3',
    requestCert: true,
    rejectUnauthorized: true,
  }, async (request, response) => {
    if (request.method !== 'POST' || request.url !== '/internal/v1/telemetry/sources/observations:accept') {
      response.writeHead(404).end();
      return;
    }
    const peer = request.socket.getPeerCertificate();
    sourceIdentityVerified = peer?.subject?.CN === 'mqtt-telemetry-adapter'
      && String(peer?.subjectaltname ?? '').includes('URI:spiffe://hvac.local/mqtt-telemetry-adapter');
    const chunks = [];
    let total = 0;
    for await (const chunk of request) {
      total += chunk.length;
      if (total > 256 * 1024) {
        response.writeHead(413).end();
        return;
      }
      chunks.push(chunk);
    }
    const observation = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    if (failNextObservation) {
      failNextObservation = false;
      transientFailures += 1;
      response.writeHead(503, { 'content-type': 'application/json' });
      response.end('{"error":"temporary test outage"}');
      return;
    }
    observations.push(observation);
    const body = JSON.stringify({
      observationId: randomUUID(),
      evidenceId: '',
      status: 'ACCEPTED',
      quality: 'GOOD',
      qualityReasons: [],
      quarantineReason: '',
      deviceId: observation.externalId,
      businessRevision: observations.length,
      stateChanged: true,
      positionAdvanced: true,
    });
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(body);
  });
  s2Server.listen({ host: '127.0.0.1', port: s2Port });
  await once(s2Server, 'listening');

  const composeEnvironment = {
    ...process.env,
    MQTT_PKI_DIR: pkiDir,
    MQTT_HOST_PORT: String(mqttPort),
  };
  await pullDockerImageWithRetry(report.brokerImage, { cwd: root, env: composeEnvironment });
  try { compose(['down', '--volumes', '--remove-orphans'], composeEnvironment); } catch {}
  compose(['up', '-d', '--pull=never', 'mqtt-broker'], composeEnvironment);

  const adapterConfigPath = join(outputDir, 'adapter.json');
  await writeFile(adapterConfigPath, `${JSON.stringify({
    schemaVersion: 1,
    integrationInstanceId,
    mqtt: {
      brokerUrl: `tls://127.0.0.1:${mqttPort}`,
      clientId: 'mqtt-telemetry-adapter',
      topicFilter: 'energy/v1/+/+/+/telemetry',
      caFile: join(pkiDir, 'ca.pem'),
      certFile: join(pkiDir, 'mqtt-adapter-cert.pem'),
      keyFile: join(pkiDir, 'mqtt-adapter-key.pem'),
      serverName: 'localhost',
      keepAliveSeconds: 30,
      sessionExpirySeconds: 86400,
      connectTimeoutSeconds: 10,
    },
    telemetryRuntime: {
      baseUrl: `https://127.0.0.1:${s2Port}`,
      caFile: join(pkiDir, 'ca.pem'),
      certFile: join(pkiDir, 'mqtt-adapter-cert.pem'),
      keyFile: join(pkiDir, 'mqtt-adapter-key.pem'),
      serverName: 'localhost',
    },
    gatewayScopes: [{ gatewayId, tenantId, siteId }],
    processingQueueCapacity: 1024,
  }, null, 2)}\n`);

  const gatewayConfigPath = join(outputDir, 'gateway.json');
  await writeFile(gatewayConfigPath, `${JSON.stringify({
    schemaVersion: 1,
    tenantId,
    siteId,
    brokerUrl: `tls://127.0.0.1:${mqttPort}`,
    clientId: gatewayId,
    caFile: join(pkiDir, 'ca.pem'),
    certFile: join(pkiDir, 'mqtt-gateway-cert.pem'),
    keyFile: join(pkiDir, 'mqtt-gateway-key.pem'),
    serverName: 'localhost',
    queueDirectory: queueDir,
    maximumQueueBytes: 536870912,
    deviceExternalIdByDeviceId: {
      'CHILLER-01': '018f3e00-4000-7000-8000-000000000001',
      'CHWP-01': '018f3e00-4000-7000-8000-000000000002',
      'CWP-01': '018f3e00-4000-7000-8000-000000000003',
      'CT-01': '018f3e00-4000-7000-8000-000000000004',
      'METER-HVAC-TOTAL': '018f3e00-4000-7000-8000-000000000005',
      'BTU-METER-01': '018f3e00-4000-7000-8000-000000000006',
      'WEATHER-STATION-01': '018f3e00-4000-7000-8000-000000000007',
    },
  }, null, 2)}\n`);

  const adapter = start(adapterBinary, ['-config', adapterConfigPath, '-diagnostics-addr', `127.0.0.1:${diagnosticsPort}`]);
  let adapterStdout = '';
  let adapterStderr = '';
  adapter.stdout.on('data', (chunk) => { adapterStdout += chunk.toString(); });
  adapter.stderr.on('data', (chunk) => { adapterStderr += chunk.toString(); });
  await waitForHTTP(`http://127.0.0.1:${diagnosticsPort}/health/ready`, 200, 30000);

  const allowedTopic = `energy/v1/${tenantId}/${siteId}/${gatewayId}/telemetry`;
  const poison = spawnSync('docker', ['compose', '-p', projectName, '-f', composePath, 'exec', '-T', 'mqtt-broker',
    'mosquitto_pub', '-h', 'localhost', '-p', '8883', '-V', 'mqttv5', '-q', '1',
    '--cafile', '/mosquitto/config/pki/ca.pem', '--cert', '/mosquitto/config/pki/mqtt-gateway-cert.pem', '--key', '/mosquitto/config/pki/mqtt-gateway-key.pem',
    '-t', allowedTopic, '-m', '{}'], { cwd: root, env: composeEnvironment, encoding: 'utf8', windowsHide: true });
  if (poison.status !== 0) throw new Error(`Mosquitto rejected the authorized poison-message probe: ${poison.stderr || poison.stdout}`);
  await new Promise((resolveWait) => setTimeout(resolveWait, 750));
  await waitForHTTP(`http://127.0.0.1:${diagnosticsPort}/health/ready`, 200, 5000);
  if (observations.length !== 0) throw new Error('invalid MQTT poison message reached S2');

  let publisher = start(publisherBinary, [
    '-plant-config', resolve(root, 'tools/eg8200-simulator/configs/central-plant.local.json'),
    '-mqtt-config', gatewayConfigPath,
    '-diagnostics-addr', `127.0.0.1:${publisherDiagnosticsPort}`,
  ]);
  let publisherStdout = '';
  let publisherStderr = '';
  publisher.stdout.on('data', (chunk) => { publisherStdout += chunk.toString(); });
  publisher.stderr.on('data', (chunk) => { publisherStderr += chunk.toString(); });
  await waitForHTTP(`http://127.0.0.1:${publisherDiagnosticsPort}/health/ready`, 200, 30000);
  await waitFor(() => observations.length >= expectedPointCount, 30000, `${expectedPointCount} MQTT observations`);
  const adapterReadyURL = `http://127.0.0.1:${diagnosticsPort}/health/ready`;
  const publisherReadyURL = `http://127.0.0.1:${publisherDiagnosticsPort}/health/ready`;
  const adapterMetricsURL = `http://127.0.0.1:${diagnosticsPort}/metrics`;
  const publisherMetricsURL = `http://127.0.0.1:${publisherDiagnosticsPort}/metrics`;
  const [adapterMetricsResponse, publisherMetricsResponse] = await Promise.all([
    fetch(adapterMetricsURL),
    fetch(publisherMetricsURL),
  ]);
  if (!adapterMetricsResponse.ok || !publisherMetricsResponse.ok) throw new Error('MQTT observability endpoints were not readable');
  const [adapterMetrics, publisherMetrics] = await Promise.all([adapterMetricsResponse.text(), publisherMetricsResponse.text()]);
  for (const marker of ['hvac_mqtt_connected 1', 'hvac_mqtt_messages_processed_total', 'hvac_mqtt_message_retries_total', 'hvac_mqtt_values_total']) {
    if (!adapterMetrics.includes(marker)) throw new Error(`MQTT adapter metrics missing ${marker}: ${adapterMetrics}`);
  }
  for (const marker of ['hvac_edge_mqtt_connected 1', 'hvac_edge_mqtt_publishes_total', 'hvac_edge_mqtt_values_total', 'hvac_edge_mqtt_queue_bytes', 'hvac_edge_mqtt_queue_utilization_ratio']) {
    if (!publisherMetrics.includes(marker)) throw new Error(`Edge MQTT metrics missing ${marker}: ${publisherMetrics}`);
  }

  await waitFor(async () => (await readPrometheusMetric(publisherMetricsURL, 'hvac_edge_mqtt_queue_bytes')) === 0, 10000, 'empty Edge queue before broker outage');
  const observationsBeforeBrokerOutage = observations.length;
  compose(['stop', 'mqtt-broker'], composeEnvironment);
  await waitForHTTP(publisherReadyURL, 503, 15000);
  await new Promise((resolveWait) => setTimeout(resolveWait, 750));
  const observationsAtBrokerDown = observations.length;
  let offlineQueueBytes = 0;
  await waitFor(async () => {
    offlineQueueBytes = await readPrometheusMetric(publisherMetricsURL, 'hvac_edge_mqtt_queue_bytes');
    return offlineQueueBytes > 0;
  }, 15000, 'Edge Store & Forward queue growth while MQTT is unavailable');
  await new Promise((resolveWait) => setTimeout(resolveWait, 1250));
  if (observations.length !== observationsAtBrokerDown) {
    throw new Error(`S2 continued receiving observations after MQTT broker outage: before=${observationsAtBrokerDown} after=${observations.length}`);
  }

  const sequenceStatePath = join(queueDir, 'measurement-sequences.v1.json');
  const sequenceStateBeforeRestart = JSON.parse(await readFile(sequenceStatePath, 'utf8'));
  const sequenceValuesBeforeRestart = Object.values(sequenceStateBeforeRestart.sequences ?? {}).map(Number);
  if (sequenceStateBeforeRestart.schemaVersion !== 1 || sequenceValuesBeforeRestart.length !== expectedPointCount || sequenceValuesBeforeRestart.some((value) => !Number.isInteger(value) || value < 1)) {
    throw new Error(`Edge measurement sequence state before restart is invalid: ${JSON.stringify(sequenceStateBeforeRestart)}`);
  }
  const maxSequenceBeforeRestart = Math.max(...sequenceValuesBeforeRestart);
  offlineQueueBytes = await readPrometheusMetric(publisherMetricsURL, 'hvac_edge_mqtt_queue_bytes');
  await stopChild(publisher);

  compose(['start', 'mqtt-broker'], composeEnvironment);
  await waitForHTTP(adapterReadyURL, 200, 30000);
  publisher = start(publisherBinary, [
    '-plant-config', resolve(root, 'tools/eg8200-simulator/configs/central-plant.local.json'),
    '-mqtt-config', gatewayConfigPath,
    '-diagnostics-addr', `127.0.0.1:${publisherDiagnosticsPort}`,
  ]);
  publisher.stdout.on('data', (chunk) => { publisherStdout += chunk.toString(); });
  publisher.stderr.on('data', (chunk) => { publisherStderr += chunk.toString(); });
  await waitForHTTP(publisherReadyURL, 200, 30000);
  await waitFor(() => observations.length > observationsAtBrokerDown, 30000, 'queued Edge observations after MQTT recovery');
  const recoveredObservationCount = observations.length - observationsAtBrokerDown;
  let queueBytesAfterRecovery = offlineQueueBytes;
  await waitFor(async () => {
    queueBytesAfterRecovery = await readPrometheusMetric(publisherMetricsURL, 'hvac_edge_mqtt_queue_bytes');
    return queueBytesAfterRecovery === 0;
  }, 30000, 'Edge Store & Forward queue drain after MQTT recovery');
  await waitFor(
    () => observations.some((observation) => Number(observation.sourcePosition?.offset) > maxSequenceBeforeRestart),
    30000,
    'post-restart observation with a sequence above the persisted pre-restart maximum',
  );
  const sequenceStateAfterRestart = JSON.parse(await readFile(sequenceStatePath, 'utf8'));
  const maxSequenceAfterRestart = Math.max(...Object.values(sequenceStateAfterRestart.sequences ?? {}).map(Number));
  if (maxSequenceAfterRestart <= maxSequenceBeforeRestart) {
    throw new Error(`Edge measurement sequence did not advance across restart: before=${maxSequenceBeforeRestart} after=${maxSequenceAfterRestart}`);
  }
  if (recoveredObservationCount <= 0 || offlineQueueBytes <= 0 || observationsAtBrokerDown < observationsBeforeBrokerOutage) {
    throw new Error('MQTT offline Store & Forward recovery evidence is incomplete');
  }

  await stopChild(publisher);
  await stopChild(adapter);

  if (!sourceIdentityVerified) throw new Error('MQTT adapter mTLS SPIFFE identity was not verified');
  if (transientFailures !== 1 || !adapterStdout.includes('mqtt_telemetry_message_retrying')) {
    throw new Error(`MQTT adapter did not recover the injected S2 failure; failures=${transientFailures} stdout=${adapterStdout} stderr=${adapterStderr}`);
  }
  if (!adapterStdout.includes('mqtt_telemetry_message_dropped')) {
    throw new Error(`MQTT adapter did not drop the permanent poison message; stdout=${adapterStdout} stderr=${adapterStderr}`);
  }
  if (observations.length < expectedPointCount) throw new Error(`expected at least ${expectedPointCount} observations, got ${observations.length}`);
  const initialObservations = observations.slice(0, expectedPointCount);
  const deviceIds = new Set(initialObservations.map((observation) => observation.externalId));
  if (deviceIds.size !== 7 || [...deviceIds].some((deviceId) => !expectedDeviceIds.has(deviceId))) {
    throw new Error(`unexpected MQTT device identities: ${JSON.stringify([...deviceIds])}`);
  }
  for (const observation of initialObservations) {
    if (observation.integrationInstanceId !== integrationInstanceId || observation.sourcePath !== 'PUSH' || observation.externalEntityType !== 'DEVICE') {
      throw new Error(`invalid S2 MQTT observation identity: ${JSON.stringify(observation)}`);
    }
    if (!String(observation.sourcePosition?.partition ?? '').startsWith(`mqtt:${gatewayId}:`)
      || observation.sourcePosition?.offset !== 1
      || !uuidV7.test(String(observation.sourcePosition?.eventId ?? ''))) {
      throw new Error(`invalid MQTT Source Position: ${JSON.stringify(observation.sourcePosition)}`);
    }
  }

  const deniedTopic = `energy/v1/${tenantId}/018f3e00-1000-7000-8000-000000000099/${gatewayId}/telemetry`;
  const denied = spawnSync('docker', ['compose', '-p', projectName, '-f', composePath, 'exec', '-T', 'mqtt-broker',
    'mosquitto_pub', '-d', '-h', 'localhost', '-p', '8883', '-V', 'mqttv5', '-q', '1',
    '--cafile', '/mosquitto/config/pki/ca.pem', '--cert', '/mosquitto/config/pki/mqtt-gateway-cert.pem', '--key', '/mosquitto/config/pki/mqtt-gateway-key.pem',
    '-t', deniedTopic, '-m', '{}'], { cwd: root, env: composeEnvironment, encoding: 'utf8', windowsHide: true });
  const deniedOutput = `${denied.stdout ?? ''}\n${denied.stderr ?? ''}`;
  if (!deniedOutput.includes('RC:135') || !deniedOutput.toLowerCase().includes('not authorized')) {
    throw new Error(`Mosquitto ACL probe did not return an explicit MQTT v5 Not authorized PUBACK; status=${denied.status} output=${deniedOutput}`);
  }

  report.assertions = {
    brokerMutualTLS: true,
    brokerGatewayACL: true,
    adapterSPIFFEIdentity: true,
    adapterMetrics: true,
    edgePublisherMetrics: true,
    poisonMessageDropped: true,
    transientS2RetryRecovered: transientFailures === 1,
    brokerOutageDetectedByPublisherReadiness: true,
    observationsBeforeBrokerOutage,
    observationsAtBrokerDown,
    edgeOfflineQueueBytes: offlineQueueBytes,
    edgeOfflineS2ObservationCountStable: true,
    edgeStoreAndForwardRecovered: recoveredObservationCount > 0,
    recoveredObservationCount,
    edgeQueueBytesAfterRecovery: queueBytesAfterRecovery,
    gatewayProcessRestartRecovered: true,
    sequenceStatePointCount: sequenceValuesBeforeRestart.length,
    maxSequenceBeforeRestart,
    maxSequenceAfterRestart,
    sequenceMonotonicAcrossRestart: maxSequenceAfterRestart > maxSequenceBeforeRestart,
    observationCount: initialObservations.length,
    deviceCount: deviceIds.size,
    sourcePath: 'PUSH',
    sourcePartitionPrefix: `mqtt:${gatewayId}:`,
    qos: 1,
  };
  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S2 MQTT telemetry integration passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  for (const child of [...children]) await stopChild(child);
  if (s2Server) {
    await new Promise((resolveClose) => s2Server.close(() => resolveClose()));
  }
  try {
    const composeEnvironment = { ...process.env, MQTT_PKI_DIR: pkiDir, MQTT_HOST_PORT: '18883' };
    compose(['down', '--volumes', '--remove-orphans'], composeEnvironment);
  } catch {}
}
