import { spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
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
const manifestPath = resolve(root, 'deploy/platform/phase1/migrations/manifest.v1.json');
const migrationListPath = resolve(root, 'deploy/platform/phase1/migrations/migration-list.tsv');
const productVersion = '0.1.0';

function redactArgs(args) {
  return args.map((arg) => String(arg).replace(/^(PGPASSWORD|POSTGRES_PASSWORD)=.+$/, '$1=[REDACTED_SECRET]'));
}

function execute(command, args, options = {}) {
  return spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
}

function run(command, args, options = {}) {
  const result = execute(command, args, options);
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${redactArgs(args).join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function runMustFail(command, args, expectedText) {
  const result = execute(command, args);
  if (!result.error && result.status === 0) {
    throw new Error(`${command} ${redactArgs(args).join(' ')} unexpectedly succeeded`);
  }
  const detail = `${result.stderr ?? ''}\n${result.stdout ?? ''}`;
  if (!detail.includes(expectedText)) {
    throw new Error(`expected failure containing ${JSON.stringify(expectedText)}, got: ${detail.trim().slice(-1600)}`);
  }
  return detail.trim().slice(-1600);
}

function psql(database, sql) {
  return run(docker, [
    'exec', '-e', `PGPASSWORD=${adminCredential}`, postgresContainer,
    'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', sql,
  ]);
}

async function waitForPostgres() {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    const probe = execute(docker, ['exec', postgresContainer, 'pg_isready', '-U', 'postgres', '-d', 'postgres'], { stdio: 'ignore' });
    if (!probe.error && probe.status === 0) return;
    await new Promise((resolveWait) => setTimeout(resolveWait, 250));
  }
  throw new Error('isolated PostgreSQL did not become ready');
}

function migratorArgs(extraEnv = []) {
  return [
    'run', '--rm', '--network', network,
    '--mount', `type=bind,src=${secretPath},dst=/run/hvac/db-role-credentials/roles.sql,readonly`,
    '-e', `PGHOST=${postgresContainer}`,
    '-e', 'PGPORT=5432',
    '-e', 'PGUSER=postgres',
    '-e', `PGPASSWORD=${adminCredential}`,
    '-e', `PHASE1_PRODUCT_VERSION=${productVersion}`,
    '-e', 'PHASE1_RELEASE_REVISION=integration-test',
    ...extraEnv,
    migratorImage,
  ];
}

function preflightArgs(version = productVersion) {
  return [
    'run', '--rm', '--network', network,
    '--entrypoint', '/opt/hvac/migrations/verify-phase1-schema.sh',
    '-e', `PGHOST=${postgresContainer}`,
    '-e', 'PGPORT=5432',
    '-e', 'PGUSER=postgres',
    '-e', `PGPASSWORD=${adminCredential}`,
    '-e', `PHASE1_PRODUCT_VERSION=${version}`,
    migratorImage,
  ];
}

const report = {
  schemaVersion: 1,
  capability: 'phase1-product-schema-migration',
  status: 'failed',
  startedAt: new Date().toISOString(),
  assertions: {},
};

try {
  run(docker, ['version']);

  const migrationManifest = JSON.parse(await readFile(manifestPath, 'utf8'));
  const migrationList = await readFile(migrationListPath, 'utf8');
  const manifestDigest = createHash('sha256').update(migrationList).digest('hex');
  const expectedCounts = Object.fromEntries(
    migrationManifest.databases.map((entry) => [entry.name, entry.migrations.length]),
  );

  run(docker, ['build', '-f', 'deploy/platform/phase1/migrations/Dockerfile', '-t', migratorImage, '.']);

  await mkdir(dirname(secretPath), { recursive: true });
  const credentialTemplate = await readFile(resolve(root, 'deploy/platform/phase1/migrations/role-credentials.sql.example'), 'utf8');
  let credentialIndex = 0;
  const credentials = credentialTemplate.replaceAll(
    '[REDACTED_SECRET]',
    () => `integration-role-${credentialIndex += 1}-${randomBytes(18).toString('hex')}`,
  );
  await writeFile(secretPath, credentials, { mode: 0o600 });

  try { run(docker, ['rm', '-f', postgresContainer]); } catch {}
  try { run(docker, ['network', 'rm', network]); } catch {}
  run(docker, ['network', 'create', network]);
  run(docker, [
    'run', '-d', '--name', postgresContainer, '--network', network,
    '-e', 'POSTGRES_DB=postgres',
    '-e', 'POSTGRES_USER=postgres',
    '-e', `POSTGRES_PASSWORD=${adminCredential}`,
    postgresImage,
  ]);
  await waitForPostgres();

  run(docker, migratorArgs());

  const migrationCounts = {};
  const productStates = {};
  for (const [database, expectedCount] of Object.entries(expectedCounts)) {
    const applied = Number(psql(database, "SELECT count(*) FROM phase1_deployment.schema_migrations WHERE status='APPLIED'"));
    if (applied !== expectedCount) {
      throw new Error(`${database} applied migration count mismatch: expected=${expectedCount} actual=${applied}`);
    }
    const incomplete = Number(psql(database, "SELECT count(*) FROM phase1_deployment.schema_migrations WHERE status<>'APPLIED'"));
    if (incomplete !== 0) throw new Error(`${database} contains incomplete migration state after successful run`);
    migrationCounts[database] = applied;

    const productState = psql(database, "SELECT product || '|' || product_version || '|' || schema_manifest_sha256 FROM phase1_deployment.product_schema WHERE product='hvac-web'");
    const expectedState = `hvac-web|${productVersion}|${manifestDigest}`;
    if (productState !== expectedState) {
      throw new Error(`${database} Product/Schema state mismatch: expected=${expectedState} actual=${productState}`);
    }
    productStates[database] = productState;
  }

  run(docker, migratorArgs());
  run(docker, preflightArgs());

  const recoveryDatabase = migrationManifest.databases.find((entry) => entry.migrations.length > 0)?.name;
  const recoveryPath = migrationManifest.databases.find((entry) => entry.name === recoveryDatabase)?.migrations[0];
  if (!recoveryDatabase || !recoveryPath) throw new Error('migration manifest has no recovery probe target');

  psql(recoveryDatabase, `UPDATE phase1_deployment.schema_migrations SET status='APPLYING', applied_at=NULL WHERE migration_path='${recoveryPath}'`);
  const incompleteFailure = runMustFail(docker, migratorArgs(), 'operator recovery is required before retry');
  psql(recoveryDatabase, `UPDATE phase1_deployment.schema_migrations SET status='APPLIED', applied_at=now() WHERE migration_path='${recoveryPath}'`);

  const originalDigest = psql(recoveryDatabase, `SELECT sha256 FROM phase1_deployment.schema_migrations WHERE migration_path='${recoveryPath}'`);
  const driftDigest = originalDigest === '0'.repeat(64) ? '1'.repeat(64) : '0'.repeat(64);
  psql(recoveryDatabase, `UPDATE phase1_deployment.schema_migrations SET sha256='${driftDigest}' WHERE migration_path='${recoveryPath}'`);
  const driftFailure = runMustFail(docker, migratorArgs(), 'migration drift detected');
  psql(recoveryDatabase, `UPDATE phase1_deployment.schema_migrations SET sha256='${originalDigest}' WHERE migration_path='${recoveryPath}'`);

  const incompatibleFailure = runMustFail(docker, preflightArgs('0.0.0-incompatible'), 'product version mismatch');
  run(docker, preflightArgs());

  const seedCounts = {
    policies: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.policies')),
    roleBindings: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.role_bindings')),
    alarmPermissions: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.alarm_permissions')),
    workOrderPermissions: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.work_order_permissions')),
    telemetryScopeBindings: Number(psql('hvac_s1', 'SELECT count(*) FROM iam.telemetry_scope_bindings')),
  };
  for (const [name, count] of Object.entries(seedCounts)) {
    if (count !== 0) throw new Error(`production migration unexpectedly seeded ${name}: ${count}`);
  }

  report.assertions = {
    exactAllowlistedMigrationsApplied: true,
    migrationCounts,
    productSchemaState: productStates,
    secondRunAlreadyApplied: true,
    incompleteStateFailsClosed: Boolean(incompleteFailure),
    digestDriftFailsClosed: Boolean(driftFailure),
    incompatibleProductVersionRejected: Boolean(incompatibleFailure),
    exactProductVersionPreflightPasses: true,
    productionFixtureSeedsExcluded: seedCounts,
  };
  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Phase 1 Product/Schema migration integration passed: ${reportPath}`);
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
