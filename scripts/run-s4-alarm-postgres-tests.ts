import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { createPostgresComposeHarness, expectEqual, type PostgresAuthorityReport } from './lib/postgres-compose-harness.ts';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s4-alarm/compose.yaml');
const projectName = `hvac-s4-alarm-${process.pid}`;
const reportPath = resolve(root, process.env.S4_ALARM_REPORT_PATH ?? 'out/s4-alarm-authority/postgres-authority.json');
const { postgresHostPort, run, compose, psql, pause } = await createPostgresComposeHarness({
  root,
  composePath,
  projectName,
  database: 'hvac_s4',
  hostPortEnvName: 'S4_POSTGRES_HOST_PORT',
  portAllocatorLabel: 'S4 PostgreSQL',
});

async function waitForPostgres() {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT (to_regclass('alarm_runtime.alarm_current') IS NOT NULL)::text || '|' || (to_regclass('alarm_runtime.alarm_timeline') IS NOT NULL)::text || '|' || (to_regclass('alarm_runtime.s13_alarm_migration_report') IS NOT NULL)::text || '|' || (to_regclass('alarm_runtime.alarm_evaluation_state') IS NOT NULL)::text || '|' || (SELECT count(*) FROM alarm_runtime.alarm_current)::text");
      if (state === 'true|true|true|true|1') return;
    } catch {}
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`S4 PostgreSQL fixture did not initialize\n${logs}`);
}

const report: PostgresAuthorityReport = {
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
      AND c.relname IN ('alarm_current', 'alarm_idempotency', 'events', 'alarm_timeline', 'alarm_policy_revision', 'alarm_policy_assignment', 'alarm_evaluation_state', 'alarm_evaluation_event')
  `), '8|8|8', 'runtime authority table/RLS baseline');
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

  expectEqual(psql(`SELECT has_table_privilege('s4_alarm_runtime', 'alarm_runtime.alarm_current', 'DELETE')::text`), 'false', 'runtime Alarm DELETE privilege');
  expectEqual(psql(`SELECT has_column_privilege('s4_alarm_runtime', 'alarm_runtime.alarm_current', 'title', 'UPDATE')::text`), 'false', 'runtime immutable title UPDATE privilege');
  expectEqual(psql(`SELECT has_column_privilege('s4_alarm_runtime', 'alarm_runtime.alarm_current', 'condition', 'UPDATE')::text`), 'true', 'runtime condition UPDATE privilege');
  expectEqual(psql(`SELECT has_column_privilege('s4_alarm_runtime', 'alarm_runtime.alarm_current', 'fingerprint', 'INSERT')::text`), 'true', 'runtime governed incident INSERT privilege');
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

  expectEqual(psql(`SELECT source_incident_count::text || '|' || target_incident_count::text || '|' || identity_preserved::text FROM alarm_runtime.s13_alarm_migration_report WHERE migration_id = 'S13-2026-08-19'`), '0|0|true', 'S13 one-shot migration report');
  expectEqual(psql(`SELECT (to_regclass('alarm_runtime.alarm_current_pre_s13_backup') IS NOT NULL)::text || '|' || (to_regclass('alarm_runtime.alarm_idempotency_pre_s13_backup') IS NOT NULL)::text`), 'true|true', 'S13 pre-migration backups');
  expectEqual(psql(`SELECT count(*)::text FROM pg_indexes WHERE schemaname = 'alarm_runtime' AND indexname = 'alarm_current_one_active_fingerprint_uidx' AND indexdef LIKE '%WHERE (condition = ''ACTIVE''::text)%'`), '1', 'S13 active fingerprint partial unique index');
  const timelineMutationDenied = psql(`
    SET ROLE s4_alarm_migrator;
    UPDATE alarm_runtime.alarm_timeline SET reason = 'tampered';
  `, { expectFailure: true }).toLowerCase();
  if (!timelineMutationDenied.includes('append-only')) throw new Error(`Alarm timeline immutability trigger did not reject mutation: ${timelineMutationDenied}`);
  report.assertions.s13MigrationEvidence = true;
  report.assertions.immutableTimeline = true;

  const policyMutationDenied = psql(`
    SET ROLE s4_alarm_migrator;
    UPDATE alarm_runtime.alarm_policy_revision SET released_by = 'tampered';
  `, { expectFailure: true }).toLowerCase();
  if (!policyMutationDenied.includes('immutable')) throw new Error(`Alarm policy release immutability trigger did not reject mutation: ${policyMutationDenied}`);
  expectEqual(psql(`SELECT has_table_privilege('s4_alarm_runtime', 'alarm_runtime.alarm_evaluation_state', 'DELETE')::text`), 'false', 'runtime evaluator state DELETE privilege');
  expectEqual(psql(`SELECT has_table_privilege('s4_alarm_runtime', 'alarm_runtime.alarm_evaluation_event', 'UPDATE')::text`), 'false', 'runtime evaluator evidence UPDATE privilege');
  report.assertions.s14ImmutableReleaseAndEvidence = true;
  report.assertions.s14LeastPrivilege = true;

  const testEnvironment = {
    ...process.env,
    S4_ALARM_TEST_DATABASE_URL: `postgres://s4_alarm_service:s4-alarm-service-local-only@127.0.0.1:${postgresHostPort}/hvac_s4?sslmode=disable`,
  };
  run(process.execPath, ['scripts/run-go.mjs', 'test', '-count=1', './services/alarm-service/...'], { env: testEnvironment, stdio: 'inherit' });
  report.assertions.goIntegrationTests = true;
  expectEqual(psql("SELECT condition || '|' || version::text || '|' || (acknowledged_at IS NOT NULL)::text || '|' || coalesce(assignee_id, '') || '|' || coalesce(suppression::text, '') || '|' || current_severity || '|' || peak_severity FROM alarm_runtime.alarm_current WHERE alarm_id = '01910000-1000-7000-8000-000000000001'"), 'ACTIVE|5|true|principal:postgres-operator-2||MAJOR|MAJOR', 'durable orthogonal projection');
  expectEqual(psql("SELECT count(*)::text FROM alarm_runtime.alarm_timeline WHERE alarm_id = '01910000-1000-7000-8000-000000000001'"), '5', 'durable immutable timeline');
  expectEqual(psql("SELECT count(*)::text FROM alarm_runtime.alarm_idempotency"), '4', 'durable idempotency record');
  expectEqual(psql("SELECT alarm_type || '|' || event_id::text || '|' || point_id::text FROM alarm_runtime.alarm_current WHERE alarm_id = '01910000-1000-7000-8000-000000000001'"), 'SUPPLY_TEMPERATURE_DRIFT|01910000-3000-7000-8000-000000000001|01910000-4000-7000-8000-000000000001', 'Event provenance survives Alarm lifecycle mutations');
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
