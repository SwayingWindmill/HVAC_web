import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s1-registry/compose.yaml');
const projectName = `hvac-s1-registry-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, 'out/s1-ticket-01/postgres-baseline.json');
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const composeInvocation = (() => {
  const plugin = spawnSync('docker', ['compose', 'version'], { stdio: 'ignore', windowsHide: true });
  if (!plugin.error && plugin.status === 0) return { command: 'docker', prefix: ['compose'] };
  return { command: 'docker-compose', prefix: [] };
})();

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
const composeEnvironment = { ...process.env, S1_POSTGRES_HOST_PORT: String(postgresHostPort) };

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
      const state = psql("SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('core_registry.organizations') IS NOT NULL)::text || '|' || (to_regclass('iam.principals') IS NOT NULL)::text");
      const [postmasterStart, coreReady, iamReady] = state.split('|');
      if (coreReady === 'true' && iamReady === 'true') {
        if (postmasterStart === stablePostmasterStart) stableChecks += 1;
        else {
          stablePostmasterStart = postmasterStart;
          stableChecks = 1;
        }
        if (stableChecks >= 5) return;
      } else {
        stableChecks = 0;
      }
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  throw new Error('S1 PostgreSQL fixture did not reach a stable initialized postmaster');
}

function expectEqual(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

function scopedCounts(organizationIDs, siteIDs) {
  return psql(`
    BEGIN;
    SET LOCAL ROLE s1_core_runtime;
    SET LOCAL app.authorized_organization_ids = '${organizationIDs}';
    SET LOCAL app.authorized_site_ids = '${siteIDs}';
    SELECT (SELECT count(*) FROM core_registry.organizations)::text || '|'
      || (SELECT count(*) FROM core_registry.sites)::text || '|'
      || (SELECT count(*) FROM core_registry.equipment)::text || '|'
      || (SELECT count(*) FROM core_registry.devices)::text;
    ROLLBACK;
  `).split('|').map(Number);
}

async function runIAMGoTests() {
  await mkdir(goCacheDir, { recursive: true });
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
    },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`S1 IAM PostgreSQL tests failed: ${signal ?? code}`);
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

  const roleState = psql(`
    SELECT string_agg(rolname || ':' || rolcanlogin::text || ':' || rolbypassrls::text, ',' ORDER BY rolname)
    FROM pg_roles
    WHERE rolname IN ('s1_iam_runtime','s1_iam_reconciler','s1_core_runtime','s1_iam_migrator','s1_core_migrator','s1_migration_operator')
  `);
  for (const role of ['s1_iam_runtime', 's1_iam_reconciler', 's1_iam_migrator']) {
    if (!roleState.includes(`${role}:true:false`)) throw new Error(`${role} must be LOGIN and NOBYPASSRLS`);
  }
  for (const role of ['s1_core_runtime', 's1_core_migrator', 's1_migration_operator']) {
    if (!roleState.includes(`${role}:false:false`)) throw new Error(`${role} must remain NOLOGIN and NOBYPASSRLS`);
  }
  report.assertions.runtimeRoles = roleState;

  const ownerA = scopedCounts('{018f1e00-0000-7000-8000-000000000001}', '{}');
  const delegated = scopedCounts('{}', '{018f1e00-1000-7000-8000-000000000001}');
  const noAccess = scopedCounts('{}', '{}');
  expectEqual(ownerA.join('|'), '1|2|2|2', 'owner Organization scope');
  expectEqual(delegated.join('|'), '0|1|1|1', 'cross-organization Site scope');
  expectEqual(noAccess.join('|'), '0|0|0|0', 'empty scope');
  report.assertions.rlsCounts = { ownerA, delegated, noAccess };

  const iamDelegated = psql(`
    BEGIN;
    SET LOCAL ROLE s1_iam_runtime;
    SET LOCAL app.principal_id = '018f1e00-2000-7000-8000-000000000002';
    SET LOCAL app.acting_organization_id = '018f1e00-0000-7000-8000-000000000003';
    SELECT (SELECT count(*) FROM iam.organization_memberships)::text || '|'
      || (SELECT count(*) FROM iam.role_bindings)::text || '|'
      || (SELECT count(*) FROM iam.site_bindings)::text || '|'
      || (SELECT count(*) FROM iam.explicit_denies)::text;
    ROLLBACK;
  `);
  const iamDenied = psql(`
    BEGIN;
    SET LOCAL ROLE s1_iam_runtime;
    SET LOCAL app.principal_id = '018f1e00-2000-7000-8000-000000000003';
    SET LOCAL app.acting_organization_id = '018f1e00-0000-7000-8000-000000000003';
    SELECT count(*) FROM iam.explicit_denies;
    ROLLBACK;
  `);
  expectEqual(iamDelegated, '1|1|1|0', 'delegated IAM fixture');
  expectEqual(iamDenied, '1', 'explicit deny fixture');
  report.assertions.iamFixtures = { delegated: iamDelegated, denied: iamDenied };

  await runIAMGoTests();
  report.assertions.iamAuthorizationStore = 'passed';

  const invalidTimezone = psql(`
    INSERT INTO core_registry.sites (id, organization_id, code, display_name, timezone, status, revision, created_at, updated_at)
    VALUES ('018f1e00-1000-7000-8000-000000000099', '018f1e00-0000-7000-8000-000000000001', 'invalid-timezone', 'Invalid Timezone', 'Mars/Olympus', 'ACTIVE', 1, now(), now())
  `, { expectFailure: true });
  if (!invalidTimezone.includes('invalid IANA timezone')) throw new Error('IANA timezone rejection did not emit the expected evidence');
  report.assertions.invalidTimezoneRejected = true;

  const duplicateExternal = psql(`
    INSERT INTO core_registry.external_bindings (id, organization_id, site_id, integration_instance_id, provider, external_entity_type, external_id, binding_status, valid_from, valid_to, revision, created_at, updated_at)
    VALUES ('018f1e00-6000-7000-8000-000000000099', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-6100-7000-8000-000000000001', 'thingsboard', 'DEVICE', 'tb-device-owner-a-1', 'ACTIVE', now(), NULL, 1, now(), now())
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
    SELECT id, organization_id, site_id, code, display_name
    FROM core_registry.equipment
    WHERE organization_id = '018f1e00-0000-7000-8000-000000000001'
      AND site_id = '018f1e00-1000-7000-8000-000000000001'
      AND (display_name COLLATE "C", id) > ('', '00000000-0000-0000-0000-000000000000')
    ORDER BY display_name COLLATE "C", id
    LIMIT 51;
  `);
  if (!plan.includes('equipment_registry_page_idx')) throw new Error('Equipment keyset query did not use the tenant-leading index');
  report.assertions.queryPlanIndex = 'equipment_registry_page_idx';

  const schemaOwners = psql("SELECT nspname || ':' || pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname IN ('iam','core_registry') ORDER BY nspname");
  expectEqual(schemaOwners, 'core_registry:s1_core_migrator\niam:s1_iam_migrator', 'schema owners');
  report.assertions.schemaOwners = schemaOwners.split('\n');

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(resolve(root, 'out/s1-ticket-01'), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S1 Registry PostgreSQL baseline passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(resolve(root, 'out/s1-ticket-01'), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
