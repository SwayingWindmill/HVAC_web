import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s4-alarm/compose.yaml');
const projectName = `hvac-s4-alarm-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, process.env.S4_ALARM_REPORT_PATH ?? 'out/s4-alarm-authority/postgres-authority.json');
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
  if (!address || typeof address === 'string') throw new Error('S4 PostgreSQL port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort();
const composeEnvironment = { ...process.env, S4_POSTGRES_HOST_PORT: String(postgresHostPort) };

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
    'exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s4', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql,
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  const stdout = String(result.stdout ?? '').trim();
  const stderr = String(result.stderr ?? '').trim();
  if (expectFailure) {
    if (!result.error && result.status === 0) throw new Error(`SQL unexpectedly succeeded: ${sql}`);
    return `${stdout}\n${stderr}`.trim();
  }
  if (result.error || result.status !== 0) throw new Error(`SQL failed: ${result.error?.message ?? stderr ?? result.status}\n${sql}`);
  return stdout;
}

async function waitForPostgres() {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT (to_regclass('alarm_runtime.alarm_current') IS NOT NULL)::text || '|' || (to_regclass('alarm_runtime.alarm_idempotency') IS NOT NULL)::text || '|' || (SELECT count(*) FROM alarm_runtime.alarm_current)::text");
      if (state === 'true|true|1') return;
    } catch {}
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`S4 PostgreSQL fixture did not initialize\n${logs}`);
}

function expectEqual(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

const report = {
  schemaVersion: 1,
  slice: 'S4',
  ticket: 'S4-P2',
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
    FROM pg_roles WHERE rolname LIKE 's4_alarm_%'
  `);
  for (const role of ['s4_alarm_migrator', 's4_alarm_runtime']) {
    if (!roleState.includes(`${role}:false:false:false`)) throw new Error(`${role} must be NOLOGIN, NOINHERIT and NOBYPASSRLS`);
  }
  if (!roleState.includes('s4_alarm_service:true:false:false')) throw new Error('s4_alarm_service must be LOGIN, NOINHERIT and NOBYPASSRLS');
  report.assertions.roleState = roleState;

  expectEqual(psql("SELECT pg_has_role('s4_alarm_service', 's4_alarm_runtime', 'MEMBER')::text"), 'true', 'runtime membership');
  report.assertions.runtimeMembership = true;
  expectEqual(psql("SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'alarm_runtime'"), 's4_alarm_migrator', 'schema owner');
  report.assertions.schemaOwner = 's4_alarm_migrator';
  expectEqual(psql(`
    SELECT count(*)::text || '|' || count(*) FILTER (WHERE relrowsecurity)::text || '|' || count(*) FILTER (WHERE relforcerowsecurity)::text
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'alarm_runtime' AND c.relkind = 'r'
  `), '3|3|3', 'table/RLS baseline');
  report.assertions.forceRls = true;

  const directLoginDenied = psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SELECT count(*) FROM alarm_runtime.alarm_current;
  `, { expectFailure: true }).toLowerCase();
  if (!directLoginDenied.includes('permission denied')) throw new Error(`runtime login bypassed explicit role activation: ${directLoginDenied}`);
  report.assertions.explicitActivationRequired = true;

  expectEqual(psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SET ROLE s4_alarm_runtime;
    SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000001', false);
    SELECT count(*) FROM alarm_runtime.alarm_current;
  `).split('\n').at(-1), '1', 'authorized Tenant visibility');
  expectEqual(psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SET ROLE s4_alarm_runtime;
    SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000099', false);
    SELECT count(*) FROM alarm_runtime.alarm_current;
  `).split('\n').at(-1), '0', 'cross-Tenant invisibility');
  expectEqual(psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SET ROLE s4_alarm_runtime;
    SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000001', false);
    SELECT event_type || '|' || coalesce(point_id::text, '') || '|' || severity || '|' || status
    FROM alarm_runtime.events;
  `).split('\n').at(-1), 'SUPPLY_TEMPERATURE_DRIFT|01910000-4000-7000-8000-000000000001|MAJOR|ACTIVE', 'canonical Event projection');
  expectEqual(psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SET ROLE s4_alarm_runtime;
    SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000099', false);
    SELECT count(*) FROM alarm_runtime.events;
  `).split('\n').at(-1), '0', 'cross-Tenant Event invisibility');
  expectEqual(psql(`
    SELECT a.alarm_type || '|' || a.event_id::text || '|' || a.point_id::text || '|' || e.event_type
    FROM alarm_runtime.alarm_current a
    JOIN alarm_runtime.events e
      ON e.tenant_id = a.tenant_id
     AND e.site_id = a.site_id
     AND e.event_id = a.event_id;
  `), 'SUPPLY_TEMPERATURE_DRIFT|01910000-3000-7000-8000-000000000001|01910000-4000-7000-8000-000000000001|SUPPLY_TEMPERATURE_DRIFT', 'Event to Alarm provenance');
  report.assertions.tenantRls = true;
  report.assertions.eventAlarmProvenance = true;

  const insertDenied = psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SET ROLE s4_alarm_runtime;
    SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000001', false);
    INSERT INTO alarm_runtime.alarm_current SELECT * FROM alarm_runtime.alarm_current;
  `, { expectFailure: true }).toLowerCase();
  if (!insertDenied.includes('permission denied')) throw new Error(`runtime can insert Alarm authority rows: ${insertDenied}`);
  const titleUpdateDenied = psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SET ROLE s4_alarm_runtime;
    SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000001', false);
    UPDATE alarm_runtime.alarm_current SET title = 'forbidden';
  `, { expectFailure: true }).toLowerCase();
  if (!titleUpdateDenied.includes('permission denied')) throw new Error(`runtime can update immutable Alarm fields: ${titleUpdateDenied}`);
  const deleteDenied = psql(`
    SET SESSION AUTHORIZATION s4_alarm_service;
    SET ROLE s4_alarm_runtime;
    SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000001', false);
    DELETE FROM alarm_runtime.alarm_current;
  `, { expectFailure: true }).toLowerCase();
  if (!deleteDenied.includes('permission denied')) throw new Error(`runtime can delete Alarm authority rows: ${deleteDenied}`);
  report.assertions.leastPrivilegeColumns = true;

  const testEnvironment = {
    ...process.env,
    S4_ALARM_TEST_DATABASE_URL: `postgres://s4_alarm_service:s4-alarm-service-local-only@127.0.0.1:${postgresHostPort}/hvac_s4?sslmode=disable`,
  };
  run(process.execPath, ['scripts/run-go.mjs', 'test', '-count=1', './services/alarm-service/...'], { env: testEnvironment, stdio: 'inherit' });
  report.assertions.goIntegrationTests = true;
  expectEqual(psql("SELECT status || '|' || version::text || '|' || jsonb_array_length(transitions)::text || '|' || coalesce(assignee_id, '') || '|' || coalesce(suppressed_until::text, '') FROM alarm_runtime.alarm_current"), 'ACKNOWLEDGED|5|5|principal:postgres-operator-2|', 'durable lifecycle projection');
  expectEqual(psql("SELECT count(*)::text FROM alarm_runtime.alarm_idempotency"), '4', 'durable idempotency record');
  expectEqual(psql("SELECT alarm_type || '|' || event_id::text || '|' || point_id::text FROM alarm_runtime.alarm_current"), 'SUPPLY_TEMPERATURE_DRIFT|01910000-3000-7000-8000-000000000001|01910000-4000-7000-8000-000000000001', 'Event provenance survives Alarm lifecycle mutations');
  report.assertions.durableProjectionAndIdempotency = true;

  report.status = 'passed';
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  console.log(`S4 Alarm PostgreSQL authority passed. Evidence: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
