import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { createPostgresComposeHarness, expectEqual, type PostgresAuthorityReport } from './lib/postgres-compose-harness.ts';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s5-work-order/compose.yaml');
const projectName = `hvac-s5-work-order-${process.pid}`;
const reportPath = resolve(root, process.env.S5_WORK_ORDER_REPORT_PATH ?? 'out/s5-work-order-authority/postgres-authority.json');
const { postgresHostPort, run, compose, psql, pause } = await createPostgresComposeHarness({
  root,
  composePath,
  projectName,
  database: 'hvac_s5',
  hostPortEnvName: 'S5_POSTGRES_HOST_PORT',
  portAllocatorLabel: 'S5 PostgreSQL',
});

async function waitForPostgres() {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT (to_regclass('work_order_runtime.work_order_current') IS NOT NULL)::text || '|' || (SELECT count(*) FROM work_order_runtime.work_order_current)::text");
      if (state === 'true|4') return;
    } catch {}
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`S5 PostgreSQL fixture did not initialize\n${logs}`);
}

const report: PostgresAuthorityReport = {
  schemaVersion: 1,
  slice: 'S5',
  ticket: 'S5-P2',
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
    FROM pg_roles WHERE rolname LIKE 's5_work_order_%'
  `);
  for (const role of ['s5_work_order_migrator', 's5_work_order_runtime', 's5_work_order_writer']) {
    if (!roleState.includes(`${role}:false:false:false`)) throw new Error(`${role} must be NOLOGIN, NOINHERIT and NOBYPASSRLS`);
  }
  if (!roleState.includes('s5_work_order_service:true:false:false')) throw new Error('s5_work_order_service must be LOGIN, NOINHERIT and NOBYPASSRLS');
  if (!roleState.includes('s5_work_order_mutation_service:true:false:false')) throw new Error('s5_work_order_mutation_service must be LOGIN, NOINHERIT and NOBYPASSRLS');
  report.assertions.roleState = roleState;

  expectEqual(psql("SELECT pg_has_role('s5_work_order_service', 's5_work_order_runtime', 'MEMBER')::text"), 'true', 'runtime membership');
  expectEqual(psql("SELECT pg_has_role('s5_work_order_mutation_service', 's5_work_order_writer', 'MEMBER')::text"), 'true', 'writer membership');
  expectEqual(psql("SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'work_order_runtime'"), 's5_work_order_migrator', 'schema owner');
  expectEqual(psql(`
    SELECT count(*)::text || '|' || count(*) FILTER (WHERE relrowsecurity)::text || '|' || count(*) FILTER (WHERE relforcerowsecurity)::text
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'work_order_runtime' AND c.relkind = 'r'
  `), '10|10|10', 'table/RLS baseline');
  report.assertions.forceRls = true;

  const directLoginDenied = psql(`
    SET SESSION AUTHORIZATION s5_work_order_service;
    SELECT count(*) FROM work_order_runtime.work_order_current;
  `, { expectFailure: true }).toLowerCase();
  if (!directLoginDenied.includes('permission denied')) throw new Error(`runtime login bypassed explicit role activation: ${directLoginDenied}`);
  report.assertions.explicitActivationRequired = true;

  expectEqual(psql("SELECT tenant_id::text FROM work_order_runtime.organization_tenant_scope WHERE organization_id = '01920000-0000-7000-8000-000000000001'::uuid"), '0191f000-0000-7000-8000-000000000001', 'Organization Tenant binding');
  expectEqual(psql(`
    SET SESSION AUTHORIZATION s5_work_order_service;
    SET ROLE s5_work_order_runtime;
    SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000001', false);
    SELECT set_config('app.tenant_id', '0191f000-0000-7000-8000-000000000001', false);
    SELECT count(*) FROM work_order_runtime.work_order_current;
  `).split('\n').at(-1), '3', 'authorized Tenant/Organization visibility');
  expectEqual(psql(`
    SET SESSION AUTHORIZATION s5_work_order_service;
    SET ROLE s5_work_order_runtime;
    SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000001', false);
    SELECT set_config('app.tenant_id', '0191f000-0000-7000-8000-000000000002', false);
    SELECT count(*) FROM work_order_runtime.work_order_current;
  `).split('\n').at(-1), '0', 'cross-Tenant invisibility');
  expectEqual(psql(`
    SET SESSION AUTHORIZATION s5_work_order_service;
    SET ROLE s5_work_order_runtime;
    SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000099', false);
    SELECT set_config('app.tenant_id', '0191f000-0000-7000-8000-000000000001', false);
    SELECT count(*) FROM work_order_runtime.work_order_current;
  `).split('\n').at(-1), '0', 'cross-Organization invisibility');
  report.assertions.organizationRls = true;
  report.assertions.tenantOrganizationRls = true;

  for (const [operation, sql] of [
    ['insert', `INSERT INTO work_order_runtime.work_order_current SELECT * FROM work_order_runtime.work_order_current`],
    ['update', `UPDATE work_order_runtime.work_order_current SET title = 'forbidden'`],
    ['delete', `DELETE FROM work_order_runtime.work_order_current`],
  ]) {
    const denied = psql(`
      SET SESSION AUTHORIZATION s5_work_order_service;
      SET ROLE s5_work_order_runtime;
      SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000001', false);
      SELECT set_config('app.tenant_id', '0191f000-0000-7000-8000-000000000001', false);
      ${sql};
    `, { expectFailure: true }).toLowerCase();
    if (!denied.includes('permission denied')) throw new Error(`runtime can ${operation} Work Order authority rows: ${denied}`);
  }
  report.assertions.readOnlyRuntime = true;


  for (const [operation, sql] of [
    ['delete current', `DELETE FROM work_order_runtime.work_order_current`],
    ['insert task', `INSERT INTO work_order_runtime.work_order_task (organization_id, site_id, work_order_id, task_id, position, title, status, version, created_at, updated_at) VALUES ('01920000-0000-7000-8000-000000000001','01920000-0001-7000-8000-000000000001','01920000-1000-7000-8000-000000000001','01930000-4000-7000-8000-000000000099',99,'forbidden','OPEN',1,now(),now())`],
  ]) {
    const denied = psql(`
      SET SESSION AUTHORIZATION s5_work_order_mutation_service;
      SET ROLE s5_work_order_writer;
      SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000001', false);
      SELECT set_config('app.tenant_id', '0191f000-0000-7000-8000-000000000001', false);
      ${sql};
    `, { expectFailure: true }).toLowerCase();
    if (!denied.includes('permission denied')) throw new Error(`writer can perform unreviewed ${operation}: ${denied}`);
  }
  report.assertions.boundedWriter = true;

  const testEnvironment = {
    ...process.env,
    S5_WORK_ORDER_TEST_DATABASE_URL: `postgres://s5_work_order_service:local-fixture-only@127.0.0.1:${postgresHostPort}/hvac_s5?sslmode=disable`,
    S5_WORK_ORDER_MUTATION_TEST_DATABASE_URL: `postgres://s5_work_order_mutation_service:local-mutation-fixture-only@127.0.0.1:${postgresHostPort}/hvac_s5?sslmode=disable`,
    S5_WORK_ORDER_ADMIN_DATABASE_URL: `postgres://postgres:local-fixture-only@127.0.0.1:${postgresHostPort}/hvac_s5?sslmode=disable`,
  };
  run(process.execPath, ['scripts/run-go.mjs', 'test', '-count=1', './services/work-order-service/...'], { env: testEnvironment, stdio: 'inherit' });
  report.assertions.goIntegrationTests = true;

  expectEqual(psql("SELECT count(*)::text FROM work_order_runtime.work_order_current"), '4', 'fixture row preservation');
  expectEqual(psql("SELECT task_total::text || '|' || task_completed::text || '|' || note_count::text || '|' || attachment_count::text FROM work_order_runtime.work_order_current WHERE work_order_id = '01920000-1000-7000-8000-000000000001'"), '2|1|1|1', 'projection restoration');
  report.assertions.projectionConvergence = true;

  report.status = 'passed';
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  console.log(`S5 Work Order PostgreSQL authority passed. Evidence: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.completedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
