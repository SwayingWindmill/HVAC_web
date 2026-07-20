import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s0-durable/compose.yaml');
const projectName = `hvac-s0-pg-test-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const composeInvocation = (() => {
  const plugin = spawnSync('docker', ['compose', 'version'], { stdio: 'ignore', windowsHide: true });
  if (!plugin.error && plugin.status === 0) return { command: 'docker', prefix: ['compose'] };
  return { command: 'docker-compose', prefix: [] };
})();

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return run(composeInvocation.command, [...composeInvocation.prefix, '-p', projectName, '-f', composePath, ...args]);
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

async function runCompatibilityCheck() {
  const sql = await readFile(resolve(root, 'infra/s0-durable/postgres/compatibility/previous-writer.sql'), 'utf8');
  run('docker', ['exec', '-i', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-v', 'ON_ERROR_STOP=1'], { input: sql });
}

async function runGoTests() {
  await mkdir(goCacheDir, { recursive: true });
  const child = spawn(goBinary, [
    'test', '-count=1', '-v',
    './libs/ownershipregistry/...',
    './libs/sessionstore/...',
    './services/audit-ledger-service/internal/audit',
  ], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      GOCACHE: goCacheDir,
      S0_ADMIN_DATABASE_URL: 'postgres://postgres:postgres-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
      S0_GATEWAY_DATABASE_URL: 'postgres://gateway_runtime:gateway-runtime-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
      S0_RELAY_DATABASE_URL: 'postgres://gateway_relay_runtime:gateway-relay-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
      S0_AUDIT_CONSUMER_DATABASE_URL: 'postgres://audit_consumer_runtime:audit-consumer-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
      S0_AUDIT_QUERY_DATABASE_URL: 'postgres://audit_query_runtime:audit-query-local-only@127.0.0.1:55432/hvac_s0?sslmode=disable',
    },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`durable PostgreSQL tests failed: ${signal ?? code}`);
}

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres']);
  await waitForPostgres();
  await runCompatibilityCheck();
  await runGoTests();
  console.log('S0 durable PostgreSQL transaction and rollback-window compatibility tests passed.');
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
