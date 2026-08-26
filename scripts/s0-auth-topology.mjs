import { randomBytes } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import https from 'node:https';
import tls from 'node:tls';

const root = resolve(process.cwd());
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function processExited(child) {
  return child && (child.exitCode !== null || child.signalCode !== null);
}

function spawnService(label, command, args, env) {
  const child = spawn(command, args, {
    cwd: root,
    stdio: 'inherit',
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
      const socket = tls.connect({
        host: '127.0.0.1',
        port,
        rejectUnauthorized: false,
        ...options,
      });
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

function telemetrySnapshot(deviceId, keys) {
  const evaluatedAt = '2026-07-24T12:00:00Z';
  return {
    schemaVersion: 1,
    deviceId,
    owningOrganizationId: '018f2e00-1000-7000-8000-000000000001',
    siteId: '018f2e00-2000-7000-8000-000000000001',
    businessRevision: 9,
    evaluatedAt,
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE',
      currentState: 'ONLINE',
      lastSeenAt: evaluatedAt,
      policyRevision: 7,
    },
    telemetryReadiness: keys.length === 0 ? 'INCOMPLETE' : 'CURRENT',
    displayState: 'ONLINE',
    values: keys.map((key, index) => ({
      state: 'PRESENT',
      key,
      value: index === 0 ? 21.5 : 48,
      valueType: 'NUMBER',
      sampledAt: evaluatedAt,
      receivedAt: evaluatedAt,
      freshness: 'FRESH',
      quality: 'GOOD',
      qualityReasons: [],
      policyRevision: 7,
    })),
  };
}

async function startTelemetryRuntimeFixture({ port, certPath, keyPath, caPath }) {
  const [cert, key, ca] = await Promise.all([readFile(certPath), readFile(keyPath), readFile(caPath)]);
  const evidence = {
    requests: 0,
    singleRequests: 0,
    batchRequests: 0,
    browserAuthorityHeaders: 0,
    missingAuthorization: 0,
  };
  const server = https.createServer({
    cert,
    key,
    ca,
    requestCert: true,
    rejectUnauthorized: true,
    minVersion: 'TLSv1.3',
  }, async (request, response) => {
    evidence.requests += 1;
    const peer = request.socket.getPeerCertificate();
    if (!request.socket.authorized || !String(peer.subjectaltname ?? '').includes('URI:spiffe://hvac.local/platform-gateway')) {
      response.writeHead(403, { 'Content-Type': 'application/problem+json' });
      response.end(JSON.stringify({ code: 'TELEMETRY_WORKLOAD_IDENTITY_INVALID', status: 403, detail: 'Invalid workload identity.', retryable: false }));
      return;
    }
    for (const header of ['cookie', 'x-csrf-token', 'x-admin', 'x-principal', 'x-organization-id', 'x-site-id', 'x-roles', 'x-scopes']) {
      if (request.headers[header]) evidence.browserAuthorityHeaders += 1;
    }
    if (!String(request.headers.authorization ?? '').startsWith('Bearer ')) {
      evidence.missingAuthorization += 1;
      response.writeHead(503, { 'Content-Type': 'application/problem+json' });
      response.end(JSON.stringify({ code: 'TELEMETRY_AUTHORIZATION_UNAVAILABLE', status: 503, detail: 'Delegation grant missing.', retryable: true }));
      return;
    }
    const url = new URL(request.url, `https://127.0.0.1:${port}`);
    const singleMatch = /^\/internal\/v1\/devices\/([^/]+)\/observation-snapshot$/.exec(url.pathname);
    if (request.method === 'GET' && singleMatch) {
      evidence.singleRequests += 1;
      const deviceId = decodeURIComponent(singleMatch[1]);
      const keys = url.searchParams.getAll('key');
      response.writeHead(200, { 'Content-Type': 'application/json' });
      response.end(JSON.stringify(telemetrySnapshot(deviceId, keys)));
      return;
    }
    if (request.method === 'POST' && url.pathname === '/internal/v1/telemetry/observation-snapshots:batchGet') {
      evidence.batchRequests += 1;
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const input = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      response.writeHead(200, { 'Content-Type': 'application/json' });
      response.end(JSON.stringify({
        schemaVersion: 1,
        items: input.requests.map((item) => ({
          status: 'OK',
          requestId: item.requestId,
          deviceId: item.deviceId,
          snapshot: telemetrySnapshot(item.deviceId, item.keys ?? []),
        })),
      }));
      return;
    }
    response.writeHead(404, { 'Content-Type': 'application/problem+json' });
    response.end(JSON.stringify({ code: 'RESOURCE_NOT_FOUND', status: 404, detail: 'Resource not found.', retryable: false }));
  });
  await new Promise((resolveListen, rejectListen) => {
    server.once('error', rejectListen);
    server.listen(port, '127.0.0.1', resolveListen);
  });
  return {
    evidence,
    url: `https://127.0.0.1:${port}`,
    async stop() {
      await new Promise((resolveClose) => server.close(resolveClose));
    },
  };
}

export async function stopProcess(child) {
  if (!child || processExited(child)) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
    if (!processExited(child)) {
      await Promise.race([
        once(child, 'exit'),
        pause(2000),
      ]);
    }
    await pause(250);
    return;
  }
  child.kill('SIGTERM');
  const stopped = await Promise.race([
    once(child, 'exit').then(() => true),
    pause(1500).then(() => false),
  ]);
  if (!stopped) child.kill('SIGKILL');
}

export async function startS0AuthTopology(options = {}) {
  const oidcPort = Number(options.oidcPort ?? process.env.S0_AUTH_OIDC_PORT ?? 19090);
  const iamPort = Number(options.iamPort ?? process.env.S0_AUTH_IAM_PORT ?? 18444);
  const gatewayPort = Number(options.gatewayPort ?? process.env.S0_AUTH_GATEWAY_PORT ?? 18080);
  const webPort = Number(options.webPort ?? process.env.S0_AUTH_WEB_PORT ?? 5179);
  const telemetryEnabled = options.telemetry === true;
  const telemetryPort = Number(options.telemetryPort ?? process.env.S2_GATEWAY_TELEMETRY_PORT ?? 18446);
  const instanceRoot = join(tmpdir(), `hvac-s0-auth-${process.pid}-${randomBytes(5).toString('hex')}`);
  const pkiDir = join(instanceRoot, 'pki');
  const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
  await mkdir(pkiDir, { recursive: true });
  await mkdir(goCacheDir, { recursive: true });

  const generated = spawnSync(goBinary, ['run', './tools/s0-auth-fixture/cmd/generate-pki', pkiDir], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    env: { ...process.env, GOCACHE: goCacheDir },
  });
  if (generated.error || generated.status !== 0) {
    throw new Error(`S0 test PKI generation failed: ${generated.error?.message ?? generated.stderr ?? generated.status}`);
  }

  const paths = {
    ca: join(pkiDir, 'ca.pem'),
    serverCert: join(pkiDir, 'iam-cert.pem'),
    serverKey: join(pkiDir, 'iam-key.pem'),
    gatewayCert: join(pkiDir, 'gateway-cert.pem'),
    gatewayKey: join(pkiDir, 'gateway-key.pem'),
  };
  const oidcURL = `https://127.0.0.1:${oidcPort}`;
  const iamURL = `https://127.0.0.1:${iamPort}`;
  const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
  const webURL = `https://127.0.0.1:${webPort}`;
  const redirectURI = `${webURL}/api/v1/auth/callback`;
  const processes = [];
  let telemetryFixture;

  try {
    const oidc = spawnService('OIDC fixture', goBinary, ['run', './tools/oidc-test-provider/cmd/oidc-test-provider'], {
      GOCACHE: goCacheDir,
      OIDC_FIXTURE_ADDR: `127.0.0.1:${oidcPort}`,
      OIDC_FIXTURE_ISSUER: oidcURL,
      OIDC_FIXTURE_CLIENT_ID: 'hvac-web-s0',
      OIDC_FIXTURE_REDIRECT_URI: redirectURI,
      OIDC_FIXTURE_TLS_CERT: paths.serverCert,
      OIDC_FIXTURE_TLS_KEY: paths.serverKey,
    });
    processes.push(oidc);
    await waitForTLS(oidcPort, 'OIDC fixture', oidc);

    const iam = spawnService('IAM service', goBinary, ['run', './modules/iam/cmd/iam-owner'], {
      GOCACHE: goCacheDir,
      IAM_SERVICE_ADDR: `127.0.0.1:${iamPort}`,
      IAM_TLS_CERT: paths.serverCert,
      IAM_TLS_KEY: paths.serverKey,
      IAM_CLIENT_CA: paths.ca,
      IAM_ALLOWED_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      IAM_AUDIENCE: 'iam-service',
      ...(telemetryEnabled ? {
        IAM_S2_AUTHORIZATION_FIXTURE: 'true',
        IAM_EXTERNAL_SUBJECT_ISSUER: oidcURL,
      } : {}),
    });
    processes.push(iam);
    const [clientCert, clientKey, ca] = await Promise.all([
      readFile(paths.gatewayCert), readFile(paths.gatewayKey), readFile(paths.ca),
    ]);
    await waitForTLS(iamPort, 'IAM service', iam, {
      cert: clientCert,
      key: clientKey,
      ca,
      servername: 'localhost',
      rejectUnauthorized: true,
    });

    if (telemetryEnabled) {
      telemetryFixture = await startTelemetryRuntimeFixture({
        port: telemetryPort,
        certPath: paths.serverCert,
        keyPath: paths.serverKey,
        caPath: paths.ca,
      });
    }

    const gateway = spawnService('Platform Gateway', goBinary, ['run', './cmd/energy-api'], {
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
      GATEWAY_WORKLOAD_SPIFFE: 'spiffe://hvac.local/platform-gateway',
      IDENTITY_POLICY_REVISION: 'policy-v1',
      SESSION_TOKEN_KEY: randomBytes(32).toString('base64url'),
      ...(options.routeRegistry === false ? { S2_ALLOW_UNROUTED_GATEWAY_FIXTURE: 'true' } : { ROUTE_OWNERSHIP_REGISTRY: resolve(root, 'contracts/ownership/route-ownership.v1.json') }),
      ...(telemetryEnabled ? {
        TELEMETRY_RUNTIME_URL: telemetryFixture.url,
        TELEMETRY_RUNTIME_SERVER_CA: paths.ca,
        TELEMETRY_RUNTIME_SERVER_NAME: 'localhost',
        TELEMETRY_RUNTIME_AUDIENCE: 'telemetry-runtime-service',
      } : {}),
      S0_ALLOW_MEMORY_ROUTE_AUDIT: 'true',
      S0_ALLOW_NO_LEGACY: 'true',
      S1_ALLOW_NO_CORE: 'true',
      S0_ALLOW_MEMORY_SESSION_STORE: 'true',
      S0_ALLOW_NO_AUDIT_LEDGER: 'true',
    });
    processes.push(gateway);
    await waitForHTTP(`${gatewayURL}/api/v1/health?includeBuild=true`, 'Platform Gateway', gateway);

    const web = spawnService('HVAC Web', process.execPath, [
      resolve(root, 'node_modules/vite/bin/vite.js'),
      'apps/hvac-web',
      '--config', 'apps/hvac-web/vite.config.ts',
      '--host', '127.0.0.1',
      '--port', String(webPort),
      '--strictPort',
    ], {
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayURL,
      S0_GATEWAY_ONLY: 'true',
      VITE_TLS_CERT: paths.serverCert,
      VITE_TLS_KEY: paths.serverKey,
    });
    processes.push(web);
    await waitForTLS(webPort, 'HVAC Web', web);

    return {
      oidcPort,
      iamPort,
      gatewayPort,
      webPort,
      oidcURL,
      iamURL,
      gatewayURL,
      webURL,
      redirectURI,
      pkiDir,
      processes,
      telemetryURL: telemetryFixture?.url ?? null,
      telemetryEvidence: telemetryFixture?.evidence ?? null,
      async stop() {
        for (const child of [...processes].reverse()) await stopProcess(child);
        await telemetryFixture?.stop();
        await rm(instanceRoot, { recursive: true, force: true });
      },
    };
  } catch (error) {
    for (const child of [...processes].reverse()) await stopProcess(child);
    await telemetryFixture?.stop();
    await rm(instanceRoot, { recursive: true, force: true });
    throw error;
  }
}
