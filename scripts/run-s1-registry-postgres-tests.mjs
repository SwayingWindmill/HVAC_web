import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s1-registry/compose.yaml');
const projectName = `hvac-s1-registry-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, process.env.S1_REGISTRY_REPORT_PATH ?? 'out/s1-registry-core/postgres-baseline.json');
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
async function findAvailablePort(requestedPort = 0) {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: Number(requestedPort) || 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('S1 PostgreSQL port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort(process.env.S1_POSTGRES_HOST_PORT ?? 0);
const telemetryGrantPassword = randomBytes(24).toString('hex');
const coreServicePassword = randomBytes(24).toString('hex');
const composeEnvironment = { ...process.env, S1_POSTGRES_HOST_PORT: String(postgresHostPort) };

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function psql(sql, { expectFailure = false } = {}) {
  const result = spawnSync('docker', [
    'exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql,
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (expectFailure) {
    if (!result.error && result.status === 0) throw new Error(`SQL unexpectedly succeeded: ${sql}`);
    return String(result.stderr ?? '').trim();
  }
  if (result.error || result.status !== 0) {
    throw new Error(`SQL failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}\n${sql}`);
  }
  return String(result.stdout ?? '').trim();
}

async function waitForPostgres() {
  let stablePostmasterStart = '';
  let stableChecks = 0;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('core_registry.sites') IS NOT NULL)::text || '|' || (to_regclass('iam.principals') IS NOT NULL)::text || '|' || (to_regclass('core_registry.backup_manifests') IS NOT NULL)::text");
      const [postmasterStart, coreReady, iamReady, governanceReady] = state.split('|');
      if (coreReady === 'true' && iamReady === 'true' && governanceReady === 'true') {
        if (postmasterStart === stablePostmasterStart) stableChecks += 1;
        else {
          stablePostmasterStart = postmasterStart;
          stableChecks = 1;
        }
        if (stableChecks >= 16) return;
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
  throw new Error(`S1 PostgreSQL fixture did not reach a stable initialized postmaster\n${logs}`);
}

function expectEqual(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

function seedIAMIntegrationFixtures() {
  psql(`
    INSERT INTO iam.policies (id, tenant_id, policy_key, policy_revision, status, document, created_at, updated_at) VALUES
      ('018f1e00-2500-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', 'telemetry-access', 2, 'ACTIVE', '{}'::jsonb, now(), now()),
      ('018f1e00-2500-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', 'alarm-access', 1, 'ACTIVE', '{}'::jsonb, now(), now()),
      ('018f1e00-2500-7000-8000-000000000004', '018f1d00-0000-7000-8000-000000000001', 'work-order-access', 1, 'ACTIVE', '{}'::jsonb, now(), now()),
      ('018f1e00-2500-7000-8000-000000000005', '018f1d00-0000-7000-8000-000000000002', 'registry-read', 1, 'ACTIVE', '{}'::jsonb, now(), now());

    UPDATE iam.role_templates
    SET capabilities = ARRAY['registry.read','telemetry.snapshot.read'], revision = revision + 1, updated_at = now()
    WHERE tenant_id = '018f1d00-0000-7000-8000-000000000001' AND role_key = 'registry-reader';
    UPDATE iam.site_bindings
    SET actions = ARRAY['registry.read','telemetry.snapshot.read'], revision = revision + 1, updated_at = now()
    WHERE id = '018f1e00-2300-7000-8000-000000000001';

    INSERT INTO iam.telemetry_scope_bindings (
      id, tenant_id, principal_id, site_id, device_id, actions, effect, status,
      valid_from, valid_to, revision, created_at, updated_at
    ) VALUES (
      '018f1e00-2600-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000002',
      '018f1e00-1000-7000-8000-000000000001', '018f1e00-4000-7000-8000-000000000001', ARRAY['telemetry.snapshot.read'],
      'ALLOW', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, now(), now()
    );
    INSERT INTO iam.telemetry_key_bindings (
      id, tenant_id, principal_id, device_id, telemetry_key, actions, effect, status,
      valid_from, valid_to, revision, created_at, updated_at
    ) VALUES
      ('018f1e00-2700-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000002', '018f1e00-4000-7000-8000-000000000001', 'zone.temperature', ARRAY['telemetry.snapshot.read'], 'ALLOW', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, now(), now()),
      ('018f1e00-2700-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000002', '018f1e00-4000-7000-8000-000000000001', 'fan.speed', ARRAY['telemetry.snapshot.read'], 'ALLOW', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, now(), now());

    INSERT INTO iam.alarm_permissions (
      id, principal_id, tenant_id, site_id, action, effect, status, valid_from, valid_to, revision, created_at, updated_at
    ) VALUES
      ('018f1e00-2800-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'alarm:read', 'ALLOW', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, now(), now()),
      ('018f1e00-2800-7000-8000-000000000002', '018f1e00-2000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'alarm:ack', 'ALLOW', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, now(), now());

    INSERT INTO iam.work_order_permissions (
      id, principal_id, tenant_id, site_id, action, effect, status, valid_from, valid_to, revision, created_at, updated_at
    ) VALUES
      ('018f1e00-2900-7000-8000-000000000001','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:list','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000002','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:read','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000003','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:create','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000004','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:assign','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000005','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:plan','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000006','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:start','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000007','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:block','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000008','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:resume','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000009','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:complete','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000010','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:cancel','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2900-7000-8000-000000000011','018f1e00-2000-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','work-order:reopen','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now());
    INSERT INTO iam.work_order_ownership_targets (
      id, tenant_id, site_id, target_type, target_id, effect, status, valid_from, valid_to, revision, created_at, updated_at
    ) VALUES
      ('018f1e00-2a00-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','PRINCIPAL','018f1e00-2000-7000-8000-000000000001','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now()),
      ('018f1e00-2a00-7000-8000-000000000002','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','TEAM','operations-a','ALLOW','ACTIVE','2026-07-21T00:00:00Z',NULL,1,now(),now());
  `);
}

function scopedCounts(tenantID, siteIDs) {
  return psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '${tenantID}';
    SET LOCAL app.authorized_site_ids = '${siteIDs}';
    SELECT (SELECT count(*) FROM core_registry.sites)::text || '|'
      || (SELECT count(*) FROM core_registry.assets)::text || '|'
      || (SELECT count(*) FROM core_registry.devices)::text;
    ROLLBACK;
  `).split('|').map(Number);
}

async function runIAMGoTests() {
  await mkdir(goCacheDir, { recursive: true });
  psql(`ALTER ROLE s2_iam_grant_runtime PASSWORD '${telemetryGrantPassword}'`);
  const child = spawn(goBinary, ['test', '-count=1', '-v', './services/iam-service/internal/iam'], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      GOCACHE: goCacheDir,
      S1_ADMIN_DATABASE_URL: `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
      S1_IAM_DATABASE_URL: `postgres://s1_iam_runtime:s1-iam-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
      S1_IAM_RECONCILER_DATABASE_URL: `postgres://s1_iam_reconciler:s1-iam-reconciler-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
      S2_IAM_GRANT_DATABASE_URL: `postgres://s2_iam_grant_runtime:${telemetryGrantPassword}@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
    },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`S1 IAM PostgreSQL tests failed: ${signal ?? code}`);
}

async function runCoreGoTests() {
  await mkdir(goCacheDir, { recursive: true });
  psql(`ALTER ROLE s1_core_service PASSWORD '${coreServicePassword}'`);
  const child = spawn(goBinary, ['test', '-count=1', '-v', './services/platform-core-service/internal/core'], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      GOCACHE: goCacheDir,
      S1_CORE_DATABASE_URL: `postgres://s1_core_service:${coreServicePassword}@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
    },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`S1 Core PostgreSQL tests failed: ${signal ?? code}`);
}

async function runLegacyMigrationGoTests() {
  await mkdir(goCacheDir, { recursive: true });
  const child = spawn(goBinary, ['test', '-count=1', '-v', './services/legacy-migration-service/internal/migration'], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      GOCACHE: goCacheDir,
      S1_ADMIN_DATABASE_URL: `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
      S1_CORE_DATABASE_URL: `postgres://s1_core_service:${coreServicePassword}@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
      S1_LEGACY_MIGRATION_DSN: `postgres://s1_legacy_migration_service:s1-legacy-migration-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`,
    },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`S1 Legacy migration PostgreSQL tests failed: ${signal ?? code}`);
}

async function runGatewayRoutingGoTests() {
  await mkdir(goCacheDir, { recursive: true });
  const child = spawn(goBinary, ['test', '-count=1', '-v', './services/platform-gateway/internal/gateway', '-run', 'TestGatewayRegistry'], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: { ...process.env, GOCACHE: goCacheDir },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`S1 Gateway Registry routing tests failed: ${signal ?? code}`);
}

const report = {
  schemaVersion: 1,
  status: 'failed',
  startedAt: new Date().toISOString(),
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres']);
  await waitForPostgres();
  seedIAMIntegrationFixtures();

  const roleState = psql(`
    SELECT string_agg(rolname || ':' || rolcanlogin::text || ':' || rolbypassrls::text, ',' ORDER BY rolname)
    FROM pg_roles
    WHERE rolname IN ('s1_iam_runtime','s1_iam_reconciler','s1_core_runtime','s1_core_writer','s1_core_service','s1_iam_migrator','s1_core_migrator','s1_migration_operator','s1_legacy_migration_service')
  `);
  for (const role of ['s1_iam_runtime', 's1_iam_reconciler', 's1_iam_migrator', 's1_core_service', 's1_legacy_migration_service']) {
    if (!roleState.includes(`${role}:true:false`)) throw new Error(`${role} must be LOGIN and NOBYPASSRLS`);
  }
  for (const role of ['s1_core_runtime', 's1_core_writer', 's1_core_migrator', 's1_migration_operator']) {
    if (!roleState.includes(`${role}:false:false`)) throw new Error(`${role} must remain NOLOGIN and NOBYPASSRLS`);
  }
  const coreServiceMembership = psql(`SELECT pg_has_role('s1_core_service', 's1_core_runtime', 'MEMBER')`);
  const coreWriterMembership = psql(`SELECT pg_has_role('s1_core_service', 's1_core_writer', 'MEMBER')`);
  const migrationServiceMembership = psql(`SELECT pg_has_role('s1_legacy_migration_service', 's1_migration_operator', 'MEMBER')`);
  expectEqual(coreServiceMembership, 't', 'Core service runtime membership');
  expectEqual(coreWriterMembership, 't', 'Core service writer membership');
  expectEqual(migrationServiceMembership, 't', 'Legacy migration operator membership');
  report.assertions.runtimeRoles = roleState;
  report.assertions.coreServiceMembership = coreServiceMembership;
  report.assertions.coreWriterMembership = coreWriterMembership;
  report.assertions.migrationServiceMembership = migrationServiceMembership;

  const tenantA = scopedCounts('018f1d00-0000-7000-8000-000000000001', '{018f1e00-1000-7000-8000-000000000001,018f1e00-1000-7000-8000-000000000002}');
  const exactSite = scopedCounts('018f1d00-0000-7000-8000-000000000001', '{018f1e00-1000-7000-8000-000000000001}');
  const noAccess = scopedCounts('018f1d00-0000-7000-8000-000000000001', '{}');
  const wrongTenant = scopedCounts('018f1d00-0000-7000-8000-000000000002', '{018f1e00-1000-7000-8000-000000000001}');
  expectEqual(tenantA.join('|'), '2|2|2', 'Tenant A exact Site set');
  expectEqual(exactSite.join('|'), '1|1|1', 'exact Site scope');
  expectEqual(noAccess.join('|'), '0|0|0', 'empty Site scope');
  expectEqual(wrongTenant.join('|'), '0|0|0', 'cross-Tenant Site scope');
  report.assertions.rlsCounts = { tenantA, exactSite, noAccess, wrongTenant };

  const canonicalUnits = psql(`
    SELECT count(*) FILTER (WHERE unit_code IN ('m3','m3/h'))::text || '|'
      || count(*) FILTER (WHERE unit_code IN ('m³','m³/h'))::text
    FROM core_registry.unit_registry;
  `);
  expectEqual(canonicalUnits, '2|0', 'canonical ASCII Unit codes');
  const invalidPointCode = psql(`
    INSERT INTO core_registry.telemetry_points (
      id, tenant_id, site_id, reporting_device_id, point_code, source_key, display_name,
      point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms,
      stale_after_ms, source_metadata, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1090-7000-8000-000000000001',
      '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001',
      '018f1e00-4000-7000-8000-000000000001',
      'invalid.point.code', 'vendor.invalid.point.code', 'Invalid Point Code',
      'TELEMETRY', 'NUMBER', 'kW', false, 1000, 1000, 5000,
      '{}'::jsonb, 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!invalidPointCode.toLowerCase().includes('point_code')) throw new Error(`dotted Point Code was not rejected by canonical lower_snake_case constraint: ${invalidPointCode}`);
  report.assertions.pointUnitStandard = {
    canonicalUnits,
    dottedPointCodeRejected: true,
  };

  const validCounter = psql(`
    BEGIN;
    INSERT INTO core_registry.telemetry_points (
      id, tenant_id, site_id, reporting_device_id, sensor_id,
      point_code, source_key, display_name, point_type, value_type, unit, writable,
      sample_interval_ms, publish_interval_ms, stale_after_ms,
      counter_decrease_mode, counter_rollover_modulus,
      source_metadata, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1100-7000-8000-000000000001',
      '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001',
      '018f1e00-4000-7000-8000-000000000001', NULL,
      'energy_total', 'meter.energy_total', 'Energy Total', 'COUNTER', 'NUMBER', 'kWh', false,
      1000, 1000, 5000, 'RESET_TO_ZERO', NULL,
      '{}'::jsonb, 'ACTIVE', 1, now(), now()
    );
    SELECT point_type || '|' || counter_decrease_mode || '|' || coalesce(counter_rollover_modulus::text, 'NULL')
    FROM core_registry.telemetry_points
    WHERE id = '01990000-1100-7000-8000-000000000001';
    ROLLBACK;
  `);
  expectEqual(validCounter, 'COUNTER|RESET_TO_ZERO|NULL', 'valid Counter Point semantics');
  const missingCounterMode = psql(`
    BEGIN;
    INSERT INTO core_registry.telemetry_points (
      id, tenant_id, site_id, reporting_device_id, point_code, source_key, display_name,
      point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms,
      source_metadata, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1100-7000-8000-000000000002',
      '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001',
      '018f1e00-4000-7000-8000-000000000001',
      'energy_total_missing_mode', 'meter.energy_total_missing_mode', 'Energy Total Missing Mode',
      'COUNTER', 'NUMBER', 'kWh', false, 1000, 1000, 5000,
      '{}'::jsonb, 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!missingCounterMode.includes('telemetry_points_counter_semantics_check')) throw new Error(`missing Counter mode was not rejected by the Counter constraint: ${missingCounterMode}`);
  const rolloverWithoutModulus = psql(`
    BEGIN;
    INSERT INTO core_registry.telemetry_points (
      id, tenant_id, site_id, reporting_device_id, point_code, source_key, display_name,
      point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms,
      counter_decrease_mode, source_metadata, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1100-7000-8000-000000000003',
      '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001',
      '018f1e00-4000-7000-8000-000000000001',
      'water_total_rollover', 'meter.water_total_rollover', 'Water Total Rollover',
      'COUNTER', 'NUMBER', 'm3', false, 1000, 1000, 5000,
      'ROLLOVER', '{}'::jsonb, 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!rolloverWithoutModulus.includes('telemetry_points_counter_semantics_check')) throw new Error(`rollover without modulus was not rejected: ${rolloverWithoutModulus}`);
  const nonCounterWithCounterMode = psql(`
    BEGIN;
    INSERT INTO core_registry.telemetry_points (
      id, tenant_id, site_id, reporting_device_id, point_code, source_key, display_name,
      point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms,
      counter_decrease_mode, source_metadata, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1100-7000-8000-000000000004',
      '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001',
      '018f1e00-4000-7000-8000-000000000001',
      'active_power_with_counter_mode', 'meter.active_power_with_counter_mode', 'Active Power',
      'TELEMETRY', 'NUMBER', 'kW', false, 1000, 1000, 5000,
      'RESET_TO_ZERO', '{}'::jsonb, 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!nonCounterWithCounterMode.includes('telemetry_points_counter_semantics_check')) throw new Error(`non-Counter Point accepted Counter semantics: ${nonCounterWithCounterMode}`);
  report.assertions.counterSemantics = {
    valid: validCounter,
    missingModeRejected: true,
    rolloverWithoutModulusRejected: true,
    nonCounterModeRejected: true,
  };

  const topologyVersionId = '01990000-1300-7000-8000-000000000001';
  const topologyVersion2Id = '01990000-1300-7000-8000-000000000002';
  const topologyNodeA = '01990000-1310-7000-8000-000000000001';
  const topologyNodeB = '01990000-1310-7000-8000-000000000002';
  const topologyNodeV2 = '01990000-1310-7000-8000-000000000003';
  const topologyEdgeId = '01990000-1320-7000-8000-000000000001';
  const meterId = '01990000-1330-7000-8000-000000000001';
  const meterPointId = '01990000-1340-7000-8000-000000000001';
  const telemetryPointId = '01990000-1340-7000-8000-000000000002';
  const meterBindingId = '01990000-1360-7000-8000-000000000001';
  const virtualMeterA = '01990000-1370-7000-8000-000000000001';
  const virtualMeterB = '01990000-1370-7000-8000-000000000002';
  const electricityEnergyTypeId = '01990000-0000-7000-8000-000000000001';
  const topologyFixture = psql(`
    INSERT INTO core_registry.energy_topology_versions (
      id, tenant_id, site_id, version, status, effective_from, revision, created_at, updated_at
    ) VALUES
      ('${topologyVersionId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 1, 'VALIDATING', '2026-08-01T00:00:00Z', 1, now(), now()),
      ('${topologyVersion2Id}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 2, 'DRAFT', NULL, 1, now(), now());
    INSERT INTO core_registry.energy_nodes (
      id, tenant_id, site_id, topology_version_id, node_type, name, status, revision, created_at, updated_at
    ) VALUES
      ('${topologyNodeA}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', 'GRID', 'Grid', 'ACTIVE', 1, now(), now()),
      ('${topologyNodeB}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', 'LOAD', 'Site Load', 'ACTIVE', 1, now(), now()),
      ('${topologyNodeV2}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersion2Id}', 'GRID', 'Grid V2', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.energy_edges (
      id, tenant_id, site_id, topology_version_id, from_node_id, to_node_id,
      energy_type_id, direction, enabled, revision, created_at, updated_at
    ) VALUES (
      '${topologyEdgeId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}',
      '${topologyNodeA}', '${topologyNodeB}', '${electricityEnergyTypeId}', 'IMPORT', true, 1, now(), now()
    );
    INSERT INTO core_registry.energy_meters (
      id, tenant_id, site_id, meter_code, display_name, device_id, energy_type_id, status, revision, created_at, updated_at
    ) VALUES (
      '${meterId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'main_incomer', 'Main Incomer', '018f1e00-4000-7000-8000-000000000001', '${electricityEnergyTypeId}', 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.telemetry_points (
      id, tenant_id, site_id, reporting_device_id, point_code, source_key, display_name,
      point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms,
      counter_decrease_mode, source_metadata, status, revision, created_at, updated_at
    ) VALUES
      ('${meterPointId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-4000-7000-8000-000000000001',
       'grid_import_energy_total', 'meter.grid_import_energy_total', 'Grid Import Energy Total', 'COUNTER', 'NUMBER', 'kWh', false, 1000, 1000, 5000,
       'RESET_TO_ZERO', '{}'::jsonb, 'ACTIVE', 1, now(), now()),
      ('${telemetryPointId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-4000-7000-8000-000000000001',
       'active_power', 'meter.active_power', 'Active Power', 'TELEMETRY', 'NUMBER', 'kW', false, 1000, 1000, 5000,
       NULL, '{}'::jsonb, 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.meter_ratio_versions (
      id, tenant_id, site_id, meter_id, version, ct_ratio, pt_ratio, meter_multiplier,
      ratio_application_mode, effective_from, effective_to, status, revision, created_at, updated_at
    ) VALUES
      ('01990000-1350-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${meterId}', 1,
       200, 2, 1.5, 'DEVICE_APPLIED', '2026-01-01T00:00:00Z', '2026-07-01T00:00:00Z', 'DRAFT', 1, now(), now()),
      ('01990000-1350-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${meterId}', 2,
       2, 3, 5, 'PLATFORM_APPLIED', '2026-07-01T00:00:00Z', NULL, 'DRAFT', 1, now(), now());
    INSERT INTO core_registry.meter_bindings (
      id, tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id,
      meter_id, device_id, point_id, point_type, meter_role, direction, priority,
      effective_from, version, status, revision, created_at, updated_at
    ) VALUES (
      '${meterBindingId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', '${topologyEdgeId}', '${electricityEnergyTypeId}',
      '${meterId}', '018f1e00-4000-7000-8000-000000000001', '${meterPointId}', 'COUNTER', 'PRIMARY', 'IMPORT', 0,
      '2026-08-01T00:00:00Z', 1, 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.virtual_meters (
      id, tenant_id, site_id, topology_version_id, virtual_meter_code, display_name,
      energy_type_id, direction, calculation_type, version, effective_from, status, revision, created_at, updated_at
    ) VALUES
      ('${virtualMeterA}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', 'site_import_virtual', 'Site Import Virtual', '${electricityEnergyTypeId}', 'IMPORT', 'SUM', 1, '2026-08-01T00:00:00Z', 'DRAFT', 1, now(), now()),
      ('${virtualMeterB}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', 'tenant_import_virtual', 'Tenant Import Virtual', '${electricityEnergyTypeId}', 'IMPORT', 'SUM', 1, '2026-08-01T00:00:00Z', 'DRAFT', 1, now(), now());
    INSERT INTO core_registry.virtual_meter_sources (
      id, tenant_id, site_id, topology_version_id, energy_type_id, virtual_meter_id,
      source_type, source_meter_binding_id, coefficient, ordinal, revision, created_at, updated_at
    ) VALUES (
      '01990000-1380-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', '${electricityEnergyTypeId}', '${virtualMeterA}',
      'METER_BINDING', '${meterBindingId}', 1, 0, 1, now(), now()
    );
    INSERT INTO core_registry.virtual_meter_sources (
      id, tenant_id, site_id, topology_version_id, energy_type_id, virtual_meter_id,
      source_type, source_virtual_meter_id, coefficient, ordinal, revision, created_at, updated_at
    ) VALUES (
      '01990000-1380-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', '${electricityEnergyTypeId}', '${virtualMeterB}',
      'VIRTUAL_METER', '${virtualMeterA}', 1, 0, 1, now(), now()
    );
    SELECT
      (SELECT count(*) FROM core_registry.energy_nodes WHERE topology_version_id = '${topologyVersionId}')::text || '|'
      || (SELECT count(*) FROM core_registry.energy_edges WHERE topology_version_id = '${topologyVersionId}')::text || '|'
      || (SELECT cloud_multiplier::text FROM core_registry.meter_ratio_versions WHERE id = '01990000-1350-7000-8000-000000000001') || '|'
      || (SELECT cloud_multiplier::text FROM core_registry.meter_ratio_versions WHERE id = '01990000-1350-7000-8000-000000000002') || '|'
      || (SELECT count(*) FROM core_registry.virtual_meter_sources)::text;
  `);
  expectEqual(topologyFixture, '2|1|1.000000000000|30.000000000000|2', 'Topology/Metering V2 fixture');

  const invalidGridDirection = psql(`
    INSERT INTO core_registry.energy_edges (
      id, tenant_id, site_id, topology_version_id, from_node_id, to_node_id,
      energy_type_id, direction, enabled, revision, created_at, updated_at
    ) VALUES (
      '01990000-1320-7000-8000-000000000010', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}',
      '${topologyNodeA}', '${topologyNodeB}', '${electricityEnergyTypeId}', 'CONSUME', true, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!invalidGridDirection.includes('Grid outward accounting flow must use IMPORT direction')) throw new Error(`Grid outward flow accepted non-IMPORT direction: ${invalidGridDirection}`);
  psql(`
    INSERT INTO core_registry.energy_nodes (
      id, tenant_id, site_id, topology_version_id, node_type, name, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1310-7000-8000-000000000010', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}',
      'ESS', 'ESS Direction Fixture', 'ACTIVE', 1, now(), now()
    );
  `);
  const invalidEssDirection = psql(`
    INSERT INTO core_registry.energy_edges (
      id, tenant_id, site_id, topology_version_id, from_node_id, to_node_id,
      energy_type_id, direction, enabled, revision, created_at, updated_at
    ) VALUES (
      '01990000-1320-7000-8000-000000000011', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}',
      '01990000-1310-7000-8000-000000000010', '${topologyNodeB}', '${electricityEnergyTypeId}', 'IMPORT', true, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!invalidEssDirection.includes('ESS outward accounting flow must use DISCHARGE direction')) throw new Error(`ESS outward flow accepted non-DISCHARGE direction: ${invalidEssDirection}`);
  report.assertions.energyDirectionStandard = {
    gridImportSignRuleEnforced: true,
    essDischargeSignRuleEnforced: true,
  };

  const crossVersionEdge = psql(`
    INSERT INTO core_registry.energy_edges (
      id, tenant_id, site_id, topology_version_id, from_node_id, to_node_id,
      energy_type_id, direction, enabled, revision, created_at, updated_at
    ) VALUES (
      '01990000-1320-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersion2Id}',
      '${topologyNodeV2}', '${topologyNodeB}', '${electricityEnergyTypeId}', 'IMPORT', true, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!crossVersionEdge.includes('energy_edges_topology_to_node_fk')) throw new Error(`cross-version topology edge was not rejected: ${crossVersionEdge}`);

  const nonCounterMeterBinding = psql(`
    INSERT INTO core_registry.meter_bindings (
      id, tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id,
      meter_id, device_id, point_id, point_type, meter_role, direction, priority,
      effective_from, version, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1360-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', '${topologyEdgeId}', '${electricityEnergyTypeId}',
      '${meterId}', '018f1e00-4000-7000-8000-000000000001', '${telemetryPointId}', 'COUNTER', 'CHECK', 'IMPORT', 1,
      '2026-08-01T00:00:00Z', 2, 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!nonCounterMeterBinding.includes('meter_bindings_tenant_id_site_id_device_id_point_id_point__fkey')) throw new Error(`Meter Binding accepted a non-Counter Point: ${nonCounterMeterBinding}`);

  const duplicatePrimary = psql(`
    INSERT INTO core_registry.meter_bindings (
      id, tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id,
      meter_id, device_id, point_id, point_type, meter_role, direction, priority,
      effective_from, version, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1360-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', '${topologyEdgeId}', '${electricityEnergyTypeId}',
      '${meterId}', '018f1e00-4000-7000-8000-000000000001', '${meterPointId}', 'COUNTER', 'PRIMARY', 'IMPORT', 1,
      '2026-08-15T00:00:00Z', 2, 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!duplicatePrimary.includes('overlapping PRIMARY Meter Binding')) throw new Error(`overlapping PRIMARY Meter Binding was accepted: ${duplicatePrimary}`);

  const virtualMeterCycle = psql(`
    INSERT INTO core_registry.virtual_meter_sources (
      id, tenant_id, site_id, topology_version_id, energy_type_id, virtual_meter_id,
      source_type, source_virtual_meter_id, coefficient, ordinal, revision, created_at, updated_at
    ) VALUES (
      '01990000-1380-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}', '${electricityEnergyTypeId}', '${virtualMeterA}',
      'VIRTUAL_METER', '${virtualMeterB}', 1, 1, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!virtualMeterCycle.includes('Virtual Meter dependency cycle')) throw new Error(`Virtual Meter cycle was accepted: ${virtualMeterCycle}`);

  const topologyActivation = psql(`
    UPDATE core_registry.energy_topology_versions
    SET status = 'ACTIVE', released_at = now(), effective_from = '2026-08-01T00:00:00Z', revision = revision + 1, updated_at = now() + interval '1 second'
    WHERE id = '${topologyVersionId}';
    SELECT status FROM core_registry.energy_topology_versions WHERE id = '${topologyVersionId}';
  `);
  expectEqual(topologyActivation, 'ACTIVE', 'Topology activation');
  const duplicateActiveTopology = psql(`
    UPDATE core_registry.energy_topology_versions
    SET status = 'ACTIVE', released_at = now(), effective_from = '2026-09-01T00:00:00Z', revision = revision + 1, updated_at = now() + interval '1 second'
    WHERE id = '${topologyVersion2Id}';
  `, { expectFailure: true });
  if (!duplicateActiveTopology.includes('energy_topology_versions_one_active_site_uidx')) throw new Error(`second ACTIVE Topology Version was accepted: ${duplicateActiveTopology}`);
  const frozenTopologyMutation = psql(`
    UPDATE core_registry.energy_nodes SET name = 'Mutated Grid', revision = revision + 1, updated_at = now()
    WHERE id = '${topologyNodeA}';
  `, { expectFailure: true });
  if (!frozenTopologyMutation.includes('released Energy Topology graph is immutable')) throw new Error(`ACTIVE Topology graph mutation was accepted: ${frozenTopologyMutation}`);

  const topologyRls = psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SET LOCAL app.authorized_site_ids = '{018f1e00-1000-7000-8000-000000000001}';
    SELECT
      (SELECT count(*) FROM core_registry.energy_topology_versions)::text || '|'
      || (SELECT count(*) FROM core_registry.energy_meters)::text || '|'
      || (SELECT count(*) FROM core_registry.meter_bindings)::text || '|'
      || (SELECT count(*) FROM core_registry.virtual_meters)::text;
    ROLLBACK;
  `);
  expectEqual(topologyRls, '2|1|1|2', 'Topology/Metering runtime RLS');
  report.assertions.energyTopologyMeteringV2 = {
    fixture: topologyFixture,
    crossVersionEdgeRejected: true,
    nonCounterMeterBindingRejected: true,
    overlappingPrimaryRejected: true,
    virtualMeterCycleRejected: true,
    topologyActivation,
    secondActiveTopologyRejected: true,
    frozenGraphMutationRejected: true,
    runtimeRls: topologyRls,
  };

  const settlementBoundaryId = '01990000-1400-7000-8000-000000000001';
  const tariffId = '01990000-1410-7000-8000-000000000001';
  const tariffVersion1 = '01990000-1420-7000-8000-000000000001';
  const tariffVersion2 = '01990000-1420-7000-8000-000000000002';
  const settlementPeriodId = '01990000-1450-7000-8000-000000000001';
  const settlementSnapshot0 = '01990000-1460-7000-8000-000000000001';
  const settlementChangeCandidate = '01990000-1470-7000-8000-000000000001';
  const settlementRevision1 = '01990000-1480-7000-8000-000000000001';
  const settlementSnapshot1 = '01990000-1460-7000-8000-000000000002';

  const settlementFixture = psql(`
    INSERT INTO core_registry.settlement_boundaries (
      id, tenant_id, site_id, topology_version_id, boundary_code, display_name,
      boundary_type, energy_type_id, direction, definition_mode, node_id,
      effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '${settlementBoundaryId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}',
      'site_grid_import', 'Site Grid Import', 'GRID_CONNECTION', '${electricityEnergyTypeId}', 'IMPORT', 'EDGE_SET', NULL,
      '2026-08-01T00:00:00Z', 'DRAFT', 1, now(), now()
    );
    INSERT INTO core_registry.settlement_boundary_edges (
      id, tenant_id, site_id, boundary_id, topology_version_id, energy_type_id,
      direction, energy_edge_id, ordinal, revision, created_at, updated_at
    ) VALUES (
      '01990000-1401-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${settlementBoundaryId}', '${topologyVersionId}', '${electricityEnergyTypeId}',
      'IMPORT', '${topologyEdgeId}', 0, 1, now(), now()
    );
    UPDATE core_registry.settlement_boundaries
    SET status = 'ACTIVE', revision = revision + 1, updated_at = now()
    WHERE id = '${settlementBoundaryId}';

    INSERT INTO core_registry.tariffs (
      id, tenant_id, site_id, tariff_code, display_name, energy_type_id, status, revision, created_at, updated_at
    ) VALUES (
      '${tariffId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'site_electricity', 'Site Electricity', '${electricityEnergyTypeId}', 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.tariff_versions (
      id, tenant_id, site_id, tariff_id, version, effective_from, effective_to,
      timezone, currency, billing_cycle, custom_cycle_spec, status, revision, created_at, updated_at
    ) VALUES
      ('${tariffVersion1}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffId}', 1,
       '2026-07-31T16:00:00Z', '2026-08-15T16:00:00Z', 'Asia/Shanghai', 'CNY', 'CALENDAR_MONTH', NULL, 'DRAFT', 1, now(), now()),
      ('${tariffVersion2}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffId}', 2,
       '2026-08-15T16:00:00Z', NULL, 'Asia/Shanghai', 'CNY', 'CALENDAR_MONTH', NULL, 'DRAFT', 1, now(), now());
    INSERT INTO core_registry.tariff_periods (
      id, tenant_id, site_id, tariff_version_id, period_code, day_type,
      local_start_minute, local_end_minute, pricing_rule, ordinal, revision, created_at, updated_at
    ) VALUES
      ('01990000-1430-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffVersion1}', 'FLAT', 'WEEKDAY', 0, 1080, '{"rate":0.70}'::jsonb, 0, 1, now(), now()),
      ('01990000-1430-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffVersion1}', 'PEAK', 'WEEKDAY', 1080, 1440, '{"rate":1.20}'::jsonb, 1, 1, now(), now()),
      ('01990000-1430-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffVersion2}', 'PEAK', 'WEEKDAY', 0, 1440, '{"rate":1.30}'::jsonb, 0, 1, now(), now());
    UPDATE core_registry.tariff_versions
    SET status = 'RELEASED', revision = revision + 1, updated_at = now()
    WHERE id IN ('${tariffVersion1}', '${tariffVersion2}');
    INSERT INTO core_registry.tariff_assignments (
      id, tenant_id, site_id, boundary_id, tariff_id, effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1440-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${settlementBoundaryId}', '${tariffId}', '2026-07-31T16:00:00Z', 'RELEASED', 1, now(), now()
    );
    INSERT INTO core_registry.settlement_periods (
      id, tenant_id, site_id, boundary_id, period_start, period_end, timezone,
      grace_period_seconds, status, locked_at, revision, created_at, updated_at
    ) VALUES (
      '${settlementPeriodId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${settlementBoundaryId}',
      '2026-07-31T16:00:00Z', '2026-08-31T16:00:00Z', 'Asia/Shanghai', 7200, 'CALCULATING', NULL, 1, now(), now()
    );
    SELECT
      (SELECT status FROM core_registry.settlement_boundaries WHERE id = '${settlementBoundaryId}') || '|'
      || (SELECT count(*) FROM core_registry.tariff_versions WHERE tariff_id = '${tariffId}' AND status = 'RELEASED')::text || '|'
      || (SELECT timezone FROM core_registry.settlement_periods WHERE id = '${settlementPeriodId}');
  `);
  expectEqual(settlementFixture, 'ACTIVE|2|Asia/Shanghai', 'Settlement/Tariff V2 fixture');

  const wrongTariffTimezone = psql(`
    INSERT INTO core_registry.tariff_versions (
      id, tenant_id, site_id, tariff_id, version, effective_from, timezone, currency,
      billing_cycle, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1420-7000-8000-000000000004', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffId}', 4,
      '2026-10-01T00:00:00Z', 'UTC', 'CNY', 'CALENDAR_MONTH', 'DRAFT', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!wrongTariffTimezone.includes('Tariff Version timezone must snapshot the Site timezone')) throw new Error(`Tariff Version accepted wrong Site timezone: ${wrongTariffTimezone}`);

  const overlappingTariffVersion = psql(`
    INSERT INTO core_registry.tariff_versions (
      id, tenant_id, site_id, tariff_id, version, effective_from, effective_to, timezone,
      currency, billing_cycle, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1420-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffId}', 3,
      '2026-08-10T00:00:00Z', '2026-08-20T00:00:00Z', 'Asia/Shanghai', 'CNY', 'CALENDAR_MONTH', 'RELEASED', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!overlappingTariffVersion.includes('released Tariff Versions cannot overlap')) throw new Error(`overlapping released Tariff Version was accepted: ${overlappingTariffVersion}`);

  const overlappingTariffPeriod = psql(`
    INSERT INTO core_registry.tariff_periods (
      id, tenant_id, site_id, tariff_version_id, period_code, day_type,
      local_start_minute, local_end_minute, pricing_rule, ordinal, revision, created_at, updated_at
    ) VALUES (
      '01990000-1430-7000-8000-000000000004', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${tariffVersion1}',
      'PEAK', 'WEEKDAY', 1000, 1100, '{"rate":1.10}'::jsonb, 2, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!overlappingTariffPeriod.includes('Tariff Period time slices cannot overlap')) throw new Error(`overlapping Tariff Period was accepted: ${overlappingTariffPeriod}`);

  const emptyBoundaryRelease = psql(`
    INSERT INTO core_registry.settlement_boundaries (
      id, tenant_id, site_id, topology_version_id, boundary_code, display_name,
      boundary_type, energy_type_id, direction, definition_mode, effective_from,
      status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1400-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${topologyVersionId}',
      'empty_edge_boundary', 'Empty Edge Boundary', 'CUSTOM', '${electricityEnergyTypeId}', 'IMPORT', 'EDGE_SET', '2026-08-01T00:00:00Z',
      'DRAFT', 1, now(), now()
    );
    UPDATE core_registry.settlement_boundaries SET status = 'RELEASED', revision = 2, updated_at = now()
    WHERE id = '01990000-1400-7000-8000-000000000002';
  `, { expectFailure: true });
  if (!emptyBoundaryRelease.includes('must contain at least one Energy Edge')) throw new Error(`empty EDGE_SET Settlement Boundary was released: ${emptyBoundaryRelease}`);

  const lockWithoutSnapshot = psql(`
    UPDATE core_registry.settlement_periods
    SET status = 'LOCKED', locked_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${settlementPeriodId}';
  `, { expectFailure: true });
  if (!lockWithoutSnapshot.includes('requires exactly one initial Snapshot before LOCKED')) throw new Error(`Settlement Period locked without Snapshot: ${lockWithoutSnapshot}`);

  psql(`
    INSERT INTO core_registry.settlement_snapshots (
      id, tenant_id, site_id, settlement_period_id, boundary_id, revision_no,
      previous_snapshot_id, settlement_revision_id, meter_binding_refs, metric_version_refs,
      tariff_version_refs, source_reading_refs, energy_breakdown, demand, cost,
      quality, completeness, dataset_revision, created_at
    ) VALUES (
      '${settlementSnapshot0}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${settlementPeriodId}', '${settlementBoundaryId}', 0,
      NULL, NULL, '["${meterBindingId}"]'::jsonb, '[]'::jsonb,
      '["${tariffVersion1}","${tariffVersion2}"]'::jsonb, '["clickhouse:telemetry_history:2026-08"]'::jsonb,
      '{"energy_kwh":1000}'::jsonb, '{"billing_demand_kw":120}'::jsonb, '{"currency":"CNY","amount":880}'::jsonb,
      'GOOD', 1.0, 1, '2026-09-01T02:00:00Z'
    );
    UPDATE core_registry.settlement_periods
    SET status = 'LOCKED', locked_at = '2026-09-01T02:05:00Z', revision = revision + 1, updated_at = now()
    WHERE id = '${settlementPeriodId}';
  `);

  const lockedSnapshotMutation = psql(`
    UPDATE core_registry.settlement_snapshots
    SET cost = '{"currency":"CNY","amount":999}'::jsonb
    WHERE id = '${settlementSnapshot0}';
  `, { expectFailure: true });
  if (!lockedSnapshotMutation.includes('Settlement Snapshot is immutable')) throw new Error(`LOCKED Settlement Snapshot was mutable: ${lockedSnapshotMutation}`);

  const lockedPeriodReopen = psql(`
    UPDATE core_registry.settlement_periods
    SET status = 'CALCULATING', revision = revision + 1, updated_at = now()
    WHERE id = '${settlementPeriodId}';
  `, { expectFailure: true });
  if (!lockedPeriodReopen.includes('cannot be reopened')) throw new Error(`LOCKED Settlement Period was reopened: ${lockedPeriodReopen}`);

  const settlementRevisionApplied = psql(`
    INSERT INTO core_registry.settlement_change_candidates (
      id, tenant_id, site_id, settlement_period_id, reason_code, impact_summary, evidence,
      status, detected_at, resolved_at, revision, created_at, updated_at
    ) VALUES (
      '${settlementChangeCandidate}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${settlementPeriodId}',
      'LATE_DATA', '{"energy_kwh":12,"cost_cny":10}'::jsonb, '{"source":"late_telemetry"}'::jsonb,
      'APPROVED', '2026-09-01T03:00:00Z', NULL, 1, now(), now()
    );
    INSERT INTO core_registry.settlement_revisions (
      id, tenant_id, site_id, settlement_period_id, revision_no, change_candidate_id,
      base_snapshot_id, revised_snapshot_id, reason, status, approved_at, applied_at,
      revision, created_at, updated_at
    ) VALUES (
      '${settlementRevision1}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${settlementPeriodId}', 1, '${settlementChangeCandidate}',
      '${settlementSnapshot0}', NULL, 'Approved late telemetry adjustment', 'DRAFT', NULL, NULL,
      1, '2026-09-01T03:10:00Z', '2026-09-01T03:10:00Z'
    );
    UPDATE core_registry.settlement_revisions
    SET status = 'APPROVED', approved_at = '2026-09-01T03:15:00Z', revision = revision + 1, updated_at = '2026-09-01T03:15:00Z'
    WHERE id = '${settlementRevision1}';
    INSERT INTO core_registry.settlement_snapshots (
      id, tenant_id, site_id, settlement_period_id, boundary_id, revision_no,
      previous_snapshot_id, settlement_revision_id, meter_binding_refs, metric_version_refs,
      tariff_version_refs, source_reading_refs, energy_breakdown, demand, cost,
      quality, completeness, dataset_revision, created_at
    ) VALUES (
      '${settlementSnapshot1}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${settlementPeriodId}', '${settlementBoundaryId}', 1,
      '${settlementSnapshot0}', '${settlementRevision1}', '["${meterBindingId}"]'::jsonb, '[]'::jsonb,
      '["${tariffVersion1}","${tariffVersion2}"]'::jsonb, '["clickhouse:telemetry_history:2026-08","late:2026-09-01T03:00Z"]'::jsonb,
      '{"energy_kwh":1012}'::jsonb, '{"billing_demand_kw":120}'::jsonb, '{"currency":"CNY","amount":890}'::jsonb,
      'GOOD', 1.0, 2, '2026-09-01T03:20:00Z'
    );
    UPDATE core_registry.settlement_periods
    SET status = 'REVISED', revision = revision + 1, updated_at = '2026-09-01T03:25:00Z'
    WHERE id = '${settlementPeriodId}';
    SELECT
      (SELECT status FROM core_registry.settlement_periods WHERE id = '${settlementPeriodId}') || '|'
      || (SELECT status FROM core_registry.settlement_revisions WHERE id = '${settlementRevision1}') || '|'
      || (SELECT count(*) FROM core_registry.settlement_snapshots WHERE settlement_period_id = '${settlementPeriodId}')::text || '|'
      || (SELECT cost->>'amount' FROM core_registry.settlement_snapshots WHERE id = '${settlementSnapshot0}') || '|'
      || (SELECT cost->>'amount' FROM core_registry.settlement_snapshots WHERE id = '${settlementSnapshot1}');
  `);
  expectEqual(settlementRevisionApplied, 'REVISED|APPLIED|2|880|890', 'Settlement revision append-only flow');

  const skippedSettlementRevision = psql(`
    INSERT INTO core_registry.settlement_revisions (
      id, tenant_id, site_id, settlement_period_id, revision_no, base_snapshot_id,
      reason, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1480-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${settlementPeriodId}', 3, '${settlementSnapshot1}',
      'Skipped revision should fail', 'DRAFT', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!skippedSettlementRevision.includes('next revision number')) throw new Error(`Settlement Revision numbering could skip: ${skippedSettlementRevision}`);

  const settlementRls = psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SET LOCAL app.authorized_site_ids = '{018f1e00-1000-7000-8000-000000000001}';
    SELECT
      (SELECT count(*) FROM core_registry.settlement_boundaries)::text || '|'
      || (SELECT count(*) FROM core_registry.tariffs)::text || '|'
      || (SELECT count(*) FROM core_registry.settlement_periods)::text || '|'
      || (SELECT count(*) FROM core_registry.settlement_snapshots)::text || '|'
      || (SELECT count(*) FROM core_registry.settlement_revisions)::text;
    ROLLBACK;
  `);
  expectEqual(settlementRls, '1|1|1|2|1', 'Settlement runtime RLS');
  report.assertions.settlementV2 = {
    fixture: settlementFixture,
    wrongTariffTimezoneRejected: true,
    overlappingTariffVersionRejected: true,
    overlappingTariffPeriodRejected: true,
    emptyBoundaryReleaseRejected: true,
    lockWithoutSnapshotRejected: true,
    lockedSnapshotMutationRejected: true,
    lockedPeriodReopenRejected: true,
    revisionFlow: settlementRevisionApplied,
    skippedRevisionRejected: true,
    runtimeRls: settlementRls,
  };

  const dailyEnergyMetricId = '01990000-1500-7000-8000-000000000001';
  const energyCostMetricId = '01990000-1500-7000-8000-000000000002';
  const dailyEnergyVersionId = '01990000-1510-7000-8000-000000000001';
  const energyCostVersionId = '01990000-1510-7000-8000-000000000002';
  const energyCostDraftVersionId = '01990000-1510-7000-8000-000000000003';
  const dailyEnergyBindingId = '01990000-1530-7000-8000-000000000001';
  const calculationRunId = '01990000-1540-7000-8000-000000000001';

  const metricFixture = psql(`
    INSERT INTO core_registry.metrics (
      id, tenant_id, metric_code, metric_name, category, description,
      status, revision, created_at, updated_at
    ) VALUES
      ('${dailyEnergyMetricId}', '018f1d00-0000-7000-8000-000000000001', 'daily_energy', 'Daily Energy', 'ENERGY', 'Site daily imported energy', 'ACTIVE', 1, now(), now()),
      ('${energyCostMetricId}', '018f1d00-0000-7000-8000-000000000001', 'energy_cost', 'Energy Cost', 'FINANCIAL', 'Site daily energy cost', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.metric_versions (
      id, tenant_id, metric_id, version, unit_code, data_type, subject_type,
      time_granularity, aggregation, calculation_method, formula, quality_policy,
      effective_from, effective_to, status, metadata, revision, created_at, updated_at
    ) VALUES
      ('${dailyEnergyVersionId}', '018f1d00-0000-7000-8000-000000000001', '${dailyEnergyMetricId}', 1, 'kWh', 'NUMBER', 'SITE',
       'DAY', 'SUM', 'COUNTER_DELTA', NULL, 'STRICT', '2026-08-01T00:00:00Z', NULL, 'DRAFT', '{}'::jsonb, 1, now(), now()),
      ('${energyCostVersionId}', '018f1d00-0000-7000-8000-000000000001', '${energyCostMetricId}', 1, 'kWh', 'NUMBER', 'SITE',
       'DAY', 'SUM', 'FORMULA', 'daily_energy', 'STRICT', '2026-08-01T00:00:00Z', '2027-01-01T00:00:00Z', 'DRAFT', '{}'::jsonb, 1, now(), now()),
      ('${energyCostDraftVersionId}', '018f1d00-0000-7000-8000-000000000001', '${energyCostMetricId}', 2, 'kWh', 'NUMBER', 'SITE',
       'DAY', 'SUM', 'FORMULA', 'daily_energy', 'STRICT', '2027-01-01T00:00:00Z', NULL, 'DRAFT', '{}'::jsonb, 1, now(), now());
    INSERT INTO core_registry.metric_dependencies (
      id, tenant_id, metric_version_id, dependency_type, dependency_code,
      dependency_metric_id, sort_order, required, metadata, revision, created_at, updated_at
    ) VALUES
      ('01990000-1520-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '${dailyEnergyVersionId}', 'POINT', 'grid_import_energy_total', NULL, 0, true, '{}'::jsonb, 1, now(), now()),
      ('01990000-1520-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '${energyCostVersionId}', 'METRIC', 'daily_energy', '${dailyEnergyMetricId}', 0, true, '{}'::jsonb, 1, now(), now());
    SELECT
      (SELECT count(*) FROM core_registry.metrics)::text || '|'
      || (SELECT count(*) FROM core_registry.metric_versions)::text || '|'
      || (SELECT count(*) FROM core_registry.metric_dependencies)::text;
  `);
  expectEqual(metricFixture, '2|3|2', 'Metric V2 definition/version/dependency fixture');

  const metricCycle = psql(`
    INSERT INTO core_registry.metric_dependencies (
      id, tenant_id, metric_version_id, dependency_type, dependency_code,
      dependency_metric_id, sort_order, required, metadata, revision, created_at, updated_at
    ) VALUES (
      '01990000-1520-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '${dailyEnergyVersionId}',
      'METRIC', 'energy_cost', '${energyCostMetricId}', 1, true, '{}'::jsonb, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!metricCycle.includes('Metric dependency cycle is not allowed')) throw new Error(`Metric DAG accepted a cycle: ${metricCycle}`);

  psql(`
    UPDATE core_registry.metric_versions
    SET status = 'RELEASED', revision = revision + 1, updated_at = now()
    WHERE id IN ('${dailyEnergyVersionId}', '${energyCostVersionId}');
  `);

  const releasedMetricMutation = psql(`
    UPDATE core_registry.metric_versions
    SET formula = 'mutated_formula', revision = revision + 1, updated_at = now()
    WHERE id = '${dailyEnergyVersionId}';
  `, { expectFailure: true });
  if (!releasedMetricMutation.includes('released Metric Version is immutable')) throw new Error(`released Metric Version was mutable: ${releasedMetricMutation}`);

  const releasedDependencyMutation = psql(`
    INSERT INTO core_registry.metric_dependencies (
      id, tenant_id, metric_version_id, dependency_type, dependency_code,
      sort_order, required, metadata, revision, created_at, updated_at
    ) VALUES (
      '01990000-1520-7000-8000-000000000004', '018f1d00-0000-7000-8000-000000000001', '${dailyEnergyVersionId}',
      'EXTERNAL', 'late_external_source', 1, false, '{}'::jsonb, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!releasedDependencyMutation.includes('dependencies of a released Metric Version are immutable')) throw new Error(`released Metric dependencies were mutable: ${releasedDependencyMutation}`);

  const overlappingMetricVersion = psql(`
    INSERT INTO core_registry.metric_versions (
      id, tenant_id, metric_id, version, unit_code, data_type, subject_type,
      time_granularity, aggregation, calculation_method, quality_policy,
      effective_from, effective_to, status, metadata, revision, created_at, updated_at
    ) VALUES (
      '01990000-1510-7000-8000-000000000004', '018f1d00-0000-7000-8000-000000000001', '${dailyEnergyMetricId}', 2, 'kWh', 'NUMBER', 'SITE',
      'DAY', 'SUM', 'COUNTER_DELTA', 'STRICT', '2026-09-01T00:00:00Z', '2026-10-01T00:00:00Z', 'RELEASED', '{}'::jsonb, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!overlappingMetricVersion.includes('released Metric Versions cannot overlap')) throw new Error(`overlapping released Metric Version was accepted: ${overlappingMetricVersion}`);

  const draftBindingRelease = psql(`
    INSERT INTO core_registry.metric_bindings (
      id, tenant_id, site_id, metric_version_id, metric_id, metric_version,
      binding_version, subject_type, subject_id, time_granularity, source_definition,
      effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1530-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${energyCostDraftVersionId}', '${energyCostMetricId}', 2, 1, 'SITE', '018f1e00-1000-7000-8000-000000000001', 'DAY',
      '{"source":"daily_energy"}'::jsonb, '2027-01-01T00:00:00Z', 'RELEASED', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!draftBindingRelease.includes('Metric Binding cannot release before its Metric Version')) throw new Error(`Metric Binding released against DRAFT Metric Version: ${draftBindingRelease}`);

  const tagGroupBinding = psql(`
    INSERT INTO core_registry.metric_bindings (
      id, tenant_id, site_id, metric_version_id, metric_id, metric_version,
      binding_version, subject_type, subject_id, time_granularity, source_definition,
      effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1530-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${dailyEnergyVersionId}', '${dailyEnergyMetricId}', 1, 1, 'TAG_GROUP', '018f1e00-1000-7000-8000-000000000001', 'DAY',
      '{"source":"tag_group"}'::jsonb, '2026-08-01T00:00:00Z', 'DRAFT', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!tagGroupBinding.includes('TAG_GROUP Metric Binding requires the Tag Group model')) throw new Error(`Metric Binding silently accepted unavailable TAG_GROUP model: ${tagGroupBinding}`);

  psql(`
    INSERT INTO core_registry.metric_bindings (
      id, tenant_id, site_id, metric_version_id, metric_id, metric_version,
      binding_version, subject_type, subject_id, time_granularity, source_definition,
      effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '${dailyEnergyBindingId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${dailyEnergyVersionId}', '${dailyEnergyMetricId}', 1, 1, 'SITE', '018f1e00-1000-7000-8000-000000000001', 'DAY',
      '{"pointCode":"grid_import_energy_total","method":"COUNTER_DELTA"}'::jsonb, '2026-08-01T00:00:00Z', 'RELEASED', 1, now(), now()
    );
    INSERT INTO core_registry.metric_calculation_runs (
      id, tenant_id, site_id, metric_binding_id, metric_version_id, subject_type,
      subject_id, binding_version, period_start, period_end, granularity, run_reason,
      input_refs, status, started_at, completed_at, revision, created_at, updated_at
    ) VALUES (
      '${calculationRunId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${dailyEnergyBindingId}', '${dailyEnergyVersionId}', 'SITE',
      '018f1e00-1000-7000-8000-000000000001', 1, '2026-08-10T16:00:00Z', '2026-08-11T16:00:00Z', 'DAY', 'SCHEDULED',
      '["clickhouse:counter_deltas:2026-08-11"]'::jsonb, 'PENDING', NULL, NULL, 1, now(), now()
    );
  `);

  const calculationRunSkip = psql(`
    UPDATE core_registry.metric_calculation_runs
    SET status = 'SUCCEEDED', started_at = now(), completed_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${calculationRunId}';
  `, { expectFailure: true });
  if (!calculationRunSkip.includes('Metric Calculation Run must start before publication')) throw new Error(`Metric Calculation Run skipped RUNNING: ${calculationRunSkip}`);

  const calculationRunFlow = psql(`
    UPDATE core_registry.metric_calculation_runs
    SET status = 'RUNNING', started_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${calculationRunId}';
    UPDATE core_registry.metric_calculation_runs
    SET status = 'PERSISTING', revision = revision + 1, updated_at = now()
    WHERE id = '${calculationRunId}';
    UPDATE core_registry.metric_calculation_runs
    SET status = 'PERSISTED', completed_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${calculationRunId}';
    SELECT status || '|' || granularity || '|' || run_reason
    FROM core_registry.metric_calculation_runs WHERE id = '${calculationRunId}';
  `);
  expectEqual(calculationRunFlow, 'PERSISTED|DAY|SCHEDULED', 'Metric Calculation Run lifecycle');

  const metricRls = psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SET LOCAL app.authorized_site_ids = '{018f1e00-1000-7000-8000-000000000001}';
    SELECT
      (SELECT count(*) FROM core_registry.metrics)::text || '|'
      || (SELECT count(*) FROM core_registry.metric_versions)::text || '|'
      || (SELECT count(*) FROM core_registry.metric_dependencies)::text || '|'
      || (SELECT count(*) FROM core_registry.metric_bindings)::text || '|'
      || (SELECT count(*) FROM core_registry.metric_calculation_runs)::text;
    ROLLBACK;
  `);
  expectEqual(metricRls, '2|3|2|1|1', 'Metric runtime RLS');
  report.assertions.metricV2 = {
    fixture: metricFixture,
    dependencyCycleRejected: true,
    releasedVersionImmutable: true,
    releasedDependenciesImmutable: true,
    overlappingReleasedVersionRejected: true,
    draftVersionBindingReleaseRejected: true,
    unavailableTagGroupRejected: true,
    calculationRunSkipRejected: true,
    calculationRunFlow,
    runtimeRls: metricRls,
  };

  const lifecyclePolicyId = '01990000-1600-7000-8000-000000000001';
  const legalHoldId = '01990000-1610-7000-8000-000000000001';
  const deletionRequestId = '01990000-1620-7000-8000-000000000001';
  const deletionTombstoneId = '01990000-1630-7000-8000-000000000001';
  const restoreRunId = '01990000-1640-7000-8000-000000000001';
  const correctionRequestId = '01990000-1650-7000-8000-000000000001';
  const correctionFactId = '01990000-1660-7000-8000-000000000001';
  const archiveBucketId = '01990000-1670-7000-8000-000000000001';
  const backupBucketId = '01990000-1671-7000-8000-000000000001';
  const archiveManifestId = '01990000-1680-7000-8000-000000000001';
  const backupManifestId = '01990000-1690-7000-8000-000000000001';
  const deletedResourceKey = 'observation:01990000-2000-7000-8000-000000009999';

  const governanceFixture = psql(`
    INSERT INTO core_registry.data_lifecycle_policies (
      id, tenant_id, dataset_code, data_class, hot_retention_days, archive_after_days,
      delete_after_days, archive_required, effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '${lifecyclePolicyId}', '018f1d00-0000-7000-8000-000000000001', 'telemetry_history', 'CRITICAL',
      90, 90, 2555, true, '2026-08-01T00:00:00Z', 'RELEASED', 1, now(), now()
    );
    INSERT INTO core_registry.legal_holds (
      id, tenant_id, site_id, dataset_code, scope_type, resource_key, reason,
      effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '${legalHoldId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'telemetry_history', 'RESOURCE', '${deletedResourceKey}', 'billing dispute evidence hold',
      now() - interval '1 day', 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.deletion_requests (
      id, tenant_id, site_id, dataset_code, resource_key, reason_code, evidence,
      status, requested_at, approved_at, applied_at, revision, created_at, updated_at
    ) VALUES (
      '${deletionRequestId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'telemetry_history', '${deletedResourceKey}', 'RETENTION', '{"policy":"${lifecyclePolicyId}"}'::jsonb,
      'DRAFT', now(), NULL, NULL, 1, now(), now()
    );
    SELECT
      (SELECT count(*) FROM core_registry.data_lifecycle_policies)::text || '|'
      || (SELECT count(*) FROM core_registry.legal_holds)::text || '|'
      || (SELECT count(*) FROM core_registry.deletion_requests)::text;
  `);
  expectEqual(governanceFixture, '1|1|1', 'Governance lifecycle/hold/deletion fixture');

  const overlappingLifecyclePolicy = psql(`
    INSERT INTO core_registry.data_lifecycle_policies (
      id, tenant_id, dataset_code, data_class, hot_retention_days, archive_after_days,
      delete_after_days, archive_required, effective_from, effective_to, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1600-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', 'telemetry_history', 'CRITICAL',
      120, 120, 3000, true, '2026-09-01T00:00:00Z', '2026-10-01T00:00:00Z', 'RELEASED', 2, now(), now()
    );
  `, { expectFailure: true });
  if (!overlappingLifecyclePolicy.includes('released Data Lifecycle Policies cannot overlap')) throw new Error(`overlapping Lifecycle Policy was accepted: ${overlappingLifecyclePolicy}`);

  const heldDeletion = psql(`
    UPDATE core_registry.deletion_requests
    SET status = 'APPROVED', approved_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${deletionRequestId}';
  `, { expectFailure: true });
  if (!heldDeletion.includes('blocked by an active Legal Hold')) throw new Error(`active Legal Hold did not block deletion: ${heldDeletion}`);

  psql(`
    UPDATE core_registry.legal_holds
    SET status = 'LIFTED', effective_to = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${legalHoldId}';
  `);

  const archiveRequiredDeletionWithoutManifest = psql(`
    UPDATE core_registry.deletion_requests
    SET status = 'APPROVED', approved_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${deletionRequestId}';
  `, { expectFailure: true });
  if (!archiveRequiredDeletionWithoutManifest.includes('archive-required deletion needs a VERIFIED Archive Manifest')) {
    throw new Error(`archive-required deletion was approved without Archive Manifest: ${archiveRequiredDeletionWithoutManifest}`);
  }

  psql(`
    INSERT INTO core_registry.object_storage_buckets (
      id, tenant_id, bucket_code, bucket_name, provider, purpose, endpoint_reference,
      region, versioning_required, immutability_required, status, revision, created_at, updated_at
    ) VALUES
      ('${archiveBucketId}', '018f1d00-0000-7000-8000-000000000001', 'energy_archive', 'energy-archive', 'S3_COMPATIBLE', 'ARCHIVE',
       'object-storage://archive', 'local', true, false, 'ACTIVE', 1, now(), now()),
      ('${backupBucketId}', '018f1d00-0000-7000-8000-000000000001', 'energy_backup_postgres', 'energy-backup-postgres', 'S3_COMPATIBLE', 'BACKUP',
       'object-storage://backup', 'local', true, true, 'ACTIVE', 1, now(), now());
  `);

  const archiveInBackupBucket = psql(`
    INSERT INTO core_registry.archive_manifests (
      id, tenant_id, site_id, dataset_code, lifecycle_policy_id, bucket_id, object_key,
      source_system, source_snapshot_ref, format, scope_selector, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1680-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'telemetry_history', '${lifecyclePolicyId}', '${backupBucketId}', 'telemetry_history/2026-08/invalid.parquet',
      'CLICKHOUSE', 'clickhouse:telemetry_history:2026-08', 'PARQUET', '{}'::jsonb, 'STAGING', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!archiveInBackupBucket.includes('Archive Manifest requires an ACTIVE ARCHIVE bucket')) {
    throw new Error(`Backup bucket accepted Archive Manifest: ${archiveInBackupBucket}`);
  }

  const archiveManifestFlow = psql(`
    INSERT INTO core_registry.archive_manifests (
      id, tenant_id, site_id, dataset_code, lifecycle_policy_id, bucket_id, object_key,
      source_system, source_snapshot_ref, format, window_start, window_end, row_count,
      scope_selector, status, revision, created_at, updated_at
    ) VALUES (
      '${archiveManifestId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'telemetry_history', '${lifecyclePolicyId}', '${archiveBucketId}', 'telemetry_history/2026-08/site-a.parquet',
      'CLICKHOUSE', 'clickhouse:telemetry_history:2026-08:site-a', 'PARQUET',
      '2026-08-01T00:00:00Z', '2026-08-13T00:00:00Z', 1000, '{"site":"018f1e00-1000-7000-8000-000000000001"}'::jsonb,
      'STAGING', 1, now(), now()
    );
    UPDATE core_registry.archive_manifests
    SET status = 'SEALED', byte_count = 4096, content_sha256 = repeat('a', 64), archived_at = now(),
        revision = revision + 1, updated_at = now()
    WHERE id = '${archiveManifestId}';
    UPDATE core_registry.archive_manifests
    SET status = 'VERIFIED', verified_at = now(), verification_evidence = '{"checksum":"verified"}'::jsonb,
        revision = revision + 1, updated_at = now()
    WHERE id = '${archiveManifestId}';
    SELECT status || '|' || (SELECT purpose FROM core_registry.object_storage_buckets WHERE id = bucket_id)
    FROM core_registry.archive_manifests WHERE id = '${archiveManifestId}';
  `);
  expectEqual(archiveManifestFlow, 'VERIFIED|ARCHIVE', 'Archive Manifest lifecycle');

  const verifiedArchiveMutation = psql(`
    UPDATE core_registry.archive_manifests
    SET object_key = 'telemetry_history/mutated.parquet', revision = revision + 1, updated_at = now()
    WHERE id = '${archiveManifestId}';
  `, { expectFailure: true });
  if (!verifiedArchiveMutation.includes('sealed Archive Manifest content is immutable')) {
    throw new Error(`VERIFIED Archive Manifest was mutable: ${verifiedArchiveMutation}`);
  }

  const backupManifestFlow = psql(`
    INSERT INTO core_registry.backup_manifests (
      id, tenant_id, bucket_id, source_system, backup_type, object_key, source_snapshot_ref,
      recovery_point_at, status, started_at, revision, created_at, updated_at
    ) VALUES (
      '${backupManifestId}', '018f1d00-0000-7000-8000-000000000001', '${backupBucketId}', 'POSTGRESQL', 'FULL',
      'postgres/2026-07-31/full.tar', 'postgres:basebackup:2026-07-31', '2026-07-31T00:00:00Z',
      'STAGING', '2026-07-31T00:00:00Z', 1, now(), now()
    );
    UPDATE core_registry.backup_manifests
    SET status = 'VERIFIED', completed_at = '2026-07-31T00:05:00Z', verified_at = '2026-07-31T00:06:00Z',
        byte_count = 8192, content_sha256 = repeat('b', 64), verification_evidence = '{"restoreProbe":"passed"}'::jsonb,
        revision = revision + 1, updated_at = now()
    WHERE id = '${backupManifestId}';
    SELECT status || '|' || (SELECT purpose FROM core_registry.object_storage_buckets WHERE id = bucket_id)
    FROM core_registry.backup_manifests WHERE id = '${backupManifestId}';
  `);
  expectEqual(backupManifestFlow, 'VERIFIED|BACKUP', 'Backup Manifest lifecycle');

  const verifiedBackupMutation = psql(`
    UPDATE core_registry.backup_manifests
    SET object_key = 'postgres/mutated.tar', revision = revision + 1, updated_at = now()
    WHERE id = '${backupManifestId}';
  `, { expectFailure: true });
  if (!verifiedBackupMutation.includes('verified Backup Manifest content is immutable')) {
    throw new Error(`VERIFIED Backup Manifest was mutable: ${verifiedBackupMutation}`);
  }

  psql(`
    UPDATE core_registry.deletion_requests
    SET archive_manifest_id = '${archiveManifestId}', status = 'APPROVED', approved_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${deletionRequestId}';
    UPDATE core_registry.deletion_requests
    SET status = 'APPLIED', applied_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${deletionRequestId}';
    INSERT INTO core_registry.deletion_tombstones (
      id, tenant_id, site_id, deletion_request_id, dataset_code, resource_key,
      deleted_at, source_revision, metadata, created_at
    ) VALUES (
      '${deletionTombstoneId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${deletionRequestId}', 'telemetry_history', '${deletedResourceKey}', now(), 'clickhouse-revision-17',
      '{"reason":"retention","archiveManifestId":"${archiveManifestId}"}'::jsonb, now()
    );
  `);

  const tombstoneMutation = psql(`
    UPDATE core_registry.deletion_tombstones SET source_revision = 'mutated' WHERE id = '${deletionTombstoneId}';
  `, { expectFailure: true });
  if (!tombstoneMutation.includes('Deletion Tombstone is immutable')) throw new Error(`Deletion Tombstone was mutable: ${tombstoneMutation}`);

  const restoreWithArchiveInsteadOfBackup = psql(`
    INSERT INTO core_registry.restore_runs (
      id, tenant_id, backup_manifest_id, tombstone_cutoff_at, status,
      started_at, completed_at, evidence, revision, created_at, updated_at
    ) VALUES (
      '01990000-1640-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '${archiveManifestId}',
      now() + interval '1 minute', 'PENDING', NULL, NULL, '{"restore":"invalid-archive"}'::jsonb, 1, now(), now()
    );
  `, { expectFailure: true });
  if (!restoreWithArchiveInsteadOfBackup.includes('Restore Run requires a VERIFIED Backup Manifest; Archive is not Backup')) {
    throw new Error(`Restore accepted Archive as Backup: ${restoreWithArchiveInsteadOfBackup}`);
  }

  psql(`
    INSERT INTO core_registry.restore_runs (
      id, tenant_id, backup_manifest_id, tombstone_cutoff_at, status,
      started_at, completed_at, evidence, revision, created_at, updated_at
    ) VALUES (
      '${restoreRunId}', '018f1d00-0000-7000-8000-000000000001', '${backupManifestId}',
      now() + interval '1 minute', 'PENDING', NULL, NULL,
      '{"restore":"fixture","backupManifestId":"${backupManifestId}"}'::jsonb, 1, now(), now()
    );
    UPDATE core_registry.restore_runs
    SET status = 'APPLYING_TOMBSTONES', started_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${restoreRunId}';
    UPDATE core_registry.restore_runs
    SET status = 'VALIDATING', revision = revision + 1, updated_at = now()
    WHERE id = '${restoreRunId}';
  `);

  const restoreWithoutTombstones = psql(`
    UPDATE core_registry.restore_runs
    SET status = 'COMPLETED', completed_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${restoreRunId}';
  `, { expectFailure: true });
  if (!restoreWithoutTombstones.includes('cannot complete until all deletion Tombstones are re-applied')) throw new Error(`Restore completed without Tombstone replay: ${restoreWithoutTombstones}`);

  const restoreWithTombstone = psql(`
    INSERT INTO core_registry.restore_tombstone_applications (
      id, tenant_id, restore_run_id, tombstone_id, action, applied_at, evidence, created_at
    ) VALUES (
      '01990000-1641-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '${restoreRunId}', '${deletionTombstoneId}',
      'REDELETE', now(), '{"target":"restored_clickhouse"}'::jsonb, now()
    );
    UPDATE core_registry.restore_runs
    SET status = 'COMPLETED', completed_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${restoreRunId}';
    SELECT status || '|' || (SELECT count(*) FROM core_registry.restore_tombstone_applications WHERE restore_run_id = '${restoreRunId}')::text
    FROM core_registry.restore_runs WHERE id = '${restoreRunId}';
  `);
  expectEqual(restoreWithTombstone, 'COMPLETED|1', 'Restore Tombstone replay');

  psql(`
    INSERT INTO core_registry.correction_requests (
      id, tenant_id, site_id, target_type, device_id, point_id, effective_time,
      correction_type, original_value, corrected_value, delta_value, reason,
      evidence_refs, impact_preview, requested_by, approved_by, status,
      requested_at, approved_at, applied_at, revision, created_at, updated_at
    ) VALUES (
      '${correctionRequestId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'READING', '018f1e00-4000-7000-8000-000000000001', '${meterPointId}', '2026-08-15T10:00:00Z',
      'COUNTER_ADJUSTMENT', '1000'::jsonb, '1012'::jsonb, 12, 'approved meter reading correction',
      '["evidence://meter-photo/1"]'::jsonb, '{"energyDifference":12,"affectedMetrics":["daily_energy"]}'::jsonb,
      '018f1e00-2000-7000-8000-000000000001', NULL, 'DRAFT', now(), NULL, NULL, 1, now(), now()
    );
  `);

  const correctionSkipReview = psql(`
    UPDATE core_registry.correction_requests
    SET status = 'APPROVED', approved_by = '018f1e00-2000-7000-8000-000000000002', approved_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${correctionRequestId}';
  `, { expectFailure: true });
  if (!correctionSkipReview.includes('must enter REVIEW before approval')) throw new Error(`Correction Request skipped REVIEW: ${correctionSkipReview}`);

  psql(`
    UPDATE core_registry.correction_requests
    SET status = 'REVIEW', revision = revision + 1, updated_at = now()
    WHERE id = '${correctionRequestId}';
    UPDATE core_registry.correction_requests
    SET status = 'APPROVED', approved_by = '018f1e00-2000-7000-8000-000000000002', approved_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${correctionRequestId}';
  `);

  const correctionApplied = psql(`
    INSERT INTO core_registry.correction_facts (
      id, tenant_id, site_id, correction_request_id, target_type, device_id, point_id,
      effective_time, correction_type, original_value, corrected_value, delta_value,
      reason, evidence_refs, applied_at, created_at
    ) VALUES (
      '${correctionFactId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '${correctionRequestId}',
      'READING', '018f1e00-4000-7000-8000-000000000001', '${meterPointId}', '2026-08-15T10:00:00Z',
      'COUNTER_ADJUSTMENT', '1000'::jsonb, '1012'::jsonb, 12, 'approved meter reading correction',
      '["evidence://meter-photo/1"]'::jsonb, now(), now()
    );
    SELECT
      (SELECT status FROM core_registry.correction_requests WHERE id = '${correctionRequestId}') || '|'
      || (SELECT original_value::text FROM core_registry.correction_facts WHERE id = '${correctionFactId}') || '|'
      || (SELECT corrected_value::text FROM core_registry.correction_facts WHERE id = '${correctionFactId}');
  `);
  expectEqual(correctionApplied, 'APPLIED|1000|1012', 'Correction Fact append-only apply');

  const correctionFactMutation = psql(`
    UPDATE core_registry.correction_facts SET corrected_value = '9999'::jsonb WHERE id = '${correctionFactId}';
  `, { expectFailure: true });
  if (!correctionFactMutation.includes('Correction Fact is immutable')) throw new Error(`Correction Fact was mutable: ${correctionFactMutation}`);

  const governanceRls = psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SET LOCAL app.authorized_site_ids = '{018f1e00-1000-7000-8000-000000000001}';
    SELECT
      (SELECT count(*) FROM core_registry.data_lifecycle_policies)::text || '|'
      || (SELECT count(*) FROM core_registry.legal_holds)::text || '|'
      || (SELECT count(*) FROM core_registry.deletion_tombstones)::text || '|'
      || (SELECT count(*) FROM core_registry.restore_runs)::text || '|'
      || (SELECT count(*) FROM core_registry.correction_requests)::text || '|'
      || (SELECT count(*) FROM core_registry.correction_facts)::text || '|'
      || (SELECT count(*) FROM core_registry.object_storage_buckets)::text || '|'
      || (SELECT count(*) FROM core_registry.archive_manifests)::text || '|'
      || (SELECT count(*) FROM core_registry.backup_manifests)::text;
    ROLLBACK;
  `);
  expectEqual(governanceRls, '1|1|1|1|1|1|2|1|1', 'Governance runtime RLS');
  report.assertions.governanceV2 = {
    fixture: governanceFixture,
    overlappingLifecyclePolicyRejected: true,
    activeLegalHoldBlockedDeletion: true,
    archiveRequiredDeletionWithoutManifestRejected: true,
    archiveInBackupBucketRejected: true,
    archiveManifestFlow,
    verifiedArchiveImmutable: true,
    backupManifestFlow,
    verifiedBackupImmutable: true,
    tombstoneImmutable: true,
    restoreWithArchiveRejected: true,
    restoreWithoutTombstoneRejected: true,
    restoreWithTombstone,
    correctionSkipReviewRejected: true,
    correctionApplied,
    correctionFactImmutable: true,
    runtimeRls: governanceRls,
  };

  const forecastFeatureSetId = '01990000-1800-7000-8000-000000000001';
  const forecastFeatureSetVersionId = '01990000-1810-7000-8000-000000000001';
  const forecastDatasetId = '01990000-1820-7000-8000-000000000001';
  const forecastModelId = '01990000-1830-7000-8000-000000000001';
  const forecastTrainingRunId = '01990000-1840-7000-8000-000000000001';
  const forecastModelVersionId = '01990000-1850-7000-8000-000000000001';
  const forecastDeploymentId = '01990000-1860-7000-8000-000000000001';
  const forecastInputSnapshotId = '01990000-1870-7000-8000-000000000001';
  const forecastJobId = '01990000-1880-7000-8000-000000000001';
  const forecastSnapshotId = '01990000-1890-7000-8000-000000000001';

  const forecastFixture = psql(`
    INSERT INTO core_registry.forecast_feature_sets (
      id, tenant_id, feature_set_code, target, status, revision, created_at, updated_at
    ) VALUES (
      '${forecastFeatureSetId}', '018f1d00-0000-7000-8000-000000000001', 'site_load_default', 'SITE_LOAD', 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.forecast_feature_set_versions (
      id, tenant_id, feature_set_id, version, feature_schema, fallback_schema,
      status, revision, created_at, updated_at
    ) VALUES (
      '${forecastFeatureSetVersionId}', '018f1d00-0000-7000-8000-000000000001', '${forecastFeatureSetId}', 1,
      '{"features":["load_t_15m","load_t_24h","hour_of_day"]}'::jsonb,
      '{"baseline":"LAST_VALUE"}'::jsonb, 'RELEASED', 1, now(), now()
    );
    INSERT INTO core_registry.forecast_dataset_snapshots (
      id, tenant_id, site_id, target, subject_type, subject_id, train_from, train_to,
      feature_set_version_id, topology_version_id, metric_version_refs, weather_source,
      data_quality_summary, manifest_uri, manifest_checksum, created_at
    ) VALUES (
      '${forecastDatasetId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001', '2026-05-01T00:00:00Z', '2026-08-01T00:00:00Z',
      '${forecastFeatureSetVersionId}', '${topologyVersionId}', '["${dailyEnergyVersionId}"]'::jsonb, 'weather_station',
      '{"goodRatio":0.99,"estimatedRatio":0.01}'::jsonb, 's3://forecast-datasets/site-a/site-load/v1/manifest.json', repeat('f', 64), now()
    );
    INSERT INTO core_registry.forecast_models (
      id, tenant_id, model_code, target, subject_type, horizon_minutes, granularity,
      status, revision, created_at, updated_at
    ) VALUES (
      '${forecastModelId}', '018f1d00-0000-7000-8000-000000000001', 'site_load_day_ahead', 'SITE_LOAD', 'SITE', 1440, '15MIN',
      'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.forecast_training_runs (
      id, tenant_id, site_id, model_id, dataset_snapshot_id, feature_set_version_id,
      topology_version_id, algorithm, hyperparameters, code_version, evaluation,
      status, started_at, finished_at, revision, created_at, updated_at
    ) VALUES (
      '${forecastTrainingRunId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastModelId}', '${forecastDatasetId}', '${forecastFeatureSetVersionId}', '${topologyVersionId}',
      'BASELINE', '{"method":"LAST_VALUE"}'::jsonb, 'forecast-go-v1', NULL,
      'PENDING', NULL, NULL, 1, now(), now()
    );
    SELECT
      (SELECT count(*) FROM core_registry.forecast_feature_sets)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_dataset_snapshots)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_models)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_training_runs)::text;
  `);
  expectEqual(forecastFixture, '1|1|1|1', 'Forecast V2 traceability fixture');

  const wrongDatasetSubject = psql(`
    INSERT INTO core_registry.forecast_dataset_snapshots (
      id, tenant_id, site_id, target, subject_type, subject_id, train_from, train_to,
      feature_set_version_id, topology_version_id, metric_version_refs,
      data_quality_summary, manifest_uri, manifest_checksum, created_at
    ) VALUES (
      '01990000-1820-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000002', '2026-05-01T00:00:00Z', '2026-08-01T00:00:00Z',
      '${forecastFeatureSetVersionId}', '${topologyVersionId}', '[]'::jsonb,
      '{"goodRatio":1}'::jsonb, 's3://forecast-datasets/bad/manifest.json', repeat('e', 64), now()
    );
  `, { expectFailure: true });
  if (!wrongDatasetSubject.includes('SITE Forecast Dataset subject must equal site_id')) throw new Error(`Forecast Dataset accepted Site subject drift: ${wrongDatasetSubject}`);

  const featureVersionMutation = psql(`
    UPDATE core_registry.forecast_feature_set_versions
    SET feature_schema = '{"features":["mutated"]}'::jsonb, revision = revision + 1, updated_at = now()
    WHERE id = '${forecastFeatureSetVersionId}';
  `, { expectFailure: true });
  if (!featureVersionMutation.includes('released Forecast Feature Set Version is immutable')) throw new Error(`released Forecast Feature Set Version was mutable: ${featureVersionMutation}`);

  const modelVersionBeforeTraining = psql(`
    INSERT INTO core_registry.forecast_model_versions (
      id, tenant_id, site_id, model_id, model_version, training_run_id, dataset_snapshot_id,
      feature_set_version_id, topology_version_id, artifact_uri, artifact_checksum,
      evaluation, compatibility, status, revision, created_at, updated_at
    ) VALUES (
      '${forecastModelVersionId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastModelId}', 1, '${forecastTrainingRunId}', '${forecastDatasetId}', '${forecastFeatureSetVersionId}', '${topologyVersionId}',
      's3://forecast-models/site-load/v1/model.bin', repeat('b', 64), '{"wape":0.10}'::jsonb, '{"runtime":"go"}'::jsonb,
      'CANDIDATE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!modelVersionBeforeTraining.includes('requires a SUCCEEDED Training Run')) throw new Error(`Forecast Model Version was created before training succeeded: ${modelVersionBeforeTraining}`);

  const trainingSkipRunning = psql(`
    UPDATE core_registry.forecast_training_runs
    SET status = 'SUCCEEDED', started_at = now(), finished_at = now(), revision = revision + 1, updated_at = now() + interval '1 second'
    WHERE id = '${forecastTrainingRunId}';
  `, { expectFailure: true });
  if (!trainingSkipRunning.includes('Forecast Training Run must start before succeeding')) throw new Error(`Forecast Training Run skipped RUNNING: ${trainingSkipRunning}`);

  psql(`
    UPDATE core_registry.forecast_training_runs
    SET status = 'RUNNING', started_at = now(), revision = revision + 1, updated_at = now() + interval '1 second'
    WHERE id = '${forecastTrainingRunId}';
    UPDATE core_registry.forecast_training_runs
    SET status = 'SUCCEEDED', evaluation = '{"wape":0.10,"baseline":"LAST_VALUE"}'::jsonb,
        finished_at = now(), revision = revision + 1, updated_at = now() + interval '1 second'
    WHERE id = '${forecastTrainingRunId}';
  `);

  const directValidatedModelVersion = psql(`
    INSERT INTO core_registry.forecast_model_versions (
      id, tenant_id, site_id, model_id, model_version, training_run_id, dataset_snapshot_id,
      feature_set_version_id, topology_version_id, artifact_uri, artifact_checksum,
      evaluation, compatibility, status, revision, created_at, updated_at
    ) VALUES (
      '${forecastModelVersionId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastModelId}', 1, '${forecastTrainingRunId}', '${forecastDatasetId}', '${forecastFeatureSetVersionId}', '${topologyVersionId}',
      's3://forecast-models/site-load/v1/model.bin', repeat('b', 64), '{"wape":0.10}'::jsonb, '{"runtime":"go"}'::jsonb,
      'VALIDATED', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!directValidatedModelVersion.includes('must enter lifecycle as CANDIDATE')) throw new Error(`Forecast Model Version skipped CANDIDATE: ${directValidatedModelVersion}`);

  psql(`
    INSERT INTO core_registry.forecast_model_versions (
      id, tenant_id, site_id, model_id, model_version, training_run_id, dataset_snapshot_id,
      feature_set_version_id, topology_version_id, artifact_uri, artifact_checksum,
      evaluation, compatibility, status, revision, created_at, updated_at
    ) VALUES (
      '${forecastModelVersionId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastModelId}', 1, '${forecastTrainingRunId}', '${forecastDatasetId}', '${forecastFeatureSetVersionId}', '${topologyVersionId}',
      's3://forecast-models/site-load/v1/model.bin', repeat('b', 64), '{"wape":0.10}'::jsonb, '{"runtime":"go"}'::jsonb,
      'CANDIDATE', 1, now(), now()
    );
  `);

  const modelVersionSkipValidation = psql(`
    UPDATE core_registry.forecast_model_versions
    SET status = 'ACTIVE', revision = revision + 1, updated_at = GREATEST(now(), created_at)
    WHERE id = '${forecastModelVersionId}';
  `, { expectFailure: true });
  if (!modelVersionSkipValidation.includes('must be validated before shadow/active use')) throw new Error(`Forecast Model Version skipped validation: ${modelVersionSkipValidation}`);

  psql(`
    UPDATE core_registry.forecast_model_versions
    SET status = 'VALIDATED', revision = revision + 1, updated_at = GREATEST(now(), created_at)
    WHERE id = '${forecastModelVersionId}';
  `);

  const mismatchedDeployment = psql(`
    INSERT INTO core_registry.forecast_deployments (
      id, tenant_id, site_id, target, subject_type, subject_id, model_version_id, model_id,
      feature_set_version_id, topology_version_id, status, effective_from, revision, created_at, updated_at
    ) VALUES (
      '01990000-1860-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'PV_GENERATION', 'SITE', '018f1e00-1000-7000-8000-000000000001', '${forecastModelVersionId}', '${forecastModelId}',
      '${forecastFeatureSetVersionId}', '${topologyVersionId}', 'ACTIVE', now(), 1, now(), now()
    );
  `, { expectFailure: true });
  if (!mismatchedDeployment.includes('Forecast Deployment target/subject must match its Model')) throw new Error(`Forecast Deployment accepted target drift: ${mismatchedDeployment}`);

  psql(`
    INSERT INTO core_registry.forecast_deployments (
      id, tenant_id, site_id, target, subject_type, subject_id, model_version_id, model_id,
      feature_set_version_id, topology_version_id, status, effective_from, revision, created_at, updated_at
    ) VALUES (
      '${forecastDeploymentId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001', '${forecastModelVersionId}', '${forecastModelId}',
      '${forecastFeatureSetVersionId}', '${topologyVersionId}', 'ACTIVE', '2026-08-13T00:00:00Z', 1, now(), now()
    );
  `);

  const duplicateActiveDeployment = psql(`
    INSERT INTO core_registry.forecast_deployments (
      id, tenant_id, site_id, target, subject_type, subject_id, model_version_id, model_id,
      feature_set_version_id, topology_version_id, status, effective_from, revision, created_at, updated_at
    ) VALUES (
      '01990000-1860-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001', '${forecastModelVersionId}', '${forecastModelId}',
      '${forecastFeatureSetVersionId}', '${topologyVersionId}', 'ACTIVE', '2026-08-13T00:00:00Z', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!duplicateActiveDeployment.includes('forecast_deployments_one_active_target_uidx')) throw new Error(`second ACTIVE Forecast Deployment was accepted: ${duplicateActiveDeployment}`);

  const mismatchedInputSnapshot = psql(`
    INSERT INTO core_registry.forecast_input_snapshots (
      id, tenant_id, site_id, deployment_id, model_version_id, feature_set_version_id,
      topology_version_id, latest_data_time, weather_issue_time, metric_version_refs,
      feature_values, input_checksum, captured_at
    ) VALUES (
      '01990000-1870-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastDeploymentId}', '${forecastModelVersionId}', '${forecastFeatureSetVersionId}', '${topologyVersion2Id}',
      '2026-08-12T23:45:00Z', NULL, '["${dailyEnergyVersionId}"]'::jsonb,
      '{"lastValue":812.5}'::jsonb, repeat('c',64), '2026-08-13T00:00:00Z'
    );
  `, { expectFailure: true });
  if (!mismatchedInputSnapshot.toLowerCase().includes('foreign key constraint')) throw new Error(`Forecast Input Snapshot accepted Deployment lineage drift: ${mismatchedInputSnapshot}`);

  psql(`
    INSERT INTO core_registry.forecast_input_snapshots (
      id, tenant_id, site_id, deployment_id, model_version_id, feature_set_version_id,
      topology_version_id, latest_data_time, weather_issue_time, metric_version_refs,
      feature_values, input_checksum, captured_at
    ) VALUES (
      '${forecastInputSnapshotId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastDeploymentId}', '${forecastModelVersionId}', '${forecastFeatureSetVersionId}', '${topologyVersionId}',
      '2026-08-12T23:45:00Z', '2026-08-12T23:30:00Z', '["${dailyEnergyVersionId}"]'::jsonb,
      '{"lastValue":812.5,"unit":"kW"}'::jsonb, repeat('c',64), '2026-08-13T00:00:00Z'
    );
  `);

  const inputSnapshotMutation = psql(`
    UPDATE core_registry.forecast_input_snapshots
    SET feature_values = '{"lastValue":9999}'::jsonb
    WHERE id = '${forecastInputSnapshotId}';
  `, { expectFailure: true });
  if (!inputSnapshotMutation.includes('Forecast traceability Snapshot/Dataset row is immutable')) throw new Error(`Forecast Input Snapshot was mutable: ${inputSnapshotMutation}`);

  const wrongJobHorizon = psql(`
    INSERT INTO core_registry.forecast_jobs (
      id, tenant_id, site_id, deployment_id, model_version_id, input_snapshot_id,
      target, subject_type, subject_id, forecast_origin, horizon_minutes, granularity,
      trigger_type, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1880-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastDeploymentId}', '${forecastModelVersionId}', '${forecastInputSnapshotId}', 'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001',
      '2026-08-13T00:00:00Z', 720, '15MIN', 'SCHEDULED', 'PENDING', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!wrongJobHorizon.includes('Forecast Job horizon/granularity must match its Model definition')) throw new Error(`Forecast Job accepted model horizon drift: ${wrongJobHorizon}`);

  psql(`
    INSERT INTO core_registry.forecast_jobs (
      id, tenant_id, site_id, deployment_id, model_version_id, input_snapshot_id,
      target, subject_type, subject_id, forecast_origin, horizon_minutes, granularity,
      trigger_type, status, revision, created_at, updated_at
    ) VALUES (
      '${forecastJobId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastDeploymentId}', '${forecastModelVersionId}', '${forecastInputSnapshotId}', 'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001',
      '2026-08-13T00:00:00Z', 1440, '15MIN', 'SCHEDULED', 'PENDING', 1, now(), now()
    );
  `);

  const snapshotBeforeJobSuccess = psql(`
    INSERT INTO core_registry.forecast_snapshots (
      id, tenant_id, site_id, forecast_job_id, deployment_id, model_version_id,
      input_snapshot_id, forecast_origin, window_start, window_end, result_count,
      result_checksum, quality_summary, created_at
    ) VALUES (
      '${forecastSnapshotId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastJobId}', '${forecastDeploymentId}', '${forecastModelVersionId}', '${forecastInputSnapshotId}',
      '2026-08-13T00:00:00Z', '2026-08-13T00:15:00Z', '2026-08-14T00:00:00Z', 96,
      repeat('d',64), '{"FALLBACK":96}'::jsonb, now()
    );
  `, { expectFailure: true });
  if (!snapshotBeforeJobSuccess.includes('requires a PERSISTED Forecast Job')) throw new Error(`Forecast Snapshot was created before Job persisted: ${snapshotBeforeJobSuccess}`);

  const jobSkipRunning = psql(`
    UPDATE core_registry.forecast_jobs
    SET status = 'PERSISTED', started_at = now(), completed_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${forecastJobId}';
  `, { expectFailure: true });
  if (!jobSkipRunning.includes('Forecast Job must start before persisting')) throw new Error(`Forecast Job skipped RUNNING: ${jobSkipRunning}`);

  const forecastTraceabilityFlow = psql(`
    UPDATE core_registry.forecast_jobs
    SET status = 'RUNNING', started_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${forecastJobId}';
    UPDATE core_registry.forecast_jobs
    SET status = 'PERSISTING', revision = revision + 1, updated_at = now()
    WHERE id = '${forecastJobId}';
    UPDATE core_registry.forecast_jobs
    SET status = 'PERSISTED', completed_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${forecastJobId}';
    INSERT INTO core_registry.forecast_snapshots (
      id, tenant_id, site_id, forecast_job_id, deployment_id, model_version_id,
      input_snapshot_id, forecast_origin, window_start, window_end, result_count,
      result_checksum, quality_summary, created_at
    ) VALUES (
      '${forecastSnapshotId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${forecastJobId}', '${forecastDeploymentId}', '${forecastModelVersionId}', '${forecastInputSnapshotId}',
      '2026-08-13T00:00:00Z', '2026-08-13T00:15:00Z', '2026-08-14T00:00:00Z', 96,
      repeat('d',64), '{"FALLBACK":96}'::jsonb, now()
    );
    SELECT
      (SELECT status FROM core_registry.forecast_training_runs WHERE id = '${forecastTrainingRunId}') || '|'
      || (SELECT status FROM core_registry.forecast_model_versions WHERE id = '${forecastModelVersionId}') || '|'
      || (SELECT status FROM core_registry.forecast_deployments WHERE id = '${forecastDeploymentId}') || '|'
      || (SELECT status FROM core_registry.forecast_jobs WHERE id = '${forecastJobId}') || '|'
      || (SELECT result_count::text FROM core_registry.forecast_snapshots WHERE id = '${forecastSnapshotId}');
  `);
  expectEqual(forecastTraceabilityFlow, 'SUCCEEDED|VALIDATED|ACTIVE|PERSISTED|96', 'Forecast traceability lifecycle');

  const forecastSnapshotMutation = psql(`
    UPDATE core_registry.forecast_snapshots SET result_count = 95 WHERE id = '${forecastSnapshotId}';
  `, { expectFailure: true });
  if (!forecastSnapshotMutation.includes('Forecast traceability Snapshot/Dataset row is immutable')) throw new Error(`Forecast Snapshot was mutable: ${forecastSnapshotMutation}`);

  const forecastRls = psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SET LOCAL app.authorized_site_ids = '{018f1e00-1000-7000-8000-000000000001}';
    SELECT
      (SELECT count(*) FROM core_registry.forecast_feature_sets)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_dataset_snapshots)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_model_versions)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_deployments)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_input_snapshots)::text || '|'
      || (SELECT count(*) FROM core_registry.forecast_snapshots)::text;
    ROLLBACK;
  `);
  expectEqual(forecastRls, '1|1|1|1|1|1', 'Forecast runtime RLS');
  report.assertions.forecastV2 = {
    fixture: forecastFixture,
    wrongDatasetSubjectRejected: true,
    featureVersionImmutable: true,
    modelVersionBeforeTrainingRejected: true,
    trainingSkipRunningRejected: true,
    modelVersionMustStartCandidate: true,
    modelVersionSkipValidationRejected: true,
    mismatchedDeploymentRejected: true,
    duplicateActiveDeploymentRejected: true,
    mismatchedInputSnapshotRejected: true,
    inputSnapshotImmutable: true,
    wrongJobHorizonRejected: true,
    snapshotBeforeJobSuccessRejected: true,
    jobSkipRunningRejected: true,
    lifecycle: forecastTraceabilityFlow,
    forecastSnapshotImmutable: true,
    runtimeRls: forecastRls,
  };

  const iamDelegated = psql(`
    BEGIN;
    SET LOCAL ROLE s1_iam_runtime;
    SET LOCAL app.principal_id = '018f1e00-2000-7000-8000-000000000002';
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SELECT (SELECT count(*) FROM iam.tenant_memberships)::text || '|'
      || (SELECT count(*) FROM iam.role_bindings)::text || '|'
      || (SELECT count(*) FROM iam.site_bindings)::text || '|'
      || (SELECT count(*) FROM iam.explicit_denies)::text;
    ROLLBACK;
  `);
  const iamDenied = psql(`
    BEGIN;
    SET LOCAL ROLE s1_iam_runtime;
    SET LOCAL app.principal_id = '018f1e00-2000-7000-8000-000000000003';
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SELECT count(*) FROM iam.explicit_denies;
    ROLLBACK;
  `);
  expectEqual(iamDelegated, '1|1|1|0', 'delegated IAM fixture');
  expectEqual(iamDenied, '1', 'explicit deny fixture');
  report.assertions.iamFixtures = { delegated: iamDelegated, denied: iamDenied };

  await runIAMGoTests();
  report.assertions.iamAuthorizationStore = 'passed';
  await runCoreGoTests();
  report.assertions.coreRegistryStore = 'passed';
  await runLegacyMigrationGoTests();
  report.assertions.legacyMigrationExecution = 'passed';
  await runGatewayRoutingGoTests();
  report.assertions.gatewayRegistryRouting = 'passed';

  const optimizationPolicyId = '01990000-1910-7000-8000-000000000001';
  const optimizationPolicyVersionId = '01990000-1920-7000-8000-000000000001';
  const optimizationInputSnapshotId = '01990000-1930-7000-8000-000000000001';
  const optimizationRunId = '01990000-1950-7000-8000-000000000001';
  const optimizationRecommendationId = '01990000-1960-7000-8000-000000000001';
  const intelligenceModelId = '01990000-1980-7000-8000-000000000001';
  const intelligenceEgressPolicyId = '01990000-1981-7000-8000-000000000001';
  const optimizationDeploymentRevisionId = '01990000-1982-7000-8000-000000000001';
  const optimizationDeploymentBindingId = '01990000-1983-7000-8000-000000000001';
  const fddFindingId = '01990000-1984-7000-8000-000000000001';

  const optimizationFixture = psql(`
    INSERT INTO core_registry.optimization_policies (
      id, tenant_id, policy_code, subject_type, resource_type, status, revision, created_at, updated_at
    ) VALUES (
      '${optimizationPolicyId}', '018f1d00-0000-7000-8000-000000000001', 'site_hvac_cost_shadow', 'SITE', 'HVAC', 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.optimization_policy_versions (
      id, tenant_id, policy_id, version, objective, weights, constraints, dispatch_mode,
      fallback_policy, risk_level, horizon, horizon_minutes, granularity,
      effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '${optimizationPolicyVersionId}', '018f1d00-0000-7000-8000-000000000001', '${optimizationPolicyId}', 1,
      'COST', '{"cost":1}'::jsonb, '{"comfort":{"zoneTempMinC":22,"zoneTempMaxC":27},"safety":{"maxChwSupplyC":12}}'::jsonb, 'SHADOW',
      'RULE_STRATEGY', 'LOW', 'DAY_AHEAD', 1440, '15MIN', '2026-08-01T00:00:00Z', 'RELEASED', 1, now(), now()
    );
    INSERT INTO core_registry.optimization_input_snapshots (
      id, tenant_id, site_id, subject_type, subject_id, policy_version_id,
      topology_version_id, load_forecast_snapshot_id, pv_forecast_snapshot_id,
      tariff_version_id, current_state, safety_constraints, maintenance_constraints,
      manual_locks, captured_at, input_checksum, status, revision, created_at, updated_at
    ) VALUES (
      '${optimizationInputSnapshotId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'SITE', '018f1e00-1000-7000-8000-000000000001', '${optimizationPolicyVersionId}',
      '${topologyVersionId}', '${forecastSnapshotId}', NULL, '${tariffVersion1}',
      '{"siteLoadKw":812.5,"gridPowerKw":800,"chwSupplyTempC":7}'::jsonb,
      '{"emergencyStop":false,"maxSiteImportKw":1200,"maxChwSupplyC":12}'::jsonb,
      '{"outOfService":[]}'::jsonb, '{"resources":[]}'::jsonb,
      '2026-08-13T00:05:00Z', NULL, 'BUILDING', 1, now(), now()
    );
    SELECT
      (SELECT status FROM core_registry.optimization_policy_versions WHERE id = '${optimizationPolicyVersionId}') || '|'
      || (SELECT status FROM core_registry.optimization_input_snapshots WHERE id = '${optimizationInputSnapshotId}') || '|'
      || (SELECT resource_type FROM core_registry.optimization_policies WHERE id = '${optimizationPolicyId}');
  `);
  expectEqual(optimizationFixture, 'RELEASED|BUILDING|HVAC', 'S22 HVAC Optimization policy/input fixture');

  const wrongOptimizationPvForecast = psql(`
    INSERT INTO core_registry.optimization_input_snapshots (
      id, tenant_id, site_id, subject_type, subject_id, policy_version_id,
      topology_version_id, load_forecast_snapshot_id, pv_forecast_snapshot_id,
      tariff_version_id, current_state, safety_constraints, maintenance_constraints,
      manual_locks, captured_at, input_checksum, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1930-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'SITE', '018f1e00-1000-7000-8000-000000000001', '${optimizationPolicyVersionId}',
      '${topologyVersionId}', '${forecastSnapshotId}', '${forecastSnapshotId}', '${tariffVersion1}',
      '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
      '2026-08-13T00:05:00Z', NULL, 'BUILDING', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!wrongOptimizationPvForecast.includes('PV forecast reference must be the exact PV_GENERATION')) throw new Error(`Optimization Input accepted SITE_LOAD Forecast as PV Forecast: ${wrongOptimizationPvForecast}`);

  const draftTopologyOptimizationInput = psql(`
    INSERT INTO core_registry.optimization_input_snapshots (
      id, tenant_id, site_id, subject_type, subject_id, policy_version_id,
      topology_version_id, load_forecast_snapshot_id, tariff_version_id,
      current_state, safety_constraints, maintenance_constraints, manual_locks,
      captured_at, input_checksum, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1930-7000-8000-000000000004', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'SITE', '018f1e00-1000-7000-8000-000000000001', '${optimizationPolicyVersionId}',
      '${topologyVersion2Id}', '${forecastSnapshotId}', '${tariffVersion1}', '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
      '2026-08-13T00:05:00Z', NULL, 'BUILDING', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!draftTopologyOptimizationInput.includes('requires a released Topology Version')) throw new Error(`Optimization Input accepted DRAFT Topology Version: ${draftTopologyOptimizationInput}`);

  const optimizationSealWithoutEssResource = psql(`
    UPDATE core_registry.optimization_input_snapshots
    SET status = 'SEALED', input_checksum = repeat('a', 64), revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationInputSnapshotId}';
    SELECT status FROM core_registry.optimization_input_snapshots WHERE id = '${optimizationInputSnapshotId}';
  `);
  expectEqual(optimizationSealWithoutEssResource, 'SEALED', 'HVAC Optimization snapshot seals without obsolete ESS child rows');

  const optimizationSnapshotMutation = psql(`
    UPDATE core_registry.optimization_input_snapshots
    SET current_state = '{"siteLoadKw":9999}'::jsonb, revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationInputSnapshotId}';
  `, { expectFailure: true });
  if (!optimizationSnapshotMutation.includes('SEALED Optimization Input Snapshot is immutable')) throw new Error(`SEALED Optimization Input Snapshot was mutable: ${optimizationSnapshotMutation}`);

  psql(`
    INSERT INTO core_registry.optimization_runs (
      id, tenant_id, site_id, subject_type, subject_id, policy_version_id, input_snapshot_id,
      objective, horizon, horizon_minutes, granularity, solver, solver_version,
      status, quality, objective_value, constraint_status, started_at, finished_at,
      revision, created_at, updated_at
    ) VALUES (
      '${optimizationRunId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'SITE', '018f1e00-1000-7000-8000-000000000001', '${optimizationPolicyVersionId}', '${optimizationInputSnapshotId}',
      'COST', 'DAY_AHEAD', 1440, '15MIN', 'hvac_recommendation_solver', '1',
      'CREATED', NULL, NULL, NULL, NULL, NULL, 1, now(), now()
    );
  `);

  const optimizationRunSkipValidation = psql(`
    UPDATE core_registry.optimization_runs
    SET status = 'FEASIBLE', quality = 'FEASIBLE', revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationRunId}';
  `, { expectFailure: true });
  if (!optimizationRunSkipValidation.includes('must validate before solving')) throw new Error(`Optimization Run skipped validation/solving: ${optimizationRunSkipValidation}`);

  psql(`
    UPDATE core_registry.optimization_runs
    SET status = 'VALIDATING', started_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationRunId}';
    UPDATE core_registry.optimization_runs
    SET status = 'SOLVING', revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationRunId}';
    UPDATE core_registry.optimization_runs
    SET status = 'FEASIBLE', quality = 'FEASIBLE', objective_value = 42,
        constraint_status = '{"comfort":true,"safety":true}'::jsonb,
        finished_at = now(), revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationRunId}';
  `);

  const invalidRemoteModel = psql(`
    INSERT INTO core_registry.ai_model_definitions (
      id, tenant_id, name, provider, model_id, capabilities, credential_ref, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1980-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', 'invalid-remote-model',
      'OPENAI', 'gpt-invalid', ARRAY['OPTIMIZATION'], NULL, 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!invalidRemoteModel.includes('ai_model_definitions_check')) throw new Error(`Remote AI Model accepted without CredentialRef: ${invalidRemoteModel}`);

  psql(`
    INSERT INTO core_registry.ai_model_definitions (
      id, tenant_id, name, provider, model_id, capabilities, credential_ref, status, revision, created_at, updated_at
    ) VALUES (
      '${intelligenceModelId}', '018f1d00-0000-7000-8000-000000000001', 'hvac-intelligence-local',
      'LOCAL', 'hvac-intelligence-v1', ARRAY['FDD','OPTIMIZATION'], NULL, 'ACTIVE', 1, '2026-08-13T00:00:00Z', '2026-08-13T00:00:00Z'
    );
    INSERT INTO core_registry.ai_data_egress_policies (
      id, tenant_id, name, allowed_data_classes, allowed_regions, max_input_bytes, enabled, revision, created_at
    ) VALUES (
      '${intelligenceEgressPolicyId}', '018f1d00-0000-7000-8000-000000000001', 'local-only', ARRAY['HVAC_TELEMETRY'], ARRAY['LOCAL'], 1048576, true, 1, '2026-08-13T00:00:00Z'
    );
    INSERT INTO core_registry.ai_deployment_revisions (
      id, tenant_id, model_definition_id, use_case, revision, output_schema_version,
      data_egress_policy_id, prompt_policy_version, enabled, created_at
    ) VALUES (
      '${optimizationDeploymentRevisionId}', '018f1d00-0000-7000-8000-000000000001', '${intelligenceModelId}',
      'OPTIMIZATION', 1, 'optimization-recommendation/v1', '${intelligenceEgressPolicyId}', NULL, true, '2026-08-13T00:00:00Z'
    );
    INSERT INTO core_registry.ai_deployment_bindings (
      id, tenant_id, site_id, use_case, deployment_revision_id, status, revision, created_at, updated_at
    ) VALUES (
      '${optimizationDeploymentBindingId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      'OPTIMIZATION', '${optimizationDeploymentRevisionId}', 'ACTIVE', 1, '2026-08-13T00:00:00Z', '2026-08-13T00:00:00Z'
    );
  `);

  const deploymentRevisionMutation = psql(`
    UPDATE core_registry.ai_deployment_revisions SET enabled = false WHERE id = '${optimizationDeploymentRevisionId}';
  `, { expectFailure: true });
  if (!deploymentRevisionMutation.includes('AI Deployment Revision is immutable')) throw new Error(`AI Deployment Revision was mutable: ${deploymentRevisionMutation}`);

  const mismatchedDeploymentBinding = psql(`
    INSERT INTO core_registry.ai_deployment_bindings (
      id, tenant_id, site_id, use_case, deployment_revision_id, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-1983-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000002',
      'FDD', '${optimizationDeploymentRevisionId}', 'ACTIVE', 1, now(), now()
    );
  `, { expectFailure: true });
  if (!mismatchedDeploymentBinding.includes('use case must match immutable Deployment Revision')) throw new Error(`AI binding accepted mismatched use case: ${mismatchedDeploymentBinding}`);

  const recommendationPublished = psql(`
    UPDATE core_registry.optimization_runs
    SET status = 'PERSISTING', revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationRunId}';
    INSERT INTO core_registry.optimization_recommendations (
      id, tenant_id, site_id, optimization_run_id, input_snapshot_id, deployment_revision_id,
      baseline, objective, constraints, candidate, expected_impact, uncertainty, risk,
      rollback_plan, verification_plan, approval_state, current_state_revalidation,
      command_intent_id, revision, created_at, updated_at
    ) VALUES (
      '${optimizationRecommendationId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '${optimizationRunId}', '${optimizationInputSnapshotId}', '${optimizationDeploymentRevisionId}',
      '{"chwSupplyTempC":7,"dailyEnergyKWh":12000}'::jsonb,
      '{"kind":"MINIMIZE_COST"}'::jsonb,
      '[{"kind":"COMFORT","zoneTempMinC":22,"zoneTempMaxC":27},{"kind":"SAFETY","maxChwSupplyC":12}]'::jsonb,
      '{"supplyTempC":7.5}'::jsonb,
      '{"energySavingKWhPerDay":420,"costSavingPerDay":310}'::jsonb,
      '{"energySavingKWhPerDay":{"lower":300,"upper":510}}'::jsonb,
      '{"level":"LOW","reason":"bounded-setpoint-change"}'::jsonb,
      '{"action":"restore-baseline","supplyTempC":7}'::jsonb,
      '{"windowMinutes":60,"metrics":["energy","comfort"]}'::jsonb,
      'DRAFT', NULL, NULL, 1, '2026-08-13T01:00:00Z', '2026-08-13T01:00:00Z'
    );
    UPDATE core_registry.optimization_runs
    SET status = 'PUBLISHED', revision = revision + 1, updated_at = now()
    WHERE id = '${optimizationRunId}';
    SELECT
      (SELECT status FROM core_registry.optimization_runs WHERE id = '${optimizationRunId}') || '|'
      || (SELECT approval_state FROM core_registry.optimization_recommendations WHERE id = '${optimizationRecommendationId}') || '|'
      || (SELECT candidate->>'supplyTempC' FROM core_registry.optimization_recommendations WHERE id = '${optimizationRecommendationId}');
  `);
  expectEqual(recommendationPublished, 'PUBLISHED|DRAFT|7.5', 'S22 Optimization Recommendation publication flow');

  const recommendationCommandWithoutRevalidation = psql(`
    UPDATE core_registry.optimization_recommendations
    SET approval_state = 'APPROVED', command_intent_id = '01990000-1990-7000-8000-000000000001',
        revision = revision + 1, updated_at = '2026-08-13T01:01:00Z'
    WHERE id = '${optimizationRecommendationId}';
  `, { expectFailure: true });
  if (!recommendationCommandWithoutRevalidation.includes('fresh independent current-state revalidation')) throw new Error(`Recommendation produced Command intent without revalidation: ${recommendationCommandWithoutRevalidation}`);

  const staleRecommendationRevalidation = psql(`
    UPDATE core_registry.optimization_recommendations
    SET approval_state = 'APPROVED',
        current_state_revalidation = '{"snapshotId":"state-before-recommendation","accepted":true,"validatedAt":"2026-08-13T00:59:00Z","expiresAt":"2026-08-13T02:00:00Z"}'::jsonb,
        command_intent_id = '01990000-1990-7000-8000-000000000001', revision = revision + 1,
        updated_at = '2026-08-13T01:01:00Z'
    WHERE id = '${optimizationRecommendationId}';
  `, { expectFailure: true });
  if (!staleRecommendationRevalidation.includes('fresh independent current-state revalidation')) throw new Error(`Recommendation accepted pre-computation revalidation: ${staleRecommendationRevalidation}`);

  const recommendationControlGate = psql(`
    UPDATE core_registry.optimization_recommendations
    SET approval_state = 'APPROVED',
        current_state_revalidation = '{"snapshotId":"state-after-recommendation","accepted":true,"reasonCode":"CURRENT_STATE_SAFE","validatedAt":"2026-08-13T01:02:00Z","expiresAt":"2026-08-13T02:00:00Z"}'::jsonb,
        command_intent_id = '01990000-1990-7000-8000-000000000001', revision = revision + 1,
        updated_at = '2026-08-13T01:03:00Z'
    WHERE id = '${optimizationRecommendationId}';
    SELECT approval_state || '|' || (current_state_revalidation->>'accepted') || '|' || (command_intent_id IS NOT NULL)::text
    FROM core_registry.optimization_recommendations WHERE id = '${optimizationRecommendationId}';
  `);
  expectEqual(recommendationControlGate, 'APPROVED|true|true', 'Recommendation requires independent current-state revalidation before Command intent');

  psql(`
    INSERT INTO core_registry.fdd_findings (
      id, tenant_id, site_id, asset_id, finding_type, evaluation_from, evaluation_to, evidence_ids,
      model_deployment_revision_id, rule_revision_id, confidence, quality_blocker, alarm_id, work_order_id, created_at
    ) VALUES (
      '${fddFindingId}', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001',
      '018f1e00-3000-7000-8000-000000000001', 'CHILLED_WATER_LOW_DELTA_T',
      '2026-08-13T00:00:00Z', '2026-08-13T00:30:00Z', ARRAY['telemetry:chw_supply','telemetry:chw_return'],
      NULL, 'low-delta-t/v1', 0.91, NULL, NULL, NULL, '2026-08-13T00:31:00Z'
    );
  `);

  const obsoleteDispatchPrivileges = psql(`
    SELECT has_table_privilege('optimization_runtime','core_registry.dispatch_plans','INSERT')::text || '|'
      || has_table_privilege('optimization_runtime','core_registry.dispatch_intervals','INSERT')::text || '|'
      || has_table_privilege('optimization_runtime','core_registry.optimization_input_resources','SELECT')::text;
  `);
  expectEqual(obsoleteDispatchPrivileges, 'false|false|false', 'Optimization runtime cannot use obsolete ESS Dispatch surface');

  const optimizationRls = psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.tenant_id = '018f1d00-0000-7000-8000-000000000001';
    SET LOCAL app.authorized_site_ids = '{018f1e00-1000-7000-8000-000000000001}';
    SELECT
      (SELECT count(*) FROM core_registry.optimization_policies)::text || '|'
      || (SELECT count(*) FROM core_registry.optimization_policy_versions)::text || '|'
      || (SELECT count(*) FROM core_registry.optimization_input_snapshots)::text || '|'
      || (SELECT count(*) FROM core_registry.optimization_runs)::text || '|'
      || (SELECT count(*) FROM core_registry.optimization_recommendations)::text || '|'
      || (SELECT count(*) FROM core_registry.ai_model_definitions)::text || '|'
      || (SELECT count(*) FROM core_registry.fdd_findings)::text;
    ROLLBACK;
  `);
  expectEqual(optimizationRls, '1|1|1|1|1|1|1', 'S22 Intelligence Core RLS');
  report.assertions.optimizationS22 = {
    fixture: optimizationFixture,
    wrongPvForecastRejected: true,
    draftTopologyRejected: true,
    sealsWithoutObsoleteEssResources: true,
    sealedSnapshotImmutable: true,
    runSkipValidationRejected: true,
    remoteModelRequiresCredentialRef: true,
    deploymentRevisionImmutable: true,
    deploymentBindingUseCaseBound: true,
    recommendationPublication: recommendationPublished,
    commandWithoutRevalidationRejected: true,
    staleRevalidationRejected: true,
    currentStateRevalidationGate: recommendationControlGate,
    obsoleteDispatchPrivileges,
    runtimeRls: optimizationRls,
  };

  const invalidTimezone = psql(`
    INSERT INTO core_registry.sites (id, tenant_id, code, display_name, timezone, status, revision, created_at, updated_at)
    VALUES ('018f1e00-1000-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', 'invalid-timezone', 'Invalid Timezone', 'Mars/Olympus', 'ACTIVE', 1, now(), now())
  `, { expectFailure: true });
  if (!invalidTimezone.includes('invalid IANA timezone')) throw new Error('IANA timezone rejection did not emit the expected evidence');
  report.assertions.invalidTimezoneRejected = true;

  const duplicateExternal = psql(`
    INSERT INTO core_registry.external_bindings (id, tenant_id, site_id, integration_instance_id, provider, external_entity_type, external_id, binding_status, valid_from, valid_to, revision, created_at, updated_at)
    VALUES ('018f1e00-6000-7000-8000-000000000099', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-6100-7000-8000-000000000001', 'mqtt', 'DEVICE', 'edge-device-owner-a-1', 'ACTIVE', now(), NULL, 1, now(), now())
  `, { expectFailure: true });
  if (!duplicateExternal.includes('external_bindings_active_external_key_uidx')) throw new Error('ExternalBinding active uniqueness was not enforced');
  report.assertions.externalBindingActiveUnique = true;

  const quarantine = psql("SELECT mapping_state || '|' || (target_resource_id IS NULL)::text FROM core_registry.legacy_resource_maps WHERE source_key = 'ambiguous-asset-1'");
  expectEqual(quarantine, 'QUARANTINED|true', 'ambiguous Legacy mapping');
  report.assertions.ambiguousMapping = quarantine;

  const operatorDenied = psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SELECT count(*) FROM core_registry.migration_quarantine;
    ROLLBACK;
  `, { expectFailure: true });
  if (!operatorDenied.includes('permission denied')) throw new Error('Core runtime unexpectedly accessed Migration Quarantine');
  report.assertions.quarantineRestricted = true;

  const plan = psql(`
    SET enable_seqscan = off;
    EXPLAIN (FORMAT JSON)
    SELECT id, tenant_id, site_id, code, display_name
    FROM core_registry.assets
    WHERE tenant_id = '018f1d00-0000-7000-8000-000000000001'
      AND site_id = '018f1e00-1000-7000-8000-000000000001'
      AND (display_name COLLATE "C", id) > ('', '00000000-0000-0000-0000-000000000000')
    ORDER BY display_name COLLATE "C", id
    LIMIT 51;
  `);
  if (!plan.includes('asset_tenant_page_idx')) throw new Error('Asset keyset query did not use the Tenant+Site index');
  report.assertions.queryPlanIndex = 'asset_tenant_page_idx';

  const schemaOwners = psql("SELECT nspname || ':' || pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname IN ('iam','core_registry') ORDER BY nspname");
  expectEqual(schemaOwners, 'core_registry:s1_core_migrator\niam:s1_iam_migrator', 'schema owners');
  report.assertions.schemaOwners = schemaOwners.split('\n');

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S1 Registry PostgreSQL baseline passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
