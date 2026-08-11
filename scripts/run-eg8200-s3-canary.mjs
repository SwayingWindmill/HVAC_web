import { randomBytes } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { chmod, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createHTTPServer, request as httpRequest } from 'node:http';
import { createServer as createHTTPSServer, request as httpsRequest } from 'node:https';
import { createServer as createTCPServer } from 'node:net';
import { join, resolve } from 'node:path';
import { buildS2SeedSQL } from './central-plant-local-seed.mjs';
import { centralPlantDevices, centralPlantIdentity } from './central-plant-local-contract.mjs';
import { buildCentralPlantSimulatorConfig, buildCentralPlantSimulatorPoints } from './central-plant-spatial-model.mjs';

const root = resolve(process.cwd());
const out = resolve(root, 'out/s3-eg8200-canary');
const pkiDirectory = join(out, 'pki');
const binDirectory = join(out, 'bin');
const configDirectory = join(out, 'config');
const stateDirectory = join(out, 'state');
const postgresDataDirectory = `/tmp/hvac-eg8200-canary-pg-${process.pid}-${randomBytes(3).toString('hex')}`;
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const goBinary = process.env.GO_BINARY ?? 'go';
const initdbBinary = '/usr/lib/postgresql/16/bin/initdb';
const postgresBinary = '/usr/lib/postgresql/16/bin/postgres';
const psqlBinary = '/usr/bin/psql';
const pgReadyBinary = '/usr/bin/pg_isready';
const chiller = centralPlantDevices.find((device) => device.slug === 'chiller-01');
if (!chiller) throw new Error('CHILLER-01 is missing from the central plant contract.');

const capability = 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT';
const capabilityRevision = 'capability:set-chilled-water-temperature-setpoint:v1';
const reportedStateKey = 'chiller.chilled_water_temperature_setpoint';
const targetSetpointC = 7.5;
const maximumBodyBytes = 256 * 1024;
const databasePasswords = Object.freeze({
  s2Runtime: ['s2', 'telemetry', 'runtime', 'local', 'only'].join('-'),
  s3Runtime: ['s3', 'command', 'service', 'local', 'only'].join('-'),
});
const allocatedPorts = new Set();

function databaseURL(user, password, port, database) {
  return `postgres://${encodeURIComponent(user)}:${encodeURIComponent(password)}@127.0.0.1:${port}/${database}?sslmode=disable`;
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd ?? root,
    env: { ...process.env, ...(options.env ?? {}) },
    encoding: 'utf8',
    stdio: options.capture ? 'pipe' : 'inherit',
    windowsHide: true,
  });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message ?? result.stderr?.trim() ?? result.stdout?.trim() ?? String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

async function findAvailablePort() {
  for (;;) {
    const server = createTCPServer();
    server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
    await once(server, 'listening');
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('port allocator failed');
    await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
    if (allocatedPorts.has(address.port)) continue;
    allocatedPorts.add(address.port);
    return address.port;
  }
}

async function writePrivate(path, content) {
  await writeFile(path, content, 'utf8');
  await chmod(path, 0o600).catch(() => undefined);
}

function startProcess(label, binary, args, env = {}) {
  const child = spawn(binary, args, {
    cwd: root,
    env: { ...process.env, ...env },
    stdio: 'inherit',
    detached: true,
    windowsHide: true,
  });
  child.once('error', (error) => console.error(`${label} process error: ${error.message}`));
  return child;
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, 'exit').then(() => true).catch(() => true);
  try { process.kill(-child.pid, 'SIGTERM'); } catch {}
  const stopped = await Promise.race([exited, pause(2500).then(() => false)]);
  if (!stopped) {
    try { process.kill(-child.pid, 'SIGKILL'); } catch {}
  }
}

async function closeServer(server) {
  if (!server) return;
  await new Promise((resolveClose) => server.close(() => resolveClose()));
}

async function waitForHTTP(url, child, label, attempts = 240) {
  let last = '';
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    if (child && (child.exitCode !== null || child.signalCode !== null)) throw new Error(`${label} exited before readiness.`);
    try {
      const response = await fetch(url, { redirect: 'manual' });
      if (response.status >= 200 && response.status < 500) return;
      last = `HTTP ${response.status}`;
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await pause(250);
  }
  throw new Error(`${label} did not become ready: ${last}`);
}

async function waitForPostgres(port, child) {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error('PostgreSQL exited before readiness.');
    const result = spawnSync(pgReadyBinary, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', 'postgres'], {
      cwd: root,
      stdio: 'ignore',
    });
    if (result.status === 0) return;
    await pause(250);
  }
  throw new Error('PostgreSQL did not become ready.');
}

function psql(port, database, args, options = {}) {
  return run(psqlBinary, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', ...args], options);
}

function sqlJSON(port, database, sql) {
  const value = psql(port, database, ['-Atqc', sql], { capture: true });
  return value ? JSON.parse(value) : null;
}

async function initializePostgres(port) {
  await rm(postgresDataDirectory, { recursive: true, force: true });
  await mkdir(postgresDataDirectory, { recursive: true });
  run(initdbBinary, ['-D', postgresDataDirectory, '-A', 'trust', '-U', 'postgres', '--encoding=UTF8', '--no-locale'], { capture: true });
  const process = startProcess('PostgreSQL', postgresBinary, ['-D', postgresDataDirectory, '-h', '127.0.0.1', '-p', String(port), '-k', postgresDataDirectory], {});
  await waitForPostgres(port, process);
  psql(port, 'postgres', ['-c', 'CREATE DATABASE hvac_s2']);
  psql(port, 'postgres', ['-c', 'CREATE DATABASE hvac_s3']);

  const s2MigrationDirectory = resolve(root, 'infra/s2-telemetry/postgres/init');
  const s2Migrations = (await readdir(s2MigrationDirectory))
    .filter((name) => name.endsWith('.sql'))
    .sort((left, right) => left.localeCompare(right, 'en'));
  for (const migration of s2Migrations) psql(port, 'hvac_s2', ['-f', resolve(s2MigrationDirectory, migration)]);
  for (const migration of [
    'infra/s3-command/postgres/init/000-bootstrap-identities.sql',
    'services/command-service/migrations/001_s3_command_runtime.sql',
    'services/command-service/migrations/002_s3_target_runtime.sql',
  ]) psql(port, 'hvac_s3', ['-f', resolve(root, migration)]);
  return process;
}

function buildBinaries(paths) {
  const goCache = resolve(root, 'out/.go-build-cache');
  const builds = [
    [paths.pkiGenerator, './tools/s0-auth-fixture/cmd/generate-central-plant-pki'],
    [paths.telemetry, './services/telemetry-runtime-service/cmd/telemetry-runtime-service'],
    [paths.simulator, './tools/eg8200-simulator/cmd/eg8200-simulator'],
    [paths.adapter, './services/thingsboard-telemetry-adapter/cmd/thingsboard-telemetry-adapter'],
    [paths.command, './services/command-service/cmd/command-service'],
    [paths.seed, './services/command-service/cmd/s3-local-seed'],
    [paths.dispatcher, './services/command-dispatcher/cmd/command-dispatcher'],
    [paths.verifier, './services/command-dispatcher/cmd/command-verifier'],
  ];
  for (const [output, source] of builds) {
    run(goBinary, ['build', '-trimpath', '-buildvcs=false', '-o', output, source], { env: { GOCACHE: goCache }, capture: true });
  }
}

function adapterPointMaps(adapterTemplate) {
  const pointsByDevice = new Map();
  adapterTemplate.devices.forEach((device, index) => {
    const platformDevice = centralPlantDevices[index];
    if (!platformDevice) throw new Error('adapter template has more devices than the central plant contract');
    pointsByDevice.set(platformDevice.platformDeviceId, device.points.map((point) => ({ ...point })));
  });
  if (pointsByDevice.size !== centralPlantDevices.length) throw new Error('adapter template does not cover all central plant devices');
  return pointsByDevice;
}

async function readRequestJSON(request) {
  const chunks = [];
  let total = 0;
  for await (const chunk of request) {
    total += chunk.length;
    if (total > maximumBodyBytes) throw new Error('request body too large');
    chunks.push(chunk);
  }
  if (chunks.length === 0) return {};
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function writeJSON(response, status, body) {
  const encoded = Buffer.from(JSON.stringify(body));
  response.writeHead(status, {
    'content-type': 'application/json',
    'content-length': String(encoded.length),
  });
  response.end(encoded);
}

function createThingsBoardProtocolBroker(port) {
  const tenantToken = ['canary', 'tenant', randomBytes(24).toString('base64url')].join('-');
  const devices = centralPlantDevices.map((device) => ({
    ...device,
    thingsBoardDeviceId: `tb-${device.slug}`,
    access: ['eg8200', device.slug, randomBytes(18).toString('base64url')].join('-'),
  }));
  const byAccess = new Map(devices.map((device) => [device.access, device]));
  const byExternalID = new Map(devices.map((device) => [device.thingsBoardDeviceId, device]));
  const samples = new Map(devices.map((device) => [device.thingsBoardDeviceId, new Map()]));
  const queues = new Map(devices.map((device) => [device.thingsBoardDeviceId, []]));
  const pending = new Map();
  let nextRPCID = 0;
  const counters = { telemetryPosts: 0, timeseriesReads: 0, serverRpcRequests: 0, deviceRpcPolls: 0, deviceRpcReplies: 0, methods: {} };

  const deviceForAccessPath = (pathname, suffix) => {
    const prefix = '/api/v1/';
    if (!pathname.startsWith(prefix) || !pathname.endsWith(suffix)) return null;
    const token = decodeURIComponent(pathname.slice(prefix.length, pathname.length - suffix.length));
    return byAccess.get(token) ?? null;
  };

  const waitForDeviceRPC = async (device, timeoutMs) => {
    const queue = queues.get(device.thingsBoardDeviceId);
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (queue.length > 0) return queue.shift();
      await pause(25);
    }
    return null;
  };

  const server = createHTTPServer(async (request, response) => {
    try {
      const url = new URL(request.url ?? '/', `http://127.0.0.1:${port}`);
      if (request.method === 'GET' && url.pathname === '/health') {
        response.writeHead(200).end('ok\n');
        return;
      }

      if (request.method === 'POST' && url.pathname.startsWith('/api/v1/') && url.pathname.endsWith('/telemetry')) {
        const device = deviceForAccessPath(url.pathname, '/telemetry');
        if (!device) return writeJSON(response, 404, { error: 'device not found' });
        const body = await readRequestJSON(request);
        if (!Number.isFinite(body.ts) || typeof body.values !== 'object' || body.values === null || Array.isArray(body.values)) {
          return writeJSON(response, 400, { error: 'invalid telemetry' });
        }
        const deviceSamples = samples.get(device.thingsBoardDeviceId);
        for (const [key, value] of Object.entries(body.values)) {
          const keySamples = deviceSamples.get(key) ?? [];
          keySamples.push({ ts: body.ts, value });
          if (keySamples.length > 4096) keySamples.splice(0, keySamples.length - 4096);
          deviceSamples.set(key, keySamples);
        }
        counters.telemetryPosts += 1;
        response.writeHead(200).end();
        return;
      }

      if (request.method === 'GET' && url.pathname.startsWith('/api/v1/') && url.pathname.endsWith('/rpc')) {
        const device = deviceForAccessPath(url.pathname, '/rpc');
        if (!device) return writeJSON(response, 404, { error: 'device not found' });
        counters.deviceRpcPolls += 1;
        const requestedTimeout = Number.parseInt(url.searchParams.get('timeout') ?? '20000', 10);
        const timeoutMs = Math.max(1, Math.min(30000, Number.isFinite(requestedTimeout) ? requestedTimeout : 20000));
        const rpc = await waitForDeviceRPC(device, timeoutMs);
        if (!rpc) {
          response.writeHead(200).end();
          return;
        }
        writeJSON(response, 200, { id: rpc.id, method: rpc.method, params: rpc.params });
        return;
      }

      const deviceReply = /^\/api\/v1\/([^/]+)\/rpc\/(\d+)$/.exec(url.pathname);
      if (request.method === 'POST' && deviceReply) {
        const device = byAccess.get(decodeURIComponent(deviceReply[1]));
        if (!device) return writeJSON(response, 404, { error: 'device not found' });
        const rpcID = Number.parseInt(deviceReply[2], 10);
        const pendingRPC = pending.get(rpcID);
        if (!pendingRPC || pendingRPC.externalDeviceID !== device.thingsBoardDeviceId) return writeJSON(response, 404, { error: 'rpc not found' });
        const body = await readRequestJSON(request);
        pending.delete(rpcID);
        clearTimeout(pendingRPC.timer);
        counters.deviceRpcReplies += 1;
        pendingRPC.resolve(body);
        response.writeHead(200).end();
        return;
      }

      const timeseriesMatch = /^\/api\/plugins\/telemetry\/DEVICE\/([^/]+)\/values\/timeseries$/.exec(url.pathname);
      if (request.method === 'GET' && timeseriesMatch) {
        if (request.headers['x-authorization'] !== `Bearer ${tenantToken}`) return writeJSON(response, 401, { error: 'unauthorized' });
        const externalDeviceID = decodeURIComponent(timeseriesMatch[1]);
        if (!byExternalID.has(externalDeviceID)) return writeJSON(response, 404, { error: 'device not found' });
        const requestedKeys = (url.searchParams.get('keys') ?? '').split(',').map((key) => key.trim()).filter(Boolean);
        const startTs = Number.parseInt(url.searchParams.get('startTs') ?? '0', 10);
        const endTs = Number.parseInt(url.searchParams.get('endTs') ?? String(Date.now()), 10);
        const limit = Math.max(1, Math.min(1000, Number.parseInt(url.searchParams.get('limit') ?? '1000', 10) || 1000));
        const deviceSamples = samples.get(externalDeviceID);
        const body = {};
        for (const key of requestedKeys) {
          body[key] = (deviceSamples.get(key) ?? [])
            .filter((sample) => sample.ts >= startTs && sample.ts <= endTs)
            .slice(0, limit)
            .map((sample) => ({ ts: sample.ts, value: sample.value }));
        }
        counters.timeseriesReads += 1;
        writeJSON(response, 200, body);
        return;
      }

      const serverRPCMatch = /^\/api\/rpc\/twoway\/([^/]+)$/.exec(url.pathname);
      if (request.method === 'POST' && serverRPCMatch) {
        if (request.headers['x-authorization'] !== `Bearer ${tenantToken}`) return writeJSON(response, 401, { success: false });
        const externalDeviceID = decodeURIComponent(serverRPCMatch[1]);
        if (!byExternalID.has(externalDeviceID)) return writeJSON(response, 404, { success: false });
        const body = await readRequestJSON(request);
        if (typeof body.method !== 'string' || !body.method.trim() || typeof body.params !== 'object' || body.params === null || Array.isArray(body.params)) {
          return writeJSON(response, 400, { success: false });
        }
        const timeoutMs = Math.max(1000, Math.min(30000, Number(body.timeout) || 5000));
        const rpcID = ++nextRPCID;
        counters.serverRpcRequests += 1;
        counters.methods[body.method] = (counters.methods[body.method] ?? 0) + 1;
        const replyPromise = new Promise((resolveReply) => {
          const timer = setTimeout(() => {
            pending.delete(rpcID);
            const queue = queues.get(externalDeviceID);
            const index = queue.findIndex((candidate) => candidate.id === rpcID);
            if (index >= 0) queue.splice(index, 1);
            resolveReply(null);
          }, timeoutMs);
          pending.set(rpcID, { externalDeviceID, resolve: resolveReply, timer });
        });
        queues.get(externalDeviceID).push({ id: rpcID, method: body.method.trim(), params: body.params });
        const reply = await replyPromise;
        if (!reply) return writeJSON(response, 504, { success: false });
        writeJSON(response, 200, reply);
        return;
      }

      writeJSON(response, 404, { error: 'not found' });
    } catch (error) {
      writeJSON(response, 500, { error: error instanceof Error ? error.message : String(error) });
    }
  });
  server.listen(port, '127.0.0.1');
  return once(server, 'listening').then(() => ({
    server,
    baseURL: `http://127.0.0.1:${port}`,
    authorization: tenantToken,
    devices,
    snapshot: () => JSON.parse(JSON.stringify(counters)),
  }));
}

async function startTLSProxy({ port, targetURL, certPath, keyPath }) {
  const [cert, key] = await Promise.all([readFile(certPath), readFile(keyPath)]);
  const target = new URL(targetURL);
  const server = createHTTPSServer({ cert, key }, (request, response) => {
    const upstream = httpRequest({ hostname: target.hostname, port: target.port, method: request.method, path: request.url, headers: request.headers }, (upstreamResponse) => {
      response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
      upstreamResponse.pipe(response);
    });
    upstream.once('error', () => response.writeHead(502).end());
    request.pipe(upstream);
  });
  server.listen(port, '127.0.0.1');
  await once(server, 'listening');
  return server;
}

function mtlsJSON({ port, path, cert, key, ca }) {
  return new Promise((resolveRequest, rejectRequest) => {
    const request = httpsRequest({
      hostname: '127.0.0.1', port, path, method: 'GET', cert, key, ca, servername: 'localhost', rejectUnauthorized: true,
      headers: { Accept: 'application/json' },
    }, (response) => {
      const chunks = [];
      response.on('data', (chunk) => chunks.push(chunk));
      response.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        if ((response.statusCode ?? 500) < 200 || (response.statusCode ?? 500) >= 300) return rejectRequest(new Error(`reported-state returned ${response.statusCode}: ${text}`));
        try { resolveRequest(JSON.parse(text)); } catch (error) { rejectRequest(error); }
      });
    });
    request.once('error', rejectRequest);
    request.end();
  });
}

function reportedNumber(state) {
  return Number(state?.reportedValue?.number);
}

async function waitForReportedState(paths, telemetryPort, expectedSetpointC) {
  const [cert, key, ca] = await Promise.all([readFile(paths.commandVerifierCert), readFile(paths.commandVerifierKey), readFile(paths.ca)]);
  let last;
  for (let attempt = 0; attempt < 240; attempt += 1) {
    try {
      last = await mtlsJSON({ port: telemetryPort, path: `/internal/v1/commands/reported-state?key=${encodeURIComponent(reportedStateKey)}`, cert, key, ca });
      const healthy = last.organizationId === centralPlantIdentity.organizationId
        && last.siteId === centralPlantIdentity.siteId
        && last.deviceId === chiller.platformDeviceId
        && last.reportedStateKey === reportedStateKey
        && last.evaluationAvailability === 'AVAILABLE'
        && last.presence === 'ONLINE'
        && last.readiness === 'CURRENT'
        && last.freshness === 'FRESH'
        && last.quality === 'GOOD'
        && Number(last.businessRevision) > 0;
      const valueMatches = expectedSetpointC === undefined || Math.abs(reportedNumber(last) - expectedSetpointC) <= 0.1;
      if (healthy && valueMatches) return last;
    } catch (error) {
      last = { error: error instanceof Error ? error.message : String(error) };
    }
    await pause(500);
  }
  throw new Error(`S2 reported-state did not converge: ${JSON.stringify(last)}`);
}

async function waitForCommand(postgresPort, commandId) {
  let last = null;
  for (let attempt = 0; attempt < 240; attempt += 1) {
    last = sqlJSON(postgresPort, 'hvac_s3', `SELECT json_build_object(
      'status', i.status,
      'version', i.version,
      'snapshotRevision', i.snapshot_revision,
      'deviceCommandSequence', i.device_command_sequence,
      'attemptStatus', (SELECT a.status FROM command_runtime.command_attempts a WHERE a.command_id=i.command_id ORDER BY a.created_at DESC LIMIT 1),
      'executionFence', (SELECT a.execution_fence FROM command_runtime.command_attempts a WHERE a.command_id=i.command_id ORDER BY a.created_at DESC LIMIT 1),
      'connectorPhase', (SELECT e.connector_phase FROM command_runtime.connector_evidence e WHERE e.command_id=i.command_id ORDER BY e.prepared_at DESC LIMIT 1),
      'providerMethod', (SELECT e.provider_method FROM command_runtime.connector_evidence e WHERE e.command_id=i.command_id ORDER BY e.prepared_at DESC LIMIT 1),
      'mappingRevision', (SELECT e.mapping_revision FROM command_runtime.connector_evidence e WHERE e.command_id=i.command_id ORDER BY e.prepared_at DESC LIMIT 1),
      'providerStatusCode', (SELECT e.provider_status_code FROM command_runtime.connector_evidence e WHERE e.command_id=i.command_id ORDER BY e.prepared_at DESC LIMIT 1),
      'verificationEvidenceId', (SELECT a.verification_evidence_id FROM command_runtime.command_attempts a WHERE a.command_id=i.command_id ORDER BY a.created_at DESC LIMIT 1),
      'transitionCount', (SELECT count(*) FROM command_runtime.command_transitions t WHERE t.command_id=i.command_id)
    )::text FROM command_runtime.command_intents i WHERE i.command_id='${commandId}'::uuid;`);
    if (last?.status === 'SUCCEEDED') return last;
    if (['FAILED', 'REJECTED', 'CANCELLED', 'EXPIRED', 'OUTCOME_UNKNOWN'].includes(last?.status)) throw new Error(`Command entered terminal failure: ${JSON.stringify(last)}`);
    await pause(250);
  }
  throw new Error(`Command did not reach SUCCEEDED: ${JSON.stringify(last)}`);
}

async function main() {
  await rm(out, { recursive: true, force: true });
  await Promise.all([mkdir(pkiDirectory, { recursive: true }), mkdir(binDirectory, { recursive: true }), mkdir(configDirectory, { recursive: true }), mkdir(stateDirectory, { recursive: true })]);

  const ports = {
    postgres: await findAvailablePort(), broker: await findAvailablePort(), brokerTLS: await findAvailablePort(), telemetry: await findAvailablePort(),
    telemetryDiagnostics: await findAvailablePort(), simulatorDiagnostics: await findAvailablePort(), adapterDiagnostics: await findAvailablePort(),
    command: await findAvailablePort(), commandDiagnostics: await findAvailablePort(), dispatcherDiagnostics: await findAvailablePort(),
    verifierDiagnostics: await findAvailablePort(), unusedIAM: await findAvailablePort(),
  };
  const paths = {
    ca: join(pkiDirectory, 'ca.pem'), iamCert: join(pkiDirectory, 'iam-cert.pem'), gatewayCert: join(pkiDirectory, 'gateway-cert.pem'),
    telemetryCert: join(pkiDirectory, 'telemetry-cert.pem'), telemetryKey: join(pkiDirectory, 'telemetry-key.pem'),
    adapterCert: join(pkiDirectory, 'adapter-cert.pem'), adapterKey: join(pkiDirectory, 'adapter-key.pem'),
    webCert: join(pkiDirectory, 'web-cert.pem'), webKey: join(pkiDirectory, 'web-key.pem'),
    commandCert: join(pkiDirectory, 'command-cert.pem'), commandKey: join(pkiDirectory, 'command-key.pem'),
    commandDispatcherCert: join(pkiDirectory, 'command-dispatcher-cert.pem'), commandDispatcherKey: join(pkiDirectory, 'command-dispatcher-key.pem'),
    commandVerifierCert: join(pkiDirectory, 'command-verifier-cert.pem'), commandVerifierKey: join(pkiDirectory, 'command-verifier-key.pem'),
    pkiGenerator: join(binDirectory, 'generate-central-plant-pki'), telemetry: join(binDirectory, 'telemetry-runtime-service'),
    simulator: join(binDirectory, 'eg8200-simulator'), adapter: join(binDirectory, 'thingsboard-telemetry-adapter'),
    command: join(binDirectory, 'command-service'), seed: join(binDirectory, 's3-local-seed'), dispatcher: join(binDirectory, 'command-dispatcher'), verifier: join(binDirectory, 'command-verifier'),
    simulatorConfig: join(configDirectory, 'simulator.json'), adapterConfig: join(configDirectory, 'adapter.json'), s2Seed: join(configDirectory, 's2-seed.sql'), cohort: join(configDirectory, 'approved-cohort.json'),
    providerCredential: join(stateDirectory, 'thingsboard-provider-authorization'), commandDatabaseURL: join(stateDirectory, 'command-database-url'), adapterCheckpoint: join(stateDirectory, 'adapter-checkpoint.json'), report: join(out, 'report.json'),
  };

  const children = [];
  let broker;
  let tlsProxy;
  try {
    buildBinaries(paths);
    run(paths.pkiGenerator, [pkiDirectory], { capture: true });
    const postgres = await initializePostgres(ports.postgres);
    children.push(postgres);

    const adapterTemplate = JSON.parse(await readFile(resolve(root, 'services/thingsboard-telemetry-adapter/configs/central-plant.local.example.json'), 'utf8'));
    const pointsByDevice = adapterPointMaps(adapterTemplate);
    const spatialPoints = buildCentralPlantSimulatorPoints(adapterTemplate);
    await writeFile(paths.s2Seed, buildS2SeedSQL({ pointsByDevice, spatialPoints }), 'utf8');
    psql(ports.postgres, 'hvac_s2', ['-f', paths.s2Seed]);
    psql(ports.postgres, 'hvac_s3', ['-c', [
      "INSERT INTO command_runtime.capability_profiles (capability_name, capability_revision, status, canonical_unit, minimum_value, maximum_value, maximum_delta, risk_level, approval_policy, retry_policy, connector_kind)",
      `VALUES ('${capability}', '${capabilityRevision}', 'DRAFT', 'CELSIUS', 5, 12, 1, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'THINGSBOARD_CE_4.3.1.3')`,
      'ON CONFLICT (capability_name, capability_revision) DO NOTHING;',
    ].join(' ')]);

    broker = await createThingsBoardProtocolBroker(ports.broker);
    await waitForHTTP(`${broker.baseURL}/health`, null, 'ThingsBoard protocol broker');
    tlsProxy = await startTLSProxy({ port: ports.brokerTLS, targetURL: broker.baseURL, certPath: paths.webCert, keyPath: paths.webKey });

    const simulatorConfig = buildCentralPlantSimulatorConfig(adapterTemplate, { thingsBoardBaseUrl: broker.baseURL, publishInterval: '2s' });
    await writeFile(paths.simulatorConfig, `${JSON.stringify(simulatorConfig, null, 2)}\n`, 'utf8');
    await writePrivate(paths.providerCredential, `${broker.authorization}\n`);

    adapterTemplate.pollInterval = '1s';
    adapterTemplate.initialLookback = '10m';
    adapterTemplate.pageLimit = 1000;
    adapterTemplate.checkpointFile = paths.adapterCheckpoint;
    adapterTemplate.thingsBoard = { baseUrl: broker.baseURL, jwtFile: paths.providerCredential };
    adapterTemplate.telemetryRuntime = { baseUrl: `https://127.0.0.1:${ports.telemetry}`, caFile: paths.ca, certFile: paths.adapterCert, keyFile: paths.adapterKey, serverName: 'localhost' };
    adapterTemplate.devices = adapterTemplate.devices.map((device, index) => ({ ...device, thingsBoardDeviceId: broker.devices[index].thingsBoardDeviceId, externalId: centralPlantDevices[index].platformDeviceId }));
    await writeFile(paths.adapterConfig, `${JSON.stringify(adapterTemplate, null, 2)}\n`, 'utf8');

    const telemetry = startProcess('Telemetry Runtime', paths.telemetry, [], {
      TELEMETRY_SERVICE_ADDR: `127.0.0.1:${ports.telemetry}`, TELEMETRY_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.telemetryDiagnostics}`,
      TELEMETRY_TLS_CERT: paths.telemetryCert, TELEMETRY_TLS_KEY: paths.telemetryKey, TELEMETRY_CLIENT_CA: paths.ca,
      TELEMETRY_IAM_CA: paths.ca, TELEMETRY_IAM_GRANT_CERT: paths.iamCert,
      TELEMETRY_DATABASE_URL: databaseURL('s2_telemetry_service', databasePasswords.s2Runtime, ports.postgres, 'hvac_s2'),
      TELEMETRY_SOURCE_BINDINGS_JSON: JSON.stringify({ 'spiffe://hvac.local/thingsboard-telemetry-adapter': [centralPlantIdentity.integrationInstanceId] }),
      TELEMETRY_IAM_ENDPOINT: `https://127.0.0.1:${ports.unusedIAM}`, TELEMETRY_REALTIME_ENABLED: 'false',
      TELEMETRY_ALLOWED_COMMAND_VERIFIER_SPIFFE: 'spiffe://hvac.local/command-verifier', TELEMETRY_COMMAND_VERIFIER_ORGANIZATION_ID: centralPlantIdentity.organizationId,
      TELEMETRY_COMMAND_VERIFIER_SITE_ID: centralPlantIdentity.siteId, TELEMETRY_COMMAND_VERIFIER_DEVICE_ID: chiller.platformDeviceId, TELEMETRY_COMMAND_REPORTED_STATE_KEY: reportedStateKey,
    });
    children.push(telemetry);
    await waitForHTTP(`http://127.0.0.1:${ports.telemetryDiagnostics}/health/ready`, telemetry, 'Telemetry Runtime');

    const simulatorEnvironment = { EG8200_SIMULATOR_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.simulatorDiagnostics}` };
    for (const [deviceID, environmentName] of Object.entries(simulatorConfig.credentialEnvByDeviceId ?? {})) {
      const provisioned = broker.devices.find((device) => device.name === deviceID);
      if (!provisioned) throw new Error(`broker credential binding missing for ${deviceID}`);
      simulatorEnvironment[environmentName] = provisioned.access;
    }
    const simulator = startProcess('EG8200 simulator', paths.simulator, ['-config', paths.simulatorConfig], simulatorEnvironment);
    children.push(simulator);
    await waitForHTTP(`http://127.0.0.1:${ports.simulatorDiagnostics}/health/ready`, simulator, 'EG8200 simulator', 600);

    const adapter = startProcess('ThingsBoard Telemetry Adapter', paths.adapter, ['-config', paths.adapterConfig, '-diagnostics-addr', `127.0.0.1:${ports.adapterDiagnostics}`]);
    children.push(adapter);
    await waitForHTTP(`http://127.0.0.1:${ports.adapterDiagnostics}/health/ready`, adapter, 'ThingsBoard Telemetry Adapter', 600);

    const baseline = await waitForReportedState(paths, ports.telemetry);
    const baselineSetpointC = reportedNumber(baseline);
    if (!Number.isFinite(baselineSetpointC) || baselineSetpointC < 5 || baselineSetpointC > 12) throw new Error(`EG8200 baseline setpoint is outside chilled-water bounds: ${baselineSetpointC}`);
    if (Math.abs(targetSetpointC - baselineSetpointC) > 1) throw new Error(`Canary target delta exceeds 1°C: ${baselineSetpointC} -> ${targetSetpointC}`);

    const chillerBrokerDevice = broker.devices.find((device) => device.slug === chiller.slug);
    if (!chillerBrokerDevice) throw new Error('CHILLER-01 broker device is missing');
    const cohort = {
      schemaVersion: 1, organizationId: centralPlantIdentity.organizationId, siteId: centralPlantIdentity.siteId, deviceId: chiller.platformDeviceId,
      integrationId: centralPlantIdentity.integrationInstanceId, externalDeviceId: chillerBrokerDevice.thingsBoardDeviceId,
      bindingRevision: 'central-plant-eg8200-canary:chiller-01:v1', capability, capabilityRevision,
      mappingRevision: 'thingsboard-ce-4.3.1.3:setChilledWaterTemperatureSetpoint:eg8200-canary:v1', mappingStatus: 'VERIFIED',
      providerContract: 'THINGSBOARD_CE_4.3.1.3', providerMethod: 'setChilledWaterTemperatureSetpoint', reportedStateKey,
      timeoutMilliseconds: 5000, credentialReference: 'workload://eg8200-canary/thingsboard-protocol-broker',
    };
    await writeFile(paths.cohort, `${JSON.stringify(cohort, null, 2)}\n`, 'utf8');
    await writePrivate(paths.commandDatabaseURL, `${databaseURL('s3_command_service', databasePasswords.s3Runtime, ports.postgres, 'hvac_s3')}\n`);

    const command = startProcess('Command Service', paths.command, [], {
      COMMAND_TLS_CERT: paths.commandCert, COMMAND_TLS_KEY: paths.commandKey, COMMAND_CLIENT_CA: paths.ca,
      COMMAND_IAM_GRANT_CERT: paths.iamCert, COMMAND_GATEWAY_DELEGATION_CERT: paths.gatewayCert, COMMAND_DATABASE_URL_FILE: paths.commandDatabaseURL,
      COMMAND_POLICY_REVISION: 's3-eg8200-virtual-canary-v1', COMMAND_EMERGENCY_REVOCATION_REVISION: '1',
      COMMAND_APPROVED_ORGANIZATION_ID: centralPlantIdentity.organizationId, COMMAND_APPROVED_SITE_ID: centralPlantIdentity.siteId, COMMAND_APPROVED_DEVICE_ID: chiller.platformDeviceId,
      COMMAND_APPROVED_CAPABILITY: capability,
      COMMAND_SERVICE_ADDR: `127.0.0.1:${ports.command}`, COMMAND_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.commandDiagnostics}`,
    });
    children.push(command);
    await waitForHTTP(`http://127.0.0.1:${ports.commandDiagnostics}/health/ready`, command, 'Command Service');

    const commonRuntime = { COMMAND_RUNTIME_URL: `https://127.0.0.1:${ports.command}`, COMMAND_RUNTIME_SERVER_CA: paths.ca, COMMAND_RUNTIME_SERVER_NAME: 'localhost', S3_APPROVED_COHORT_FILE: paths.cohort };
    const dispatcher = startProcess('Command Dispatcher', paths.dispatcher, [], {
      ...commonRuntime, COMMAND_RUNTIME_CLIENT_CERT: paths.commandDispatcherCert, COMMAND_RUNTIME_CLIENT_KEY: paths.commandDispatcherKey,
      THINGSBOARD_CREDENTIAL_FILE: paths.providerCredential, THINGSBOARD_SERVER_CA: paths.ca, THINGSBOARD_SERVER_NAME: 'localhost', THINGSBOARD_BASE_URL: `https://127.0.0.1:${ports.brokerTLS}`,
      COMMAND_DISPATCHER_WORKER_ID: 'eg8200-canary-dispatcher', COMMAND_DISPATCHER_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.dispatcherDiagnostics}`,
    });
    children.push(dispatcher);
    await waitForHTTP(`http://127.0.0.1:${ports.dispatcherDiagnostics}/health/ready`, dispatcher, 'Command Dispatcher');

    const verifier = startProcess('Command Verifier', paths.verifier, [], {
      ...commonRuntime, COMMAND_RUNTIME_CLIENT_CERT: paths.commandVerifierCert, COMMAND_RUNTIME_CLIENT_KEY: paths.commandVerifierKey,
      S2_REPORTED_STATE_CLIENT_CERT: paths.commandVerifierCert, S2_REPORTED_STATE_CLIENT_KEY: paths.commandVerifierKey,
      S2_REPORTED_STATE_SERVER_CA: paths.ca, S2_REPORTED_STATE_SERVER_NAME: 'localhost', S2_REPORTED_STATE_URL: `https://127.0.0.1:${ports.telemetry}`,
      COMMAND_VERIFIER_WORKER_ID: 'eg8200-canary-verifier', COMMAND_VERIFIER_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.verifierDiagnostics}`,
    });
    children.push(verifier);
    await waitForHTTP(`http://127.0.0.1:${ports.verifierDiagnostics}/health/ready`, verifier, 'Command Verifier');

    const seedEnvironment = {
      S3_LOCAL_DATABASE_URL_FILE: paths.commandDatabaseURL, S3_LOCAL_ORGANIZATION_ID: centralPlantIdentity.organizationId, S3_LOCAL_SITE_ID: centralPlantIdentity.siteId,
      S3_LOCAL_DEVICE_ID: chiller.platformDeviceId, S3_LOCAL_PRINCIPAL_ID: centralPlantIdentity.principalId, S3_LOCAL_IDEMPOTENCY_KEY: `eg8200-canary-${Date.now()}`,
      S3_LOCAL_CAPABILITY: capability, S3_LOCAL_VERIFICATION_POINT_KEY: reportedStateKey, S3_LOCAL_PARAMETER_VALUE: String(targetSetpointC), S3_LOCAL_CURRENT_VALUE: String(baselineSetpointC), S3_LOCAL_CURRENT_BUSINESS_REVISION: String(baseline.businessRevision),
    };
    const firstSeed = JSON.parse(run(paths.seed, [], { capture: true, env: seedEnvironment }));
    const commandState = await waitForCommand(ports.postgres, firstSeed.commandId);
    const finalReportedState = await waitForReportedState(paths, ports.telemetry, targetSetpointC);
    const finalSetpointC = reportedNumber(finalReportedState);
    const replaySeed = JSON.parse(run(paths.seed, [], { capture: true, env: seedEnvironment }));
    const providerEvidence = broker.snapshot();

    if (replaySeed.commandId !== firstSeed.commandId || replaySeed.replayed !== true) throw new Error(`Idempotent replay did not converge: ${JSON.stringify({ firstSeed, replaySeed })}`);
    if (commandState.providerMethod !== 'setChilledWaterTemperatureSetpoint' || commandState.connectorPhase !== 'ACKNOWLEDGED') throw new Error(`Connector evidence is incomplete: ${JSON.stringify(commandState)}`);
    if (commandState.attemptStatus !== 'VERIFIED' || !commandState.verificationEvidenceId) throw new Error(`Verifier evidence is incomplete: ${JSON.stringify(commandState)}`);
    if ((providerEvidence.methods.setChilledWaterTemperatureSetpoint ?? 0) < 1 || providerEvidence.deviceRpcReplies < 1) throw new Error(`EG8200 RPC path was not observed: ${JSON.stringify(providerEvidence)}`);

    const report = {
      schemaVersion: 1, status: 'passed', classification: 'ACCELERATED_VIRTUAL_INTERNAL_CANARY', productionEligible: false, formalS309Completion: false,
      providerEmulated: true, providerImplementation: 'THINGSBOARD_PROTOCOL_BROKER',
      topology: 'Command Service -> Dispatcher -> ThingsBoard protocol broker -> EG8200 CHILLER-01 -> Adapter -> S2 -> Command Verifier',
      organizationId: centralPlantIdentity.organizationId, siteId: centralPlantIdentity.siteId, device: { name: chiller.name, deviceId: chiller.platformDeviceId },
      capability, capabilityRevision,
      baseline: { setpointC: baselineSetpointC, businessRevision: Number(baseline.businessRevision), evidenceId: baseline.evidenceId },
      targetSetpointC,
      command: { commandId: firstSeed.commandId, initialStatus: firstSeed.status, final: commandState, idempotentReplay: replaySeed.replayed },
      finalReportedState: { setpointC: finalSetpointC, businessRevision: Number(finalReportedState.businessRevision), evidenceId: finalReportedState.evidenceId, observedAt: finalReportedState.observedAt },
      providerEvidence,
      checks: {
        lowRiskDeltaWithinOneC: Math.abs(targetSetpointC - baselineSetpointC) <= 1,
        eg8200TelemetryObserved: providerEvidence.telemetryPosts > 0, adapterTimeseriesReadsObserved: providerEvidence.timeseriesReads > 0,
        providerRPCObserved: providerEvidence.serverRpcRequests > 0 && commandState.connectorPhase === 'ACKNOWLEDGED', eg8200DeviceRPCReplyObserved: providerEvidence.deviceRpcReplies > 0,
        providerMethodMatchedEG8200: commandState.providerMethod === 'setChilledWaterTemperatureSetpoint', commandVerified: commandState.attemptStatus === 'VERIFIED' && commandState.status === 'SUCCEEDED',
        s2BusinessRevisionAdvanced: Number(finalReportedState.businessRevision) > Number(baseline.businessRevision), s2ReportedSetpointMatched: Math.abs(finalSetpointC - targetSetpointC) <= 0.1,
        idempotentReplayReturnedSameCommand: replaySeed.commandId === firstSeed.commandId && replaySeed.replayed === true,
      },
      note: 'Accelerated virtual-device canary. It intentionally omits the formal 240-minute real-device window and dual-person approval, and uses a ThingsBoard-compatible protocol broker rather than a ThingsBoard server.',
      verifiedAt: new Date().toISOString(),
    };
    await writeFile(paths.report, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
    console.log(JSON.stringify(report, null, 2));
  } finally {
    for (const child of children.reverse()) await stopProcess(child);
    await closeServer(tlsProxy);
    await closeServer(broker?.server);
    await rm(paths.providerCredential, { force: true });
    await rm(paths.commandDatabaseURL, { force: true });
    await rm(postgresDataDirectory, { recursive: true, force: true });
  }
}

await main();
