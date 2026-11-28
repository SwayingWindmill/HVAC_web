import { spawnSync, type SpawnSyncOptions } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.ts';
import { findAvailablePort } from './lib/postgres-compose-harness.ts';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/telemetry/compose.yaml');
const projectName = `hvac-s2-telemetry-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const redisContainerName = `${projectName}-latest-redis-1`;
const reportPath = resolve(root, process.env.S2_TELEMETRY_REPORT_PATH ?? 'out/s2-telemetry-baseline/postgres-baseline.json');
const pause = (milliseconds: number): Promise<void> => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const postgresHostPort = await findAvailablePort('S2 PostgreSQL');
const latestRedisHostPort = await findAvailablePort('S2 Latest Redis');
const composeEnvironment = {
  ...process.env,
  S2_POSTGRES_HOST_PORT: String(postgresHostPort),
  S2_LATEST_REDIS_HOST_PORT: String(latestRedisHostPort),
};

function run(command: string, args: string[], options: SpawnSyncOptions = {}): string {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? String(result.stderr ?? '').trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args: string[]): string {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function redisCli(args: string[]): string {
  return run('docker', ['exec', redisContainerName, 'redis-cli', ...args]);
}

function psql(sql: string, { expectFailure = false }: { expectFailure?: boolean } = {}): string {
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
      const state = psql("SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('telemetry_runtime.device_observation_snapshots') IS NOT NULL)::text || '|' || (to_regclass('telemetry_runtime.telemetry_publication_outbox') IS NOT NULL)::text || '|' || (EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'telemetry_runtime' AND table_name = 'telemetry_publication_outbox' AND column_name = 'latest_cache_state'))::text || '|' || (EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'telemetry_runtime' AND table_name = 'telemetry_publication_outbox' AND column_name = 'alarm_dead_lettered_at'))::text");
      const [startedAt, snapshotReady, outboxReady, latestCacheReady, alarmDeadLetterReady] = state.split('|');
      if (snapshotReady === 'true' && outboxReady === 'true' && latestCacheReady === 'true' && alarmDeadLetterReady === 'true') {
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

function expectEqual(actual: string, expected: string, label: string): void {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

function expectPermissionDenied(sql: string, label: string): boolean {
  const failure = psql(sql, { expectFailure: true }).toLowerCase();
  if (!failure.includes('permission denied') && !failure.includes('must be member of role')) {
    throw new Error(`${label}: expected permission denial, got ${failure}`);
  }
  return true;
}

const report: {
  schemaVersion: number;
  ticket: number;
  status: string;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  postgresImage: string;
  assertions: Record<string, unknown>;
} = {
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
  expectEqual(tableState, '22|22|22', 'table/RLS baseline');
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
      || count(*)::text
    FROM telemetry_runtime.devices
  `);
  expectEqual(fixtureTenancy, '2|3|3', 'two-Tenant/multi-Site Devices');
  report.assertions.fixtureTenancy = fixtureTenancy;

  const keyStates = psql(`
    SELECT (snapshot #>> '{values,0,state}') || '|'
      || (snapshot #>> '{values,1,missingReason}') || '|'
      || (snapshot #>> '{values,2,missingReason}')
    FROM telemetry_runtime.device_observation_snapshots
    WHERE device_id = '018f2e00-3000-7000-8000-000000000001'
  `);
  expectEqual(keyStates, 'PRESENT|NEVER_OBSERVED|ONLY_REJECTED_CANDIDATES', 'configured/missing/rejected key fixture');
  report.assertions.keyStates = keyStates;

  const quarantine = psql("SELECT reason_code || '|' || (device_id IS NULL)::text FROM telemetry_runtime.ingest_quarantine WHERE external_id = 'mqtt-conflicted-asset'");
  expectEqual(quarantine, 'MAPPING_CONFLICT|true', 'Device scope conflict quarantine');
  report.assertions.deviceScopeConflict = quarantine;

  const crossTenantPoint = psql(`
    INSERT INTO telemetry_runtime.points (
      device_id, telemetry_key, tenant_id, site_id, point_id, point_type, value_type, unit, point_revision, updated_at
    ) VALUES (
      '018f2e00-3000-7000-8000-000000000001', 'zone.cross_tenant',
      '018f2d00-0000-7000-8000-000000000002', '018f2e00-1000-7000-8000-000000000003',
      '01990000-1210-7000-8000-000000000009', 'TELEMETRY', 'NUMBER', 'Cel', 1, now()
    )
  `, { expectFailure: true });
  if (!crossTenantPoint.includes('points_tenant_id_device_id_fkey')) {
    throw new Error(`Point outside its Device's Tenant was accepted: ${crossTenantPoint}`);
  }
  report.assertions.pointTenantMatchesDevice = true;

  const cbm = psql("BEGIN;SET LOCAL ROLE s2_telemetry_migrator;INSERT INTO telemetry_runtime.points(device_id,telemetry_key,tenant_id,site_id,point_id,sensor_id,point_type,value_type,unit,counter_decrease_mode,counter_rollover_modulus,point_revision,updated_at)VALUES('018f2e00-3000-7000-8000-000000000001','meter.energy_total','018f2d00-0000-7000-8000-000000000001','018f2e00-1000-7000-8000-000000000001','01990000-1210-7000-8000-000000000001',NULL,'COUNTER','NUMBER','kWh','ROLLOVER',1000000,1,now());SELECT point_type||'|'||counter_decrease_mode||'|'||counter_rollover_modulus::text FROM telemetry_runtime.points WHERE device_id='018f2e00-3000-7000-8000-000000000001' AND telemetry_key='meter.energy_total';ROLLBACK;");
  expectEqual(cbm, 'COUNTER|ROLLOVER|1000000', 'valid runtime Counter binding semantics');
  for (const [label, key, mode] of [
    ['missingMode', 'meter.energy_total_missing_mode', "'COUNTER','NUMBER','kWh',NULL,1"],
    ['rolloverNoMod', 'meter.water_total_rollover', "'COUNTER','NUMBER','m3','ROLLOVER',1"],
    ['nonCounterMode', 'meter.active_power_with_counter_mode', "'TELEMETRY','NUMBER','kW','RESET_TO_ZERO',1"],
  ]) {
    const out = psql(`BEGIN;SET LOCAL ROLE s2_telemetry_migrator;INSERT INTO telemetry_runtime.points(device_id,telemetry_key,tenant_id,site_id,point_id,point_type,value_type,unit,counter_decrease_mode,point_revision,updated_at)VALUES('018f2e00-3000-7000-8000-000000000001','${key}','018f2d00-0000-7000-8000-000000000001','018f2e00-1000-7000-8000-000000000001','01990000-1210-7000-8000-000000000002',${mode},now());`, { expectFailure: true });
    if (!out.includes('points_counter_semantics_check')) throw new Error(`${label} accepted: ${out}`);
  }
  report.assertions.counterBindingSemantics = {
    valid: cbm,
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
      quarantine_id, source_id, external_entity_type, external_id,
      reason_code, evidence, detected_at
    ) VALUES (
      '018f2e00-8200-7000-8000-000000000099',
      'gateway-a',
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
      quarantine_id, source_id, external_entity_type, external_id,
      reason_code, evidence, detected_at
    ) VALUES (
      '018f2e00-8200-7000-8000-000000000098',
      'gateway-a',
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

  const dlqMeta = psql(`SELECT (SELECT string_agg(pg_get_constraintdef(oid),'|') FROM pg_constraint WHERE conrelid='telemetry_runtime.telemetry_publication_outbox'::regclass AND conname IN ('telemetry_publication_outbox_alarm_delivery_state_check','telemetry_publication_outbox_alarm_published_check'))||'|'||(to_regclass('telemetry_runtime.telemetry_publication_outbox_alarm_dead_letter_idx') IS NOT NULL)::text||'|'||has_column_privilege('s2_telemetry_relay','telemetry_runtime.telemetry_publication_outbox','alarm_dead_lettered_at','UPDATE')::text||'|'||(SELECT coalesce(col_description('telemetry_runtime.telemetry_publication_outbox'::regclass,attnum),'') FROM pg_attribute WHERE attrelid='telemetry_runtime.telemetry_publication_outbox'::regclass AND attname='alarm_dead_lettered_at')`);
  if (!dlqMeta.includes('DEAD_LETTER') || !dlqMeta.includes('alarm_dead_lettered_at') || !dlqMeta.includes('|true|true|') || !/[\u4e00-\u9fff]/.test(dlqMeta)) {
    throw new Error(`Alarm DLQ schema/grant/comment check failed: ${dlqMeta}`);
  }
  const runTelemetryGo = (pattern: string, extraEnv: NodeJS.ProcessEnv): string => run(process.execPath, [
    '--experimental-strip-types', 'scripts/run-isolated-go.ts', '--module=modules/telemetry', 'test', '-count=1', '-run', pattern, '-v', './pkg/telemetry/...',
  ], { env: { ...process.env, ...extraEnv } });
  report.assertions.alarmEvaluationDeadLetter = { schemaAndGrantReady: true };
  report.assertions.redisLatestCAS = runTelemetryGo('Test(RedisLatestCacheNeverRegressesBusinessRevision|RebuildLatestCacheRestoresBusinessSnapshots)', {
    S2_TELEMETRY_LATEST_REDIS_TEST_URL: `redis://127.0.0.1:${latestRedisHostPort}/0`,
  });
  const edgeMeta = psql(`SELECT (SELECT string_agg(pg_get_constraintdef(oid),'|') FROM pg_constraint WHERE conrelid='telemetry_runtime.mqtt_runtime_events'::regclass AND conname LIKE 'mqtt_runtime_events_alarm_%')||'|'||(to_regclass('telemetry_runtime.mqtt_runtime_events_alarm_pending_idx') IS NOT NULL)::text||'|'||has_column_privilege('s2_telemetry_runtime','telemetry_runtime.mqtt_runtime_events','alarm_delivery_state','UPDATE')::text||'|'||(SELECT coalesce(col_description('telemetry_runtime.mqtt_runtime_events'::regclass,attnum),'') FROM pg_attribute WHERE attrelid='telemetry_runtime.mqtt_runtime_events'::regclass AND attname='alarm_delivery_state')`);
  if (!edgeMeta.includes('DEAD_LETTER') || !edgeMeta.includes('|true|true|') || !/[\u4e00-\u9fff]/.test(edgeMeta)) throw new Error(`Edge alarm delivery check failed: ${edgeMeta}`);
  report.assertions.alarmEvaluationPostgres = runTelemetryGo('TestPostgresAlarmEvaluation|TestPostgresEdgeAlarmEventRelay', {
    S2_TELEMETRY_TEST_DATABASE_URL: `postgres://s2_telemetry_service:s2-telemetry-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`,
    S2_TELEMETRY_ADMIN_DATABASE_URL: `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`,
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
