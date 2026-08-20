import { randomBytes } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createTCPServer } from 'node:net';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import tls from 'node:tls';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s0-durable/compose.yaml');
const projectName = process.env.S0_DURABLE_COMPOSE_PROJECT ?? `hvac-s0-durable-${process.pid}-${randomBytes(3).toString('hex')}`;
const postgresContainer = `${projectName}-postgres-1`;
const redpandaContainer = `${projectName}-redpanda-1`;
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
let serviceStdio = 'inherit';
const serviceProcessGroups = new WeakSet();

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args, options = {}) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], options);
}

function docker(args, options = {}) {
  return run('docker', args, options);
}

function processExited(child) {
  return child && (child.exitCode !== null || child.signalCode !== null);
}

function signalProcessTree(child, signal) {
  if (process.platform !== 'win32' && serviceProcessGroups.has(child)) {
    try {
      process.kill(-child.pid, signal);
    } catch (error) {
      if (error?.code !== 'ESRCH') throw error;
    }
    return;
  }
  child.kill(signal);
}

async function signalAndWait(child, signal, timeoutMilliseconds = 1500) {
  if (processExited(child)) return true;
  const exited = once(child, 'exit').then(() => true);
  signalProcessTree(child, signal);
  return Promise.race([
    exited,
    pause(timeoutMilliseconds).then(() => false),
  ]);
}

async function findAvailablePort(requestedPort = 0) {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: Number(requestedPort) || 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function spawnService(label, command, args, env) {
  const child = spawn(command, args, {
    cwd: root,
    stdio: serviceStdio,
    shell: false,
    detached: process.platform !== 'win32',
    env: { ...process.env, ...env },
  });
  if (process.platform !== 'win32') serviceProcessGroups.add(child);
  child.once('error', (error) => console.error(`${label} process error:`, error));
  return child;
}

async function waitForHTTP(url, label, child) {
  for (let attempt = 0; attempt < 900; attempt += 1) {
    if (processExited(child)) throw new Error(`${label} exited before becoming ready: ${child.signalCode ?? child.exitCode}`);
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`${label} did not become ready at ${url}`);
}

async function waitForTLS(port, label, child, options = {}) {
  for (let attempt = 0; attempt < 900; attempt += 1) {
    if (processExited(child)) throw new Error(`${label} exited before becoming ready: ${child.signalCode ?? child.exitCode}`);
    const connected = await new Promise((resolveConnected) => {
      const socket = tls.connect({ host: '127.0.0.1', port, rejectUnauthorized: false, ...options });
      socket.setTimeout(500);
      socket.once('secureConnect', () => { socket.end(); resolveConnected(true); });
      socket.once('timeout', () => { socket.destroy(); resolveConnected(false); });
      socket.once('error', () => resolveConnected(false));
    });
    if (connected) return;
    await pause(100);
  }
  throw new Error(`${label} did not become ready on TLS port ${port}`);
}

async function waitForContainer(command, label) {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      command();
      return;
    } catch {}
    await pause(200);
  }
  throw new Error(`${label} did not become ready`);
}

async function waitForChildAlive(child, label) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (processExited(child)) throw new Error(`${label} exited: ${child.signalCode ?? child.exitCode}`);
    await pause(100);
  }
}

async function startOTLPRecorder(port) {
  const payloads = [];
  let available = true;
  const server = createServer((request, response) => {
    if (request.method !== 'POST' || request.url !== '/v1/traces') {
      response.writeHead(404).end();
      return;
    }
    if (!available) {
      response.writeHead(503, { 'content-type': 'application/json' }).end('{"code":"COLLECTOR_UNAVAILABLE"}');
      return;
    }
    const chunks = [];
    let bytes = 0;
    request.on('data', (chunk) => {
      bytes += chunk.length;
      if (bytes > 4 * 1024 * 1024) request.destroy();
      else chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        payloads.push(JSON.parse(Buffer.concat(chunks).toString('utf8')));
        response.writeHead(200, { 'content-type': 'application/json' }).end('{}');
      } catch {
        response.writeHead(400).end();
      }
    });
  });
  server.listen(port, '127.0.0.1');
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('OTLP recorder did not expose a TCP address');
  return {
    endpoint: `http://127.0.0.1:${address.port}`,
    payloads,
    setAvailable(value) { available = Boolean(value); },
    async close() {
      await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
    },
  };
}

export async function stopProcess(child) {
  if (!child || processExited(child)) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    return;
  }
  const stopped = await signalAndWait(child, 'SIGTERM');
  if (!stopped) await signalAndWait(child, 'SIGKILL');
}

export async function killProcess(child) {
  if (!child || processExited(child)) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    return;
  }
  const stopped = await signalAndWait(child, 'SIGKILL');
  if (!stopped) throw new Error(`process group ${child.pid} did not stop after SIGKILL`);
}

function assertSafeIdentifier(value, label) {
  if (!/^[A-Za-z0-9_-]{8,160}$/.test(value)) throw new Error(`${label} is not a safe test identifier`);
}

export async function startS0DurableTopology(options = {}) {
  serviceStdio = options.quiet ? 'ignore' : 'inherit';
  const oidcPort = Number(options.oidcPort ?? process.env.S0_DURABLE_OIDC_PORT ?? 19094);
  const iamPort = Number(options.iamPort ?? process.env.S0_DURABLE_IAM_PORT ?? 18444);
  const auditPort = Number(options.auditPort ?? process.env.S0_DURABLE_AUDIT_PORT ?? 18446);
  const gatewayPort = Number(options.gatewayPort ?? process.env.S0_DURABLE_GATEWAY_PORT ?? 18082);
  const webPort = Number(options.webPort ?? process.env.S0_DURABLE_WEB_PORT ?? 5181);
  const otlpPort = Number(options.otlpPort ?? process.env.S0_DURABLE_OTLP_PORT ?? 0);
  const [oidcDiagnosticsPort, iamDiagnosticsPort, auditDiagnosticsPort, relayDiagnosticsPort, gatewayDiagnosticsPort] = await Promise.all([
    findAvailablePort(options.oidcDiagnosticsPort ?? process.env.S0_DURABLE_OIDC_DIAGNOSTICS_PORT ?? 0),
    findAvailablePort(options.iamDiagnosticsPort ?? process.env.S0_DURABLE_IAM_DIAGNOSTICS_PORT ?? 0),
    findAvailablePort(options.auditDiagnosticsPort ?? process.env.S0_DURABLE_AUDIT_DIAGNOSTICS_PORT ?? 0),
    findAvailablePort(options.relayDiagnosticsPort ?? process.env.S0_DURABLE_RELAY_DIAGNOSTICS_PORT ?? 0),
    findAvailablePort(options.gatewayDiagnosticsPort ?? process.env.S0_DURABLE_GATEWAY_DIAGNOSTICS_PORT ?? 0),
  ]);
  const postgresHostPort = await findAvailablePort(options.postgresHostPort ?? process.env.S0_POSTGRES_HOST_PORT ?? 0);
  const redpandaHostPort = await findAvailablePort(options.redpandaHostPort ?? process.env.S0_REDPANDA_HOST_PORT ?? 0);
  const otelGrpcHostPort = await findAvailablePort(options.otelGrpcHostPort ?? process.env.S0_OTEL_GRPC_HOST_PORT ?? 0);
  const otelHTTPHostPort = await findAvailablePort(options.otelHTTPHostPort ?? process.env.S0_OTEL_HTTP_HOST_PORT ?? 0);
  const otelMetricsHostPort = await findAvailablePort(options.otelMetricsHostPort ?? process.env.S0_OTEL_METRICS_HOST_PORT ?? 0);
  const otelHealthHostPort = await findAvailablePort(options.otelHealthHostPort ?? process.env.S0_OTEL_HEALTH_HOST_PORT ?? 0);
  const prometheusHostPort = await findAvailablePort(options.prometheusHostPort ?? process.env.S0_PROMETHEUS_HOST_PORT ?? 0);
  const toxiproxyAPIHostPort = await findAvailablePort(options.toxiproxyAPIHostPort ?? process.env.S0_TOXIPROXY_API_HOST_PORT ?? 0);
  const toxiproxyPostgresHostPort = await findAvailablePort(options.toxiproxyPostgresHostPort ?? process.env.S0_TOXIPROXY_POSTGRES_HOST_PORT ?? 0);
  const composeEnvironment = {
    ...process.env,
    S0_POSTGRES_HOST_PORT: String(postgresHostPort),
    S0_REDPANDA_HOST_PORT: String(redpandaHostPort),
    S0_OTEL_GRPC_HOST_PORT: String(otelGrpcHostPort),
    S0_OTEL_HTTP_HOST_PORT: String(otelHTTPHostPort),
    S0_OTEL_METRICS_HOST_PORT: String(otelMetricsHostPort),
    S0_OTEL_HEALTH_HOST_PORT: String(otelHealthHostPort),
    S0_PROMETHEUS_HOST_PORT: String(prometheusHostPort),
    S0_TOXIPROXY_API_HOST_PORT: String(toxiproxyAPIHostPort),
    S0_TOXIPROXY_POSTGRES_HOST_PORT: String(toxiproxyPostgresHostPort),
  };
  const composeOptions = { env: composeEnvironment };
  const telemetryRecorder = options.captureTelemetry === false ? null : await startOTLPRecorder(otlpPort);
  const telemetryEnvironment = telemetryRecorder ? { OTEL_EXPORTER_OTLP_ENDPOINT: telemetryRecorder.endpoint } : {};
  const instanceRoot = join(tmpdir(), `hvac-s0-durable-${process.pid}-${randomBytes(5).toString('hex')}`);
  const pkiDir = join(instanceRoot, 'pki');
  const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
  await mkdir(pkiDir, { recursive: true });
  await mkdir(goCacheDir, { recursive: true });

  const oidcURL = `https://127.0.0.1:${oidcPort}`;
  const iamURL = `https://127.0.0.1:${iamPort}`;
  const auditURL = `https://127.0.0.1:${auditPort}`;
  const toxiproxyURL = `http://127.0.0.1:${toxiproxyAPIHostPort}`;
  const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
  const webURL = `https://127.0.0.1:${webPort}`;
  const redirectURI = `${webURL}/api/v1/auth/callback`;
  const brokers = `127.0.0.1:${redpandaHostPort}`;
  const database = {
    admin: `postgres://postgres:postgres-local-only@127.0.0.1:${toxiproxyPostgresHostPort}/hvac_s0?sslmode=disable&connect_timeout=1`,
    gateway: `postgres://gateway_runtime:gateway-runtime-local-only@127.0.0.1:${toxiproxyPostgresHostPort}/hvac_s0?sslmode=disable&connect_timeout=1`,
    relay: `postgres://gateway_relay_runtime:gateway-relay-local-only@127.0.0.1:${toxiproxyPostgresHostPort}/hvac_s0?sslmode=disable&connect_timeout=1`,
    auditConsumer: `postgres://audit_consumer_runtime:audit-consumer-local-only@127.0.0.1:${toxiproxyPostgresHostPort}/hvac_s0?sslmode=disable&connect_timeout=1`,
    auditQuery: `postgres://audit_query_runtime:audit-query-local-only@127.0.0.1:${toxiproxyPostgresHostPort}/hvac_s0?sslmode=disable&connect_timeout=1`,
  };
  const services = { oidc: null, iam: null, audit: null, relay: null, gateway: null, web: null };

  const paths = {
    ca: join(pkiDir, 'ca.pem'),
    iamCert: join(pkiDir, 'iam-cert.pem'),
    iamKey: join(pkiDir, 'iam-key.pem'),
    auditCert: join(pkiDir, 'audit-cert.pem'),
    auditKey: join(pkiDir, 'audit-key.pem'),
    gatewayCert: join(pkiDir, 'gateway-cert.pem'),
    gatewayKey: join(pkiDir, 'gateway-key.pem'),
    routeRegistry: join(instanceRoot, 'route-ownership.v1.json'),
  };

  const toxiproxyRequest = async (path, init = {}) => {
    const response = await fetch(`${toxiproxyURL}${path}`, {
      ...init,
      headers: { 'content-type': 'application/json', ...(init.headers ?? {}) },
    });
    const text = await response.text();
    if (!response.ok) throw new Error(`Toxiproxy ${path} failed with ${response.status}: ${text}`);
    return text ? JSON.parse(text) : null;
  };

  const setProxyEnabled = async (name, enabled) => {
    const proxy = await toxiproxyRequest(`/proxies/${encodeURIComponent(name)}`);
    return toxiproxyRequest(`/proxies/${encodeURIComponent(name)}`, {
      method: 'POST',
      body: JSON.stringify({ name: proxy.name, listen: proxy.listen, upstream: proxy.upstream, enabled: Boolean(enabled) }),
    });
  };

  const startAudit = async () => {
    services.audit = spawnService('Audit Ledger', goBinary, ['run', './services/audit-ledger-service/cmd/audit-ledger-service'], {
      ...telemetryEnvironment,
      GOCACHE: goCacheDir,
      AUDIT_SERVICE_ADDR: `127.0.0.1:${auditPort}`,
      AUDIT_DIAGNOSTICS_ADDR: `127.0.0.1:${auditDiagnosticsPort}`,
      AUDIT_TLS_CERT: paths.auditCert,
      AUDIT_TLS_KEY: paths.auditKey,
      AUDIT_CLIENT_CA: paths.ca,
      AUDIT_ALLOWED_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      AUDIT_AUDIENCE: 'audit-ledger-service',
      AUDIT_CONSUMER_DATABASE_URL: database.auditConsumer,
      AUDIT_QUERY_DATABASE_URL: database.auditQuery,
      CONTROL_BACKBONE_BROKERS: brokers,
      AUDIT_TOPIC: 'control.security.session.v1',
      AUDIT_CONSUMER_GROUP: 'audit-ledger-session-v1',
    });
    const [clientCert, clientKey, ca] = await Promise.all([readFile(paths.gatewayCert), readFile(paths.gatewayKey), readFile(paths.ca)]);
    await waitForTLS(auditPort, 'Audit Ledger', services.audit, { cert: clientCert, key: clientKey, ca, servername: 'localhost', rejectUnauthorized: true });
  };

  const startRelay = async () => {
    services.relay = spawnService('Outbox Relay', goBinary, ['run', './services/outbox-relay/cmd/outbox-relay'], {
      ...telemetryEnvironment,
      GOCACHE: goCacheDir,
      OUTBOX_RELAY_DIAGNOSTICS_ADDR: `127.0.0.1:${relayDiagnosticsPort}`,
      OUTBOX_DATABASE_URL: database.relay,
      CONTROL_BACKBONE_BROKERS: brokers,
      OUTBOX_RELAY_OWNER: `relay-${process.pid}`,
    });
    await waitForChildAlive(services.relay, 'Outbox Relay');
  };

  try {
    try { compose(['down', '--volumes', '--remove-orphans'], composeOptions); } catch {}
    compose(['up', '-d'], composeOptions);
    await waitForHTTP(`${toxiproxyURL}/version`, 'Toxiproxy', null);
    await toxiproxyRequest('/populate', {
      method: 'POST',
      body: JSON.stringify([
        { name: 's0_postgres', listen: '0.0.0.0:15432', upstream: 'postgres:5432', enabled: true },
      ]),
    });
    await waitForContainer(() => docker(['exec', postgresContainer, 'pg_isready', '-U', 'postgres', '-d', 'hvac_s0']), 'PostgreSQL');
    await waitForContainer(() => docker(['exec', redpandaContainer, 'rpk', 'cluster', 'health', '-X', 'brokers=127.0.0.1:9092']), 'Redpanda');
    try { docker(['exec', redpandaContainer, 'rpk', 'topic', 'delete', 'control.security.session.v1', '-X', 'brokers=127.0.0.1:9092']); } catch {}
    await waitForContainer(() => {
      try {
        docker(['exec', redpandaContainer, 'rpk', 'topic', 'create', 'control.security.session.v1', '-p', '3', '-r', '1', '-X', 'brokers=127.0.0.1:9092']);
      } catch (error) {
        const topics = docker(['exec', redpandaContainer, 'rpk', 'topic', 'list', '-X', 'brokers=127.0.0.1:9092']);
        if (!topics.split(/\r?\n/).some((line) => line.trim().startsWith('control.security.session.v1'))) throw error;
      }
    }, 'Control Backbone topic');

    const generated = spawnSync(goBinary, ['run', './tools/s0-auth-fixture/cmd/generate-pki', pkiDir], {
      cwd: root,
      encoding: 'utf8',
      windowsHide: true,
      env: { ...process.env, GOCACHE: goCacheDir },
    });
    if (generated.error || generated.status !== 0) throw new Error(`S0 PKI generation failed: ${generated.error?.message ?? generated.stderr ?? generated.status}`);
    const routeRegistry = JSON.parse(await readFile(resolve(root, 'contracts/ownership/route-ownership.v1.json'), 'utf8'));
    await writeFile(paths.routeRegistry, `${JSON.stringify(routeRegistry, null, 2)}\n`);

    services.oidc = spawnService('OIDC fixture', goBinary, ['run', './services/oidc-test-provider/cmd/oidc-test-provider'], {
      GOCACHE: goCacheDir,
      OIDC_FIXTURE_ADDR: `127.0.0.1:${oidcPort}`,
      OIDC_FIXTURE_DIAGNOSTICS_ADDR: `127.0.0.1:${oidcDiagnosticsPort}`,
      OIDC_FIXTURE_ISSUER: oidcURL,
      OIDC_FIXTURE_CLIENT_ID: 'hvac-web-s0',
      OIDC_FIXTURE_REDIRECT_URI: redirectURI,
      OIDC_FIXTURE_TLS_CERT: paths.iamCert,
      OIDC_FIXTURE_TLS_KEY: paths.iamKey,
    });
    await waitForTLS(oidcPort, 'OIDC fixture', services.oidc);

    services.iam = spawnService('IAM service', goBinary, ['run', './services/iam-service/cmd/iam-service'], {
      ...telemetryEnvironment,
      GOCACHE: goCacheDir,
      IAM_SERVICE_ADDR: `127.0.0.1:${iamPort}`,
      IAM_DIAGNOSTICS_ADDR: `127.0.0.1:${iamDiagnosticsPort}`,
      IAM_TLS_CERT: paths.iamCert,
      IAM_TLS_KEY: paths.iamKey,
      IAM_CLIENT_CA: paths.ca,
      IAM_ALLOWED_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      IAM_AUDIENCE: 'iam-service',
    });
    const [clientCert, clientKey, ca] = await Promise.all([readFile(paths.gatewayCert), readFile(paths.gatewayKey), readFile(paths.ca)]);
    await waitForTLS(iamPort, 'IAM service', services.iam, { cert: clientCert, key: clientKey, ca, servername: 'localhost', rejectUnauthorized: true });

    await startAudit();
    await startRelay();

    services.gateway = spawnService('Platform Gateway', goBinary, ['run', './services/platform-gateway/cmd/platform-gateway'], {
      ...telemetryEnvironment,
      GOCACHE: goCacheDir,
      PLATFORM_GATEWAY_ADDR: `127.0.0.1:${gatewayPort}`,
      PLATFORM_GATEWAY_DIAGNOSTICS_ADDR: `127.0.0.1:${gatewayDiagnosticsPort}`,
      OIDC_ISSUER: oidcURL,
      OIDC_CLIENT_ID: 'hvac-web-s0',
      OIDC_REDIRECT_URI: redirectURI,
      OIDC_SERVER_CA: paths.ca,
      OIDC_SERVER_NAME: 'localhost',
      PLATFORM_PUBLIC_ORIGIN: webURL,
      IAM_URL: iamURL,
      IAM_AUDIENCE: 'iam-service',
      IAM_CLIENT_CERT: paths.gatewayCert,
      IAM_CLIENT_KEY: paths.gatewayKey,
      IAM_SERVER_CA: paths.ca,
      IAM_SERVER_NAME: 'localhost',
      AUDIT_URL: auditURL,
      AUDIT_AUDIENCE: 'audit-ledger-service',
      AUDIT_SERVER_CA: paths.ca,
      AUDIT_SERVER_NAME: 'localhost',
      ROUTE_OWNERSHIP_REGISTRY: paths.routeRegistry,
      GATEWAY_DATABASE_URL: database.gateway,
      GATEWAY_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      IDENTITY_POLICY_REVISION: 'policy-v1',
      S1_ALLOW_NO_CORE: 'true',
      SESSION_TOKEN_KEY: randomBytes(32).toString('base64url'),
    });
    await waitForHTTP(`${gatewayURL}/api/v1/health?includeBuild=true`, 'Platform Gateway', services.gateway);

    services.web = spawnService('HVAC Web', process.execPath, [
      resolve(root, 'node_modules/vite/bin/vite.js'), 'apps/hvac-web',
      '--config', 'apps/hvac-web/vite.config.ts', '--host', '127.0.0.1',
      '--port', String(webPort), '--strictPort',
    ], {
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayURL,
      S0_GATEWAY_ONLY: 'true',
      VITE_TLS_CERT: paths.iamCert,
      VITE_TLS_KEY: paths.iamKey,
    });
    await waitForTLS(webPort, 'HVAC Web', services.web);

    return {
      oidcURL, iamURL, auditURL, toxiproxyURL, gatewayURL,
      gatewayDiagnosticsURL: `http://127.0.0.1:${gatewayDiagnosticsPort}/diagnostics`,
      webURL, redirectURI, brokers, database, pkiDir, routeRegistryPath: paths.routeRegistry, services,
      telemetryPayloads() { return telemetryRecorder ? JSON.parse(JSON.stringify(telemetryRecorder.payloads)) : []; },
      setTelemetryAvailable(value) { telemetryRecorder?.setAvailable(value); },
      async setPostgresAvailable(value) { await setProxyEnabled('s0_postgres', value); },
      async resetFaults() { await toxiproxyRequest('/reset', { method: 'POST', body: '{}' }); },
      async stopBroker() { docker(['stop', redpandaContainer]); },
      async startBroker() {
        docker(['start', redpandaContainer]);
        await waitForContainer(() => docker(['exec', redpandaContainer, 'rpk', 'cluster', 'health', '-X', 'brokers=127.0.0.1:9092']), 'Redpanda');
      },
      async stopAudit(force = false) {
        await (force ? killProcess : stopProcess)(services.audit);
        services.audit = null;
      },
      async startAudit() { if (!services.audit || processExited(services.audit)) await startAudit(); },
      async stopRelay(force = false) {
        await (force ? killProcess : stopProcess)(services.relay);
        services.relay = null;
      },
      async startRelay() { if (!services.relay || processExited(services.relay)) await startRelay(); },
      async restartAudit(force = false) { await (force ? killProcess : stopProcess)(services.audit); await startAudit(); },
      async restartRelay(force = false) { await (force ? killProcess : stopProcess)(services.relay); await startRelay(); },
      async setPlatformStatusRevision(registryRevision, routeRevision) {
        const registry = JSON.parse(await readFile(paths.routeRegistry, 'utf8'));
        const route = registry.routes.find((entry) => entry.method === 'GET' && entry.path === '/api/v1/platform/status');
        if (!route) throw new Error('platform status route is missing from test registry');
        registry.registryRevision = registryRevision;
        route.owner = 'platform-gateway';
        route.revision = routeRevision;
        route.compatibilityMode = 'native';
        route.rollout = { mode: 'all' };
        await writeFile(paths.routeRegistry, `${JSON.stringify(registry, null, 2)}\n`);
      },
      routeAuditCount(eventType = '') {
        const where = eventType ? ` WHERE event_type='${eventType}'` : '';
        return Number(docker(['exec', postgresContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-Atc', `SELECT count(*) FROM gateway.route_audit_records${where}`]));
      },
      platformRouteAuditSnapshot() {
        const output = docker(['exec', postgresContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-Atc', `SELECT row_to_json(record)::text FROM gateway.route_audit_records AS record WHERE route_key='GET /api/v1/platform/status' ORDER BY occurred_at DESC LIMIT 1`]);
        return output ? JSON.parse(output) : null;
      },
      setOutboxPending(messageID) {
        assertSafeIdentifier(messageID, 'message ID');
        docker(['exec', postgresContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-v', 'ON_ERROR_STOP=1', '-c', `UPDATE gateway.outbox SET published_at=NULL, available_at=clock_timestamp(), claim_owner='', claim_expires_at=NULL WHERE message_id='${messageID}'`]);
      },
      pendingOutboxCount(messageID = '') {
        if (messageID) assertSafeIdentifier(messageID, 'message ID');
        const where = messageID ? ` AND message_id='${messageID}'` : '';
        return Number(docker(['exec', postgresContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-Atc', `SELECT count(*) FROM gateway.outbox WHERE published_at IS NULL${where}`]));
      },
      auditRecordCount(messageID) {
        assertSafeIdentifier(messageID, 'message ID');
        return Number(docker(['exec', postgresContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-Atc', `SELECT count(*) FROM audit_ledger.records WHERE message_id='${messageID}'`]));
      },
      async stop() {
        for (const child of [services.web, services.gateway, services.relay, services.audit, services.iam, services.oidc]) await stopProcess(child);
        try { compose(['down', '--volumes', '--remove-orphans'], composeOptions); } catch {}
        if (telemetryRecorder) await telemetryRecorder.close();
        await rm(instanceRoot, { recursive: true, force: true });
      },
    };
  } catch (error) {
    for (const child of [services.web, services.gateway, services.relay, services.audit, services.iam, services.oidc]) await stopProcess(child);
    try { compose(['down', '--volumes', '--remove-orphans'], composeOptions); } catch {}
    if (telemetryRecorder) await telemetryRecorder.close();
    await rm(instanceRoot, { recursive: true, force: true });
    throw error;
  }
}
