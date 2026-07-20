import { randomBytes } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import tls from 'node:tls';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s0-durable/compose.yaml');
const projectName = 'hvac-s0-durable';
const postgresContainer = `${projectName}-postgres-1`;
const redpandaContainer = `${projectName}-redpanda-1`;
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
let serviceStdio = 'inherit';

const composeInvocation = (() => {
  const plugin = spawnSync('docker', ['compose', 'version'], { stdio: 'ignore', windowsHide: true });
  if (!plugin.error && plugin.status === 0) return { command: 'docker', prefix: ['compose'] };
  return { command: 'docker-compose', prefix: [] };
})();

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
  return run(composeInvocation.command, [
    ...composeInvocation.prefix,
    '-p', projectName,
    '-f', composePath,
    ...args,
  ], options);
}

function docker(args, options = {}) {
  return run('docker', args, options);
}

function processExited(child) {
  return child && (child.exitCode !== null || child.signalCode !== null);
}

function spawnService(label, command, args, env) {
  const child = spawn(command, args, {
    cwd: root,
    stdio: serviceStdio,
    shell: false,
    env: { ...process.env, ...env },
  });
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

export async function stopProcess(child) {
  if (!child || processExited(child)) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    return;
  }
  child.kill('SIGTERM');
  const stopped = await Promise.race([
    once(child, 'exit').then(() => true),
    pause(1500).then(() => false),
  ]);
  if (!stopped) child.kill('SIGKILL');
}

function assertSafeIdentifier(value, label) {
  if (!/^[A-Za-z0-9_-]{8,160}$/.test(value)) throw new Error(`${label} is not a safe test identifier`);
}

export async function startS0DurableTopology(options = {}) {
  serviceStdio = options.quiet ? 'ignore' : 'inherit';
  const oidcPort = Number(options.oidcPort ?? process.env.S0_DURABLE_OIDC_PORT ?? 19094);
  const iamPort = Number(options.iamPort ?? process.env.S0_DURABLE_IAM_PORT ?? 18444);
  const auditPort = Number(options.auditPort ?? process.env.S0_DURABLE_AUDIT_PORT ?? 18446);
  const legacyPort = Number(options.legacyPort ?? process.env.S0_DURABLE_LEGACY_PORT ?? 13001);
  const gatewayPort = Number(options.gatewayPort ?? process.env.S0_DURABLE_GATEWAY_PORT ?? 18082);
  const webPort = Number(options.webPort ?? process.env.S0_DURABLE_WEB_PORT ?? 5181);
  const instanceRoot = join(tmpdir(), `hvac-s0-durable-${process.pid}-${randomBytes(5).toString('hex')}`);
  const pkiDir = join(instanceRoot, 'pki');
  const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
  await mkdir(pkiDir, { recursive: true });
  await mkdir(goCacheDir, { recursive: true });

  const oidcURL = `https://127.0.0.1:${oidcPort}`;
  const iamURL = `https://127.0.0.1:${iamPort}`;
  const auditURL = `https://127.0.0.1:${auditPort}`;
  const legacyURL = `https://127.0.0.1:${legacyPort}`;
  const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
  const webURL = `https://127.0.0.1:${webPort}`;
  const redirectURI = `${webURL}/api/v1/auth/callback`;
  const brokers = '127.0.0.1:19092';
  const database = {
    admin: 'postgres://postgres:postgres-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
    gateway: 'postgres://gateway_runtime:gateway-runtime-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
    relay: 'postgres://gateway_relay_runtime:gateway-relay-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
    auditConsumer: 'postgres://audit_consumer_runtime:audit-consumer-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
    auditQuery: 'postgres://audit_query_runtime:audit-query-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
  };
  const services = { oidc: null, iam: null, audit: null, relay: null, legacy: null, gateway: null, web: null };

  const paths = {
    ca: join(pkiDir, 'ca.pem'),
    iamCert: join(pkiDir, 'iam-cert.pem'),
    iamKey: join(pkiDir, 'iam-key.pem'),
    auditCert: join(pkiDir, 'audit-cert.pem'),
    auditKey: join(pkiDir, 'audit-key.pem'),
    legacyCert: join(pkiDir, 'legacy-cert.pem'),
    legacyKey: join(pkiDir, 'legacy-key.pem'),
    gatewayCert: join(pkiDir, 'gateway-cert.pem'),
    gatewayKey: join(pkiDir, 'gateway-key.pem'),
    routeRegistry: join(instanceRoot, 'route-ownership.v1.json'),
  };

  const startAudit = async () => {
    services.audit = spawnService('Audit Ledger', goBinary, ['run', './services/audit-ledger-service/cmd/audit-ledger-service'], {
      GOCACHE: goCacheDir,
      AUDIT_SERVICE_ADDR: `127.0.0.1:${auditPort}`,
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
      GOCACHE: goCacheDir,
      OUTBOX_DATABASE_URL: database.relay,
      CONTROL_BACKBONE_BROKERS: brokers,
      OUTBOX_RELAY_OWNER: `relay-${process.pid}`,
    });
    await waitForChildAlive(services.relay, 'Outbox Relay');
  };

  const startLegacy = async () => {
    services.legacy = spawnService('Legacy HVAC backend', process.execPath, [resolve(root, 'hvac-backend/dist/main.js')], {
      NODE_ENV: 'test',
      PORT: String(legacyPort),
      LEGACY_PRIVATE_MODE: 'true',
      LEGACY_BIND_ADDRESS: '127.0.0.1',
      LEGACY_TLS_CERT: paths.legacyCert,
      LEGACY_TLS_KEY: paths.legacyKey,
      LEGACY_CLIENT_CA: paths.ca,
      LEGACY_ALLOWED_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      LEGACY_AUDIENCE: 'legacy-hvac-backend',
    });
    const [clientCert, clientKey, ca] = await Promise.all([readFile(paths.gatewayCert), readFile(paths.gatewayKey), readFile(paths.ca)]);
    await waitForTLS(legacyPort, 'Legacy HVAC backend', services.legacy, { cert: clientCert, key: clientKey, ca, servername: 'localhost', rejectUnauthorized: true });
  };

  try {
    try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
    compose(['up', '-d']);
    await waitForContainer(() => docker(['exec', postgresContainer, 'pg_isready', '-U', 'postgres', '-d', 'hvac_s0']), 'PostgreSQL');
    await waitForContainer(() => docker(['exec', redpandaContainer, 'rpk', 'cluster', 'health', '-X', 'brokers=127.0.0.1:9092']), 'Redpanda');
    try { docker(['exec', redpandaContainer, 'rpk', 'topic', 'delete', 'control.security.session.v1', '-X', 'brokers=127.0.0.1:9092']); } catch {}
    docker(['exec', redpandaContainer, 'rpk', 'topic', 'create', 'control.security.session.v1', '-p', '3', '-r', '1', '-X', 'brokers=127.0.0.1:9092']);

    const generated = spawnSync(goBinary, ['run', './tools/s0-auth-fixture/cmd/generate-pki', pkiDir], {
      cwd: root,
      encoding: 'utf8',
      windowsHide: true,
      env: { ...process.env, GOCACHE: goCacheDir },
    });
    if (generated.error || generated.status !== 0) throw new Error(`S0 PKI generation failed: ${generated.error?.message ?? generated.stderr ?? generated.status}`);
    run(process.execPath, [resolve(root, 'hvac-backend/node_modules/@nestjs/cli/bin/nest.js'), 'build'], { cwd: resolve(root, 'hvac-backend') });
    await writeFile(paths.routeRegistry, await readFile(resolve(root, 'contracts/ownership/route-ownership.v1.json')));

    services.oidc = spawnService('OIDC fixture', goBinary, ['run', './services/oidc-test-provider/cmd/oidc-test-provider'], {
      GOCACHE: goCacheDir,
      OIDC_FIXTURE_ADDR: `127.0.0.1:${oidcPort}`,
      OIDC_FIXTURE_ISSUER: oidcURL,
      OIDC_FIXTURE_CLIENT_ID: 'hvac-web-s0',
      OIDC_FIXTURE_REDIRECT_URI: redirectURI,
      OIDC_FIXTURE_TLS_CERT: paths.iamCert,
      OIDC_FIXTURE_TLS_KEY: paths.iamKey,
    });
    await waitForTLS(oidcPort, 'OIDC fixture', services.oidc);

    services.iam = spawnService('IAM service', goBinary, ['run', './services/iam-service/cmd/iam-service'], {
      GOCACHE: goCacheDir,
      IAM_SERVICE_ADDR: `127.0.0.1:${iamPort}`,
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
    await startLegacy();

    services.gateway = spawnService('Platform Gateway', goBinary, ['run', './services/platform-gateway/cmd/platform-gateway'], {
      GOCACHE: goCacheDir,
      PLATFORM_GATEWAY_ADDR: `127.0.0.1:${gatewayPort}`,
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
      LEGACY_URL: legacyURL,
      LEGACY_AUDIENCE: 'legacy-hvac-backend',
      LEGACY_SERVER_CA: paths.ca,
      LEGACY_SERVER_NAME: 'localhost',
      ROUTE_OWNERSHIP_REGISTRY: paths.routeRegistry,
      GATEWAY_DATABASE_URL: database.gateway,
      GATEWAY_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      IDENTITY_POLICY_REVISION: 'policy-v1',
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
      oidcURL, iamURL, auditURL, legacyURL, gatewayURL, webURL, redirectURI, brokers, database, pkiDir, routeRegistryPath: paths.routeRegistry, services,
      async stopBroker() { docker(['stop', redpandaContainer]); },
      async startBroker() {
        docker(['start', redpandaContainer]);
        await waitForContainer(() => docker(['exec', redpandaContainer, 'rpk', 'cluster', 'health', '-X', 'brokers=127.0.0.1:9092']), 'Redpanda');
      },
      async restartAudit() { await stopProcess(services.audit); await startAudit(); },
      async restartRelay() { await stopProcess(services.relay); await startRelay(); },
      async setPlatformStatusOwner(owner, registryRevision, routeRevision, percentage = 100) {
        const registry = JSON.parse(await readFile(paths.routeRegistry, 'utf8'));
        const route = registry.routes.find((entry) => entry.method === 'GET' && entry.path === '/api/v1/platform/status');
        if (!route) throw new Error('platform status route is missing from test registry');
        registry.registryRevision = registryRevision;
        route.owner = owner;
        route.revision = routeRevision;
        route.compatibilityMode = owner === 'legacy-hvac-backend' ? 'legacy-read' : 'native';
        route.rollout = {
          mode: 'percentage',
          percentage,
          fallbackOwner: owner === 'legacy-hvac-backend' ? 'platform-gateway' : 'legacy-hvac-backend',
          cohortSalt: 'platform-status-v1',
        };
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
        for (const child of [services.web, services.gateway, services.legacy, services.relay, services.audit, services.iam, services.oidc]) await stopProcess(child);
        try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
        await rm(instanceRoot, { recursive: true, force: true });
      },
    };
  } catch (error) {
    for (const child of [services.web, services.gateway, services.legacy, services.relay, services.audit, services.iam, services.oidc]) await stopProcess(child);
    try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
    await rm(instanceRoot, { recursive: true, force: true });
    throw error;
  }
}
