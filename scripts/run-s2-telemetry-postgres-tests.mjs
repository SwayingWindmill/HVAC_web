import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s2-telemetry/compose.yaml');
const projectName = `hvac-s2-telemetry-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const redisContainerName = `${projectName}-latest-redis-1`;
const reportPath = resolve(root, process.env.S2_TELEMETRY_REPORT_PATH ?? 'out/s2-telemetry-baseline/postgres-baseline.json');
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
  if (!address || typeof address === 'string') throw new Error('S2 PostgreSQL port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort();
const latestRedisHostPort = await findAvailablePort();
const composeEnvironment = {
  ...process.env,
  S2_POSTGRES_HOST_PORT: String(postgresHostPort),
  S2_LATEST_REDIS_HOST_PORT: String(latestRedisHostPort),
};

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

function redisCli(args) {
  return run('docker', ['exec', redisContainerName, 'redis-cli', ...args]);
}

function psql(sql, { expectFailure = false } = {}) {
  const result = spawnSync('docker', [
    'exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql,
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  const stdout = String(result.stdout ?? '').trim();
  const stderr = String(result.stderr ?? '').trim();
  if (expectFailure) {
    if (!result.error && result.status === 0) throw new Error(`SQL unexpectedly succeeded: ${sql}`);
    return `${stdout}\n${stderr}`.trim();
  }
  if (result.error || result.status !== 0) {
    throw new Error(`SQL failed: ${result.error?.message ?? stderr ?? result.status}\n${sql}`);
  }
  return stdout;
}

async function waitForRedis() {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    try {
      if (redisCli(['ping']) === 'PONG') return;
    } catch {}
    await pause(250);
  }
  const logs = (() => {
    try { return compose(['logs', '--no-color', 'latest-redis']); } catch (error) { return String(error); }
  })();
  throw new Error(`S2 Latest Redis fixture did not initialize\n${logs}`);
}

async function waitForPostgres() {
  let stableStart = '';
  let stableChecks = 0;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('telemetry_runtime.device_observation_snapshots') IS NOT NULL)::text || '|' || (to_regclass('telemetry_runtime.telemetry_publication_outbox') IS NOT NULL)::text || '|' || (EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'telemetry_runtime' AND table_name = 'telemetry_publication_outbox' AND column_name = 'latest_cache_state'))::text");
      const [startedAt, snapshotReady, outboxReady, latestCacheReady] = state.split('|');
      if (snapshotReady === 'true' && outboxReady === 'true' && latestCacheReady === 'true') {
        if (startedAt === stableStart) stableChecks += 1;
        else {
          stableStart = startedAt;
          stableChecks = 1;
        }
        if (stableChecks >= 4) return;
      } else {
        stableChecks = 0;
      }
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  const logs = (() => {
    try { return compose(['logs', '--no-color', 'postgres']); } catch (error) { return String(error); }
  })();
  throw new Error(`S2 PostgreSQL fixture did not initialize\n${logs}`);
}

function expectEqual(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

function expectPermissionDenied(sql, label) {
  const failure = psql(sql, { expectFailure: true }).toLowerCase();
  if (!failure.includes('permission denied') && !failure.includes('must be member of role')) {
    throw new Error(`${label}: expected permission denial, got ${failure}`);
  }
  return true;
}

const report = {
  schemaVersion: 1,
  ticket: 60,
  status: 'failed',
  startedAt: new Date().toISOString(),
  postgresImage: 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412',
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres', 'latest-redis']);
  await Promise.all([waitForPostgres(), waitForRedis()]);

  const roleState = psql(`
    SELECT string_agg(rolname || ':' || rolcanlogin::text || ':' || rolinherit::text || ':' || rolbypassrls::text, ',' ORDER BY rolname)
    FROM pg_roles
    WHERE rolname LIKE 's2_telemetry_%'
  `);
  for (const role of ['s2_telemetry_migrator', 's2_telemetry_runtime', 's2_telemetry_relay']) {
    if (!roleState.includes(`${role}:false:false:false`)) throw new Error(`${role} must be NOLOGIN, NOINHERIT and NOBYPASSRLS`);
  }
  for (const role of ['s2_telemetry_migrator_service', 's2_telemetry_service', 's2_telemetry_gateway', 's2_telemetry_iam', 's2_telemetry_relay_service']) {
    if (!roleState.includes(`${role}:true:false:false`)) throw new Error(`${role} must be LOGIN, NOINHERIT and NOBYPASSRLS`);
  }
  report.assertions.roleState = roleState;

  const memberships = psql(`
    SELECT pg_has_role('s2_telemetry_migrator_service', 's2_telemetry_migrator', 'MEMBER')::text || '|'
      || pg_has_role('s2_telemetry_service', 's2_telemetry_runtime', 'MEMBER')::text || '|'
      || pg_has_role('s2_telemetry_relay_service', 's2_telemetry_relay', 'MEMBER')::text || '|'
      || pg_has_role('s2_telemetry_gateway', 's2_telemetry_runtime', 'MEMBER')::text || '|'
      || pg_has_role('s2_telemetry_iam', 's2_telemetry_runtime', 'MEMBER')::text
  `);
  expectEqual(memberships, 'true|true|true|false|false', 'role memberships');
  report.assertions.roleMemberships = memberships;

  const schemaOwner = psql("SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'telemetry_runtime'");
  expectEqual(schemaOwner, 's2_telemetry_migrator', 'schema owner');
  report.assertions.schemaOwner = schemaOwner;

  const tableState = psql(`
    SELECT count(*)::text || '|'
      || count(*) FILTER (WHERE c.relrowsecurity)::text || '|'
      || count(*) FILTER (WHERE c.relforcerowsecurity)::text
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'telemetry_runtime' AND c.relkind = 'r'
  `);
  expectEqual(tableState, '18|18|18', 'table/RLS baseline');
  report.assertions.tableRlsState = tableState;

  const deliveryEvidenceRls = psql(`
    SELECT c.relrowsecurity::text || '|'
      || c.relforcerowsecurity::text || '|'
      || string_agg(p.polname, ',' ORDER BY p.polname)
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_policy p ON p.polrelid = c.oid
    WHERE n.nspname = 'telemetry_runtime'
      AND c.relname = 'source_delivery_evidence'
    GROUP BY c.relrowsecurity, c.relforcerowsecurity
  `);
  expectEqual(
    deliveryEvidenceRls,
    'true|true|source_delivery_evidence_migrator_all,source_delivery_evidence_runtime_all',
    'delivery evidence RLS baseline',
  );
  report.assertions.deliveryEvidenceRls = deliveryEvidenceRls;

  const fixtureTenancy = psql(`
    SELECT count(DISTINCT tenant_id)::text || '|'
      || count(DISTINCT site_id)::text || '|'
      || count(*) FILTER (WHERE binding_status = 'ACTIVE')::text || '|'
      || count(*) FILTER (WHERE binding_status = 'QUARANTINED')::text
    FROM telemetry_runtime.registry_device_bindings
  `);
  expectEqual(fixtureTenancy, '2|3|3|1', 'two-Tenant/multi-Site bindings');
  report.assertions.fixtureTenancy = fixtureTenancy;

  const iamFixture = psql(`
    SELECT count(DISTINCT tenant_id)::text || '|'
      || count(*) FILTER (WHERE decision = 'DENY' AND site_id = '018f2e00-1000-7000-8000-000000000002')::text
    FROM telemetry_runtime.iam_scope_projections
  `);
  expectEqual(iamFixture, '2|1', 'multi-Tenant and sibling-Site fixture');
  report.assertions.iamFixture = iamFixture;

  const keyStates = psql(`
    SELECT (snapshot #>> '{values,0,state}') || '|'
      || (snapshot #>> '{values,1,missingReason}') || '|'
      || (snapshot #>> '{values,2,missingReason}')
    FROM telemetry_runtime.device_observation_snapshots
    WHERE device_id = '018f2e00-3000-7000-8000-000000000001'
  `);
  expectEqual(keyStates, 'PRESENT|NEVER_OBSERVED|ONLY_REJECTED_CANDIDATES', 'configured/missing/rejected key fixture');
  report.assertions.keyStates = keyStates;

  const quarantine = psql("SELECT reason_code || '|' || (device_id IS NULL)::text FROM telemetry_runtime.ingest_quarantine WHERE external_id = 'tb-conflicted-asset'");
  expectEqual(quarantine, 'MAPPING_CONFLICT|true', 'ExternalBinding conflict quarantine');
  report.assertions.externalBindingConflict = quarantine;

  const duplicateBinding = psql(`
    INSERT INTO telemetry_runtime.registry_device_bindings (
      tenant_id, device_id, site_id, integration_instance_id,
      external_entity_type, external_id, binding_status, binding_revision,
      source_registry_revision, valid_from, valid_to, updated_at
    ) VALUES (
      '018f2d00-0000-7000-8000-000000000001',
      '018f2e00-3000-7000-8000-000000000099',
      '018f2e00-1000-7000-8000-000000000001',
      '018f2e00-6000-7000-8000-000000000001',
      'DEVICE', 'tb-device-org-a-site-1', 'ACTIVE', 1, 12,
      '2026-07-23T01:00:00Z', NULL, '2026-07-23T01:00:00Z'
    )
  `, { expectFailure: true });
  if (!duplicateBinding.includes('registry_device_bindings_active_external_key_uidx')) {
    throw new Error('active ExternalBinding uniqueness did not reject a duplicate');
  }
  report.assertions.activeBindingUnique = true;

  const validCounterBinding = psql(`
    BEGIN;
    SET LOCAL ROLE s2_telemetry_migrator;
    INSERT INTO telemetry_runtime.registry_point_bindings (
      projection_id, tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key,
      point_type, value_type, unit, counter_decrease_mode, counter_rollover_modulus,
      binding_status, point_revision, source_registry_revision, valid_from, valid_to, updated_at
    ) VALUES (
      '01990000-1200-7000-8000-000000000001',
      '018f2d00-0000-7000-8000-000000000001',
      '018f2e00-1000-7000-8000-000000000001',
      '01990000-1210-7000-8000-000000000001', NULL,
      '018f2e00-3000-7000-8000-000000000001', 'meter.energy_total',
      'COUNTER', 'NUMBER', 'kWh', 'ROLLOVER', 1000000,
      'ACTIVE', 1, 12, now(), NULL, now()
    );
    SELECT point_type || '|' || counter_decrease_mode || '|' || counter_rollover_modulus::text
    FROM telemetry_runtime.registry_point_bindings
    WHERE projection_id = '01990000-1200-7000-8000-000000000001';
    ROLLBACK;
  `);
  expectEqual(validCounterBinding, 'COUNTER|ROLLOVER|1000000', 'valid runtime Counter binding semantics');
  const counterBindingMissingMode = psql(`
    BEGIN;
    SET LOCAL ROLE s2_telemetry_migrator;
    INSERT INTO telemetry_runtime.registry_point_bindings (
      projection_id, tenant_id, site_id, point_id, device_id, telemetry_key,
      point_type, value_type, unit, binding_status, point_revision, source_registry_revision, valid_from, updated_at
    ) VALUES (
      '01990000-1200-7000-8000-000000000002',
      '018f2d00-0000-7000-8000-000000000001',
      '018f2e00-1000-7000-8000-000000000001',
      '01990000-1210-7000-8000-000000000002',
      '018f2e00-3000-7000-8000-000000000001', 'meter.energy_total_missing_mode',
      'COUNTER', 'NUMBER', 'kWh', 'ACTIVE', 1, 12, now(), now()
    );
  `, { expectFailure: true });
  if (!counterBindingMissingMode.includes('registry_point_bindings_counter_semantics_check')) throw new Error(`runtime Counter without mode was accepted: ${counterBindingMissingMode}`);
  const counterBindingRolloverMissingModulus = psql(`
    BEGIN;
    SET LOCAL ROLE s2_telemetry_migrator;
    INSERT INTO telemetry_runtime.registry_point_bindings (
      projection_id, tenant_id, site_id, point_id, device_id, telemetry_key,
      point_type, value_type, unit, counter_decrease_mode,
      binding_status, point_revision, source_registry_revision, valid_from, updated_at
    ) VALUES (
      '01990000-1200-7000-8000-000000000003',
      '018f2d00-0000-7000-8000-000000000001',
      '018f2e00-1000-7000-8000-000000000001',
      '01990000-1210-7000-8000-000000000003',
      '018f2e00-3000-7000-8000-000000000001', 'meter.water_total_rollover',
      'COUNTER', 'NUMBER', 'm3', 'ROLLOVER',
      'ACTIVE', 1, 12, now(), now()
    );
  `, { expectFailure: true });
  if (!counterBindingRolloverMissingModulus.includes('registry_point_bindings_counter_semantics_check')) throw new Error(`runtime rollover Counter without modulus was accepted: ${counterBindingRolloverMissingModulus}`);
  const nonCounterRuntimeMode = psql(`
    BEGIN;
    SET LOCAL ROLE s2_telemetry_migrator;
    INSERT INTO telemetry_runtime.registry_point_bindings (
      projection_id, tenant_id, site_id, point_id, device_id, telemetry_key,
      point_type, value_type, unit, counter_decrease_mode,
      binding_status, point_revision, source_registry_revision, valid_from, updated_at
    ) VALUES (
      '01990000-1200-7000-8000-000000000004',
      '018f2d00-0000-7000-8000-000000000001',
      '018f2e00-1000-7000-8000-000000000001',
      '01990000-1210-7000-8000-000000000004',
      '018f2e00-3000-7000-8000-000000000001', 'meter.active_power_with_counter_mode',
      'TELEMETRY', 'NUMBER', 'kW', 'RESET_TO_ZERO',
      'ACTIVE', 1, 12, now(), now()
    );
  `, { expectFailure: true });
  if (!nonCounterRuntimeMode.includes('registry_point_bindings_counter_semantics_check')) throw new Error(`non-Counter runtime binding accepted Counter mode: ${nonCounterRuntimeMode}`);
  report.assertions.counterBindingSemantics = {
    valid: validCounterBinding,
    missingModeRejected: true,
    rolloverWithoutModulusRejected: true,
    nonCounterModeRejected: true,
  };

  expectPermissionDenied(`
    SET SESSION AUTHORIZATION s2_telemetry_gateway;
    SELECT count(*) FROM telemetry_runtime.device_observation_snapshots;
  `, 'Gateway read isolation');
  expectPermissionDenied(`
    SET SESSION AUTHORIZATION s2_telemetry_gateway;
    SET ROLE s2_telemetry_runtime;
  `, 'Gateway runtime-role isolation');
  expectPermissionDenied(`
    SET SESSION AUTHORIZATION s2_telemetry_iam;
    INSERT INTO telemetry_runtime.ingest_quarantine (
      quarantine_id, integration_instance_id, external_entity_type, external_id,
      reason_code, evidence, detected_at
    ) VALUES (
      '018f2e00-8200-7000-8000-000000000099',
      '018f2e00-6000-7000-8000-000000000001',
      'DEVICE', 'forbidden-iam-write', 'SOURCE_UNTRUSTED', '{}'::jsonb, now()
    );
  `, 'IAM write isolation');
  report.assertions.gatewayIamIsolation = true;

  expectPermissionDenied(`
    SET SESSION AUTHORIZATION s2_telemetry_service;
    SELECT count(*) FROM telemetry_runtime.device_observation_snapshots;
  `, 'runtime login requires explicit activation');
  const runtimeActivated = psql(`
    SET SESSION AUTHORIZATION s2_telemetry_service;
    BEGIN;
    SET LOCAL ROLE s2_telemetry_runtime;
    INSERT INTO telemetry_runtime.ingest_quarantine (
      quarantine_id, integration_instance_id, external_entity_type, external_id,
      reason_code, evidence, detected_at
    ) VALUES (
      '018f2e00-8200-7000-8000-000000000098',
      '018f2e00-6000-7000-8000-000000000001',
      'DEVICE', 'runtime-authorized-write', 'SOURCE_UNTRUSTED', '{}'::jsonb, now()
    );
    SELECT count(*) FROM telemetry_runtime.ingest_quarantine WHERE external_id = 'runtime-authorized-write';
    ROLLBACK;
  `);
  expectEqual(runtimeActivated, '1', 'runtime activation write');
  report.assertions.runtimeActivation = true;

  expectPermissionDenied(`
    SET SESSION AUTHORIZATION s2_telemetry_relay_service;
    SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox;
  `, 'relay login requires explicit activation');
  const relayRead = psql(`
    SET SESSION AUTHORIZATION s2_telemetry_relay_service;
    BEGIN;
    SET LOCAL ROLE s2_telemetry_relay;
    SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox;
    ROLLBACK;
  `);
  expectEqual(relayRead, '1', 'relay outbox read');
  expectPermissionDenied(`
    SET SESSION AUTHORIZATION s2_telemetry_relay_service;
    SET ROLE s2_telemetry_relay;
    UPDATE telemetry_runtime.telemetry_publication_outbox
    SET payload = '{}'::jsonb
    WHERE event_id = '018f2e00-8400-7000-8000-000000000001';
  `, 'relay payload mutation isolation');
  expectPermissionDenied(`
    SET SESSION AUTHORIZATION s2_telemetry_relay_service;
    SET ROLE s2_telemetry_relay;
    INSERT INTO telemetry_runtime.latest_accepted_telemetry (
      device_id, telemetry_key, business_revision, value, value_type,
      sampled_at, received_at, freshness, quality, policy_revision, updated_at
    ) VALUES (
      '018f2e00-3000-7000-8000-000000000001', 'relay.forbidden', 1,
      '1'::jsonb, 'NUMBER', now(), now(), 'FRESH', 'GOOD', 1, now()
    );
  `, 'relay business write isolation');
  const relayUpdate = psql(`
    SET SESSION AUTHORIZATION s2_telemetry_relay_service;
    BEGIN;
    SET LOCAL ROLE s2_telemetry_relay;
    UPDATE telemetry_runtime.telemetry_publication_outbox
    SET attempts = attempts + 1, last_error_code = 'FIXTURE_RETRY'
    WHERE event_id = '018f2e00-8400-7000-8000-000000000001';
    SELECT attempts || '|' || last_error_code FROM telemetry_runtime.telemetry_publication_outbox
    WHERE event_id = '018f2e00-8400-7000-8000-000000000001';
    ROLLBACK;
  `);
  expectEqual(relayUpdate, '1|FIXTURE_RETRY', 'relay delivery metadata update');

  const latestCacheSchema = psql(`
    SELECT latest_cache_state || '|'
      || (latest_cache_available_at IS NOT NULL)::text || '|'
      || latest_cache_attempts::text || '|'
      || (latest_cache_materialized_at IS NULL)::text
    FROM telemetry_runtime.telemetry_publication_outbox
    WHERE event_id = '018f2e00-8400-7000-8000-000000000001';
  `);
  expectEqual(latestCacheSchema, 'NOT_APPLICABLE|true|0|true', 'subscription publication must not enter Redis Latest outbox');
  const canonicalLatestCacheTrigger = psql(`
    BEGIN;
    INSERT INTO telemetry_runtime.telemetry_publication_outbox (
      event_id, device_id, business_revision, subscription_id, event_family,
      payload, payload_sha256, delivery_state, available_at, attempts,
      last_error_code, published_at, created_at
    ) VALUES (
      '018f2e00-8400-7000-8000-000000000099',
      '018f2e00-3000-7000-8000-000000000001', 99, NULL,
      'hvac.telemetry.device-snapshot.v1', '{}'::jsonb, repeat('a', 64),
      'PENDING', '2026-07-23T00:01:00Z', 0, NULL, NULL, '2026-07-23T00:01:00Z'
    );
    SELECT latest_cache_state || '|' || latest_cache_available_at::text
    FROM telemetry_runtime.telemetry_publication_outbox
    WHERE event_id = '018f2e00-8400-7000-8000-000000000099';
    ROLLBACK;
  `);
  if (!canonicalLatestCacheTrigger.startsWith('PENDING|2026-07-23 00:01:00')) throw new Error(`canonical snapshot outbox did not initialize Redis Latest materialization: ${canonicalLatestCacheTrigger}`);
  const latestCacheConstraints = psql(`
    SELECT string_agg(conname || ':' || pg_get_constraintdef(oid), E'\n' ORDER BY conname)
    FROM pg_constraint
    WHERE conrelid = 'telemetry_runtime.telemetry_publication_outbox'::regclass
      AND conname IN (
        'telemetry_publication_outbox_latest_cache_value_check',
        'telemetry_publication_outbox_latest_cache_materialized_check'
      );
  `);
  if (!latestCacheConstraints.includes("NOT_APPLICABLE") || !latestCacheConstraints.includes("MATERIALIZED") || !latestCacheConstraints.includes("latest_cache_materialized_at")) {
    throw new Error(`Redis Latest cache constraints are incomplete: ${latestCacheConstraints}`);
  }
  const invalidLatestCacheState = psql(`
    UPDATE telemetry_runtime.telemetry_publication_outbox
    SET latest_cache_state = 'INVALID'
    WHERE event_id = '018f2e00-8400-7000-8000-000000000001';
  `, { expectFailure: true });
  if (!invalidLatestCacheState.toLowerCase().includes('latest_cache')) throw new Error(`Redis Latest cache state constraint was not enforced: ${invalidLatestCacheState}`);
  const relayLatestCacheUpdate = psql(`
    SET SESSION AUTHORIZATION s2_telemetry_relay_service;
    BEGIN;
    SET LOCAL ROLE s2_telemetry_relay;
    UPDATE telemetry_runtime.telemetry_publication_outbox
    SET latest_cache_attempts = latest_cache_attempts + 1,
        latest_cache_last_error_code = 'FIXTURE_REDIS_RETRY',
        latest_cache_available_at = latest_cache_available_at + interval '1 second'
    WHERE event_id = '018f2e00-8400-7000-8000-000000000001';
    SELECT latest_cache_attempts || '|' || latest_cache_last_error_code
    FROM telemetry_runtime.telemetry_publication_outbox
    WHERE event_id = '018f2e00-8400-7000-8000-000000000001';
    ROLLBACK;
  `);
  expectEqual(relayLatestCacheUpdate, '1|FIXTURE_REDIS_RETRY', 'relay Redis Latest metadata update');
  report.assertions.relayIsolation = true;
  report.assertions.redisLatestOutbox = {
    schema: latestCacheSchema,
    invalidStateRejected: true,
    relayMetadataUpdate: relayLatestCacheUpdate,
  };

  report.assertions.redisLatestCAS = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/telemetry-runtime-service',
    'test', '-count=1', '-run', 'TestRedisLatestCacheNeverRegressesBusinessRevision', '-v', './internal/telemetry/...',
  ], {
    env: {
      ...process.env,
      S2_TELEMETRY_LATEST_REDIS_TEST_URL: `redis://127.0.0.1:${latestRedisHostPort}/0`,
    },
  });

  const migratorActivation = psql(`
    SET SESSION AUTHORIZATION s2_telemetry_migrator_service;
    BEGIN;
    SET LOCAL ROLE s2_telemetry_migrator;
    SELECT current_user || '|' || session_user;
    ROLLBACK;
  `);
  expectEqual(migratorActivation, 's2_telemetry_migrator|s2_telemetry_migrator_service', 'migrator activation');
  report.assertions.migratorActivation = true;

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S2 Telemetry PostgreSQL baseline passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
