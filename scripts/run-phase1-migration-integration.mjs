import { spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const docker = 'docker';
const suffix = `${process.pid}-${Date.now()}`;
const network = `hvac-phase1-migration-${suffix}`;
const postgresContainer = `${network}-postgres`;
const migratorImage = `hvac-phase1-migrator:integration-${suffix}`;
const reportPath = resolve(root, 'out/phase1-migration-integration/report.json');
const secretPath = resolve(root, 'out/phase1-migration-integration/roles.sql');
const postgresImage = 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412';
const adminCredential = `integration-${randomBytes(24).toString('hex')}`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    const safeArgs = args.map((arg) => String(arg).replace(/^(PGPASSWORD|POSTGRES_PASSWORD)=.+$/, '$1=[REDACTED_SECRET]'));
    throw new Error(`${command} ${safeArgs.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function psql(database, sql) {
  return run(docker, ['exec', '-e', `PGPASSWORD=${adminCredential}`, postgresContainer, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}

function psqlMustFail(database, sql) {
  const result = spawnSync(docker, ['exec', '-e', `PGPASSWORD=${adminCredential}`, postgresContainer, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', sql], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (!result.error && result.status === 0) throw new Error(`psql ${database} unexpectedly accepted a negative data-foundation case`);
  return String(result.stderr ?? result.stdout ?? '').trim().slice(-1200);
}

async function waitForPostgres() {
  let lastStart = '';
  let stable = 0;
  for (let attempt = 0; attempt < 240; attempt += 1) {
    const probe = spawnSync(docker, ['exec', postgresContainer, 'pg_isready', '-U', 'postgres', '-d', 'hvac_s0'], { cwd: root, stdio: 'ignore', windowsHide: true });
    if (!probe.error && probe.status === 0) {
      const started = spawnSync(docker, ['exec', '-e', `PGPASSWORD=${adminCredential}`, postgresContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s0', '-Atqc', 'SELECT pg_postmaster_start_time()'], { cwd: root, encoding: 'utf8', windowsHide: true });
      if (!started.error && started.status === 0) {
        const value = String(started.stdout).trim();
        if (value === lastStart) stable += 1;
        else {
          lastStart = value;
          stable = 1;
        }
        if (stable >= 3) return;
      }
    } else {
      stable = 0;
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 250));
  }
  throw new Error('isolated PostgreSQL did not become stable');
}

function runMigrator() {
  run(docker, [
    'run', '--rm',
    '--network', network,
    '--mount', `type=bind,src=${secretPath},dst=/run/hvac/db-role-credentials/roles.sql,readonly`,
    '-e', `PGHOST=${postgresContainer}`,
    '-e', 'PGPORT=5432',
    '-e', 'PGUSER=postgres',
    '-e', `PGPASSWORD=${adminCredential}`,
    '-e', 'PHASE1_RELEASE_REVISION=integration-test',
    migratorImage,
  ]);
}

const report = {
  schemaVersion: 1,
  capability: 'phase1-production-safe-migration',
  status: 'failed',
  startedAt: new Date().toISOString(),
  assertions: {},
};

try {
  run('which', [docker]);
  run(docker, ['version']);
  run(docker, ['build', '-f', 'deploy/platform/phase1/migrations/Dockerfile', '-t', migratorImage, '.']);

  await mkdir(dirname(secretPath), { recursive: true });
  const credentialTemplate = await readFile(resolve(root, 'deploy/platform/phase1/migrations/role-credentials.sql.example'), 'utf8');
  let credentialIndex = 0;
  const credentials = credentialTemplate.replaceAll('[REDACTED_SECRET]', () => `integration-role-${credentialIndex += 1}-${randomBytes(18).toString('hex')}`);
  await writeFile(secretPath, credentials, { mode: 0o600 });

  try { run(docker, ['rm', '-f', postgresContainer]); } catch {}
  try { run(docker, ['network', 'rm', network]); } catch {}
  run(docker, ['network', 'create', network]);
  run(docker, [
    'run', '-d', '--name', postgresContainer, '--network', network,
    '-e', 'POSTGRES_DB=hvac_s0',
    '-e', 'POSTGRES_USER=postgres',
    '-e', `POSTGRES_PASSWORD=${adminCredential}`,
    postgresImage,
  ]);
  await waitForPostgres();

  runMigrator();
  runMigrator();

  const expectedMigrationCounts = { hvac_s0: 3, hvac_s1: 13, hvac_s2: 7, hvac_s3: 3, hvac_s4: 4, hvac_s5: 4 };
  const actualMigrationCounts = {};
  for (const [database, expected] of Object.entries(expectedMigrationCounts)) {
    const actual = Number(psql(database, 'SELECT count(*) FROM phase1_deployment.schema_migrations'));
    if (actual !== expected) throw new Error(`${database} migration count mismatch: expected ${expected}, got ${actual}`);
    actualMigrationCounts[database] = actual;
  }

  const criticalTables = {
    hvac_s0: 'audit_ledger.records',
    hvac_s1: 'core_registry.organizations',
    hvac_s2: 'telemetry_runtime.registry_device_bindings',
    hvac_s3: 'command_runtime.command_intents',
    hvac_s4: 'alarm_runtime.alarm_current',
    hvac_s5: 'work_order_runtime.work_order_current',
  };
  const rls = {};
  for (const [database, relation] of Object.entries(criticalTables)) {
    const [schema, table] = relation.split('.');
    const state = psql(database, `SELECT (to_regclass('${relation}') IS NOT NULL)::text || '|' || c.relrowsecurity::text || '|' || c.relforcerowsecurity::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='${schema}' AND c.relname='${table}'`);
    if (state !== 'true|true|true') throw new Error(`${relation} is not present with forced RLS: ${state}`);
    rls[relation] = state;
  }

  const dataFoundationTables = [
    'core_registry.device_products',
    'core_registry.point_templates',
    'core_registry.energy_nodes',
    'core_registry.energy_edges',
    'core_registry.metric_definitions',
  ];
  const dataFoundationRls = {};
  for (const relation of dataFoundationTables) {
    const [schema, table] = relation.split('.');
    const state = psql('hvac_s1', `SELECT (to_regclass('${relation}') IS NOT NULL)::text || '|' || c.relrowsecurity::text || '|' || c.relforcerowsecurity::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='${schema}' AND c.relname='${table}'`);
    if (state !== 'true|true|true') throw new Error(`${relation} is not present with forced RLS: ${state}`);
    dataFoundationRls[relation] = state;
  }
  for (const relation of ['core_registry.unit_registry', 'core_registry.energy_types', 'core_registry.energy_directions']) {
    const exists = psql('hvac_s1', `SELECT (to_regclass('${relation}') IS NOT NULL)::text`);
    if (exists !== 'true') throw new Error(`${relation} is missing from the data foundation`);
  }

  const publicConnect = {};
  for (const database of Object.keys(expectedMigrationCounts)) {
    const allowed = psql('postgres', `SELECT has_database_privilege('public', '${database}', 'CONNECT')`);
    if (allowed !== 'f') throw new Error(`PUBLIC still has CONNECT on ${database}`);
    publicConnect[database] = false;
  }

  const productionSeedCounts = {
    policies: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.policies')),
    roleBindings: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.role_bindings')),
    alarmPermissions: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.alarm_permissions')),
    workOrderPermissions: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.work_order_permissions')),
    telemetryScopeBindings: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.telemetry_scope_bindings')),
  };
  for (const [name, count] of Object.entries(productionSeedCounts)) {
    if (count !== 0) throw new Error(`production migration unexpectedly seeded ${name}: ${count}`);
  }

  const tenantA = '019a0000-0000-7000-8000-000000000001';
  const tenantB = '019a0000-0000-7000-8000-000000000002';
  const organizationA = '019a0000-1000-7000-8000-000000000001';
  const organizationB = '019a0000-1000-7000-8000-000000000002';
  const siteA = '019a0000-2000-7000-8000-000000000001';
  const siteB = '019a0000-2000-7000-8000-000000000002';
  const productA = '019a0000-3000-7000-8000-000000000001';
  const productB = '019a0000-3000-7000-8000-000000000002';
  const templateA = '019a0000-4000-7000-8000-000000000001';
  const equipmentParent = '019a0000-5000-7000-8000-000000000001';
  const equipmentChild = '019a0000-5000-7000-8000-000000000002';
  const deviceA = '019a0000-6000-7000-8000-000000000001';
  const deviceB = '019a0000-6000-7000-8000-000000000002';
  const pointA = '019a0000-7000-7000-8000-000000000001';
  const nodeGridA = '019a0000-8000-7000-8000-000000000001';
  const nodeMeterA = '019a0000-8000-7000-8000-000000000002';
  const nodeGridB = '019a0000-8000-7000-8000-000000000003';
  const edgeA = '019a0000-9000-7000-8000-000000000001';
  const metricA = '019a0000-a000-7000-8000-000000000001';

  psql('hvac_s1', `
    INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at) VALUES
      ('${tenantA}', 'data-a', 'Data Tenant A', 'UTC', 'USD', 'US', 'ACTIVE', 1, now(), now()),
      ('${tenantB}', 'data-b', 'Data Tenant B', 'UTC', 'USD', 'US', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.organizations (id, tenant_id, code, display_name, status, revision, created_at, updated_at) VALUES
      ('${organizationA}', '${tenantA}', 'data-org-a', 'Data Org A', 'ACTIVE', 1, now(), now()),
      ('${organizationB}', '${tenantB}', 'data-org-b', 'Data Org B', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.sites (id, tenant_id, organization_id, code, display_name, timezone, status, revision, created_at, updated_at) VALUES
      ('${siteA}', '${tenantA}', '${organizationA}', 'data-site-a', 'Data Site A', 'UTC', 'ACTIVE', 1, now(), now()),
      ('${siteB}', '${tenantB}', '${organizationB}', 'data-site-b', 'Data Site B', 'UTC', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.device_products (id, tenant_id, product_code, product_name, manufacturer, model, status, revision, created_at, updated_at) VALUES
      ('${productA}', '${tenantA}', 'meter-a', 'Meter A', 'Vendor A', 'A1', 'ACTIVE', 1, now(), now()),
      ('${productB}', '${tenantB}', 'meter-b', 'Meter B', 'Vendor B', 'B1', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.point_templates (
      id, tenant_id, device_product_id, point_code, point_name, point_type, data_type, unit_code, access_type,
      sampling_interval_ms, minimum_number, maximum_number, precision_digits, multiplier, value_offset, enabled,
      status, revision, created_at, updated_at
    ) VALUES ('${templateA}', '${tenantA}', '${productA}', 'active_power', 'Active Power', 'TELEMETRY', 'NUMBER', 'kW', 'READ_ONLY', 1000, 0, 10000, 2, 1, 0, true, 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.equipment (
      id, tenant_id, organization_id, site_id, code, display_name, equipment_type, status, revision, created_at, updated_at,
      parent_equipment_id, rated_power, manufacturer, model, commission_date
    ) VALUES
      ('${equipmentParent}', '${tenantA}', '${organizationA}', '${siteA}', 'asset-parent', 'Asset Parent', 'METERING_SYSTEM', 'ACTIVE', 1, now(), now(), NULL, 1000, 'Vendor A', 'Asset-A', DATE '2026-01-01'),
      ('${equipmentChild}', '${tenantA}', '${organizationA}', '${siteA}', 'asset-child', 'Asset Child', 'METER', 'ACTIVE', 1, now(), now(), '${equipmentParent}', 100, 'Vendor A', 'Meter-A', DATE '2026-01-02');
    INSERT INTO core_registry.devices (id, tenant_id, organization_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at, product_id) VALUES
      ('${deviceA}', '${tenantA}', '${organizationA}', '${siteA}', 'device-a', 'Device A', 'ELECTRIC_METER', 'ACTIVE', 1, now(), now(), '${productA}'),
      ('${deviceB}', '${tenantB}', '${organizationB}', '${siteB}', 'device-b', 'Device B', 'ELECTRIC_METER', 'ACTIVE', 1, now(), now(), '${productB}');
    INSERT INTO core_registry.telemetry_points (
      id, tenant_id, organization_id, site_id, reporting_device_id, sensor_id, point_key, source_key, display_name,
      point_kind, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms,
      formula_revision, source_metadata, status, revision, created_at, updated_at, point_template_id
    ) VALUES ('${pointA}', '${tenantA}', '${organizationA}', '${siteA}', '${deviceA}', NULL, 'active_power', 'active_power', 'Active Power',
      'MEASURED', 'NUMBER', 'kW', false, 1000, 1000, 5000, NULL, '{}'::jsonb, 'ACTIVE', 1, now(), now(), '${templateA}');
    INSERT INTO core_registry.energy_nodes (id, tenant_id, organization_id, site_id, node_type, equipment_id, device_id, name, status, revision, created_at, updated_at) VALUES
      ('${nodeGridA}', '${tenantA}', '${organizationA}', '${siteA}', 'GRID', NULL, NULL, 'Grid A', 'ACTIVE', 1, now(), now()),
      ('${nodeMeterA}', '${tenantA}', '${organizationA}', '${siteA}', 'METER', '${equipmentChild}', '${deviceA}', 'Meter A', 'ACTIVE', 1, now(), now()),
      ('${nodeGridB}', '${tenantB}', '${organizationB}', '${siteB}', 'GRID', NULL, NULL, 'Grid B', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.energy_edges (id, tenant_id, organization_id, site_id, from_node_id, to_node_id, energy_type_id, direction, enabled, revision, created_at, updated_at)
      VALUES ('${edgeA}', '${tenantA}', '${organizationA}', '${siteA}', '${nodeGridA}', '${nodeMeterA}', '01990000-0000-7000-8000-000000000001', 'IMPORT', true, 1, now(), now());
    INSERT INTO core_registry.metric_definitions (id, tenant_id, metric_code, metric_name, metric_type, unit_code, calculation_method, aggregation, period, status, revision, created_at, updated_at)
      VALUES ('${metricA}', '${tenantA}', 'daily_energy', 'Daily Energy', 'ENERGY', 'kWh', 'SUM_INTERVAL_ENERGY', 'SUM', 'DAY', 'ACTIVE', 1, now(), now());
  `);

  const dataFoundation = {
    unitConversion: psql('hvac_s1', "SELECT unit_code || '|' || canonical_unit_code || '|' || multiplier::text || '|' || conversion_offset::text FROM core_registry.unit_registry WHERE unit_code='W'"),
    energyTypeCount: Number(psql('hvac_s1', "SELECT count(*) FROM core_registry.energy_types WHERE status='ACTIVE'")),
    directionCount: Number(psql('hvac_s1', "SELECT count(*) FROM core_registry.energy_directions WHERE status='ACTIVE'")),
    productTemplateBinding: psql('hvac_s1', `SELECT d.product_id::text || '|' || p.point_template_id::text FROM core_registry.devices d JOIN core_registry.telemetry_points p ON p.tenant_id=d.tenant_id AND p.reporting_device_id=d.id WHERE d.id='${deviceA}'`),
    topologyEdgeCount: Number(psql('hvac_s1', `SELECT count(*) FROM core_registry.energy_edges WHERE tenant_id='${tenantA}' AND site_id='${siteA}'`)),
    metricDefinitionCount: Number(psql('hvac_s1', `SELECT count(*) FROM core_registry.metric_definitions WHERE tenant_id='${tenantA}' AND metric_code='daily_energy'`)),
  };
  if (dataFoundation.unitConversion !== 'W|kW|0.001|0' || dataFoundation.energyTypeCount !== 8 || dataFoundation.directionCount !== 6 ||
      dataFoundation.productTemplateBinding !== `${productA}|${templateA}` || dataFoundation.topologyEdgeCount !== 1 || dataFoundation.metricDefinitionCount !== 1) {
    throw new Error(`data foundation positive invariant mismatch: ${JSON.stringify(dataFoundation)}`);
  }
  const negativeDataFoundation = {
    crossTenantProductRejected: psqlMustFail('hvac_s1', `UPDATE core_registry.devices SET product_id='${productB}' WHERE id='${deviceA}'`),
    crossSiteEnergyEdgeRejected: psqlMustFail('hvac_s1', `INSERT INTO core_registry.energy_edges (id, tenant_id, organization_id, site_id, from_node_id, to_node_id, energy_type_id, direction, enabled, revision, created_at, updated_at) VALUES ('019a0000-9000-7000-8000-000000000002', '${tenantA}', '${organizationA}', '${siteA}', '${nodeGridA}', '${nodeGridB}', '01990000-0000-7000-8000-000000000001', 'IMPORT', true, 1, now(), now())`),
    equipmentCycleRejected: psqlMustFail('hvac_s1', `UPDATE core_registry.equipment SET parent_equipment_id='${equipmentChild}' WHERE id='${equipmentParent}'`),
  };

  const forbiddenRoles = ['s3_command_dispatcher', 's3_command_dispatcher_service'];
  for (const role of forbiddenRoles) {
    const exists = psql('postgres', `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='${role}')`);
    if (exists !== 'f') throw new Error(`production migration created forbidden direct-database role ${role}`);
  }

  const unpasswordedLoginRoles = Number(psql('postgres', "SELECT count(*) FROM pg_authid WHERE rolcanlogin AND rolname ~ '^(s0_|gateway_|audit_|s1_|s2_|s3_|s4_|s5_)' AND rolpassword IS NULL"));
  if (unpasswordedLoginRoles !== 0) throw new Error(`production login roles without credentials: ${unpasswordedLoginRoles}`);

  report.assertions = {
    exactAllowlistedMigrationsApplied: true,
    migrationCounts: actualMigrationCounts,
    secondRunIdempotent: true,
    criticalForcedRls: rls,
    dataFoundationForcedRls: dataFoundationRls,
    publicDatabaseConnectRevoked: publicConnect,
    environmentFixtureSeedsExcluded: productionSeedCounts,
    dataFoundation,
    negativeDataFoundation,
    commandDispatcherHasNoDatabaseRole: true,
    allProductionLoginRolesCredentialed: true,
  };
  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Phase 1 production-safe migration integration passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { run(docker, ['rm', '-f', postgresContainer]); } catch {}
  try { run(docker, ['network', 'rm', network]); } catch {}
  try { run(docker, ['image', 'rm', '-f', migratorImage]); } catch {}
  try { await rm(secretPath, { force: true }); } catch {}
}
