import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s2-telemetry/compose.yaml');
const projectName = `hvac-phase1-restore-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, process.env.PHASE1_RESTORE_REPORT_PATH ?? 'out/phase1-acceptance/restore-report.json');

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('restore test port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const dockerPath = run('which', ['docker']);
if (!dockerPath.startsWith('/') || dockerPath.startsWith('/mnt/')) {
  throw new Error(`Linux Docker CLI is required; resolved=${dockerPath}`);
}
run(dockerPath, ['version']);
const postgresImage = 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412';
const imageProbe = spawnSync(dockerPath, ['image', 'inspect', postgresImage], { cwd: root, stdio: 'ignore', windowsHide: true });
if (imageProbe.status !== 0) {
  run(dockerPath, ['pull', postgresImage], { env: { ...process.env, DOCKER_CONFIG: `/tmp/hvac-phase1-restore-docker-${process.pid}` } });
}
const port = await findAvailablePort();
const env = { ...process.env, S2_POSTGRES_HOST_PORT: String(port) };
const compose = (args) => run('docker', ['compose', '-p', projectName, '-f', composePath, ...args], { env });
const psql = (database, sql) => run('docker', ['exec', containerName, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);

async function waitForDatabase(database) {
  let stableStart = '';
  let stableChecks = 0;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    const probe = spawnSync('docker', ['exec', containerName, 'pg_isready', '-U', 'postgres', '-d', database], { cwd: root, stdio: 'ignore', windowsHide: true });
    if (!probe.error && probe.status === 0) {
      const schemaProbe = spawnSync('docker', [
        'exec', containerName, 'psql', '-U', 'postgres', '-d', database, '-Atqc',
        "SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('telemetry_runtime.registry_device_bindings') IS NOT NULL)::text",
      ], { cwd: root, encoding: 'utf8', windowsHide: true });
      if (!schemaProbe.error && schemaProbe.status === 0) {
        const [startedAt, schemaReady] = String(schemaProbe.stdout).trim().split('|');
        if (schemaReady === 'true') {
          if (startedAt === stableStart) stableChecks += 1;
          else {
            stableStart = startedAt;
            stableChecks = 1;
          }
          if (stableChecks >= 4) return;
        } else {
          stableChecks = 0;
        }
      }
    } else {
      stableChecks = 0;
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 250));
  }
  throw new Error(`database ${database} did not become schema-ready on a stable postmaster`);
}

function databaseSnapshot(database) {
  const tables = psql(database, `
    SELECT c.relname
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'telemetry_runtime' AND c.relkind = 'r'
    ORDER BY c.relname
  `).split('\n').filter(Boolean);
  const counts = Object.fromEntries(tables.map((table) => {
    if (!/^[a-z0-9_]+$/.test(table)) throw new Error(`unexpected table name ${table}`);
    return [table, Number(psql(database, `SELECT count(*) FROM telemetry_runtime.${table}`))];
  }));
  const rls = psql(database, `
    SELECT count(*)::text || '|' || count(*) FILTER (WHERE c.relrowsecurity)::text || '|' || count(*) FILTER (WHERE c.relforcerowsecurity)::text
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'telemetry_runtime' AND c.relkind = 'r'
  `);
  const fixture = psql(database, `
    SELECT count(*)::text || '|'
      || count(DISTINCT owning_organization_id)::text || '|'
      || count(DISTINCT site_id)::text
    FROM telemetry_runtime.registry_device_bindings
  `);
  return { tables, counts, rls, fixture };
}

const report = {
  schemaVersion: 1,
  capability: 'phase1-postgres-backup-restore',
  status: 'failed',
  startedAt: new Date().toISOString(),
  isolatedEnvironment: true,
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', '--pull=never', 'postgres']);
  await waitForDatabase('hvac_s2');

  const before = databaseSnapshot('hvac_s2');
  if (before.tables.length < 10) throw new Error(`unexpected telemetry table count before backup: ${before.tables.length}`);

  run('docker', ['exec', containerName, 'pg_dump', '-U', 'postgres', '-d', 'hvac_s2', '-Fc', '-f', '/tmp/phase1-hvac-s2.dump']);
  const backupBytes = Number(run('docker', ['exec', containerName, 'stat', '-c', '%s', '/tmp/phase1-hvac-s2.dump']));
  if (!Number.isFinite(backupBytes) || backupBytes < 1024) throw new Error(`backup is unexpectedly small: ${backupBytes}`);

  psql('hvac_s2', "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='hvac_s2' AND pid <> pg_backend_pid()");
  run('docker', ['exec', containerName, 'dropdb', '-U', 'postgres', 'hvac_s2']);
  const missingProbe = spawnSync('docker', ['exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-Atqc', 'SELECT 1'], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (missingProbe.status === 0) throw new Error('database destruction was not observed');

  run('docker', ['exec', containerName, 'createdb', '-U', 'postgres', 'hvac_s2']);
  run('docker', ['exec', containerName, 'pg_restore', '-U', 'postgres', '-d', 'hvac_s2', '--no-owner', '--exit-on-error', '/tmp/phase1-hvac-s2.dump']);
  await waitForDatabase('hvac_s2');
  const after = databaseSnapshot('hvac_s2');

  const tableSetPreserved = JSON.stringify(before.tables) === JSON.stringify(after.tables);
  const rowCountsPreserved = JSON.stringify(before.counts) === JSON.stringify(after.counts);
  const rlsPreserved = before.rls === after.rls;
  const fixturePreserved = before.fixture === after.fixture;
  if (!tableSetPreserved || !rowCountsPreserved || !rlsPreserved || !fixturePreserved) {
    throw new Error(`restore mismatch: ${JSON.stringify({ tableSetPreserved, rowCountsPreserved, rlsPreserved, fixturePreserved, before, after })}`);
  }

  report.assertions = {
    backupCreated: true,
    backupBytes,
    databaseDestroyedBeforeRestore: true,
    tableSetPreserved,
    rowCountsPreserved,
    rlsPreserved,
    fixturePreserved,
    telemetryRuntimeTableCount: before.tables.length,
  };
  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Phase 1 PostgreSQL backup/restore passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
