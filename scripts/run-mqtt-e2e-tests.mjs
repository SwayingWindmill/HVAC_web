// End-to-end MQTT uplink gate: the EG8200 publisher, the real Mosquitto broker and ACL,
// Connectivity resolving the Gateway, Devices and Points from a real Registry database,
// and a stand-in Telemetry Runtime that records what Connectivity sends.
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { chmod, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { createServer as createHTTPSServer } from 'node:https';
import { dirname, join, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const outputDir = resolve(root, process.env.MQTT_E2E_REPORT_DIR ?? 'out/mqtt-e2e');
const pkiDir = join(outputDir, 'pki');
const queueDir = join(outputDir, 'gateway-queue');
const binDir = join(outputDir, 'bin');
const reportPath = join(outputDir, 'integration.json');
const brokerCompose = resolve(root, 'infra/telemetry/mqtt/compose.yaml');
const registryCompose = resolve(root, 'infra/registry/compose.yaml');
const brokerProject = `hvac-mqtt-e2e-${process.pid}`;
const registryProject = `hvac-mqtt-e2e-registry-${process.pid}`;
const registryContainer = `${registryProject}-postgres-1`;
const tenantId = '0193f000-0000-7000-8000-000000000001';
const siteId = '0193f000-1000-7000-8000-000000000001';
const gatewayId = '018f3e00-4000-7000-8000-000000000100';
const deviceIdFor = (index) => `0193f000-2000-7000-8000-${String(index + 1).padStart(12, '0')}`;
const plantConfigPath = resolve(root, 'tools/eg8200-simulator/configs/central-plant.local.json');
const plantConfig = JSON.parse(await readFile(plantConfigPath, 'utf8'));
const plantDevices = [...new Set(plantConfig.points.map((point) => point.deviceId))].sort();
const registeredPoints = plantConfig.points.filter((point) => point.pointType !== 'COMMAND');
const expectedPointCount = plantConfig.points.length;
const uuidV7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const connectivityPassword = randomBytes(24).toString('hex');
const children = new Set();
let telemetryServer;

const report = { schemaVersion: 1, capability: 'mqtt-uplink-e2e', status: 'failed', startedAt: new Date().toISOString(), assertions: {} };

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message || result.stderr?.trim() || result.stdout?.trim() || result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function start(command, args, env = {}) {
  const child = spawn(command, args, { cwd: root, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true, env: { ...process.env, ...env } });
  child.output = '';
  child.stdout.on('data', (chunk) => { child.output += chunk.toString(); });
  child.stderr.on('data', (chunk) => { child.output += chunk.toString(); });
  children.add(child);
  child.once('exit', () => children.delete(child));
  return child;
}

async function stopChild(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  await Promise.race([once(child, 'exit'), new Promise((resolveWait) => setTimeout(resolveWait, 5000))]);
  if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const { port } = server.address();
  await new Promise((resolveClose) => server.close(resolveClose));
  return port;
}

async function waitFor(predicate, timeoutMs, description) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise((resolveWait) => setTimeout(resolveWait, 200));
  }
  throw new Error(`timed out waiting for ${description}`);
}

async function httpStatus(url) {
  try { return (await fetch(url)).status; } catch { return 0; }
}

async function metric(url, name) {
  const body = await (await fetch(url)).text();
  // name is a metric name (summing every series) or one series such as name{label="value"}.
  const matches = (line) => line.startsWith(`${name} `) || (!name.includes('{') && line.startsWith(`${name}{`));
  return body.split('\n').filter(matches).reduce((sum, line) => sum + Number(line.split(' ').at(-1)), 0);
}

function psql(sql) {
  return run('docker', ['exec', '-i', registryContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-Atq'], { input: sql });
}

const quote = (value) => `'${String(value).replaceAll("'", "''")}'`;

function registrySeed() {
  const devices = plantDevices.map((name, index) => `(${quote(deviceIdFor(index))}, ${quote(tenantId)}, ${quote(siteId)}, ${quote(`e2e-${index}`)}, ${quote(name)}, 'CONTROLLER', 'ACTIVE', 1, now(), now())`);
  const sourceKeys = plantDevices.map((name, index) => `(${quote(`0193f000-3000-7000-8000-${String(index + 1).padStart(12, '0')}`)}, ${quote(tenantId)}, ${quote(siteId)}, ${quote(gatewayId)}, ${quote(name)}, ${quote(deviceIdFor(index))}, 'ACTIVE', 1, now(), now())`);
  const points = registeredPoints.map((point, index) => {
    const deviceId = deviceIdFor(plantDevices.indexOf(point.deviceId));
    const counterMode = point.pointType === 'COUNTER' ? "'RESET_TO_ZERO'" : 'NULL';
    return `(${quote(`0193f000-4000-7000-8000-${String(index + 1).padStart(12, '0')}`)}, ${quote(tenantId)}, ${quote(siteId)}, ${quote(deviceId)}, ${quote(point.pointCode)}, ${quote(point.telemetryKey)}, ${quote(point.name)}, ${quote(point.pointType)}, ${quote(point.valueType)}, ${point.unit ? quote(point.unit) : 'NULL'}, 1000, 5000, 30000, ${counterMode}, 'ACTIVE', 1, now(), now())`;
  });
  return `BEGIN;
INSERT INTO iam.tenants (id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES (${quote(tenantId)},'mqtt-e2e','MQTT E2E','UTC','USD','US','ACTIVE',1,now(),now());
INSERT INTO core_registry.sites (id,code,display_name,timezone,status,revision,created_at,updated_at,tenant_id) VALUES (${quote(siteId)},'mqtt-e2e-site','MQTT E2E site','UTC','ACTIVE',1,now(),now(),${quote(tenantId)});
INSERT INTO core_registry.devices (id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES
  (${quote(gatewayId)}, ${quote(tenantId)}, ${quote(siteId)}, 'e2e-gateway', 'E2E Gateway', 'GATEWAY', 'ACTIVE', 1, now(), now()),
  ${devices.join(',\n  ')};
INSERT INTO core_registry.gateway_device_source_keys (id,tenant_id,site_id,gateway_device_id,source_key,device_id,status,revision,created_at,updated_at) VALUES
  ${sourceKeys.join(',\n  ')};
INSERT INTO core_registry.telemetry_points (id,tenant_id,site_id,reporting_device_id,point_code,source_key,display_name,point_type,value_type,unit,sample_interval_ms,publish_interval_ms,stale_after_ms,counter_decrease_mode,status,revision,created_at,updated_at) VALUES
  ${points.join(',\n  ')};
INSERT INTO connectivity.gateway_credentials (id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,revoked_at,created_at,updated_at)
VALUES ('0193f000-5000-7000-8000-000000000001', ${quote(tenantId)}, ${quote(gatewayId)}, repeat('e',64), 'ACTIVE', now() - interval '1 minute', now() + interval '1 day', NULL, now(), now());
ALTER ROLE connectivity_runtime PASSWORD ${quote(connectivityPassword)};
COMMIT;`;
}

try {
  await rm(outputDir, { recursive: true, force: true });
  await mkdir(binDir, { recursive: true });
  await mkdir(queueDir, { recursive: true });
  const pkiGenerator = join(binDir, 'generate-central-plant-pki');
  const connectivityBinary = join(binDir, 'connectivity');
  const publisherBinary = join(binDir, 'eg8200-mqtt-publisher');
  run(process.execPath, ['scripts/run-go.mjs', 'build', '-o', pkiGenerator, './tools/s0-auth-fixture/cmd/generate-central-plant-pki']);
  run(process.execPath, ['scripts/run-go.mjs', 'build', '-o', connectivityBinary, './cmd/connectivity']);
  run(process.execPath, ['scripts/run-go.mjs', 'build', '-o', publisherBinary, './tools/eg8200-simulator/cmd/eg8200-mqtt-publisher']);
  run(pkiGenerator, [pkiDir]);
  // Throwaway test keys: the broker runs as its own user inside the container.
  await chmod(pkiDir, 0o755);
  await chmod(join(pkiDir, 'mqtt-broker-key.pem'), 0o644);

  const [mqttPort, postgresPort, telemetryPort, connectivityPort, publisherPort] = await Promise.all(Array.from({ length: 5 }, findAvailablePort));
  const brokerEnv = { ...process.env, MQTT_PKI_DIR: pkiDir, MQTT_HOST_PORT: String(mqttPort) };
  const registryEnv = { ...process.env, S1_POSTGRES_HOST_PORT: String(postgresPort) };
  runDockerCompose(run, ['-p', registryProject, '-f', registryCompose, 'up', '-d', '--wait', 'postgres'], { env: registryEnv });
  await waitFor(() => {
    const ready = spawnSync('docker', ['exec', registryContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-Atqc',
      "SELECT to_regclass('connectivity.gateway_credentials') IS NOT NULL AND to_regclass('core_registry.point_bindings_v1') IS NOT NULL"], { encoding: 'utf8' });
    return ready.status === 0 && ready.stdout.trim() === 't';
  }, 120000, 'Registry migrations');
  psql(registrySeed());
  runDockerCompose(run, ['-p', brokerProject, '-f', brokerCompose, 'up', '-d', '--wait', 'mqtt-broker'], { env: brokerEnv });

  // Stand-in Telemetry Runtime: fails the first observation, can be slowed, records the rest.
  const observations = [];
  let sourceIdentityVerified = false;
  let failNext = true;
  let responseDelay = 0;
  telemetryServer = createHTTPSServer({
    key: await readFile(join(pkiDir, 'telemetry-key.pem')), cert: await readFile(join(pkiDir, 'telemetry-cert.pem')),
    ca: await readFile(join(pkiDir, 'ca.pem')), minVersion: 'TLSv1.3', requestCert: true, rejectUnauthorized: true,
  }, async (request, response) => {
    if (request.method !== 'POST' || request.url !== '/internal/v1/telemetry/sources/observations:accept') {
      response.writeHead(404).end();
      return;
    }
    const peer = request.socket.getPeerCertificate();
    sourceIdentityVerified = String(peer?.subjectaltname ?? '').includes('URI:spiffe://hvac.local/mqtt-telemetry-adapter');
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const observation = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    if (responseDelay > 0) await new Promise((resolveWait) => setTimeout(resolveWait, responseDelay));
    if (failNext) {
      failNext = false;
      response.writeHead(503, { 'content-type': 'application/json' }).end('{"error":"temporary test outage"}');
      return;
    }
    observations.push(observation);
    response.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify({
      observationId: randomUUID(), evidenceId: '', status: observation.device && observation.point ? 'ACCEPTED' : 'QUARANTINED', quality: 'GOOD',
      qualityReasons: [], quarantineReason: '', deviceId: observation.device?.deviceId ?? '', businessRevision: observations.length,
      stateChanged: true, positionAdvanced: true,
    }));
  });
  telemetryServer.listen({ host: '127.0.0.1', port: telemetryPort });
  await once(telemetryServer, 'listening');

  const connectivity = start(connectivityBinary, ['-diagnostics-addr', `127.0.0.1:${connectivityPort}`], {
    CONNECTIVITY_MQTT_URL: `tls://127.0.0.1:${mqttPort}`, CONNECTIVITY_MQTT_SERVER_NAME: 'localhost',
    CONNECTIVITY_TLS_CERT: join(pkiDir, 'mqtt-adapter-cert.pem'), CONNECTIVITY_TLS_KEY: join(pkiDir, 'mqtt-adapter-key.pem'), CONNECTIVITY_CA: join(pkiDir, 'ca.pem'),
    CONNECTIVITY_TELEMETRY_URL: `https://127.0.0.1:${telemetryPort}`, CONNECTIVITY_TELEMETRY_SERVER_NAME: 'localhost',
    CONNECTIVITY_DATABASE_URL: `postgres://connectivity_runtime:${connectivityPassword}@127.0.0.1:${postgresPort}/hvac_s1?sslmode=disable`,
    CONNECTIVITY_TENANT_ID: tenantId, CONNECTIVITY_QUEUE_CAPACITY: '2',
  });
  const connectivityReady = `http://127.0.0.1:${connectivityPort}/health/ready`;
  const connectivityMetrics = `http://127.0.0.1:${connectivityPort}/metrics`;
  await waitFor(async () => await httpStatus(connectivityReady) === 200, 30000, 'Connectivity readiness');

  // A message Connectivity cannot parse is quarantined with evidence and acknowledged.
  run('docker', ['compose', '-p', brokerProject, '-f', brokerCompose, 'exec', '-T', 'mqtt-broker', 'mosquitto_pub',
    '-h', 'localhost', '-p', '8883', '-V', 'mqttv5', '-q', '1', '--cafile', '/mosquitto/config/pki/ca.pem',
    '--cert', '/mosquitto/config/pki/mqtt-gateway-cert.pem', '--key', '/mosquitto/config/pki/mqtt-gateway-key.pem',
    '-t', `hvac/v1/${gatewayId}/up/telemetry`, '-m', '{}'], { env: brokerEnv });
  await waitFor(() => psql(`SELECT count(*) FROM connectivity.uplink_quarantine WHERE gateway_id = ${quote(gatewayId)} AND reason_code = 'MESSAGE_INVALID'`) === '1', 15000, 'poison message quarantine evidence');

  const gatewayConfigPath = join(outputDir, 'gateway.json');
  await writeFile(gatewayConfigPath, `${JSON.stringify({
    schemaVersion: 4, gatewayId, tenantId, siteId, brokerUrl: `tls://127.0.0.1:${mqttPort}`,
    caFile: join(pkiDir, 'ca.pem'), certFile: join(pkiDir, 'mqtt-gateway-cert.pem'), keyFile: join(pkiDir, 'mqtt-gateway-key.pem'),
    serverName: 'localhost', queueDirectory: queueDir, maximumQueueBytes: 64 * 1024 * 1024, credentialRevision: 1,
  }, null, 2)}\n`);
  const publisherArgs = ['-plant-config', plantConfigPath, '-mqtt-config', gatewayConfigPath, '-diagnostics-addr', `127.0.0.1:${publisherPort}`];
  let publisher = start(publisherBinary, publisherArgs);
  const publisherReady = `http://127.0.0.1:${publisherPort}/health/ready`;
  const publisherMetrics = `http://127.0.0.1:${publisherPort}/metrics`;
  await waitFor(async () => await httpStatus(publisherReady) === 200, 30000, 'publisher readiness');
  await waitFor(() => observations.length >= expectedPointCount, 60000, `${expectedPointCount} observations`);

  // Every value names the Gateway as its source and carries the Registry identity.
  const first = observations.slice(0, expectedPointCount);
  const deviceIds = new Set(first.map((observation) => observation.device?.deviceId));
  const unresolved = first.filter((observation) => observation.sourceId !== gatewayId || observation.device?.tenantId !== tenantId || observation.device?.siteId !== siteId
    || !uuidV7.test(String(observation.sourcePosition?.eventId ?? '')) || !String(observation.sourcePosition?.partition).startsWith(`mqtt:${gatewayId}:`));
  const registeredCodes = new Set(registeredPoints.map((point) => `${point.deviceId}/${point.pointCode}`));
  const missingPoints = first.filter((observation) => registeredCodes.has(`${observation.externalId}/${observation.telemetryKey}`) && !observation.point?.pointId);
  if (deviceIds.size !== plantDevices.length || unresolved.length || missingPoints.length) {
    throw new Error(`observations lack Registry identity: devices=${deviceIds.size} unresolved=${JSON.stringify(unresolved[0])} missingPoint=${JSON.stringify(missingPoints[0])}`);
  }

  // Backlog: a slow Telemetry fills the Gateway queue; nothing is dropped once it recovers.
  responseDelay = 150;
  let peakQueueDepth = 0;
  await waitFor(async () => {
    peakQueueDepth = Math.max(peakQueueDepth, await metric(connectivityMetrics, 'hvac_mqtt_gateway_queue_depth'));
    return peakQueueDepth >= 2;
  }, 60000, 'a full Gateway queue while Telemetry is slow');
  responseDelay = 0;
  const publishedMessages = async () => metric(publisherMetrics, 'hvac_edge_mqtt_publishes_total{outcome="queued"}');
  const processedMessages = async () => metric(connectivityMetrics, 'hvac_mqtt_messages_processed_total{outcome="success"}');
  await waitFor(async () => (await processedMessages()) >= (await publishedMessages()) && await metric(connectivityMetrics, 'hvac_mqtt_gateway_queue_depth') === 0, 120000, 'backlog drained with every published message processed');
  const backlog = { peakQueueDepth, published: await publishedMessages(), processed: await processedMessages() };

  // Store & Forward: with the broker down the publisher queues on disk and replays on recovery.
  await waitFor(async () => await metric(publisherMetrics, 'hvac_edge_mqtt_queue_bytes') === 0, 15000, 'empty Edge queue before broker outage');
  runDockerCompose(run, ['-p', brokerProject, '-f', brokerCompose, 'stop', 'mqtt-broker'], { env: brokerEnv });
  await waitFor(async () => await httpStatus(publisherReady) === 503, 15000, 'publisher readiness drop');
  const observationsAtBrokerDown = observations.length;
  let offlineQueueBytes = 0;
  await waitFor(async () => (offlineQueueBytes = await metric(publisherMetrics, 'hvac_edge_mqtt_queue_bytes')) > 0, 15000, 'Edge queue growth during broker outage');
  const sequencePath = join(queueDir, 'measurement-sequences.v1.json');
  const maxSequenceBeforeRestart = Math.max(...Object.values(JSON.parse(await readFile(sequencePath, 'utf8')).sequences).map(Number));
  await stopChild(publisher);
  runDockerCompose(run, ['-p', brokerProject, '-f', brokerCompose, 'start', 'mqtt-broker'], { env: brokerEnv });
  await waitFor(async () => await httpStatus(connectivityReady) === 200, 30000, 'Connectivity reconnect');
  publisher = start(publisherBinary, publisherArgs);
  await waitFor(async () => await httpStatus(publisherReady) === 200, 30000, 'publisher restart');
  await waitFor(async () => await metric(publisherMetrics, 'hvac_edge_mqtt_queue_bytes') === 0, 30000, 'Edge queue drain after recovery');
  await waitFor(() => observations.some((observation) => Number(observation.sourcePosition?.offset) > maxSequenceBeforeRestart), 30000, 'a sequence above the pre-restart maximum');
  const recovered = observations.length - observationsAtBrokerDown;

  // Revoking the Gateway Credential quarantines its uplink instead of forwarding it.
  psql(`UPDATE connectivity.gateway_credentials SET status='REVOKED', revoked_at=now(), updated_at=now() WHERE gateway_id = ${quote(gatewayId)}`);
  await waitFor(() => Number(psql(`SELECT count(*) FROM connectivity.uplink_quarantine WHERE gateway_id = ${quote(gatewayId)} AND reason_code = 'GATEWAY_CREDENTIAL_INACTIVE' AND tenant_id = ${quote(tenantId)}`)) > 0, 30000, 'revoked-credential quarantine evidence');
  const forwardedAfterRevocation = observations.length;
  await new Promise((resolveWait) => setTimeout(resolveWait, 3000));
  if (observations.length !== forwardedAfterRevocation) throw new Error('a revoked Gateway uplink still reached Telemetry');

  await stopChild(publisher);
  await stopChild(connectivity);
  if (!sourceIdentityVerified) throw new Error('Connectivity mTLS SPIFFE identity was not verified');
  if (!connectivity.output.includes('mqtt_uplink_message_retrying')) throw new Error('Connectivity did not retry the injected Telemetry failure');
  if (recovered <= 0 || offlineQueueBytes <= 0) throw new Error('Store & Forward recovery evidence is incomplete');

  report.assertions = {
    observationCount: first.length, deviceCount: deviceIds.size, registeredPointCount: registeredPoints.length,
    registryIdentityResolved: true, connectivitySPIFFEIdentity: true, transientTelemetryRetryRecovered: true,
    poisonMessageQuarantined: true, backlog, offlineQueueBytes, recoveredAfterBrokerOutage: recovered,
    maxSequenceBeforeRestart, revokedCredentialQuarantined: true,
  };
  report.status = 'passed';
  console.log(`MQTT uplink end-to-end evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.connectivityOutput = [...children].map((child) => child.output.slice(-4000));
  try {
    report.brokerLogs = runDockerCompose(run, ['-p', brokerProject, '-f', brokerCompose, 'logs', '--no-color', '--tail', '40', 'mqtt-broker'], { env: { ...process.env, MQTT_PKI_DIR: pkiDir } });
  } catch {}
  throw error;
} finally {
  for (const child of [...children]) await stopChild(child);
  if (telemetryServer) await new Promise((resolveClose) => telemetryServer.close(resolveClose));
  try { runDockerCompose(run, ['-p', brokerProject, '-f', brokerCompose, 'down', '--volumes', '--remove-orphans'], { env: { ...process.env, MQTT_PKI_DIR: pkiDir } }); } catch {}
  try { runDockerCompose(run, ['-p', registryProject, '-f', registryCompose, 'down', '--volumes', '--remove-orphans'], { env: process.env }); } catch {}
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
}
