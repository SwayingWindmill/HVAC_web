import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, readdir, rm } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const postgresBin = '/usr/lib/postgresql/16/bin';
const initdb = join(postgresBin, 'initdb');
const postgres = join(postgresBin, 'postgres');
const pgIsReady = join(postgresBin, 'pg_isready');
const psql = join(postgresBin, 'psql');
const initDirectory = resolve(root, 'infra/s1-registry/postgres/init');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: 'utf8',
    stdio: options.capture ? 'pipe' : 'inherit',
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

async function availablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('temporary PostgreSQL port was not allocated');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function sql(port, database, statement) {
  return run(psql, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', statement], { capture: true });
}

function sqlFailure(port, database, statement) {
  const result = spawnSync(psql, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', statement], {
    cwd: root,
    encoding: 'utf8',
  });
  if (!result.error && result.status === 0) throw new Error(`SQL unexpectedly succeeded: ${statement}`);
  return String(result.stderr ?? '').trim();
}

async function waitReady(port, postgresProcess) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (postgresProcess.exitCode !== null || postgresProcess.signalCode !== null) throw new Error('temporary PostgreSQL exited before readiness');
    const ready = spawnSync(pgIsReady, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', 'postgres'], { stdio: 'ignore' });
    if (ready.status === 0) return;
    await pause(100);
  }
  throw new Error('temporary PostgreSQL did not become ready');
}

function expect(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

const dataDirectory = await mkdtemp(join(tmpdir(), 'hvac-s1-tenant-'));
const port = await availablePort();
let postgresProcess;

try {
  run(initdb, ['-D', dataDirectory, '-A', 'trust', '-U', 'postgres', '--encoding=UTF8', '--no-locale'], { capture: true });
  postgresProcess = spawn(postgres, ['-D', dataDirectory, '-h', '127.0.0.1', '-p', String(port), '-k', dataDirectory], {
    cwd: root,
    stdio: 'ignore',
  });
  await waitReady(port, postgresProcess);
  sql(port, 'postgres', 'CREATE DATABASE hvac_s1');

  const migrations = (await readdir(initDirectory))
    .filter((name) => name.endsWith('.sql'))
    .sort((left, right) => left.localeCompare(right, 'en'));
  for (const migration of migrations) {
    run(psql, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-f', resolve(initDirectory, migration)], { capture: true });
  }

  expect(sql(port, 'hvac_s1', 'SELECT count(*) FROM iam.tenants'), '2', 'Tenant fixture count');
  expect(
    sql(port, 'hvac_s1', "SELECT string_agg(code || ':' || tenant_id::text, ',' ORDER BY code) FROM core_registry.organizations"),
    'acting:018f1d00-0000-7000-8000-000000000001,owner-a:018f1d00-0000-7000-8000-000000000001,owner-b:018f1d00-0000-7000-8000-000000000002',
    'Organization Tenant ownership',
  );

  const tenantAVisible = sql(port, 'hvac_s1', `
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SET LOCAL app.authorized_organization_ids = '{018f1e00-0000-7000-8000-000000000001}';
    SET LOCAL app.authorized_site_ids = '{}';
    SELECT count(*) FROM core_registry.sites;
    ROLLBACK;
  `);
  expect(tenantAVisible, '2', 'Tenant A Site visibility');

  const crossTenantVisible = sql(port, 'hvac_s1', `
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000002';
    SET LOCAL app.authorized_organization_ids = '{018f1e00-0000-7000-8000-000000000001}';
    SET LOCAL app.authorized_site_ids = '{018f1e00-1000-7000-8000-000000000001}';
    SELECT count(*) FROM core_registry.sites;
    ROLLBACK;
  `);
  expect(crossTenantVisible, '0', 'cross-Tenant Site isolation');

  const tenantColumns = sql(port, 'hvac_s1', `
    SELECT count(*)
    FROM information_schema.columns
    WHERE table_schema = 'core_registry'
      AND table_name IN ('areas','equipment','devices','sensors','telemetry_points','point_subject_bindings')
      AND column_name = 'tenant_id'
      AND is_nullable = 'NO';
  `);
  expect(tenantColumns, '6', 'Tenant columns on Registry asset model');

  const tenantRLS = sql(port, 'hvac_s1', `
    BEGIN;
    SET LOCAL ROLE s1_iam_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SELECT count(*) FROM iam.tenants;
    ROLLBACK;
  `);
  expect(tenantRLS, '1', 'Tenant RLS visibility');

  sql(port, 'hvac_s1', `
    INSERT INTO core_registry.equipment
      (id, tenant_id, organization_id, site_id, code, display_name, equipment_type, status, revision, created_at, updated_at)
    VALUES
      ('018f1e00-3000-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'ahu-2', 'Air Handling Unit 2', 'AHU', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.device_bindings
      (id, tenant_id, organization_id, site_id, device_id, equipment_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at)
    VALUES
      ('018f1e00-5000-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-4000-7000-8000-000000000001', '018f1e00-3000-7000-8000-000000000099', 'CONTROLLER', 'ACTIVE', now(), NULL, 1, now(), now());
  `);
  expect(
    sql(port, 'hvac_s1', "SELECT count(*) FROM core_registry.device_bindings WHERE device_id = '018f1e00-4000-7000-8000-000000000001' AND binding_role = 'CONTROLLER' AND status = 'ACTIVE' AND valid_to IS NULL"),
    '2',
    'one Device Endpoint controlling multiple Equipment',
  );

  sql(port, 'hvac_s1', `
    INSERT INTO core_registry.telemetry_points
      (id, tenant_id, organization_id, site_id, reporting_device_id, sensor_id, point_key, source_key, display_name, point_kind, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, formula_revision, source_metadata, status, revision, created_at, updated_at)
    VALUES
      ('018f1e00-8000-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-4000-7000-8000-000000000001', NULL, 'test.measured', 'testMeasured', 'Measured Test Point', 'MEASURED', 'NUMBER', 'Cel', false, 1000, 1000, 5000, NULL, '{}'::jsonb, 'ACTIVE', 1, now(), now());
  `);
  const invalidControl = sqlFailure(port, 'hvac_s1', `
    INSERT INTO core_registry.point_subject_bindings
      (id, tenant_id, organization_id, site_id, point_id, subject_type, area_id, equipment_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at)
    VALUES
      ('018f1e00-8100-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-8000-7000-8000-000000000099', 'EQUIPMENT', NULL, '018f1e00-3000-7000-8000-000000000001', 'CONTROLS', 'ACTIVE', now(), NULL, 1, now(), now());
  `);
  if (!invalidControl.includes('current CONTROLS binding requires an active writable COMMAND point')) {
    throw new Error(`invalid CONTROLS binding was not rejected by the expected invariant: ${invalidControl}`);
  }

  console.log(`S1 Tenant foundation PostgreSQL verification passed on temporary PostgreSQL 16 (${migrations.length} migrations).`);
} finally {
  if (postgresProcess && postgresProcess.exitCode === null && postgresProcess.signalCode === null) {
    postgresProcess.kill('SIGTERM');
    await Promise.race([once(postgresProcess, 'exit'), pause(3000)]).catch(() => {});
  }
  await rm(dataDirectory, { recursive: true, force: true });
}
