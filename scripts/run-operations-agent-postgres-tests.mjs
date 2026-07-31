import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd(), process.cwd().endsWith('operations-agent-service') ? '../..' : '.');
const composePath = resolve(root, 'infra/operations-agent/compose.yaml');
const projectName = `hvac-operations-agent-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(
  root,
  process.env.OPERATIONS_AGENT_POSTGRES_REPORT_PATH
    ?? 'out/operations-agent/postgres-persistence.json',
);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const npmCliPath = process.env.npm_execpath;
const composeInvocation = (() => {
  const plugin = spawnSync('docker', ['compose', 'version'], { stdio: 'ignore', windowsHide: true });
  if (!plugin.error && plugin.status === 0) return { command: 'docker', prefix: ['compose'] };
  return { command: 'docker-compose', prefix: [] };
})();

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') {
    throw new Error('Operations Agent PostgreSQL port allocator did not expose a TCP address.');
  }
  await new Promise((resolveClose, rejectClose) => server.close((error) => (
    error ? rejectClose(error) : resolveClose()
  )));
  return address.port;
}

const postgresHostPort = await findAvailablePort();
const composeEnvironment = {
  ...process.env,
  OPERATIONS_AGENT_POSTGRES_HOST_PORT: String(postgresHostPort),
};

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(
      `${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}`,
    );
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return run(
    composeInvocation.command,
    [...composeInvocation.prefix, '-p', projectName, '-f', composePath, ...args],
    { env: composeEnvironment },
  );
}

function adminPsql(sql) {
  return run('docker', [
    'exec',
    containerName,
    'psql',
    '-U',
    'postgres',
    '-d',
    'hvac_operations_agent',
    '-v',
    'ON_ERROR_STOP=1',
    '-Atqc',
    sql,
  ]);
}

function applyMigration(role, password, path) {
  run('docker', [
    'exec',
    '-e',
    `PGPASSWORD=${password}`,
    containerName,
    'psql',
    '-h',
    '127.0.0.1',
    '-U',
    role,
    '-d',
    'hvac_operations_agent',
    '-v',
    'ON_ERROR_STOP=1',
    '-f',
    path,
  ]);
}

async function waitForPostgres() {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    try {
      const state = adminPsql(
        `SELECT
          (SELECT count(*) FROM pg_roles WHERE rolname LIKE 'operations_agent_%')::text
          || '|'
          || current_database()`,
      );
      if (state === '4|hvac_operations_agent') return;
    } catch {}
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`Operations Agent PostgreSQL fixture did not initialize.\n${logs}`);
}

const operationsUrl = `postgres://operations_agent_operations_runtime:operations-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_operations_agent?sslmode=disable`;
const checkpointsUrl = `postgres://operations_agent_checkpoints_runtime:checkpoints-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_operations_agent?sslmode=disable`;

const report = {
  schemaVersion: 1,
  component: 'operations-agent-service',
  ticket: 154,
  coveredTickets: [144, 151, 154],
  status: 'failed',
  startedAt: new Date().toISOString(),
  postgresImage: 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412',
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres']);
  await waitForPostgres();

  applyMigration(
    'operations_agent_operations_migrator',
    'operations-migrator-local-only',
    '/migrations/operations/001_agent_operations.sql',
  );
  applyMigration(
    'operations_agent_operations_migrator',
    'operations-migrator-local-only',
    '/migrations/operations/002_typed_business_records.sql',
  );
  applyMigration(
    'operations_agent_checkpoints_migrator',
    'checkpoints-migrator-local-only',
    '/migrations/checkpoints/001_agent_checkpoints.sql',
  );

  const roleState = adminPsql(`
    SELECT rolname || ':'
      || rolcanlogin::text || ':'
      || rolsuper::text || ':'
      || rolcreatedb::text || ':'
      || rolcreaterole::text || ':'
      || rolbypassrls::text
    FROM pg_roles
    WHERE rolname LIKE 'operations_agent_%'
    ORDER BY rolname
  `).split('\n');
  if (roleState.length !== 4 || roleState.some((value) => !value.endsWith(':true:false:false:false:false'))) {
    throw new Error(`Unexpected Operations Agent role privileges: ${roleState.join(', ')}`);
  }
  report.assertions.roleState = roleState;

  const ownership = adminPsql(`
    SELECT nspname || ':' || pg_get_userbyid(nspowner)
    FROM pg_namespace
    WHERE nspname IN ('agent_operations', 'agent_checkpoints')
    ORDER BY nspname
  `).split('\n');
  const expectedOwnership = [
    'agent_checkpoints:operations_agent_checkpoints_migrator',
    'agent_operations:operations_agent_operations_migrator',
  ];
  if (JSON.stringify(ownership) !== JSON.stringify(expectedOwnership)) {
    throw new Error(`Unexpected Operations Agent Schema ownership: ${ownership.join(', ')}`);
  }
  report.assertions.schemaOwnership = ownership;

  const migrationState = adminPsql(`
    SELECT
      (to_regclass('agent_operations.investigations') IS NOT NULL)::text
      || '|'
      || (to_regclass('agent_operations.investigation_effects') IS NOT NULL)::text
      || '|'
      || (to_regclass('agent_operations.investigation_business_records') IS NOT NULL)::text
      || '|'
      || (to_regclass('agent_checkpoints.runtime_checkpoints') IS NOT NULL)::text
  `);
  if (migrationState !== 'true|true|true|true') {
    throw new Error(`Operations Agent migrations are incomplete: ${migrationState}`);
  }
  report.assertions.migrations = migrationState;

  if (!npmCliPath) throw new Error('npm_execpath is required to run the service build.');
  run(process.execPath, [
    npmCliPath,
    '--prefix',
    'services/operations-agent-service',
    'run',
    'build',
  ], {
    stdio: 'inherit',
  });
  run(process.execPath, [
    '--test',
    '--test-concurrency=1',
    'services/operations-agent-service/test/postgres-persistence.test.mjs',
    'services/operations-agent-service/test/postgres-langgraph-runtime.test.mjs',
    'services/operations-agent-service/test/postgres-site-night-energy-investigation.test.mjs',
  ], {
    env: {
      ...process.env,
      OPERATIONS_AGENT_OPERATIONS_DATABASE_URL: operationsUrl,
      OPERATIONS_AGENT_CHECKPOINTS_DATABASE_URL: checkpointsUrl,
    },
    stdio: 'inherit',
  });
  report.assertions.integrationTests = true;
  report.assertions.runtimeCheckpointRecovery = true;
  report.assertions.typedBusinessRecordPersistence = true;
  report.assertions.atomicRollback = true;
  report.assertions.checkpointIndependence = true;

  report.status = 'passed';
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  console.log(`Operations Agent PostgreSQL boundaries passed. Evidence: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
