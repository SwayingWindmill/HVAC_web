import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s5-work-order/compose.yaml');
const projectName = `hvac-s5-work-order-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, process.env.S5_WORK_ORDER_REPORT_PATH ?? 'out/s5-work-order-authority/postgres-authority.json');
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
  if (!address || typeof address === 'string') throw new Error('S5 PostgreSQL port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort();
const composeEnvironment = { ...process.env, S5_POSTGRES_HOST_PORT: String(postgresHostPort) };

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
    'exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s5', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql,
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
      const state = psql("SELECT (to_regclass('work_order_runtime.work_order_current') IS NOT NULL)::text || '|' || (SELECT count(*) FROM work_order_runtime.work_order_current)::text");
      if (state === 'true|4') return;
    } catch {}
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`S5 PostgreSQL fixture did not initialize\n${logs}`);
}

function expectEqual(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

const report = {
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
  for (const role of ['s5_work_order_migrator', 's5_work_order_runtime']) {
    if (!roleState.includes(`${role}:false:false:false`)) throw new Error(`${role} must be NOLOGIN, NOINHERIT and NOBYPASSRLS`);
  }
  if (!roleState.includes('s5_work_order_service:true:false:false')) throw new Error('s5_work_order_service must be LOGIN, NOINHERIT and NOBYPASSRLS');
  report.assertions.roleState = roleState;

  expectEqual(psql("SELECT pg_has_role('s5_work_order_service', 's5_work_order_runtime', 'MEMBER')::text"), 'true', 'runtime membership');
  expectEqual(psql("SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'work_order_runtime'"), 's5_work_order_migrator', 'schema owner');
  expectEqual(psql(`
    SELECT count(*)::text || '|' || count(*) FILTER (WHERE relrowsecurity)::text || '|' || count(*) FILTER (WHERE relforcerowsecurity)::text
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'work_order_runtime' AND c.relkind = 'r'
  `), '7|7|7', 'table/RLS baseline');
  report.assertions.forceRls = true;

  const directLoginDenied = psql(`
    SET SESSION AUTHORIZATION s5_work_order_service;
    SELECT count(*) FROM work_order_runtime.work_order_current;
  `, { expectFailure: true }).toLowerCase();
  if (!directLoginDenied.includes('permission denied')) throw new Error(`runtime login bypassed explicit role activation: ${directLoginDenied}`);
  report.assertions.explicitActivationRequired = true;

  expectEqual(psql(`
    SET SESSION AUTHORIZATION s5_work_order_service;
    SET ROLE s5_work_order_runtime;
    SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000001', false);
    SELECT count(*) FROM work_order_runtime.work_order_current;
  `).split('\n').at(-1), '3', 'authorized Organization visibility');
  expectEqual(psql(`
    SET SESSION AUTHORIZATION s5_work_order_service;
    SET ROLE s5_work_order_runtime;
    SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000099', false);
    SELECT count(*) FROM work_order_runtime.work_order_current;
  `).split('\n').at(-1), '0', 'cross-Organization invisibility');
  report.assertions.organizationRls = true;

  for (const [operation, sql] of [
    ['insert', `INSERT INTO work_order_runtime.work_order_current SELECT * FROM work_order_runtime.work_order_current`],
    ['update', `UPDATE work_order_runtime.work_order_current SET title = 'forbidden'`],
    ['delete', `DELETE FROM work_order_runtime.work_order_current`],
  ]) {
    const denied = psql(`
      SET SESSION AUTHORIZATION s5_work_order_service;
      SET ROLE s5_work_order_runtime;
      SELECT set_config('app.organization_id', '01920000-0000-7000-8000-000000000001', false);
      ${sql};
    `, { expectFailure: true }).toLowerCase();
    if (!denied.includes('permission denied')) throw new Error(`runtime can ${operation} Work Order authority rows: ${denied}`);
  }
  report.assertions.readOnlyRuntime = true;

  const testEnvironment = {
    ...process.env,
    S5_WORK_ORDER_TEST_DATABASE_URL: `postgres://s5_work_order_service:local-fixture-only@127.0.0.1:${postgresHostPort}/hvac_s5?sslmode=disable`,
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
