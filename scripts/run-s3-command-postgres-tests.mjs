import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s3-command/compose.yaml');
const projectName = `hvac-s3-command-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, process.env.S3_COMMAND_REPORT_PATH ?? 'out/s3-ticket-02/postgres-authority.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
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
  if (!address || typeof address === 'string') throw new Error('S3 PostgreSQL port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort();
const composeEnvironment = { ...process.env, S3_POSTGRES_HOST_PORT: String(postgresHostPort) };

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return run(composeInvocation.command, [...composeInvocation.prefix, '-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function psql(sql, { expectFailure = false } = {}) {
  const result = spawnSync('docker', [
    'exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s3', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql,
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  const stdout = String(result.stdout ?? '').trim();
  const stderr = String(result.stderr ?? '').trim();
  if (expectFailure) {
    if (!result.error && result.status === 0) throw new Error(`SQL unexpectedly succeeded: ${sql}`);
    return `${stdout}\n${stderr}`.trim();
  }
  if (result.error || result.status !== 0) {
    throw new Error(`SQL failed: ${result.error?.message ?? stderr ?? result.status}\n${sql}`);
  }
  return stdout;
}

async function waitForPostgres() {
  let stableStart = '';
  let stableChecks = 0;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('command_runtime.command_intents') IS NOT NULL)::text || '|' || (to_regclass('command_runtime.command_dispatch_outbox') IS NOT NULL)::text");
      const [startedAt, intentsReady, outboxReady] = state.split('|');
      if (intentsReady === 'true' && outboxReady === 'true') {
        if (startedAt === stableStart) stableChecks += 1;
        else {
          stableStart = startedAt;
          stableChecks = 1;
        }
        if (stableChecks >= 3) return;
      } else {
        stableChecks = 0;
      }
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`S3 PostgreSQL fixture did not initialize\n${logs}`);
}

function expectEqual(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

const report = {
  schemaVersion: 1,
  slice: 'S3',
  ticket: 'S3-02',
  status: 'failed',
  startedAt: new Date().toISOString(),
  postgresImage: 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412',
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres']);
  await waitForPostgres();

  const roleState = psql(`
    SELECT string_agg(rolname || ':' || rolcanlogin::text || ':' || rolinherit::text || ':' || rolbypassrls::text, ',' ORDER BY rolname)
    FROM pg_roles
    WHERE rolname LIKE 's3_command_%'
  `);
  for (const role of ['s3_command_migrator', 's3_command_runtime', 's3_command_dispatcher']) {
    if (!roleState.includes(`${role}:false:false:false`)) throw new Error(`${role} must be NOLOGIN, NOINHERIT and NOBYPASSRLS`);
  }
  for (const role of ['s3_command_migrator_service', 's3_command_service', 's3_command_dispatcher_service']) {
    if (!roleState.includes(`${role}:true:false:false`)) throw new Error(`${role} must be LOGIN, NOINHERIT and NOBYPASSRLS`);
  }
  report.assertions.roleState = roleState;

  const memberships = psql(`
    SELECT pg_has_role('s3_command_migrator_service', 's3_command_migrator', 'MEMBER')::text || '|'
      || pg_has_role('s3_command_service', 's3_command_runtime', 'MEMBER')::text || '|'
      || pg_has_role('s3_command_dispatcher_service', 's3_command_dispatcher', 'MEMBER')::text
  `);
  expectEqual(memberships, 'true|true|true', 'role memberships');
  report.assertions.roleMemberships = memberships;

  const schemaOwner = psql("SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'command_runtime'");
  expectEqual(schemaOwner, 's3_command_migrator', 'schema owner');
  report.assertions.schemaOwner = schemaOwner;

  const tableState = psql(`
    SELECT count(*)::text || '|'
      || count(*) FILTER (WHERE c.relrowsecurity)::text || '|'
      || count(*) FILTER (WHERE c.relforcerowsecurity)::text
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'command_runtime' AND c.relkind = 'r'
  `);
  expectEqual(tableState, '11|10|10', 'table/RLS baseline');
  report.assertions.tableRlsState = tableState;

  const capability = psql(`
    SELECT capability_name || '|' || capability_revision || '|' || status || '|' || connector_kind || '|' || retry_policy
    FROM command_runtime.capability_profiles
  `);
  expectEqual(capability, 'SET_TEMPERATURE_SETPOINT|capability:set-temperature-setpoint:v1|DRAFT|SYNTHETIC_ONLY|PRE_SEND_ONLY', 'Capability baseline');
  report.assertions.capability = capability;

  const serviceDenied = psql(`
    SET SESSION AUTHORIZATION s3_command_service;
    SELECT count(*) FROM command_runtime.command_intents;
  `, { expectFailure: true }).toLowerCase();
  if (!serviceDenied.includes('permission denied')) throw new Error(`runtime login bypassed explicit activation: ${serviceDenied}`);
  report.assertions.runtimeRequiresActivation = true;

  const dispatcherDenied = psql(`
    SET SESSION AUTHORIZATION s3_command_dispatcher_service;
    SELECT count(*) FROM command_runtime.command_dispatch_outbox;
  `, { expectFailure: true }).toLowerCase();
  if (!dispatcherDenied.includes('permission denied')) throw new Error(`S3-02 dispatcher unexpectedly has direct database access: ${dispatcherDenied}`);
  report.assertions.dispatcherAccessDeferred = true;

  const testEnvironment = {
    ...process.env,
    S3_COMMAND_TEST_DATABASE_URL: `postgres://s3_command_service:s3-command-service-local-only@127.0.0.1:${postgresHostPort}/hvac_s3?sslmode=disable`,
    S3_COMMAND_ADMIN_DATABASE_URL: `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s3?sslmode=disable`,
  };
  run(process.execPath, ['scripts/run-go.mjs', 'test', './services/command-service/...'], { env: testEnvironment, stdio: 'inherit' });
  report.assertions.goIntegrationTests = true;

  report.status = 'passed';
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  console.log(`S3 Command PostgreSQL authority passed. Evidence: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
