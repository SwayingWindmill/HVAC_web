import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const suffix = `${process.pid}-${Date.now()}`;
const network = `hvac-s2-migrator-${suffix}`;
const databaseContainer = `hvac-s2-migrator-db-${suffix}`;
const migratorImage = `hvac/s2-telemetry-runtime-migrator:smoke-${suffix}`;
const postgresImage = 'postgres:16-alpine@sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777';
const migrations = [
  '001-s2-telemetry-baseline.sql',
  '002-s2-telemetry-runtime-snapshot.sql',
  '003-s2-telemetry-ingest.sql',
  '004-s2-telemetry-history-outbox.sql',
  '005-s2-realtime-backend.sql',
];
const reportPath = resolve(root, 'out/s2-telemetry-release/migrator-image-smoke.json');

const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function execute(args, { allowFailure = false, quiet = false } = {}) {
  const result = spawnSync('docker', args, {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
  if (!quiet && output.trim()) process.stdout.write(output);
  if (!allowFailure && result.status !== 0) {
    throw new Error(`docker ${args.join(' ')} failed with status ${result.status}:\n${output}`);
  }
  return { status: result.status ?? 1, stdout: result.stdout ?? '', stderr: result.stderr ?? '', output };
}

async function waitForPostgres() {
  let consecutiveReadyChecks = 0;
  for (let attempt = 1; attempt <= 120; attempt += 1) {
    const result = execute([
      'exec', databaseContainer,
      'psql', '-U', 'postgres', '-d', 'hvac_s2', '-At', '-c', 'SELECT 1',
    ], { allowFailure: true, quiet: true });
    if (result.status === 0 && result.stdout.trim() === '1') {
      consecutiveReadyChecks += 1;
      if (consecutiveReadyChecks >= 4) return;
    } else {
      consecutiveReadyChecks = 0;
    }
    await pause(500);
  }
  throw new Error('PostgreSQL smoke container did not reach stable readiness');
}

function runMigrator({ allowFailure = false, user = 's2_telemetry_migrator_service' } = {}) {
  return execute([
    'run', '--rm',
    '--network', network,
    '-e', `PGHOST=${databaseContainer}`,
    '-e', 'PGDATABASE=hvac_s2',
    '-e', `PGUSER=${user}`,
    migratorImage,
  ], { allowFailure });
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

try {
  execute(['build', '-f', 'deploy/s2/images/telemetry-runtime-migrator.Dockerfile', '-t', migratorImage, '.']);
  execute(['network', 'create', network]);
  execute([
    'run', '-d',
    '--name', databaseContainer,
    '--network', network,
    '-e', 'POSTGRES_HOST_AUTH_METHOD=trust',
    '-e', 'POSTGRES_DB=hvac_s2',
    postgresImage,
  ]);
  await waitForPostgres();

  execute(['cp', 'infra/s2-telemetry/postgres/init/000-bootstrap-identities.sql', `${databaseContainer}:/tmp/000-bootstrap-identities.sql`]);
  execute(['exec', databaseContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-f', '/tmp/000-bootstrap-identities.sql']);

  const imageUser = execute(['image', 'inspect', '--format', '{{.Config.User}}', migratorImage], { quiet: true }).stdout.trim();
  assert(imageUser === 'postgres', `migrator image user drifted: ${imageUser}`);
  execute(['run', '--rm', '--entrypoint', 'sh', migratorImage, '-c', 'test ! -e /usr/local/bin/gosu']);
  const psqlVersion = execute(['run', '--rm', '--entrypoint', 'psql', migratorImage, '--version'], { quiet: true }).stdout.trim();
  assert(/^psql \(PostgreSQL\) 16\./.test(psqlVersion), `migrator psql major version drifted: ${psqlVersion}`);

  const unauthorized = runMigrator({ allowFailure: true, user: 's2_telemetry_service' });
  assert(unauthorized.status !== 0, 'runtime service identity executed the migrator');
  assert(unauthorized.output.includes('permission denied to set role "s2_telemetry_migrator"'), 'unauthorized migrator failure did not enforce the role boundary');

  const first = runMigrator();
  for (const migration of migrations) {
    assert(first.stdout.includes(`applying ${migration}`), `first migrator run did not apply ${migration}`);
  }

  const second = runMigrator();
  for (const migration of migrations) {
    assert(second.stdout.includes(`already applied ${migration}`), `second migrator run did not skip ${migration}`);
  }
  assert(!second.stdout.includes('applying '), 'second migrator run replayed an applied migration');

  const ledger = execute([
    'exec', databaseContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-At', '-c',
    "SELECT migration_name || ':' || sha256 || ':' || status FROM telemetry_runtime.schema_migrations ORDER BY migration_name",
  ], { quiet: true }).stdout.trim().split(/\r?\n/).filter(Boolean);
  assert(ledger.length === migrations.length, `migration ledger count drifted: ${ledger.length}`);
  for (const [index, entry] of ledger.entries()) {
    assert(entry.startsWith(`${migrations[index]}:`), `migration ledger order/name drifted: ${entry}`);
    assert(/:[a-f0-9]{64}:APPLIED$/.test(entry), `migration ledger hash/status is invalid: ${entry}`);
  }
  const firstMigrationSha256 = ledger[0].split(':')[1];

  const ledgerPrivileges = execute([
    'exec', databaseContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-At', '-c',
    "SELECT has_table_privilege('s2_telemetry_runtime','telemetry_runtime.schema_migrations','SELECT'), has_table_privilege('s2_telemetry_relay','telemetry_runtime.schema_migrations','SELECT'), has_table_privilege('s2_telemetry_migrator','telemetry_runtime.schema_migrations','SELECT')",
  ], { quiet: true }).stdout.trim();
  assert(ledgerPrivileges === 'f|f|t', `migration ledger privilege boundary drifted: ${ledgerPrivileges}`);

  const schemaReady = execute([
    'exec', databaseContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-At', '-c',
    "SELECT to_regclass('telemetry_runtime.latest_accepted_telemetry') IS NOT NULL AND to_regclass('telemetry_runtime.telemetry_history_outbox') IS NOT NULL AND to_regclass('telemetry_runtime.telemetry_publication_outbox') IS NOT NULL AND to_regclass('telemetry_runtime.schema_migrations') IS NOT NULL",
  ], { quiet: true }).stdout.trim();
  assert(schemaReady === 't', 'migrator did not create the required runtime and realtime schema');

  execute([
    'exec', databaseContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-c',
    "UPDATE telemetry_runtime.schema_migrations SET sha256 = repeat('0', 64) WHERE migration_name = '001-s2-telemetry-baseline.sql'",
  ]);
  const mismatch = runMigrator({ allowFailure: true });
  assert(mismatch.status !== 0, 'migrator accepted a migration hash mismatch');
  assert(mismatch.output.includes('migration hash mismatch for 001-s2-telemetry-baseline.sql'), 'migrator hash mismatch diagnostic is missing the migration name');
  assert(mismatch.output.includes('ERROR:  migration hash mismatch'), 'migrator hash mismatch did not raise a PostgreSQL error');

  execute([
    'exec', databaseContainer, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-c',
    `UPDATE telemetry_runtime.schema_migrations SET sha256 = '${firstMigrationSha256}', status = 'APPLYING', applied_at = NULL WHERE migration_name = '001-s2-telemetry-baseline.sql'`,
  ]);
  const incomplete = runMigrator({ allowFailure: true });
  assert(incomplete.status !== 0, 'migrator replayed an incomplete migration');
  assert(incomplete.output.includes('incomplete migration requires operator review: 001-s2-telemetry-baseline.sql'), 'incomplete migration diagnostic is missing the migration name');
  assert(incomplete.output.includes('ERROR:  incomplete migration requires operator review'), 'incomplete migration did not raise a PostgreSQL error');

  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify({
    schemaVersion: 1,
    status: 'passed',
    image: migratorImage,
    imageUser,
    psqlVersion,
    postgresImage,
    migrations,
    migrationUser: 's2_telemetry_migrator_service',
    unauthorizedRuntimeIdentityRejected: true,
    firstRunApplied: migrations.length,
    secondRunSkipped: migrations.length,
    ledgerPrivileges,
    hashMismatchRejected: true,
    incompleteMigrationRejected: true,
    gosuPresent: false,
  }, null, 2)}\n`);
  console.log(`S2 migrator image smoke test passed: ${reportPath}`);
} finally {
  execute(['rm', '-f', databaseContainer], { allowFailure: true, quiet: true });
  execute(['network', 'rm', network], { allowFailure: true, quiet: true });
  execute(['image', 'rm', '-f', migratorImage], { allowFailure: true, quiet: true });
}
