import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createTCPServer } from 'node:net';
import { existsSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/durability/compose.yaml');
const projectName = `hvac-s0-pg-test-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
async function findAvailablePort(requestedPort = 0) {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: Number(requestedPort) || 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('PostgreSQL test port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort(process.env.S0_POSTGRES_HOST_PORT ?? 0);
const composeEnvironment = { ...process.env, S0_POSTGRES_HOST_PORT: String(postgresHostPort) };

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

async function waitForPostgres() {
  let stablePostmasterStart = '';
  let stableChecks = 0;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = run('docker', [
        'exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-Atqc',
        "SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('gateway.sessions') IS NOT NULL)::text || '|' || EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's0_migrator')::text",
      ]);
      const [postmasterStart, schemaReady, migratorReady] = state.split('|');
      if (schemaReady === 'true' && migratorReady === 'true') {
        if (postmasterStart === stablePostmasterStart) stableChecks += 1;
        else {
          stablePostmasterStart = postmasterStart;
          stableChecks = 1;
        }
        if (stableChecks >= 5) return;
      } else {
        stableChecks = 0;
      }
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  throw new Error('PostgreSQL integration fixture did not reach a stable initialized postmaster');
}

async function runGoTests() {
  await mkdir(goCacheDir, { recursive: true });
  const child = spawn(goBinary, [
    'test', '-count=1', '-v',
    './libs/ownershipregistry/...',
    './libs/sessionstore/...',
    './modules/audit/internal/audit',
  ], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      GOCACHE: goCacheDir,
      S0_ADMIN_DATABASE_URL: `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s0?sslmode=disable`,
      S0_GATEWAY_DATABASE_URL: `postgres://gateway_runtime:gateway-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s0?sslmode=disable`,
      S0_RELAY_DATABASE_URL: `postgres://gateway_relay_runtime:gateway-relay-local-only@127.0.0.1:${postgresHostPort}/hvac_s0?sslmode=disable`,
      S0_AUDIT_CONSUMER_DATABASE_URL: `postgres://audit_consumer_runtime:audit-consumer-local-only@127.0.0.1:${postgresHostPort}/hvac_s0?sslmode=disable`,
      S0_AUDIT_QUERY_DATABASE_URL: `postgres://audit_query_runtime:audit-query-local-only@127.0.0.1:${postgresHostPort}/hvac_s0?sslmode=disable`,
    },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`durable PostgreSQL tests failed: ${signal ?? code}`);
}

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres']);
  await waitForPostgres();
  await runGoTests();
  console.log('S0 durable PostgreSQL transaction tests passed.');
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
