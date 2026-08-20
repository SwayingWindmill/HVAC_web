import { randomBytes } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { createServer as createHTTPServer, request as httpRequest } from 'node:http';
import { createServer as createHTTPSServer, request as httpsRequest } from 'node:https';
import { createServer as createTCPServer, connect as connectTCP } from 'node:net';
import { chmod, mkdir, open, readFile, rm, unlink, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import tls from 'node:tls';
import {
  centralPlantDevices,
  centralPlantIdentity,
  localUUID,
} from './central-plant-local-contract.mjs';
import { centralPlantLogtoAccountProfile, provisionCentralPlantLogto } from './central-plant-logto.mjs';
import { buildCentralPlantRouteOwnership } from './central-plant-local-routing.mjs';
import { buildCentralPlantSpatialIdentities, buildS1SeedSQL, buildS2SeedSQL } from './central-plant-local-seed.mjs';
import { buildCentralPlantControlPoints, buildCentralPlantSimulatorConfig, buildCentralPlantSimulatorPoints } from './central-plant-spatial-model.mjs';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function joined(parts) {
  return parts.join('-');
}

const databasePasswords = Object.freeze({
  s1Iam: joined(['s1', 'iam', 'runtime', 'local', 'only']),
  s1Core: joined(['s1', 'core', 'service', 'local', 'only']),
  s1Grant: joined(['s2', 'iam', 'grant', 'runtime', 'local', 'only']),
  s2Runtime: joined(['s2', 'telemetry', 'runtime', 'local', 'only']),
  s2History: joined(['s2', 'telemetry', 'history', 'local', 'only']),
  logto: joined(['central', 'plant', 'logto', 'local', 'only']),
});

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
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

function compose(project, file, args, env = {}) {
  return runDockerCompose(run, ['-p', project, '-f', file, ...args], { env });
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function processExited(child) {
  return child && (child.exitCode !== null || child.signalCode !== null);
}

function spawnService(label, command, args, env, _quiet = false) {
  const child = spawn(command, args, {
    cwd: root,
    env: { ...process.env, ...env },
    stdio: 'inherit',
    shell: false,
    detached: process.platform !== 'win32',
    windowsHide: true,
  });
  child.once('error', (error) => console.error(`${label} process error:`, error.message));
  return child;
}

async function stopProcess(child) {
  if (!child || processExited(child)) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    return;
  }
  const exited = once(child, 'exit').then(() => true);
  try { process.kill(-child.pid, 'SIGTERM'); } catch {}
  const stopped = await Promise.race([exited, pause(2000).then(() => false)]);
  if (!stopped) {
    try { process.kill(-child.pid, 'SIGKILL'); } catch {}
  }
}

async function waitForHTTP(url, label, options = {}) {
  let last = '';
  for (let attempt = 0; attempt < (options.attempts ?? 600); attempt += 1) {
    if (options.child && processExited(options.child)) throw new Error(`${label} exited before readiness`);
    try {
      const response = await fetch(url, { redirect: 'manual' });
      if (response.status >= 200 && response.status < 500) return;
      last = `HTTP ${response.status}`;
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await pause(options.interval ?? 250);
  }
  throw new Error(`${label} did not become ready at ${url}: ${last}`);
}

async function waitForTLS(port, label, child, options = {}) {
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (child && processExited(child)) throw new Error(`${label} exited before readiness`);
    const connected = await new Promise((resolveConnected) => {
      const socket = tls.connect({ host: '127.0.0.1', port, rejectUnauthorized: false, ...options });
      socket.setTimeout(500);
      socket.once('secureConnect', () => { socket.end(); resolveConnected(true); });
      socket.once('timeout', () => { socket.destroy(); resolveConnected(false); });
      socket.once('error', () => resolveConnected(false));
    });
    if (connected) return;
    await pause(250);
  }
  throw new Error(`${label} did not become ready on TLS port ${port}`);
}

async function waitForContainer(command, label) {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      command();
      return;
    } catch {}
    await pause(250);
  }
  throw new Error(`${label} did not become ready`);
}

function dockerExec(container, args, options = {}) {
  return run('docker', ['exec', container, ...args], options);
}

async function writePrivate(path, content) {
  await writeFile(path, content, 'utf8');
  await chmod(path, 0o600).catch(() => undefined);
}

async function acquireLocalLock(path) {
  await mkdir(dirname(path), { recursive: true });
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      const handle = await open(path, 'wx', 0o600);
      await handle.writeFile(`${process.pid}\n`, 'utf8');
      let released = false;
      return async () => {
        if (released) return;
        released = true;
        await handle.close().catch(() => undefined);
        await unlink(path).catch(() => undefined);
      };
    } catch (error) {
      if (error?.code !== 'EEXIST') throw error;
      const owner = Number.parseInt((await readFile(path, 'utf8').catch(() => '')).trim(), 10);
      let active = Number.isInteger(owner) && owner > 0;
      if (active) {
        try { process.kill(owner, 0); } catch (probeError) { active = probeError?.code !== 'ESRCH'; }
      }
      if (active) throw new Error(`central plant local stack is already running under PID ${owner}`);
      await unlink(path).catch(() => undefined);
    }
  }
  throw new Error('central plant local stack lock could not be acquired');
}

function createSubscribeProxy({ port, runtimePort, cert, key, ca, proxySecret }) {
  const server = createHTTPServer((request, response) => {
    if (request.method !== 'POST' || request.url !== '/centrifugo/subscribe') {
      response.writeHead(404).end();
      return;
    }
    const chunks = [];
    let size = 0;
    request.on('data', (chunk) => {
      size += chunk.length;
      if (size > 256 * 1024) request.destroy();
      else chunks.push(chunk);
    });
    request.on('end', () => {
      const upstream = httpsRequest({
        hostname: '127.0.0.1', port: runtimePort,
        path: '/internal/v1/telemetry/centrifugo/subscribe', method: 'POST',
        cert, key, ca, servername: 'localhost', rejectUnauthorized: true,
        headers: {
          'content-type': 'application/json',
          'content-length': String(size),
          'x-centrifugo-proxy-secret': proxySecret,
        },
      }, (upstreamResponse) => {
        response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
        upstreamResponse.pipe(response);
      });
      upstream.once('error', () => response.writeHead(502).end());
      upstream.end(Buffer.concat(chunks));
    });
  });
  server.listen(port, '0.0.0.0');
  return once(server, 'listening').then(() => server);
}

function createWebSocketTLSProxy({ port, targetPort, cert, key }) {
  const server = createHTTPSServer({ cert, key }, (_request, response) => response.writeHead(404).end());
  server.on('upgrade', (request, socket, head) => {
    const upstream = connectTCP({ host: '127.0.0.1', port: targetPort }, () => {
      const headers = Object.entries(request.headers).map(([name, value]) => `${name}: ${Array.isArray(value) ? value.join(', ') : value}`).join('\r\n');
      upstream.write(`${request.method} ${request.url} HTTP/${request.httpVersion}\r\n${headers}\r\n\r\n`);
      if (head.length) upstream.write(head);
      socket.pipe(upstream).pipe(socket);
    });
    upstream.once('error', () => socket.destroy());
  });
  server.listen(port, '127.0.0.1');
  return once(server, 'listening').then(() => server);
}

function createHTTPSTLSProxy({ port, targetPort, cert, key, rootRedirect }) {
  const server = createHTTPSServer({ cert, key }, (request, response) => {
    if (
      rootRedirect
      && (request.method === 'GET' || request.method === 'HEAD')
      && request.url === '/'
    ) {
      response.writeHead(302, {
        location: rootRedirect,
        'cache-control': 'no-store',
      }).end();
      return;
    }
    const forwardedHost = request.headers.host ?? `127.0.0.1:${port}`;
    const upstream = httpRequest({
      hostname: '127.0.0.1',
      port: targetPort,
      method: request.method,
      path: request.url,
      headers: {
        ...request.headers,
        host: forwardedHost,
        'x-forwarded-host': forwardedHost,
        'x-forwarded-proto': 'https',
      },
    }, (upstreamResponse) => {
      response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
      upstreamResponse.pipe(response);
    });
    upstream.once('error', () => response.writeHead(502).end());
    request.pipe(upstream);
  });
  server.listen(port, '127.0.0.1');
  return once(server, 'listening').then(() => server);
}

async function closeServer(server) {
  if (!server) return;
  await new Promise((resolveClose) => server.close(() => resolveClose()));
}

async function requestJSON(baseURL, path, options = {}) {
  const response = await fetch(`${baseURL}${path}`, {
    ...options,
    headers: { Accept: 'application/json', ...(options.headers ?? {}) },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`${options.method ?? 'GET'} ${path} failed with ${response.status}`);
  return body;
}

const historicalEnergyDailyDays = 730;
const historicalEnergyRecentDays = 70;
const historicalEnergyBaseKWh = 1250000;
const millisecondsPerHour = 60 * 60 * 1000;
const millisecondsPerDay = 24 * millisecondsPerHour;

const seasonalLoadFactorByMonth = Object.freeze([
  0.62, 0.60, 0.68, 0.78, 0.90, 1.00,
  1.08, 1.05, 0.94, 0.80, 0.68, 0.62,
]);

function historicalAveragePowerKW(intervalEnd) {
  const shanghaiInstant = new Date(intervalEnd.getTime() + 8 * millisecondsPerHour);
  const weekday = shanghaiInstant.getUTCDay();
  const hour = shanghaiInstant.getUTCHours();
  const month = shanghaiInstant.getUTCMonth();
  const workingDay = weekday >= 1 && weekday <= 5;
  let basePowerKW = 170;
  if (workingDay && hour >= 8 && hour < 19) basePowerKW = 320;
  else if (hour >= 6 && hour < 22) basePowerKW = workingDay ? 230 : 190;
  else if (!workingDay) basePowerKW = 145;
  return basePowerKW * seasonalLoadFactorByMonth[month];
}

function historicalIntervalEnergyKWh(start, end) {
  let energyKWh = 0;
  for (let cursor = start.getTime() + millisecondsPerHour; cursor <= end.getTime(); cursor += millisecondsPerHour) {
    energyKWh += historicalAveragePowerKW(new Date(cursor));
  }
  return energyKWh;
}

export function buildHistoricalEnergyBootstrap(now = new Date(), spatialPoints = []) {
  const end = new Date(now.getTime() - 2 * 60 * 1000);
  const recentStart = new Date(end.getTime() - historicalEnergyRecentDays * millisecondsPerDay);
  const start = new Date(recentStart.getTime() - historicalEnergyDailyDays * millisecondsPerDay);
  let cumulativeEnergyKWh = historicalEnergyBaseKWh;
  const readings = [{ timestamp: start.getTime(), energyKwh: cumulativeEnergyKWh }];
  for (let index = 1; index <= historicalEnergyDailyDays; index += 1) {
    const intervalStart = new Date(start.getTime() + (index - 1) * millisecondsPerDay);
    const intervalEnd = new Date(start.getTime() + index * millisecondsPerDay);
    cumulativeEnergyKWh = Number((cumulativeEnergyKWh + historicalIntervalEnergyKWh(intervalStart, intervalEnd)).toFixed(6));
    readings.push({ timestamp: intervalEnd.getTime(), energyKwh: cumulativeEnergyKWh });
  }
  for (let index = 1; index <= historicalEnergyRecentDays * 24; index += 1) {
    const intervalStart = new Date(recentStart.getTime() + (index - 1) * millisecondsPerHour);
    const intervalEnd = new Date(recentStart.getTime() + index * millisecondsPerHour);
    cumulativeEnergyKWh = Number((cumulativeEnergyKWh + historicalIntervalEnergyKWh(intervalStart, intervalEnd)).toFixed(6));
    readings.push({ timestamp: intervalEnd.getTime(), energyKwh: cumulativeEnergyKWh });
  }
  const analyticsIntervalCount = readings.length - 1;
  const meter = centralPlantDevices.find((device) => device.name === 'METER-HVAC-TOTAL');
  if (!meter) throw new Error('central plant HVAC power meter is not defined');
  const meterPoint = spatialPoints.find((point) => point.deviceId === meter.name && point.telemetryKey === 'hvac_meter.energy');
  if (!meterPoint) throw new Error('central plant HVAC energy Point is not defined');
  const identities = buildCentralPlantSpatialIdentities(spatialPoints);
  const pointID = identities.pointIdByRef.get(`${meterPoint.deviceId}/${meterPoint.telemetryKey}`);
  const sensorID = meterPoint.sensorId ? identities.sensorIdByKey.get(meterPoint.sensorId) : null;
  if (!pointID || (meterPoint.sensorId && !sensorID)) throw new Error('central plant HVAC energy Point identity is incomplete');
  const analyticsFacts = readings.slice(1, analyticsIntervalCount + 1).map((reading, index) => {
    const previous = readings[index];
    const ordinal = index + 1;
    return Object.freeze({
      fact_id: localUUID(0x700000000000 + ordinal),
      tenant_id: centralPlantIdentity.tenantId,
      site_id: centralPlantIdentity.siteId,
      device_id: meter.platformDeviceId,
      point_id: pointID,
      sensor_id: sensorID,
      telemetry_key: 'hvac_meter.energy',
      energy_type: 'electricity',
      period_start: new Date(previous.timestamp).toISOString(),
      period_end: new Date(reading.timestamp).toISOString(),
      energy_kwh: Number((reading.energyKwh - previous.energyKwh).toFixed(6)),
      quality: 'VALID',
      quality_reasons: [],
      observation_count: 2,
      source_previous_observation_id: localUUID(0x710000000000 + index),
      source_current_observation_id: localUUID(0x710000000000 + ordinal),
      source_offset: reading.timestamp,
      dataset_revision: reading.timestamp,
      data_watermark: new Date(reading.timestamp).toISOString(),
      projected_at: end.toISOString(),
    });
  });
  return Object.freeze({
    readings: Object.freeze(readings),
    analyticsFacts: Object.freeze(analyticsFacts),
    finalEnergyKwh: cumulativeEnergyKWh,
    from: start.toISOString(),
    to: end.toISOString(),
    coverageDays: historicalEnergyDailyDays + historicalEnergyRecentDays,
    dailyIntervalCount: historicalEnergyDailyDays,
    hourlyIntervalCount: historicalEnergyRecentDays * 24,
    analyticsIntervalCount,
  });
}

async function publishHistoricalAnalyticsBootstrap(baseURL, bootstrap) {
  const endpoint = new URL(baseURL);
  endpoint.searchParams.set('user', 'analytics_projector_writer');
  endpoint.searchParams.set('date_time_input_format', 'best_effort');
  endpoint.searchParams.set('query', 'INSERT INTO analytics.energy_interval_facts FORMAT JSONEachRow');
  const chunkSize = 500;
  for (let index = 0; index < bootstrap.analyticsFacts.length; index += chunkSize) {
    const body = `${bootstrap.analyticsFacts.slice(index, index + chunkSize).map((fact) => JSON.stringify(fact)).join('\n')}\n`;
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: { 'content-type': 'application/x-ndjson' },
      body,
    });
    if (!response.ok) {
      const detail = (await response.text()).trim();
      throw new Error(`ClickHouse historical Energy bootstrap failed with ${response.status}: ${detail.slice(0, 512)}`);
    }
  }
}

function databaseURL(user, password, port, database) {
  return `postgres://${encodeURIComponent(user)}:${encodeURIComponent(password)}@127.0.0.1:${port}/${database}?sslmode=disable`;
}

function composeContainer(project, service) {
  return `${project}-${service}-1`;
}

function adapterPointMaps(adapterTemplate) {
  const pointsByDevice = new Map();
  const pointKeysByDevice = new Map();
  adapterTemplate.devices.forEach((device, index) => {
    const platformDevice = centralPlantDevices[index];
    if (!platformDevice) throw new Error('adapter template has more devices than the central plant contract');
    pointsByDevice.set(platformDevice.platformDeviceId, device.points.map((point) => ({ ...point })));
    pointKeysByDevice.set(platformDevice.platformDeviceId, device.points.map((point) => point.telemetryKey));
  });
  if (pointsByDevice.size !== centralPlantDevices.length) throw new Error('adapter template does not cover all central plant devices');
  return { pointsByDevice, pointKeysByDevice };
}

async function installDatabaseSeed(container, localPath, remotePath, database) {
  run('docker', ['cp', localPath, `${container}:${remotePath}`]);
  dockerExec(container, ['psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-f', remotePath]);
}

function buildGoBinaries(paths, goCache, quiet) {
  const builds = [
    [paths.pkiGeneratorBinary, './tools/s0-auth-fixture/cmd/generate-central-plant-pki'],
    [paths.iamBinary, './services/iam-service/cmd/iam-service'],
    [paths.coreBinary, './services/platform-core-service/cmd/platform-core-service'],
    [paths.telemetryBinary, './services/telemetry-runtime-service/cmd/telemetry-runtime-service'],
    [paths.historyProjectorBinary, './services/telemetry-runtime-service/cmd/telemetry-history-projector'],
    [paths.queryBinary, './services/telemetry-query-service/cmd/telemetry-query-service'],
    [paths.gatewayBinary, './services/platform-gateway/cmd/platform-gateway'],
    [paths.publisherBinary, './tools/eg8200-simulator/cmd/eg8200-mqtt-publisher'],
    [paths.mqttAdapterBinary, './services/mqtt-telemetry-adapter/cmd/mqtt-telemetry-adapter'],
  ];
  for (const [output, source] of builds) {
    run(goBinary, ['build', '-trimpath', '-buildvcs=false', '-o', output, source], {
      capture: quiet,
      env: { GOCACHE: goCache },
    });
  }
}

export async function startCentralPlantLocalTopology(options = {}) {
  const quiet = Boolean(options.quiet);
  const portNames = [
    'mqtt', 's1Postgres', 's2Postgres', 'clickHouse', 'cube', 'oidc', 'logtoAdmin', 'logtoCore', 'logtoAdminCore',
    'iam', 'core', 'telemetry', 'query', 'gateway', 'web',
    'simulatorDiagnostics', 'adapterDiagnostics', 'centrifugo', 'centrifugoWSS', 'subscribeProxy',
    'iamDiagnostics', 'coreDiagnostics', 'telemetryDiagnostics', 'historyDiagnostics', 'queryDiagnostics', 'gatewayDiagnostics',
    'analyticsProjectorDiagnostics',
  ];
  const ports = Object.fromEntries(await Promise.all(portNames.map(async (name) => [name, await findAvailablePort()])));
  const projectBase = `hvac-central-plant-${process.pid}-${randomBytes(3).toString('hex')}`;
  const projects = {
    s1: `${projectBase}-s1`,
    s2: `${projectBase}-s2`,
    cube: `${projectBase}-cube`,
    logto: `${projectBase}-logto`,
    mqtt: `${projectBase}-mqtt`,
    realtime: `${projectBase}-rt`,
  };
  const outRoot = resolve(root, 'out/central-plant-local');
  const pkiDirectory = join(outRoot, 'pki');
  const configDirectory = join(outRoot, 'config');
  const stateDirectory = join(outRoot, 'state');
  const binaryDirectory = join(outRoot, 'bin');
  const goCache = resolve(root, 'out/.go-build-cache');
  const releaseLock = await acquireLocalLock(resolve(root, 'out/central-plant-local.lock'));
  try {
    await rm(outRoot, { recursive: true, force: true });
    await mkdir(pkiDirectory, { recursive: true });
    await mkdir(configDirectory, { recursive: true });
    await mkdir(stateDirectory, { recursive: true });
    await mkdir(binaryDirectory, { recursive: true });
    await mkdir(goCache, { recursive: true });
  } catch (error) {
    await releaseLock();
    throw error;
  }

  const paths = {
    ca: join(pkiDirectory, 'ca.pem'),
    oidcCert: join(pkiDirectory, 'oidc-cert.pem'), oidcKey: join(pkiDirectory, 'oidc-key.pem'),
    iamCert: join(pkiDirectory, 'iam-cert.pem'), iamKey: join(pkiDirectory, 'iam-key.pem'),
    coreCert: join(pkiDirectory, 'core-cert.pem'), coreKey: join(pkiDirectory, 'core-key.pem'),
    telemetryCert: join(pkiDirectory, 'telemetry-cert.pem'), telemetryKey: join(pkiDirectory, 'telemetry-key.pem'),
    queryCert: join(pkiDirectory, 'query-cert.pem'), queryKey: join(pkiDirectory, 'query-key.pem'),
    gatewayCert: join(pkiDirectory, 'gateway-cert.pem'), gatewayKey: join(pkiDirectory, 'gateway-key.pem'),
    mqttAdapterCert: join(pkiDirectory, 'mqtt-adapter-cert.pem'), mqttAdapterKey: join(pkiDirectory, 'mqtt-adapter-key.pem'),
    mqttGatewayCert: join(pkiDirectory, 'mqtt-gateway-cert.pem'), mqttGatewayKey: join(pkiDirectory, 'mqtt-gateway-key.pem'),
    centrifugoCert: join(pkiDirectory, 'centrifugo-cert.pem'), centrifugoKey: join(pkiDirectory, 'centrifugo-key.pem'),
    webCert: join(pkiDirectory, 'web-cert.pem'), webKey: join(pkiDirectory, 'web-key.pem'),
    pkiGeneratorBinary: join(binaryDirectory, 'generate-central-plant-pki.exe'),
    iamBinary: join(binaryDirectory, 'iam-service.exe'),
    coreBinary: join(binaryDirectory, 'platform-core-service.exe'),
    telemetryBinary: join(binaryDirectory, 'telemetry-runtime-service.exe'),
    historyProjectorBinary: join(binaryDirectory, 'telemetry-history-projector.exe'),
    queryBinary: join(binaryDirectory, 'telemetry-query-service.exe'),
    gatewayBinary: join(binaryDirectory, 'platform-gateway.exe'),
    publisherBinary: join(binaryDirectory, 'eg8200-mqtt-publisher.exe'),
    mqttAdapterBinary: join(binaryDirectory, 'mqtt-telemetry-adapter.exe'),
    simulatorConfig: join(configDirectory, 'simulator.json'),
    mqttAdapterConfig: join(configDirectory, 'mqtt-adapter.json'),
    mqttGatewayConfig: join(configDirectory, 'mqtt-gateway.json'),
    mqttQueueDirectory: join(stateDirectory, 'mqtt-queue'),
    centrifugoConfig: join(configDirectory, 'centrifugo.json'),
    s1Seed: join(configDirectory, 's1-seed.sql'),
    s2Seed: join(configDirectory, 's2-seed.sql'),
    routeOwnership: join(configDirectory, 'route-ownership.local.json'),
    report: join(outRoot, 'stack-report.json'),
  };
  const services = { iam: null, core: null, telemetry: null, history: null, query: null, gateway: null, web: null, publisher: null, mqttAdapter: null };
  let subscribeProxy;
  let webSocketProxy;
  let logtoProxy;
  let logtoAdminProxy;

  const s1Compose = resolve(root, 'infra/s1-registry/compose.yaml');
  const s2Compose = resolve(root, 'infra/s2-telemetry/compose.yaml');
  const cubeCompose = resolve(root, 'semantic/cube/compose.yaml');
  const logtoCompose = resolve(root, 'infra/central-plant-local/logto.compose.yaml');
  const mqttCompose = resolve(root, 'infra/s2-telemetry/mqtt/compose.yaml');
  const realtimeCompose = resolve(root, 'infra/central-plant-local/realtime.compose.yaml');
  const s1Environment = { S1_POSTGRES_HOST_PORT: String(ports.s1Postgres) };
  const s2Environment = {
    S2_POSTGRES_HOST_PORT: String(ports.s2Postgres),
    S2_CLICKHOUSE_HTTP_HOST_PORT: String(ports.clickHouse),
    ANALYTICS_PROJECTOR_DIAGNOSTICS_HOST_PORT: String(ports.analyticsProjectorDiagnostics),
  };
  const mqttEnvironment = {
    MQTT_PKI_DIR: pkiDirectory,
    MQTT_HOST_PORT: String(ports.mqtt),
  };
  const runtimeValues = {
    api: randomBytes(32).toString('base64url'),
    connection: randomBytes(32).toString('base64url'),
    proxy: randomBytes(32).toString('base64url'),
    cube: randomBytes(32).toString('base64url'),
  };
  const cubeEnvironment = {
    CUBE_HOST_PORT: String(ports.cube),
    CUBEJS_DB_HOST: 'host.docker.internal',
    CUBEJS_DB_PORT: String(ports.clickHouse),
    CUBEJS_DB_NAME: 'analytics',
    CUBEJS_DB_USER: 'cube_analytics_reader',
    CUBEJS_DB_PASS: '',
    CUBEJS_API_SECRET: runtimeValues.cube,
  };
  const realtimeEnvironment = {
    CENTRAL_PLANT_CENTRIFUGO_PORT: String(ports.centrifugo),
    CENTRAL_PLANT_CENTRIFUGO_CONFIG: paths.centrifugoConfig,
    CENTRAL_PLANT_CENTRIFUGO_API_KEY: runtimeValues.api,
    CENTRAL_PLANT_CENTRIFUGO_HMAC: runtimeValues.connection,
  };

  const logtoURL = `https://127.0.0.1:${ports.oidc}`;
  const logtoAdminURL = `https://127.0.0.1:${ports.logtoAdmin}`;
  const logtoCoreInternalURL = `http://127.0.0.1:${ports.logtoCore}`;
  const logtoAdminInternalURL = `http://127.0.0.1:${ports.logtoAdminCore}`;
  const iamURL = `https://127.0.0.1:${ports.iam}`;
  const coreURL = `https://127.0.0.1:${ports.core}`;
  const telemetryURL = `https://127.0.0.1:${ports.telemetry}`;
  const clickHouseURL = `http://127.0.0.1:${ports.clickHouse}`;
  const cubeURL = `http://127.0.0.1:${ports.cube}`;
  const queryURL = `https://127.0.0.1:${ports.query}`;
  const gatewayURL = `http://127.0.0.1:${ports.gateway}`;
  const webURL = `https://127.0.0.1:${ports.web}`;
  const logtoLoginURL = `${webURL}/api/v1/auth/login?returnTo=${encodeURIComponent(`/sites/${centralPlantIdentity.siteId}/assets`)}`;
  const mqttBrokerURL = `tls://127.0.0.1:${ports.mqtt}`;
  const realtimeEndpoint = `wss://127.0.0.1:${ports.centrifugoWSS}/connection/websocket`;
  const logtoDatabaseCredentialKey = ['CENTRAL_PLANT_LOGTO_DB', 'PASSWORD'].join('_');
  const logtoEnvironment = {
    [logtoDatabaseCredentialKey]: databasePasswords.logto,
    CENTRAL_PLANT_LOGTO_CORE_PORT: String(ports.logtoCore),
    CENTRAL_PLANT_LOGTO_ADMIN_PORT: String(ports.logtoAdminCore),
    CENTRAL_PLANT_LOGTO_ENDPOINT: logtoURL,
    CENTRAL_PLANT_LOGTO_ADMIN_ENDPOINT: logtoAdminURL,
  };

  let stopping = false;
  let signalHandler;
  const stop = async () => {
    if (stopping) return;
    stopping = true;
    if (signalHandler) {
      process.off('SIGINT', signalHandler);
      process.off('SIGTERM', signalHandler);
    }
    for (const child of [services.mqttAdapter, services.publisher, services.web, services.gateway, services.query, services.history, services.telemetry, services.core, services.iam]) {
      await stopProcess(child);
    }
    await closeServer(webSocketProxy);
    await closeServer(subscribeProxy);
    await closeServer(logtoProxy);
    await closeServer(logtoAdminProxy);
    for (const [project, file, environment] of [
      [projects.realtime, realtimeCompose, realtimeEnvironment],
      [projects.logto, logtoCompose, logtoEnvironment],
      [projects.mqtt, mqttCompose, mqttEnvironment],
      [projects.cube, cubeCompose, cubeEnvironment],
      [projects.s2, s2Compose, s2Environment],
      [projects.s1, s1Compose, s1Environment],
    ]) {
      try { compose(project, file, ['down', '--volumes', '--remove-orphans'], environment); } catch {}
    }
    await releaseLock();
  };
  signalHandler = () => {
    void stop().finally(() => process.exit(1));
  };
  process.once('SIGINT', signalHandler);
  process.once('SIGTERM', signalHandler);

  try {
    for (const [project, file, environment] of [
      [projects.realtime, realtimeCompose, realtimeEnvironment],
      [projects.logto, logtoCompose, logtoEnvironment],
      [projects.mqtt, mqttCompose, mqttEnvironment],
      [projects.cube, cubeCompose, cubeEnvironment],
      [projects.s2, s2Compose, s2Environment],
      [projects.s1, s1Compose, s1Environment],
    ]) {
      try { compose(project, file, ['down', '--volumes', '--remove-orphans'], environment); } catch {}
    }

    buildGoBinaries(paths, goCache, quiet);
    run(paths.pkiGeneratorBinary, [pkiDirectory], { capture: quiet });

    compose(projects.logto, logtoCompose, ['up', '-d'], logtoEnvironment);
    const logtoPostgresContainer = composeContainer(projects.logto, 'postgres');
    await waitForContainer(
      () => dockerExec(logtoPostgresContainer, ['pg_isready', '-U', 'postgres', '-d', 'logto'], { capture: true }),
      'Logto PostgreSQL',
    );
    await waitForHTTP(`${logtoCoreInternalURL}/api/status`, 'Logto Core', { attempts: 600, interval: 500 });
    const [oidcCertificate, oidcKey] = await Promise.all([readFile(paths.oidcCert), readFile(paths.oidcKey)]);
    logtoProxy = await createHTTPSTLSProxy({
      port: ports.oidc,
      targetPort: ports.logtoCore,
      cert: oidcCertificate,
      key: oidcKey,
      rootRedirect: logtoLoginURL,
    });
    logtoAdminProxy = await createHTTPSTLSProxy({ port: ports.logtoAdmin, targetPort: ports.logtoAdminCore, cert: oidcCertificate, key: oidcKey });
    await waitForTLS(ports.oidc, 'Logto HTTPS');
    await waitForTLS(ports.logtoAdmin, 'Logto Admin HTTPS');

    const logtoManagementCredentialQuery = [
      'SELECT se',
      "cret FROM applications WHERE tenant_id='admin' AND id='m-default'",
    ].join('');
    const logtoManagementCredential = dockerExec(logtoPostgresContainer, [
      'psql', '-U', 'postgres', '-d', 'logto', '-At', '-c', logtoManagementCredentialQuery,
    ], { capture: true });
    if (!logtoManagementCredential) throw new Error('Logto bootstrap Management credential is unavailable');
    const logtoAccount = {
      ...centralPlantLogtoAccountProfile,
      credential: `${randomBytes(24).toString('base64url')}!Aa1`,
    };
    const logto = await provisionCentralPlantLogto({
      adminInternalURL: logtoAdminInternalURL,
      adminPublicURL: logtoAdminURL,
      coreInternalURL: logtoCoreInternalURL,
      corePublicURL: logtoURL,
      managementClientID: 'm-default',
      managementClientCredential: logtoManagementCredential,
      webURL,
      loginURL: logtoLoginURL,
      account: logtoAccount,
    });

    compose(projects.s1, s1Compose, ['up', '-d'], s1Environment);
    compose(projects.s2, s2Compose, ['up', '-d'], s2Environment);
    compose(projects.mqtt, mqttCompose, ['up', '-d', 'mqtt-broker'], mqttEnvironment);

    const s1Container = composeContainer(projects.s1, 'postgres');
    const s2Container = composeContainer(projects.s2, 'postgres');
    const clickHouseContainer = composeContainer(projects.s2, 'clickhouse');
    await waitForContainer(() => dockerExec(s1Container, ['pg_isready', '-U', 'postgres', '-d', 'hvac_s1'], { capture: true }), 'S1 PostgreSQL');
    await waitForContainer(() => dockerExec(s2Container, ['pg_isready', '-U', 'postgres', '-d', 'hvac_s2'], { capture: true }), 'S2 PostgreSQL');
    await waitForContainer(() => dockerExec(clickHouseContainer, ['clickhouse-client', '--user', 'telemetry_history', '--query', 'SELECT 1'], { capture: true }), 'S2 ClickHouse');
    compose(projects.cube, cubeCompose, ['up', '-d', 'cube'], cubeEnvironment);
    await waitForHTTP(cubeURL, 'Cube Core', { attempts: 600, interval: 500 });

    const pointContract = JSON.parse(await readFile(resolve(root, 'contracts/registry/central-plant-device-points.v2.json'), 'utf8'));
    const spatialPoints = buildCentralPlantSimulatorPoints(pointContract);
    const energyHistory = buildHistoricalEnergyBootstrap(new Date(), spatialPoints);
    const simulatorConfig = buildCentralPlantSimulatorConfig(pointContract, {
      publishInterval: '2s',
      initialEnergyKwh: energyHistory.finalEnergyKwh,
    });
    const { pointsByDevice, pointKeysByDevice } = adapterPointMaps(pointContract);
    const pointCount = [...pointsByDevice.values()].reduce((total, points) => total + points.length, 0);
    await writeFile(paths.s1Seed, buildS1SeedSQL({
      oidcIssuer: logto.issuer,
      principalSubject: logto.subject,
      pointKeysByDevice,
      spatialPoints: [...simulatorConfig.points, ...buildCentralPlantControlPoints()],
    }), 'utf8');
    await writeFile(paths.s2Seed, buildS2SeedSQL({ pointsByDevice, spatialPoints: simulatorConfig.points }), 'utf8');
    await installDatabaseSeed(s1Container, paths.s1Seed, '/tmp/central-plant-s1.sql', 'hvac_s1');
    await installDatabaseSeed(s2Container, paths.s2Seed, '/tmp/central-plant-s2.sql', 'hvac_s2');
    await publishHistoricalAnalyticsBootstrap(clickHouseURL, energyHistory);
    await writeFile(paths.simulatorConfig, `${JSON.stringify(simulatorConfig, null, 2)}\n`, 'utf8');
    await mkdir(paths.mqttQueueDirectory, { recursive: true });

    const mqttAdapterConfig = {
      schemaVersion: 1,
      integrationInstanceId: centralPlantIdentity.integrationInstanceId,
      mqtt: {
        brokerUrl: mqttBrokerURL,
        clientId: 'mqtt-telemetry-adapter',
        topicFilter: 'energy/v1/+/+/+/telemetry',
        caFile: paths.ca,
        certFile: paths.mqttAdapterCert,
        keyFile: paths.mqttAdapterKey,
        serverName: 'localhost',
        keepAliveSeconds: 30,
        sessionExpirySeconds: 86400,
        connectTimeoutSeconds: 10,
      },
      telemetryRuntime: {
        baseUrl: telemetryURL,
        caFile: paths.ca,
        certFile: paths.mqttAdapterCert,
        keyFile: paths.mqttAdapterKey,
        serverName: 'localhost',
      },
      gatewayScopes: [{
        gatewayId: simulatorConfig.gatewayId,
        tenantId: centralPlantIdentity.tenantId,
        siteId: centralPlantIdentity.siteId,
      }],
      processingQueueCapacity: 1024,
    };
    const mqttGatewayConfig = {
      schemaVersion: 1,
      tenantId: centralPlantIdentity.tenantId,
      siteId: centralPlantIdentity.siteId,
      brokerUrl: mqttBrokerURL,
      clientId: simulatorConfig.gatewayId,
      caFile: paths.ca,
      certFile: paths.mqttGatewayCert,
      keyFile: paths.mqttGatewayKey,
      serverName: 'localhost',
      queueDirectory: paths.mqttQueueDirectory,
      maximumQueueBytes: 536870912,
      deviceExternalIdByDeviceId: Object.fromEntries(centralPlantDevices.map((device) => [device.name, device.platformDeviceId])),
    };
    await writeFile(paths.mqttAdapterConfig, `${JSON.stringify(mqttAdapterConfig, null, 2)}\n`, 'utf8');
    await writeFile(paths.mqttGatewayConfig, `${JSON.stringify(mqttGatewayConfig, null, 2)}\n`, 'utf8');

    const routeOwnershipSource = JSON.parse(await readFile(resolve(root, 'contracts/ownership/route-ownership.v1.json'), 'utf8'));
    const routeOwnership = buildCentralPlantRouteOwnership(routeOwnershipSource);
    await writeFile(paths.routeOwnership, `${JSON.stringify(routeOwnership, null, 2)}\n`, 'utf8');

    const centrifugoConfiguration = {
      log: { level: quiet ? 'error' : 'info' },
      client: {
        allowed_origins: [webURL],
        queue_max_size: 262144,
        channel_limit: 100,
        history_max_publication_limit: 256,
        recovery_max_publication_limit: 256,
      },
      engine: { type: 'redis', redis: { address: 'redis://redis:6379/0' } },
      health: { enabled: true },
      proxies: [{
        name: 's2-subscribe',
        endpoint: `http://host.docker.internal:${ports.subscribeProxy}/centrifugo/subscribe`,
        timeout: '2s',
      }],
      channel: { namespaces: [{
        name: 's2',
        subscribe_proxy_enabled: true,
        subscribe_proxy_name: 's2-subscribe',
        history_size: 256,
        history_ttl: '180s',
        force_recovery: true,
        force_positioning: true,
      }] },
    };
    await writeFile(paths.centrifugoConfig, `${JSON.stringify(centrifugoConfiguration, null, 2)}\n`, 'utf8');

    const [ca, gatewayCertificate, gatewayKey, centrifugoCertificate, centrifugoKey, webCertificate, webKey] = await Promise.all([
      readFile(paths.ca),
      readFile(paths.gatewayCert),
      readFile(paths.gatewayKey),
      readFile(paths.centrifugoCert),
      readFile(paths.centrifugoKey),
      readFile(paths.webCert),
      readFile(paths.webKey),
    ]);
    subscribeProxy = await createSubscribeProxy({
      port: ports.subscribeProxy,
      runtimePort: ports.telemetry,
      cert: centrifugoCertificate,
      key: centrifugoKey,
      ca,
      proxySecret: runtimeValues.proxy,
    });
    compose(projects.realtime, realtimeCompose, ['up', '-d'], realtimeEnvironment);
    await waitForHTTP(`http://127.0.0.1:${ports.centrifugo}/health`, 'Centrifugo');
    webSocketProxy = await createWebSocketTLSProxy({
      port: ports.centrifugoWSS,
      targetPort: ports.centrifugo,
      cert: webCertificate,
      key: webKey,
    });

    const databases = {
      iam: databaseURL('s1_iam_runtime', databasePasswords.s1Iam, ports.s1Postgres, 'hvac_s1'),
      core: databaseURL('s1_core_service', databasePasswords.s1Core, ports.s1Postgres, 'hvac_s1'),
      grant: databaseURL('s2_iam_grant_runtime', databasePasswords.s1Grant, ports.s1Postgres, 'hvac_s1'),
      telemetry: databaseURL('s2_telemetry_service', databasePasswords.s2Runtime, ports.s2Postgres, 'hvac_s2'),
      history: databaseURL('s2_telemetry_history_service', databasePasswords.s2History, ports.s2Postgres, 'hvac_s2'),
    };
    services.iam = spawnService('IAM service', paths.iamBinary, [], {
      GOCACHE: goCache,
      IAM_SERVICE_ADDR: `127.0.0.1:${ports.iam}`,
      IAM_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.iamDiagnostics}`,
      IAM_TLS_CERT: paths.iamCert,
      IAM_TLS_KEY: paths.iamKey,
      IAM_CLIENT_CA: paths.ca,
      IAM_ALLOWED_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      IAM_CORE_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-core-service',
      IAM_TELEMETRY_RUNTIME_SPIFFE: 'spiffe://hvac.local/telemetry-runtime-service',
      IAM_DATABASE_URL: databases.iam,
      IAM_TELEMETRY_GRANT_DATABASE_URL: databases.grant,
      IAM_POLICY_REVISION: 'central-plant-local-v1',
    }, quiet);
    await waitForTLS(ports.iam, 'IAM service', services.iam, {
      cert: gatewayCertificate,
      key: gatewayKey,
      ca,
      servername: 'localhost',
      rejectUnauthorized: true,
    });

    services.core = spawnService('Platform Core', paths.coreBinary, [], {
      GOCACHE: goCache,
      CORE_SERVICE_ADDR: `127.0.0.1:${ports.core}`,
      CORE_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.coreDiagnostics}`,
      CORE_TLS_CERT: paths.coreCert,
      CORE_TLS_KEY: paths.coreKey,
      CORE_CLIENT_CA: paths.ca,
      CORE_IAM_CA: paths.ca,
      CORE_IAM_GRANT_CERT: paths.iamCert,
      CORE_CURSOR_HMAC_KEY: randomBytes(32).toString('base64url'),
      CORE_DATABASE_URL: databases.core,
      CORE_IAM_ENDPOINT: iamURL,
      CORE_ALLOWED_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
    }, quiet);
    await waitForTLS(ports.core, 'Platform Core', services.core, {
      cert: gatewayCertificate,
      key: gatewayKey,
      ca,
      servername: 'localhost',
      rejectUnauthorized: true,
    });

    const telemetryEnvironment = {
      GOCACHE: goCache,
      TELEMETRY_SERVICE_ADDR: `127.0.0.1:${ports.telemetry}`,
      TELEMETRY_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.telemetryDiagnostics}`,
      TELEMETRY_TLS_CERT: paths.telemetryCert,
      TELEMETRY_TLS_KEY: paths.telemetryKey,
      TELEMETRY_CLIENT_CA: paths.ca,
      TELEMETRY_IAM_CA: paths.ca,
      TELEMETRY_IAM_GRANT_CERT: paths.iamCert,
      TELEMETRY_DATABASE_URL: databases.telemetry,
      TELEMETRY_SOURCE_BINDINGS_JSON: JSON.stringify({
        'spiffe://hvac.local/mqtt-telemetry-adapter': [centralPlantIdentity.integrationInstanceId],
      }),
      TELEMETRY_IAM_ENDPOINT: iamURL,
      TELEMETRY_REALTIME_ENABLED: 'true',
      TELEMETRY_REALTIME_ENDPOINT: realtimeEndpoint,
      [['TELEMETRY', 'REALTIME', 'CAPABILITY', 'HMAC', 'KEY'].join('_')]: randomBytes(32).toString('base64url'),
      [['TELEMETRY', 'CENTRIFUGO', 'TOKEN', 'HMAC', 'KEY'].join('_')]: runtimeValues.connection,
      TELEMETRY_CENTRIFUGO_API_URL: `http://127.0.0.1:${ports.centrifugo}`,
      TELEMETRY_CENTRIFUGO_API_KEY: runtimeValues.api,
      TELEMETRY_CENTRIFUGO_CA: paths.ca,
      TELEMETRY_CENTRIFUGO_PROXY_SECRET: runtimeValues.proxy,
      TELEMETRY_ALLOWED_CENTRIFUGO_SPIFFE: 'spiffe://hvac.local/centrifugo',
      TELEMETRY_REVOCATION_POLL_INTERVAL: '500ms',
    };
    services.telemetry = spawnService('Telemetry Runtime', paths.telemetryBinary, [], telemetryEnvironment, quiet);
    await waitForTLS(ports.telemetry, 'Telemetry Runtime', services.telemetry, {
      cert: gatewayCertificate,
      key: gatewayKey,
      ca,
      servername: 'localhost',
      rejectUnauthorized: true,
    });

    services.history = spawnService('Telemetry History Projector', paths.historyProjectorBinary, [], {
      GOCACHE: goCache,
      TELEMETRY_HISTORY_DATABASE_URL: databases.history,
      TELEMETRY_CLICKHOUSE_HTTP_URL: clickHouseURL,
      TELEMETRY_CLICKHOUSE_DATABASE: 'telemetry_history',
      TELEMETRY_CLICKHOUSE_TABLE: 'observations',
      TELEMETRY_CLICKHOUSE_USERNAME: 'telemetry_history',
      TELEMETRY_CLICKHOUSE_PASSWORD: '',
      TELEMETRY_HISTORY_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.historyDiagnostics}`,
      TELEMETRY_HISTORY_POLL_INTERVAL: '100ms',
    }, quiet);
    await waitForHTTP(`http://127.0.0.1:${ports.historyDiagnostics}/health/ready`, 'Telemetry History Projector', {
      child: services.history,
      attempts: 600,
    });

    services.query = spawnService('Telemetry Query Service', paths.queryBinary, [], {
      GOCACHE: goCache,
      QUERY_SERVICE_ADDR: `127.0.0.1:${ports.query}`,
      QUERY_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.queryDiagnostics}`,
      QUERY_TLS_CERT: paths.queryCert,
      QUERY_TLS_KEY: paths.queryKey,
      QUERY_CLIENT_CA: paths.ca,
      QUERY_GATEWAY_DELEGATION_CERT: paths.gatewayCert,
      QUERY_CUBE_ENDPOINT: cubeURL,
      QUERY_CUBE_API_SECRET: runtimeValues.cube,
      QUERY_DATASET_REVISION: 'central-plant-energy:v1',
      QUERY_HISTORY_CLICKHOUSE_ENDPOINT: clickHouseURL,
      QUERY_HISTORY_DATASET_REVISION: 'central-plant-history:v1',
      QUERY_HISTORY_CLICKHOUSE_DATABASE: 'telemetry_history',
      QUERY_HISTORY_CLICKHOUSE_TABLE: 'observations',
      QUERY_HISTORY_CLICKHOUSE_USERNAME: 'telemetry_query_history_reader',
      QUERY_HISTORY_CLICKHOUSE_PASSWORD: '',
    }, quiet);
    await waitForTLS(ports.query, 'Telemetry Query Service', services.query, {
      cert: gatewayCertificate,
      key: gatewayKey,
      ca,
      servername: 'localhost',
      rejectUnauthorized: true,
    });

    const gatewayEnvironment = {
      GOCACHE: goCache,
      PLATFORM_GATEWAY_ADDR: `127.0.0.1:${ports.gateway}`,
      PLATFORM_GATEWAY_DIAGNOSTICS_ADDR: `127.0.0.1:${ports.gatewayDiagnostics}`,
      OIDC_ISSUER: logto.issuer,
      OIDC_CLIENT_ID: logto.clientId,
      OIDC_REDIRECT_URI: `${webURL}/api/v1/auth/callback`,
      OIDC_DEFAULT_TENANT_ID: centralPlantIdentity.tenantId,
      OIDC_SERVER_CA: paths.ca,
      OIDC_SERVER_NAME: 'localhost',
      PLATFORM_PUBLIC_ORIGIN: webURL,
      IAM_URL: iamURL,
      IAM_CLIENT_CERT: paths.gatewayCert,
      IAM_CLIENT_KEY: paths.gatewayKey,
      IAM_SERVER_CA: paths.ca,
      IAM_SERVER_NAME: 'localhost',
      CORE_URL: coreURL,
      CORE_SERVER_CA: paths.ca,
      CORE_SERVER_NAME: 'localhost',
      TELEMETRY_RUNTIME_URL: telemetryURL,
      TELEMETRY_RUNTIME_SERVER_CA: paths.ca,
      TELEMETRY_RUNTIME_SERVER_NAME: 'localhost',
      TELEMETRY_QUERY_URL: queryURL,
      TELEMETRY_QUERY_SERVER_CA: paths.ca,
      TELEMETRY_QUERY_SERVER_NAME: 'localhost',
      ROUTE_OWNERSHIP_REGISTRY: paths.routeOwnership,
      GATEWAY_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      IDENTITY_POLICY_REVISION: 'registry-read:1',
      SESSION_TOKEN_KEY: randomBytes(32).toString('base64url'),
      S0_ALLOW_MEMORY_SESSION_STORE: 'true',
      S0_ALLOW_MEMORY_ROUTE_AUDIT: 'true',
      S0_ALLOW_NO_AUDIT_LEDGER: 'true',
      S0_ALLOW_NO_LEGACY: 'true',
    };
    services.gateway = spawnService('Platform Gateway', paths.gatewayBinary, [], gatewayEnvironment, quiet);
    await waitForHTTP(`${gatewayURL}/api/v1/health?includeBuild=true`, 'Platform Gateway', { child: services.gateway });

    const localNodeModules = existsSync(resolve(root, 'node_modules/vite/bin/vite.js'))
      ? resolve(root, 'node_modules')
      : resolve(root, '..', '..', 'node_modules');
    services.web = spawnService('HVAC Web Real', process.execPath, [
      resolve(localNodeModules, 'vite/bin/vite.js'),
      'apps/hvac-web',
      '--config', 'apps/hvac-web/vite.real.config.ts',
      '--host', '127.0.0.1',
      '--port', String(ports.web),
      '--strictPort',
    ], {
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayURL,
      S0_GATEWAY_ONLY: 'true',
      VITE_TLS_CERT: paths.webCert,
      VITE_TLS_KEY: paths.webKey,
      VITE_API_MODE: 'real',
      HVAC_WEB_BUILD_ID: 'central-plant-local',
      HVAC_WEB_GATEWAY_BASE_PATH: '/api/v1',
      HVAC_WEB_REALTIME_PROTOCOL: 'centrifugo-v1',
    }, quiet);
    await waitForTLS(ports.web, 'HVAC Web Real', services.web);

    services.mqttAdapter = spawnService('MQTT Telemetry Adapter', paths.mqttAdapterBinary, [
      '-config', paths.mqttAdapterConfig,
      '-diagnostics-addr', `127.0.0.1:${ports.adapterDiagnostics}`,
    ], {}, quiet);
    await waitForHTTP(`http://127.0.0.1:${ports.adapterDiagnostics}/health/ready`, 'MQTT Telemetry Adapter', {
      child: services.mqttAdapter,
      attempts: 600,
    });

    services.publisher = spawnService('EG8200 MQTT Publisher', paths.publisherBinary, [
      '-plant-config', paths.simulatorConfig,
      '-mqtt-config', paths.mqttGatewayConfig,
      '-diagnostics-addr', `127.0.0.1:${ports.simulatorDiagnostics}`,
    ], { GOCACHE: goCache }, quiet);
    await waitForHTTP(`http://127.0.0.1:${ports.simulatorDiagnostics}/health/ready`, 'EG8200 MQTT Publisher', {
      child: services.publisher,
      attempts: 600,
    });

    const report = {
      schemaVersion: 1,
      status: 'ready',
      webURL,
      mqttBrokerURL,
      clickHouseURL,
      cubeURL,
      queryURL,
      gatewayURL,
      logto: {
        loginURL: logtoLoginURL,
        coreURL: logtoURL,
        adminURL: logtoAdminURL,
        issuer: logto.issuer,
        clientId: logto.clientId,
        subject: logto.subject,
        username: logto.account.username,
        credential: logto.account.credential,
        registrationEnabled: logto.registrationEnabled,
      },
      tenantId: centralPlantIdentity.tenantId,
      siteId: centralPlantIdentity.siteId,
      deviceCount: centralPlantDevices.length,
      pointCount,
      energyHistory: {
        readingCount: energyHistory.readings.length,
        from: energyHistory.from,
        to: energyHistory.to,
        coverageDays: energyHistory.coverageDays,
        dailyIntervalCount: energyHistory.dailyIntervalCount,
        hourlyIntervalCount: energyHistory.hourlyIntervalCount,
        analyticsIntervalCount: energyHistory.analyticsIntervalCount,
        initialEnergyKwh: energyHistory.finalEnergyKwh,
      },
      devices: centralPlantDevices.map(({ name, type, platformDeviceId }) => ({ name, type, platformDeviceId })),
      paths: { output: outRoot, report: paths.report },
      startedAt: new Date().toISOString(),
    };
    await writePrivate(paths.report, `${JSON.stringify(report, null, 2)}\n`);
    return {
      ...report,
      ports,
      projects,
      services,
      paths,
      database: { logtoPostgresContainer, s1Container, s2Container, clickHouseContainer },
      stop,
    };
  } catch (error) {
    await stop();
    throw error;
  }
}
