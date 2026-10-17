import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';
import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/telemetry/compose.yaml');
const projectName = `hvac-analytics-history-${process.pid}`;
const registryComposePath = resolve(root, 'infra/registry/compose.yaml');
const registryProjectName = `hvac-forecast-history-registry-${process.pid}`;
const reportPath = resolve(root, process.env.ANALYTICS_HISTORY_REPORT_PATH ?? 'out/analytics-history/clickhouse-integration.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('analytics history port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const clickHouseHostPort = await findAvailablePort();
const postgresHostPort = await findAvailablePort();
const composeEnvironment = {
  ...process.env,
  S2_CLICKHOUSE_HTTP_HOST_PORT: String(clickHouseHostPort),
  S1_POSTGRES_HOST_PORT: String(postgresHostPort),
};
const clickHouseURL = `http://127.0.0.1:${clickHouseHostPort}`;
const forecastPostgresDSN = `postgres://forecast_runtime:forecast-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`;
const optimizationPostgresDSN = `postgres://optimization_runtime:optimization-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`;
const fddPostgresDSN = `postgres://fdd_runtime:fdd-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`;
const mvPostgresDSN = `postgres://mv_runtime:mv-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`;
const reportPostgresDSN = `postgres://report_runtime:report-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s1?sslmode=disable`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function runExpectFailure(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (!result.error && result.status === 0) {
    throw new Error(`${command} ${args.join(' ')} unexpectedly succeeded`);
  }
  return String(result.stderr ?? result.stdout ?? '').trim().slice(-2000);
}

function compose(args) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function registryCompose(args) {
  return runDockerCompose(run, ['-p', registryProjectName, '-f', registryComposePath, ...args], { env: composeEnvironment });
}

function container(service) {
  return compose(['ps', '-q', service]);
}

function registryContainer(service) {
  return registryCompose(['ps', '-q', service]);
}

function clickHouse(sql) {
  return run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_history', '--query', sql]);
}

function clickHouseMustFail(sql, user) {
  return runExpectFailure('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', user, '--query', sql]);
}

function psql(sql) {
  return run('docker', ['exec', registryContainer('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}

async function waitForClickHouse() {
  let stableChecks = 0;
  for (let attempt = 0; attempt < 360; attempt += 1) {
    try {
      const tableReady = clickHouse(`SELECT count() FROM system.tables WHERE database = 'analytics' AND name = 'energy_interval_facts'`);
      const userCount = clickHouse(`SELECT count() FROM system.users WHERE name IN ('analytics_projector_reader', 'analytics_projector_writer', 'cube_analytics_reader', 'telemetry_query_history_reader')`);
      if (tableReady === '1' && userCount === '4') {
        stableChecks += 1;
        if (stableChecks >= 3) return;
      } else stableChecks = 0;
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'clickhouse']); } catch (error) { logs = String(error); }
  throw new Error(`analytics ClickHouse model did not initialize\n${logs}`);
}

async function waitForPostgres() {
  let stableChecks = 0;
  for (let attempt = 0; attempt < 360; attempt += 1) {
    try {
      // The image runs its init scripts on a socket-only server and then restarts it; only the final
      // server listens on TCP, so a TCP answer means initialization has finished.
      run('docker', ['exec', registryContainer('postgres'), 'pg_isready', '-h', '127.0.0.1', '-U', 'postgres', '-d', 'hvac_s1']);
      const state = psql("SELECT (to_regclass('core_registry.forecast_jobs') IS NOT NULL)::text || '|' || (to_regclass('core_registry.job_instances') IS NOT NULL)::text");
      if (state === 'true|true') {
        stableChecks += 1;
        if (stableChecks >= 3) return;
      } else stableChecks = 0;
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  let logs = '';
  try { logs = registryCompose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`forecast Registry PostgreSQL model did not initialize\n${logs}`);
}

function seedForecastPreparationFixture() {
  psql(`
    INSERT INTO core_registry.energy_topology_versions (
      id, tenant_id, site_id, version, status, effective_from, revision, created_at, updated_at
    ) VALUES (
      '01990000-2300-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', 1, 'VALIDATING', '2026-08-01T00:00:00Z', 1, now(), now()
    );
    INSERT INTO core_registry.energy_nodes (
      id, tenant_id, site_id, topology_version_id, node_type, name, status, revision, created_at, updated_at
    ) VALUES
      ('01990000-2310-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001', 'GRID', 'Forecast Grid', 'ACTIVE', 1, now(), now()),
      ('01990000-2310-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001', 'LOAD', 'Forecast Site Load', 'ACTIVE', 1, now(), now());
    INSERT INTO core_registry.energy_edges (
      id, tenant_id, site_id, topology_version_id, from_node_id, to_node_id,
      energy_type_id, direction, enabled, revision, created_at, updated_at
    ) VALUES (
      '01990000-2320-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001',
      '01990000-2310-7000-8000-000000000001', '01990000-2310-7000-8000-000000000002',
      '01990000-0000-7000-8000-000000000001', 'IMPORT', true, 1, now(), now()
    );
    UPDATE core_registry.energy_topology_versions
    SET status='ACTIVE', released_at=now(), revision=revision+1, updated_at=now()+interval '1 second'
    WHERE id='01990000-2300-7000-8000-000000000001';

    INSERT INTO core_registry.forecast_feature_sets (
      id, tenant_id, feature_set_code, target, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2400-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      'authoritative_site_load', 'SITE_LOAD', 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.forecast_feature_set_versions (
      id, tenant_id, feature_set_id, version, feature_schema, fallback_schema, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2410-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '01990000-2400-7000-8000-000000000001', 1,
      '{"targetMetricVersionId":"01990000-2510-7000-8000-000000000001","features":["load_history"]}'::jsonb,
      NULL, 'RELEASED', 1, now(), now()
    );
    INSERT INTO core_registry.forecast_dataset_snapshots (
      id, tenant_id, site_id, target, subject_type, subject_id, train_from, train_to,
      feature_set_version_id, topology_version_id, metric_version_refs, weather_source,
      data_quality_summary, manifest_uri, manifest_checksum, created_at
    ) VALUES (
      '01990000-2420-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', 'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001',
      '2026-08-01T00:00:00Z', '2026-08-28T00:00:00Z', '01990000-2410-7000-8000-000000000001',
      '01990000-2300-7000-8000-000000000001', '["01990000-2510-7000-8000-000000000001"]'::jsonb,
      NULL, '{"goodRatio":1}'::jsonb, 's3://forecast-authoritative-history/manifest.json', repeat('a',64), now()
    );
    INSERT INTO core_registry.forecast_models (
      id, tenant_id, model_code, target, subject_type, horizon_minutes, granularity, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2430-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      'authoritative_site_load_1h', 'SITE_LOAD', 'SITE', 60, '15MIN', 'ACTIVE', 1, now(), now()
    );
    INSERT INTO core_registry.forecast_training_runs (
      id, tenant_id, site_id, model_id, dataset_snapshot_id, feature_set_version_id, topology_version_id,
      algorithm, hyperparameters, code_version, evaluation, status, started_at, finished_at, revision, created_at, updated_at
    ) VALUES (
      '01990000-2440-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2430-7000-8000-000000000001',
      '01990000-2420-7000-8000-000000000001', '01990000-2410-7000-8000-000000000001',
      '01990000-2300-7000-8000-000000000001', 'BASELINE', '{"method":"LINEAR_TREND"}'::jsonb,
      'forecast-authoritative-history-tracer', NULL, 'PENDING', NULL, NULL, 1, now(), now()
    );
    UPDATE core_registry.forecast_training_runs
    SET status='RUNNING', started_at=now(), revision=revision+1, updated_at=now()+interval '1 second'
    WHERE id='01990000-2440-7000-8000-000000000001';
    UPDATE core_registry.forecast_training_runs
    SET status='SUCCEEDED', evaluation='{"tracer":true}'::jsonb, finished_at=now(), revision=revision+1, updated_at=now()+interval '2 seconds'
    WHERE id='01990000-2440-7000-8000-000000000001';
    INSERT INTO core_registry.forecast_model_versions (
      id, tenant_id, site_id, model_id, model_version, training_run_id, dataset_snapshot_id,
      feature_set_version_id, topology_version_id, artifact_uri, artifact_checksum, evaluation, compatibility,
      status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2450-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2430-7000-8000-000000000001', 1,
      '01990000-2440-7000-8000-000000000001', '01990000-2420-7000-8000-000000000001',
      '01990000-2410-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001',
      's3://forecast-authoritative-history/model.bin', repeat('b',64), '{"tracer":true}'::jsonb, '{"runtime":"go"}'::jsonb,
      'CANDIDATE', 1, now(), now()
    );
    UPDATE core_registry.forecast_model_versions
    SET status='VALIDATED', revision=revision+1, updated_at=now()+interval '1 second'
    WHERE id='01990000-2450-7000-8000-000000000001';
    INSERT INTO core_registry.forecast_deployments (
      id, tenant_id, site_id, target, subject_type, subject_id, model_version_id, model_id,
      feature_set_version_id, topology_version_id, status, effective_from, revision, created_at, updated_at
    ) VALUES (
      '01990000-2460-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', 'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001',
      '01990000-2450-7000-8000-000000000001', '01990000-2430-7000-8000-000000000001',
      '01990000-2410-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001',
      'ACTIVE', '2026-08-01T00:00:00Z', 1, now(), now()
    );
  `);
}

function seedOptimizationPreparationFixture() {
  psql(`
    UPDATE core_registry.forecast_deployments
    SET status='RETIRED', effective_to='2026-08-28T12:00:00Z', revision=revision+1, updated_at=now()
    WHERE id='01990000-2460-7000-8000-000000000001';

    INSERT INTO core_registry.forecast_models (
      id, tenant_id, model_code, target, subject_type, horizon_minutes, granularity, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2600-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      'authoritative_site_load_day_ahead', 'SITE_LOAD', 'SITE', 1440, '15MIN', 'ACTIVE', 1,
      '2026-08-28T11:50:00Z', '2026-08-28T11:50:00Z'
    );
    INSERT INTO core_registry.forecast_training_runs (
      id, tenant_id, site_id, model_id, dataset_snapshot_id, feature_set_version_id, topology_version_id,
      algorithm, hyperparameters, code_version, evaluation, status, started_at, finished_at, revision, created_at, updated_at
    ) VALUES (
      '01990000-2610-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2600-7000-8000-000000000001',
      '01990000-2420-7000-8000-000000000001', '01990000-2410-7000-8000-000000000001',
      '01990000-2300-7000-8000-000000000001', 'BASELINE', '{"method":"LINEAR_TREND"}'::jsonb,
      'optimization-authoritative-input-tracer', NULL, 'PENDING', NULL, NULL, 1,
      '2026-08-28T11:51:00Z', '2026-08-28T11:51:00Z'
    );
    UPDATE core_registry.forecast_training_runs
    SET status='RUNNING', started_at='2026-08-28T11:52:00Z', revision=revision+1, updated_at='2026-08-28T11:52:00Z'
    WHERE id='01990000-2610-7000-8000-000000000001';
    UPDATE core_registry.forecast_training_runs
    SET status='SUCCEEDED', evaluation='{"tracer":true}'::jsonb, finished_at='2026-08-28T11:53:00Z', revision=revision+1, updated_at='2026-08-28T11:53:00Z'
    WHERE id='01990000-2610-7000-8000-000000000001';
    INSERT INTO core_registry.forecast_model_versions (
      id, tenant_id, site_id, model_id, model_version, training_run_id, dataset_snapshot_id,
      feature_set_version_id, topology_version_id, artifact_uri, artifact_checksum, evaluation, compatibility,
      status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2620-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2600-7000-8000-000000000001', 1,
      '01990000-2610-7000-8000-000000000001', '01990000-2420-7000-8000-000000000001',
      '01990000-2410-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001',
      's3://optimization-authoritative-input/day-ahead-model.bin', repeat('c',64), '{"tracer":true}'::jsonb, '{"runtime":"go"}'::jsonb,
      'CANDIDATE', 1, '2026-08-28T11:54:00Z', '2026-08-28T11:54:00Z'
    );
    UPDATE core_registry.forecast_model_versions
    SET status='VALIDATED', revision=revision+1, updated_at='2026-08-28T11:55:00Z'
    WHERE id='01990000-2620-7000-8000-000000000001';
    INSERT INTO core_registry.forecast_deployments (
      id, tenant_id, site_id, target, subject_type, subject_id, model_version_id, model_id,
      feature_set_version_id, topology_version_id, status, effective_from, revision, created_at, updated_at
    ) VALUES (
      '01990000-2630-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', 'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001',
      '01990000-2620-7000-8000-000000000001', '01990000-2600-7000-8000-000000000001',
      '01990000-2410-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001',
      'ACTIVE', '2026-08-28T12:00:00Z', 1, '2026-08-28T11:56:00Z', '2026-08-28T11:56:00Z'
    );
    INSERT INTO core_registry.forecast_input_snapshots (
      id, tenant_id, site_id, deployment_id, model_version_id, feature_set_version_id, topology_version_id,
      latest_data_time, weather_issue_time, metric_version_refs, feature_values, input_checksum, captured_at
    ) VALUES (
      '01990000-2640-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2630-7000-8000-000000000001',
      '01990000-2620-7000-8000-000000000001', '01990000-2410-7000-8000-000000000001',
      '01990000-2300-7000-8000-000000000001', '2026-08-28T11:45:00Z', NULL,
      '["01990000-2510-7000-8000-000000000001"]'::jsonb, '{"schemaVersion":1,"targetMetricVersionId":"01990000-2510-7000-8000-000000000001"}'::jsonb,
      repeat('d',64), '2026-08-28T12:00:00Z'
    );
    INSERT INTO core_registry.forecast_jobs (
      id, tenant_id, site_id, deployment_id, model_version_id, input_snapshot_id, target, subject_type, subject_id,
      forecast_origin, horizon_minutes, granularity, trigger_type, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2650-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2630-7000-8000-000000000001',
      '01990000-2620-7000-8000-000000000001', '01990000-2640-7000-8000-000000000001',
      'SITE_LOAD', 'SITE', '018f1e00-1000-7000-8000-000000000001', '2026-08-28T12:00:00Z', 1440, '15MIN',
      'ON_DEMAND', 'PENDING', 1, '2026-08-28T12:00:00Z', '2026-08-28T12:00:00Z'
    );
    UPDATE core_registry.forecast_jobs
    SET status='RUNNING', started_at='2026-08-28T12:00:01Z', revision=revision+1, updated_at='2026-08-28T12:00:01Z'
    WHERE id='01990000-2650-7000-8000-000000000001';
    UPDATE core_registry.forecast_jobs
    SET status='PERSISTING', revision=revision+1, updated_at='2026-08-28T12:00:02Z'
    WHERE id='01990000-2650-7000-8000-000000000001';
    UPDATE core_registry.forecast_jobs
    SET status='PERSISTED', completed_at='2026-08-28T12:00:03Z', revision=revision+1, updated_at='2026-08-28T12:00:03Z'
    WHERE id='01990000-2650-7000-8000-000000000001';
    INSERT INTO core_registry.forecast_snapshots (
      id, tenant_id, site_id, forecast_job_id, deployment_id, model_version_id, input_snapshot_id,
      forecast_origin, window_start, window_end, result_count, result_checksum, quality_summary, created_at
    ) VALUES (
      '01990000-2660-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2650-7000-8000-000000000001',
      '01990000-2630-7000-8000-000000000001', '01990000-2620-7000-8000-000000000001',
      '01990000-2640-7000-8000-000000000001', '2026-08-28T12:00:00Z', '2026-08-28T12:15:00Z',
      '2026-08-29T12:00:00Z', 96, repeat('e',64), '{"quality":"VALID","source":"forecast-owner"}'::jsonb,
      '2026-08-28T12:00:04Z'
    );

    INSERT INTO core_registry.optimization_policies (
      id, tenant_id, policy_code, subject_type, resource_type, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2700-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      'authoritative_hvac_cost', 'SITE', 'HVAC', 'ACTIVE', 1, '2026-08-28T11:40:00Z', '2026-08-28T11:40:00Z'
    );
    INSERT INTO core_registry.optimization_policy_versions (
      id, tenant_id, policy_id, version, objective, weights, constraints, dispatch_mode, fallback_policy, risk_level,
      horizon, horizon_minutes, granularity, effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2710-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '01990000-2700-7000-8000-000000000001', 1, 'COST', '{"cost":1}'::jsonb,
      '{"comfort":{"zoneTempMinC":21,"zoneTempMaxC":25},"safety":{"supplyTempMinC":6,"supplyTempMaxC":10,"maxSupplyTempStepC":1},"inputMapping":{"supplyTemperatureKey":"btu_meter.supply_water_temperature","zoneTemperatureKey":"zone.temperature"},"maintenanceConstraints":{"outOfService":[]},"manualLocks":{"resources":[]},"responseModel":{"dailyEnergyDeltaKWhPerSupplyTempC":-180,"zoneTempDeltaCPerSupplyTempC":0.4,"energyUncertaintyP90KWh":60,"zoneTempUncertaintyP90C":0.2}}'::jsonb,
      'SHADOW', 'RULE_STRATEGY', 'LOW', 'DAY_AHEAD', 1440, '15MIN', '2026-08-28T00:00:00Z', 'RELEASED', 1,
      '2026-08-28T11:40:00Z', '2026-08-28T11:40:00Z'
    );

    INSERT INTO core_registry.settlement_boundaries (
      id, tenant_id, site_id, topology_version_id, boundary_code, display_name, boundary_type, energy_type_id,
      direction, definition_mode, node_id, effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2720-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2300-7000-8000-000000000001',
      'optimization_grid_import', 'Optimization Grid Import', 'GRID_CONNECTION', '01990000-0000-7000-8000-000000000001',
      'IMPORT', 'NODE', '01990000-2310-7000-8000-000000000001', '2026-08-28T00:00:00Z', 'DRAFT', 1,
      '2026-08-28T11:41:00Z', '2026-08-28T11:41:00Z'
    );
    UPDATE core_registry.settlement_boundaries
    SET status='ACTIVE', revision=revision+1, updated_at='2026-08-28T11:42:00Z'
    WHERE id='01990000-2720-7000-8000-000000000001';
    INSERT INTO core_registry.tariffs (
      id, tenant_id, site_id, tariff_code, display_name, energy_type_id, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2730-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', 'optimization_import_tariff', 'Optimization Import Tariff',
      '01990000-0000-7000-8000-000000000001', 'ACTIVE', 1, '2026-08-28T11:43:00Z', '2026-08-28T11:43:00Z'
    );
    INSERT INTO core_registry.tariff_versions (
      id, tenant_id, site_id, tariff_id, version, effective_from, timezone, currency, billing_cycle,
      status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2740-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2730-7000-8000-000000000001', 1,
      '2026-08-28T00:00:00Z', 'Asia/Shanghai', 'CNY', 'CALENDAR_MONTH', 'RELEASED', 1,
      '2026-08-28T11:44:00Z', '2026-08-28T11:44:00Z'
    );
    INSERT INTO core_registry.tariff_assignments (
      id, tenant_id, site_id, boundary_id, tariff_id, effective_from, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2750-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', '01990000-2720-7000-8000-000000000001',
      '01990000-2730-7000-8000-000000000001', '2026-08-28T00:00:00Z', 'RELEASED', 1,
      '2026-08-28T11:45:00Z', '2026-08-28T11:45:00Z'
    );

    INSERT INTO core_registry.ai_model_definitions (
      id, tenant_id, name, provider, model_id, capabilities, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2760-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      'Optimization Authoritative Tracer', 'LOCAL', 'hvac-recommendation-v1', ARRAY['optimization'], 'ACTIVE', 1,
      '2026-08-28T11:46:00Z', '2026-08-28T11:46:00Z'
    );
    INSERT INTO core_registry.ai_deployment_revisions (
      id, tenant_id, model_definition_id, use_case, revision, output_schema_version, enabled, created_at
    ) VALUES (
      '01990000-2770-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '01990000-2760-7000-8000-000000000001', 'OPTIMIZATION', 1, 'optimization-recommendation-v1', true,
      '2026-08-28T11:47:00Z'
    );
    INSERT INTO core_registry.ai_deployment_bindings (
      id, tenant_id, site_id, use_case, deployment_revision_id, status, revision, created_at, updated_at
    ) VALUES (
      '01990000-2780-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001',
      '018f1e00-1000-7000-8000-000000000001', 'OPTIMIZATION', '01990000-2770-7000-8000-000000000001',
      'ACTIVE', 1, '2026-08-28T11:48:00Z', '2026-08-28T11:48:00Z'
    );
  `);

  clickHouse(`INSERT INTO telemetry_history.observations (
    observation_id, tenant_id, site_id, device_id, point_id,
    source_id, source_event_id, source_partition, source_offset, source_path,
    telemetry_key, point_type, point_revision, value_type, unit, value_number, sampled_at, received_at,
    acceptance_status, quality, quality_reasons, payload_sha256
  ) VALUES
    (toUUID('01990000-2940-7000-8000-000000000001'), toUUID('018f1d00-0000-7000-8000-000000000001'), toUUID('018f1e00-1000-7000-8000-000000000001'),
     toUUID('01990000-2900-7000-8000-000000000001'), toUUID('01990000-2910-7000-8000-000000000001'),
     'gateway-a', toUUID('01990000-2930-7000-8000-000000000001'), 'optimization-owner', 1, 'PUSH',
     'btu_meter.supply_water_temperature', 'TELEMETRY', 3, 'NUMBER', 'Cel', 7,
     toDateTime64('2026-08-28 11:59:40', 3, 'UTC'), toDateTime64('2026-08-28 11:59:41', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('7',64)),
    (toUUID('01990000-2940-7000-8000-000000000002'), toUUID('018f1d00-0000-7000-8000-000000000001'), toUUID('018f1e00-1000-7000-8000-000000000001'),
     toUUID('01990000-2900-7000-8000-000000000002'), toUUID('01990000-2910-7000-8000-000000000002'),
     'gateway-a', toUUID('01990000-2930-7000-8000-000000000002'), 'optimization-owner', 2, 'PUSH',
     'zone.temperature', 'TELEMETRY', 4, 'NUMBER', 'Cel', 23,
     toDateTime64('2026-08-28 11:59:45', 3, 'UTC'), toDateTime64('2026-08-28 11:59:46', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('8',64))`);
}

const report = {
  schemaVersion: 1,
  capability: 'analytics-energy-interval-read-model',
  status: 'failed',
  startedAt: new Date().toISOString(),
  clickHouseImage: 'clickhouse/clickhouse-server:26.3.39.7@sha256:3a91276f066905da0edbbd622d3fc2a2df632c87ea7fe32ef4e74fdf8c9567b0',
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  try { registryCompose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'clickhouse']);
  registryCompose(['up', '-d', 'postgres']);
  await waitForClickHouse();
  await waitForPostgres();
  seedForecastPreparationFixture();
  report.assertions.goIntegration = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=modules/energy',
    'test', '-count=1', '-run', 'TestCanonicalCounterDeltaProjectsEnergyFactsIdempotently|TestLateCounterObservationCorrectsSuccessorFactFromCheckpoint|TestProjectorWritesEveryIntervalOfALargeHistoryBatch|TestMixedAgeBatchProjectsTheSharedSuccessorOnce|TestPriceWindowProjectionShowsOnlyTheLatestTariffTimeline', '-v', './internal/clickhouse/...',
  ], {
    env: {
      ...process.env,
      ANALYTICS_CLICKHOUSE_TEST_URL: clickHouseURL,
      ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME: 'telemetry_history',
    },
  });
  report.assertions.metricCounterWindow = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=modules/metric',
    'test', '-count=1', '-run', 'TestCounterDeltaTakesTheStepIntoTheWindow', '-v', './pkg/metric/...',
  ], {
    env: {
      ...process.env,
      METRIC_CLICKHOUSE_TEST_URL: clickHouseURL,
      METRIC_CLICKHOUSE_TEST_ADMIN_USERNAME: 'telemetry_history',
    },
  });
  report.assertions.deviceHistoryQuery = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', 'TestClickHouseHistoryClientQueriesBoundedRealProjection', '-v', './internal/history/...',
  ], {
    env: {
      ...process.env,
      HISTORY_QUERY_CLICKHOUSE_TEST_URL: clickHouseURL,
      HISTORY_QUERY_CLICKHOUSE_TEST_USERNAME: 'telemetry_query_history_reader',
    },
  });
  report.assertions.forecastGoIntegration = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/forecast-service',
    'test', '-count=1', '-run', 'TestClickHouseForecastSinkPersistsTraceableBaseline', '-v', './internal/forecast/...',
  ], {
    env: {
      ...process.env,
      FORECAST_CLICKHOUSE_TEST_URL: clickHouseURL,
    },
  });
  report.assertions.factCount = clickHouse(`SELECT count() FROM analytics.energy_interval_facts WHERE tenant_id = toUUID('018f4f00-0100-7000-8000-000000000001')`);
  if (report.assertions.factCount !== '3') throw new Error(`unexpected energy interval fact count ${report.assertions.factCount}`);

  const rollupPointId = '01990000-1000-7000-8000-000000000001';
  clickHouse(`INSERT INTO telemetry_history.observations (
    observation_id, tenant_id, site_id, device_id, point_id,
    source_id, source_event_id, source_partition, source_offset, source_path,
    telemetry_key, point_type, point_revision, value_type, unit, value_number, sampled_at, received_at,
    acceptance_status, quality, quality_reasons, payload_sha256
  ) VALUES
    (toUUID('01990000-2000-7000-8000-000000000001'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000001'), 'rollup-fixture', 1, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 10, toDateTime64('2026-08-11 10:00:05', 3, 'UTC'), toDateTime64('2026-08-11 10:00:05.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('a', 64)),
    (toUUID('01990000-2000-7000-8000-000000000002'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000002'), 'rollup-fixture', 2, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 20, toDateTime64('2026-08-11 10:00:35', 3, 'UTC'), toDateTime64('2026-08-11 10:00:35.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('b', 64)),
    (toUUID('01990000-2000-7000-8000-000000000003'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000003'), 'rollup-fixture', 3, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 30, toDateTime64('2026-08-11 10:01:10', 3, 'UTC'), toDateTime64('2026-08-11 10:01:10.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('c', 64)),
    (toUUID('01990000-2000-7000-8000-000000000004'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000004'), 'rollup-fixture', 4, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 40, toDateTime64('2026-08-11 10:16:00', 3, 'UTC'), toDateTime64('2026-08-11 10:16:00.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('d', 64))`);

  report.assertions.rollup1Min = clickHouse(`SELECT toString(bucket) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_1min WHERE point_id = toUUID('${rollupPointId}') ORDER BY bucket FORMAT TSVRaw`);
  if (report.assertions.rollup1Min !== '2026-08-11 10:00:00|2|15|10|20\n2026-08-11 10:01:00|1|30|30|30\n2026-08-11 10:16:00|1|40|40|40') throw new Error(`unexpected 1 minute rollup ${report.assertions.rollup1Min}`);
  report.assertions.rollup15Min = clickHouse(`SELECT toString(bucket) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_15min WHERE point_id = toUUID('${rollupPointId}') ORDER BY bucket FORMAT TSVRaw`);
  if (report.assertions.rollup15Min !== '2026-08-11 10:00:00|3|20|10|30\n2026-08-11 10:15:00|1|40|40|40') throw new Error(`unexpected 15 minute rollup ${report.assertions.rollup15Min}`);
  report.assertions.rollupHourly = clickHouse(`SELECT toString(hour) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_hourly WHERE point_id = toUUID('${rollupPointId}') FORMAT TSVRaw`);
  if (report.assertions.rollupHourly !== '2026-08-11 10:00:00|4|25|10|40') throw new Error(`unexpected hourly rollup ${report.assertions.rollupHourly}`);

  const resetCounterPointId = '01990000-1000-7000-8000-000000000010';
  const rolloverCounterPointId = '01990000-1000-7000-8000-000000000020';
  const invalidCounterPointId = '01990000-1000-7000-8000-000000000030';
  const revisionCounterPointId = '01990000-1000-7000-8000-000000000040';
  clickHouse(`INSERT INTO telemetry_history.observations (
    observation_id, tenant_id, site_id, device_id, point_id,
    source_id, source_event_id, source_partition, source_offset, source_path,
    telemetry_key, point_type, point_revision, counter_decrease_mode, counter_rollover_modulus,
    value_type, unit, value_number, sampled_at, received_at,
    acceptance_status, quality, quality_reasons, payload_sha256
  ) VALUES
    (toUUID('01990000-2000-7000-8000-000000000101'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${resetCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000101'), 'counter-reset', 1, 'PUSH', 'energy_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'kWh', 100, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('1', 64)),
    (toUUID('01990000-2000-7000-8000-000000000103'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${resetCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000103'), 'counter-reset', 3, 'PUSH', 'energy_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'kWh', 5, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('3', 64)),
    (toUUID('01990000-2000-7000-8000-000000000102'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${resetCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000102'), 'counter-reset', 2, 'PUSH', 'energy_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'kWh', 125, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:03:00', 3, 'UTC'), 'OUT_OF_ORDER', 'GOOD', ['OUT_OF_ORDER'], repeat('2', 64)),
    (toUUID('01990000-2000-7000-8000-000000000201'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rolloverCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000201'), 'counter-rollover', 1, 'PUSH', 'water_total', 'COUNTER', 1, 'ROLLOVER', 1000, 'NUMBER', 'm3', 990, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('4', 64)),
    (toUUID('01990000-2000-7000-8000-000000000202'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rolloverCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000202'), 'counter-rollover', 2, 'PUSH', 'water_total', 'COUNTER', 1, 'ROLLOVER', 1000, 'NUMBER', 'm3', 5, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:01:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('5', 64)),
    (toUUID('01990000-2000-7000-8000-000000000203'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rolloverCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000203'), 'counter-rollover', 3, 'PUSH', 'water_total', 'COUNTER', 1, 'ROLLOVER', 1000, 'NUMBER', 'm3', 20, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('6', 64)),
    (toUUID('01990000-2000-7000-8000-000000000301'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000301'), 'counter-invalid', 1, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 50, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('7', 64)),
    (toUUID('01990000-2000-7000-8000-000000000302'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000302'), 'counter-invalid', 2, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 40, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:01:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('8', 64)),
    (toUUID('01990000-2000-7000-8000-000000000303'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000303'), 'counter-invalid', 3, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 45, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('9', 64)),
    (toUUID('01990000-2000-7000-8000-000000000304'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000304'), 'counter-invalid', 4, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 55, toDateTime64('2026-08-11 11:03:00', 3, 'UTC'), toDateTime64('2026-08-11 11:03:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('a', 64)),
    (toUUID('01990000-2000-7000-8000-000000000401'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000401'), 'counter-revision', 1, 'PUSH', 'cycle_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 10, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('b', 64)),
    (toUUID('01990000-2000-7000-8000-000000000402'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000402'), 'counter-revision', 2, 'PUSH', 'cycle_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 20, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:01:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('c', 64)),
    (toUUID('01990000-2000-7000-8000-000000000403'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000403'), 'counter-revision', 3, 'PUSH', 'cycle_total', 'COUNTER', 2, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 25, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('d', 64)),
    (toUUID('01990000-2000-7000-8000-000000000404'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000404'), 'counter-revision', 4, 'PUSH', 'cycle_total', 'COUNTER', 2, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 30, toDateTime64('2026-08-11 11:03:00', 3, 'UTC'), toDateTime64('2026-08-11 11:03:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('e', 64))`);

  report.assertions.counterResetTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${resetCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterResetTransitions !== 'INITIAL:NULL\nINCREASE:25\nRESET:5') throw new Error(`unexpected reset Counter transitions ${report.assertions.counterResetTransitions}`);
  report.assertions.counterRolloverTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${rolloverCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterRolloverTransitions !== 'INITIAL:NULL\nROLLOVER:15\nINCREASE:15') throw new Error(`unexpected rollover Counter transitions ${report.assertions.counterRolloverTransitions}`);
  report.assertions.counterInvalidTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${invalidCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterInvalidTransitions !== 'INITIAL:NULL\nINVALID_DECREASE:NULL\nINVALID_DECREASE:NULL\nRECOVERY:5') throw new Error(`unexpected invalid Counter transitions ${report.assertions.counterInvalidTransitions}`);
  report.assertions.counterRevisionTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${revisionCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterRevisionTransitions !== 'INITIAL:NULL\nINCREASE:10\nREVISION_BOUNDARY:NULL\nINCREASE:5') throw new Error(`unexpected Counter revision transitions ${report.assertions.counterRevisionTransitions}`);

  report.assertions.counterHourly = clickHouse(`SELECT toString(point_id) || '|' || toString(delta_sum) || '|' || toString(delta_count) || '|' || toString(reset_count) || '|' || toString(rollover_count) || '|' || toString(excluded_transition_count) FROM telemetry_history.counter_hourly WHERE point_id IN (toUUID('${resetCounterPointId}'), toUUID('${rolloverCounterPointId}'), toUUID('${invalidCounterPointId}'), toUUID('${revisionCounterPointId}')) ORDER BY point_id FORMAT TSVRaw`);
  const expectedCounterHourly = [
    `${resetCounterPointId}|30|2|1|0|0`,
    `${rolloverCounterPointId}|30|2|0|1|0`,
    `${invalidCounterPointId}|5|1|0|0|2`,
    `${revisionCounterPointId}|15|2|0|0|1`,
  ].join('\n');
  if (report.assertions.counterHourly !== expectedCounterHourly) throw new Error(`unexpected Counter hourly rollup ${report.assertions.counterHourly}`);
  report.assertions.counterExcludedFromGenericRollup = clickHouse(`SELECT count() FROM telemetry_history.numeric_hourly WHERE point_id IN (toUUID('${resetCounterPointId}'), toUUID('${rolloverCounterPointId}'), toUUID('${invalidCounterPointId}'), toUUID('${revisionCounterPointId}'))`);
  if (report.assertions.counterExcludedFromGenericRollup !== '0') throw new Error(`Counter leaked into generic numeric rollup: ${report.assertions.counterExcludedFromGenericRollup}`);

  // The bounded counter deltas (#441) equal the unbounded view from `since` through `until` and
  // at the first observation after `until`: ANCHOR supplies the first predecessor, including its
  // NULL unit, PREFIX_MAX the INVALID-decrease maximum, and NEXT the successor's delta.
  const nullUnitCounterPointId = '01990000-1000-7000-8000-000000000050';
  clickHouse(`INSERT INTO telemetry_history.observations (
    observation_id, tenant_id, site_id, device_id, point_id,
    source_id, source_event_id, source_partition, source_offset, source_path,
    telemetry_key, point_type, point_revision, counter_decrease_mode, counter_rollover_modulus,
    value_type, unit, value_number, sampled_at, received_at,
    acceptance_status, quality, quality_reasons, payload_sha256
  ) VALUES
    (toUUID('01990000-2000-7000-8000-000000000501'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${nullUnitCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000501'), 'counter-null-unit', 1, 'PUSH', 'pulse_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'kWh', 10, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('a', 64)),
    (toUUID('01990000-2000-7000-8000-000000000502'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${nullUnitCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000502'), 'counter-null-unit', 2, 'PUSH', 'pulse_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', NULL, 12, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:01:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('b', 64)),
    (toUUID('01990000-2000-7000-8000-000000000503'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${nullUnitCounterPointId}'), 'gateway-a', toUUID('01990000-8000-7000-8000-000000000503'), 'counter-null-unit', 3, 'PUSH', 'pulse_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', NULL, 15, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('c', 64))`);
  const counterDeltaRow = "toString(observation_id) || ':' || transition_type || ':' || ifNull(toString(delta_value), 'NULL') || ':' || ifNull(toString(previous_observation_id), '')";
  report.assertions.boundedCounterDeltas = {};
  // since, until, and the last sample the unbounded view is compared through: the second case's
  // range holds only the 11:01 rows, so its 11:02 deltas come from NEXT.
  for (const [since, until, through] of [
    ['2026-08-11 11:02:00.000', '2299-12-31 23:59:59.999', '2299-12-31 23:59:59.999'],
    ['2026-08-11 11:01:00.000', '2026-08-11 11:01:00.000', '2026-08-11 11:02:00.000'],
  ]) {
    for (const pointId of [resetCounterPointId, rolloverCounterPointId, invalidCounterPointId, revisionCounterPointId, nullUnitCounterPointId]) {
      const bounded = clickHouse(`SELECT ${counterDeltaRow} FROM telemetry_history.counter_deltas_from(points = [toUUID('${pointId}')], since = '${since}', until = '${until}') ORDER BY sampled_at, observation_id FORMAT TSVRaw`);
      const unbounded = clickHouse(`SELECT ${counterDeltaRow} FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${pointId}') AND sampled_at >= toDateTime64('${since}', 3, 'UTC') AND sampled_at <= toDateTime64('${through}', 3, 'UTC') ORDER BY sampled_at, observation_id FORMAT TSVRaw`);
      if (bounded === '' || bounded !== unbounded) throw new Error(`bounded Counter deltas for ${pointId} from ${since} through ${until} differ from the unbounded view:\n${bounded}\n---\n${unbounded}`);
      report.assertions.boundedCounterDeltas[`${pointId}@${since}..${until}`] = bounded;
    }
  }

  const metricId = '01990000-1500-7000-8000-000000000001';
  const metricVersionId = '01990000-1510-7000-8000-000000000001';
  const metricBindingId = '01990000-1530-7000-8000-000000000001';
  const metricCalculationRunId = '01990000-1540-7000-8000-000000000001';
  clickHouse(`INSERT INTO analytics.metric_result_facts (
    result_id, tenant_id, site_id, subject_type, subject_id,
    metric_id, metric_version_id, metric_code, metric_version,
    metric_binding_id, binding_version, period_start, period_end, calculated_at,
    granularity, value_type, value_json, value_number, value_string, value_boolean,
    unit, quality, completeness, calculation_run_id, revision, provenance
  ) VALUES
    (toUUID('01990000-1550-7000-8000-000000000001'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), 'SITE', toUUID('01990000-5000-7000-8000-000000000001'),
     toUUID('${metricId}'), toUUID('${metricVersionId}'), 'daily_energy', 1,
     toUUID('${metricBindingId}'), 1, toDateTime64('2026-08-10 16:00:00', 3, 'UTC'), toDateTime64('2026-08-11 16:00:00', 3, 'UTC'), toDateTime64('2026-08-11 16:02:00', 3, 'UTC'),
     'DAY', 'NUMBER', '1000', 1000, NULL, NULL, 'kWh', 'PARTIAL', 0.99, toUUID('${metricCalculationRunId}'), 1, '{"reason":"scheduled","source":"counter_deltas"}'),
    (toUUID('01990000-1550-7000-8000-000000000002'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), 'SITE', toUUID('01990000-5000-7000-8000-000000000001'),
     toUUID('${metricId}'), toUUID('${metricVersionId}'), 'daily_energy', 1,
     toUUID('${metricBindingId}'), 1, toDateTime64('2026-08-10 16:00:00', 3, 'UTC'), toDateTime64('2026-08-11 16:00:00', 3, 'UTC'), toDateTime64('2026-08-12 02:00:00', 3, 'UTC'),
     'DAY', 'NUMBER', '1012', 1012, NULL, NULL, 'kWh', 'GOOD', 1.0, toUUID('01990000-1540-7000-8000-000000000002'), 2, '{"reason":"late_data_recalculation","previousRevision":1}')`);
  report.assertions.metricResultFactsEngine = clickHouse(`SELECT engine FROM system.tables WHERE database = 'analytics' AND name = 'metric_result_facts'`);
  if (report.assertions.metricResultFactsEngine !== 'MergeTree') throw new Error(`unexpected metric_result_facts engine ${report.assertions.metricResultFactsEngine}`);
  report.assertions.metricResultFactHistory = clickHouse(`SELECT toString(count()) || '|' || arrayStringConcat(arrayMap(value -> toString(value), arraySort(groupArray(revision))), ',') FROM analytics.metric_result_facts WHERE metric_code = 'daily_energy' AND subject_id = toUUID('01990000-5000-7000-8000-000000000001') AND period_start = toDateTime64('2026-08-10 16:00:00', 3, 'UTC') FORMAT TSVRaw`);
  if (report.assertions.metricResultFactHistory !== '2|1,2') throw new Error(`Metric historical recalculation did not preserve both revisions: ${report.assertions.metricResultFactHistory}`);
  report.assertions.metricResultRevisionTwoPayload = clickHouse(`SELECT toString(revision) || '|' || toString(value_number) || '|' || quality || '|' || toString(completeness) || '|' || toString(metric_version) || '|' || toString(binding_version) FROM analytics.metric_result_facts WHERE metric_code = 'daily_energy' AND subject_id = toUUID('01990000-5000-7000-8000-000000000001') AND period_start = toDateTime64('2026-08-10 16:00:00', 3, 'UTC') ORDER BY revision DESC LIMIT 1 FORMAT TSVRaw`);
  if (report.assertions.metricResultRevisionTwoPayload !== '2|1012|GOOD|1|1|1') throw new Error(`Metric revision 2 payload is incorrect: ${report.assertions.metricResultRevisionTwoPayload}`);

  clickHouse(`INSERT INTO analytics.metric_result_facts (
    result_id, tenant_id, site_id, subject_type, subject_id,
    metric_id, metric_version_id, metric_code, metric_version,
    metric_binding_id, binding_version, period_start, period_end, calculated_at,
    granularity, value_type, value_json, value_number, value_string, value_boolean,
    unit, quality, completeness, calculation_run_id, revision, provenance
  ) VALUES
    (toUUID('01990000-2530-7000-8000-000000000001'), toUUID('018f1d00-0000-7000-8000-000000000001'), toUUID('018f1e00-1000-7000-8000-000000000001'), 'SITE', toUUID('018f1e00-1000-7000-8000-000000000001'),
     toUUID('01990000-2500-7000-8000-000000000001'), toUUID('01990000-2510-7000-8000-000000000001'), 'site_load_authoritative', 1,
     toUUID('01990000-2520-7000-8000-000000000001'), 1, toDateTime64('2026-08-28 10:45:00', 3, 'UTC'), toDateTime64('2026-08-28 11:00:00', 3, 'UTC'), toDateTime64('2026-08-28 11:00:10', 3, 'UTC'),
     '15MIN', 'NUMBER', '760', 760, NULL, NULL, 'kW', 'GOOD', 1.0, toUUID('01990000-2540-7000-8000-000000000001'), 1, '{"source":"metric-owner"}'),
    (toUUID('01990000-2530-7000-8000-000000000002'), toUUID('018f1d00-0000-7000-8000-000000000001'), toUUID('018f1e00-1000-7000-8000-000000000001'), 'SITE', toUUID('018f1e00-1000-7000-8000-000000000001'),
     toUUID('01990000-2500-7000-8000-000000000001'), toUUID('01990000-2510-7000-8000-000000000001'), 'site_load_authoritative', 1,
     toUUID('01990000-2520-7000-8000-000000000001'), 1, toDateTime64('2026-08-28 11:00:00', 3, 'UTC'), toDateTime64('2026-08-28 11:15:00', 3, 'UTC'), toDateTime64('2026-08-28 11:15:10', 3, 'UTC'),
     '15MIN', 'NUMBER', '780', 780, NULL, NULL, 'kW', 'GOOD', 1.0, toUUID('01990000-2540-7000-8000-000000000002'), 1, '{"source":"metric-owner"}'),
    (toUUID('01990000-2530-7000-8000-000000000003'), toUUID('018f1d00-0000-7000-8000-000000000001'), toUUID('018f1e00-1000-7000-8000-000000000001'), 'SITE', toUUID('018f1e00-1000-7000-8000-000000000001'),
     toUUID('01990000-2500-7000-8000-000000000001'), toUUID('01990000-2510-7000-8000-000000000001'), 'site_load_authoritative', 1,
     toUUID('01990000-2520-7000-8000-000000000001'), 1, toDateTime64('2026-08-28 11:15:00', 3, 'UTC'), toDateTime64('2026-08-28 11:30:00', 3, 'UTC'), toDateTime64('2026-08-28 11:30:10', 3, 'UTC'),
     '15MIN', 'NUMBER', '800', 800, NULL, NULL, 'kW', 'GOOD', 1.0, toUUID('01990000-2540-7000-8000-000000000003'), 1, '{"source":"metric-owner"}'),
    (toUUID('01990000-2530-7000-8000-000000000004'), toUUID('018f1d00-0000-7000-8000-000000000001'), toUUID('018f1e00-1000-7000-8000-000000000001'), 'SITE', toUUID('018f1e00-1000-7000-8000-000000000001'),
     toUUID('01990000-2500-7000-8000-000000000001'), toUUID('01990000-2510-7000-8000-000000000001'), 'site_load_authoritative', 1,
     toUUID('01990000-2520-7000-8000-000000000001'), 1, toDateTime64('2026-08-28 11:30:00', 3, 'UTC'), toDateTime64('2026-08-28 11:45:00', 3, 'UTC'), toDateTime64('2026-08-28 11:45:10', 3, 'UTC'),
     '15MIN', 'NUMBER', '820', 820, NULL, NULL, 'kW', 'GOOD', 1.0, toUUID('01990000-2540-7000-8000-000000000004'), 1, '{"source":"metric-owner"}')`);
  report.assertions.forecastAuthoritativeHistoryTracer = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/forecast-service',
    'test', '-count=1', '-run', 'TestAuthoritativeHistoryPreparesAndPublishesServerOwnedForecast', '-v', './internal/forecast/...',
  ], {
    env: {
      ...process.env,
      FORECAST_CLICKHOUSE_TEST_URL: clickHouseURL,
      FORECAST_POSTGRES_TEST_DSN: forecastPostgresDSN,
    },
  });

  seedOptimizationPreparationFixture();
  report.assertions.optimizationAuthoritativeInputTracer = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/optimization-service',
    'test', '-count=1', '-run', 'TestAuthoritativeOwnersPrepareSealedOptimizationAndPublishRecommendation', '-v', './internal/optimization/...',
  ], {
    env: {
      ...process.env,
      OPTIMIZATION_CLICKHOUSE_TEST_URL: clickHouseURL,
      OPTIMIZATION_POSTGRES_TEST_DSN: optimizationPostgresDSN,
    },
  });

  // A finding persists with its measurement under the Tenant's rule revision and reads back through
  // the FDD runtime's row scope; the runtime works a scheduled FDD_EVALUATION job the Scheduler left READY.
  const fddJobId = '01990000-1990-7000-8000-000000000001';
  const fddRuleId = '01990000-1985-7000-8000-000000000001';
  psql(`INSERT INTO core_registry.fdd_rule_revisions (id, tenant_id, rule_code, revision, minimum_delta_tc, window_minutes, effective_from, created_at)
    VALUES ('${fddRuleId}', '018f1d00-0000-7000-8000-000000000001', 'CHILLED_WATER_LOW_DELTA_T', 1, 5, 15, '2026-01-01T00:00:00Z', now())`);
  psql(`INSERT INTO core_registry.job_instances (job_id, trigger_type, job_type, tenant_id, site_id, scheduled_for, dedup_key, payload, state, max_attempts, timeout_seconds, created_at, updated_at)
    VALUES ('${fddJobId}', 'SCHEDULE', 'FDD_EVALUATION', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000002', now(), 'fdd-integration-scheduled-evaluation', '{}'::jsonb, 'READY', 3, 60, now(), now())`);
  report.assertions.fddFindingPersistence = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/fdd-service',
    'test', '-count=1', '-run', 'TestPostgresServicePersistsAuthoritativeHistoryFinding|TestPostgresFDDRuntimeWorksItsScheduledEvaluations', '-v', './internal/fdd/...',
  ], {
    env: {
      ...process.env,
      FDD_POSTGRES_INTEGRATION_DSN: fddPostgresDSN,
      FDD_POSTGRES_INTEGRATION_TENANT_ID: '018f1d00-0000-7000-8000-000000000001',
      FDD_POSTGRES_INTEGRATION_SITE_ID: '018f1e00-1000-7000-8000-000000000002',
      FDD_POSTGRES_INTEGRATION_ASSET_ID: '018f1e00-3000-7000-8000-000000000002',
      FDD_POSTGRES_INTEGRATION_JOB_ID: fddJobId,
      FDD_POSTGRES_INTEGRATION_RULE_ID: fddRuleId,
    },
  });
  for (const name of ['TestPostgresServicePersistsAuthoritativeHistoryFinding', 'TestPostgresFDDRuntimeWorksItsScheduledEvaluations']) {
    if (!report.assertions.fddFindingPersistence.includes(`--- PASS: ${name}`)) throw new Error(`${name} did not run`);
  }

  // M&V keeps one reporting measure per Site, verifies each settled day once and works its
  // MV_EVALUATION job as mv_runtime; it reads complete hours of released Metrics as mv_service_reader.
  const mvJobId = '01990000-1995-7000-8000-000000000001';
  psql(`INSERT INTO core_registry.job_instances (job_id, trigger_type, job_type, tenant_id, site_id, scheduled_for, dedup_key, payload, state, max_attempts, timeout_seconds, created_at, updated_at)
    VALUES ('${mvJobId}', 'SCHEDULE', 'MV_EVALUATION', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', now(), 'mv-integration-scheduled-evaluation', '{}'::jsonb, 'READY', 3, 300, now(), now())`);
  const mvHour = (id, code, start, revision, value) => `(toUUID('01990000-2850-7000-8000-00000000000${id}'), toUUID('018f1d00-0000-7000-8000-000000000001'), toUUID('018f1e00-1000-7000-8000-000000000001'), 'SITE', toUUID('018f1e00-1000-7000-8000-000000000001'),
     toUUID('01990000-2800-7000-8000-00000000001${id}'), toUUID('01990000-2810-7000-8000-00000000001${id}'), '${code}', 1,
     toUUID('01990000-2820-7000-8000-00000000001${id}'), 1, toDateTime64('${start}', 3, 'UTC'), toDateTime64('${start}', 3, 'UTC') + INTERVAL 1 HOUR, toDateTime64('2026-08-28 00:00:0${revision}', 3, 'UTC'),
     'HOUR', 'NUMBER', '${value}', ${value}, NULL, NULL, 'kWh', 'GOOD', 1.0, toUUID('01990000-2830-7000-8000-00000000001${id}'), ${revision}, '{"source":"metric-owner"}')`;
  clickHouse(`INSERT INTO analytics.metric_result_facts (
    result_id, tenant_id, site_id, subject_type, subject_id,
    metric_id, metric_version_id, metric_code, metric_version,
    metric_binding_id, binding_version, period_start, period_end, calculated_at,
    granularity, value_type, value_json, value_number, value_string, value_boolean,
    unit, quality, completeness, calculation_run_id, revision, provenance
  ) VALUES ${[
    mvHour(1, 'hourly_energy', '2026-08-27 00:00:00', 1, 210),
    mvHour(2, 'hourly_cooling', '2026-08-27 00:00:00', 1, 740),
    mvHour(3, 'hourly_energy', '2026-08-27 01:00:00', 1, 230),
    mvHour(4, 'hourly_energy', '2026-08-27 01:00:00', 2, 190),
    mvHour(5, 'hourly_cooling', '2026-08-27 01:00:00', 1, 700),
    mvHour(6, 'hourly_energy', '2026-08-27 02:00:00', 1, 200),
  ].join(',\n')}`);
  const mvTests = ['TestPostgresMVRuntimeKeepsBoundariesApartAndVerifiesDaysOnce', 'TestPostgresMVRuntimeWorksItsScheduledEvaluations', 'TestClickHouseHoursReadTheLatestRevisionOfCompleteHours', 'TestPostgresSiteClockReadsTheSiteTimezone'];
  report.assertions.mvSavingsVerification = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/mv-service',
    'test', '-count=1', '-run', mvTests.join('|'), '-v', './internal/mv/...',
  ], {
    env: {
      ...process.env,
      MV_POSTGRES_INTEGRATION_DSN: mvPostgresDSN,
      MV_POSTGRES_INTEGRATION_TENANT_ID: '018f1d00-0000-7000-8000-000000000001',
      MV_POSTGRES_INTEGRATION_SITE_ID: '018f1e00-1000-7000-8000-000000000001',
      MV_POSTGRES_INTEGRATION_UNPRICED_SITE_ID: '018f1e00-1000-7000-8000-000000000002',
      MV_POSTGRES_INTEGRATION_ACTOR_ID: '018f1f00-0000-7000-8000-000000000001',
      MV_POSTGRES_INTEGRATION_DEVICE_ID: '018f1e00-4000-7000-8000-000000000001',
      MV_POSTGRES_INTEGRATION_JOB_ID: mvJobId,
      MV_CLICKHOUSE_INTEGRATION_URL: clickHouseURL,
    },
  });
  for (const name of mvTests) {
    if (!report.assertions.mvSavingsVerification.includes(`--- PASS: ${name}`)) throw new Error(`${name} did not run`);
  }

  // report-service takes a month's snapshot on a REPORT_GENERATE job as report_runtime. Its hours
  // come from the Telemetry Query Service (covered by its client test and the server acceptance).
  const reportJobId = '01990000-1996-7000-8000-000000000001';
  psql(`INSERT INTO core_registry.job_instances (job_id, trigger_type, job_type, tenant_id, site_id, scheduled_for, dedup_key, payload, state, max_attempts, timeout_seconds, created_at, updated_at)
    VALUES ('${reportJobId}', 'BACKFILL', 'REPORT_GENERATE', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', now(), 'report-integration-2026-07', '{"month":"2026-07"}'::jsonb, 'READY', 3, 120, now(), now())`);
  report.assertions.reportSnapshots = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/report-service',
    'test', '-count=1', '-run', 'TestReportRuntimeSnapshotsTheMonthItsJobNames', '-v', './internal/report/...',
  ], {
    env: {
      ...process.env,
      REPORT_POSTGRES_INTEGRATION_DSN: reportPostgresDSN,
      REPORT_INTEGRATION_TENANT_ID: '018f1d00-0000-7000-8000-000000000001',
      REPORT_INTEGRATION_SITE_ID: '018f1e00-1000-7000-8000-000000000001',
      REPORT_INTEGRATION_JOB_ID: reportJobId,
    },
  });
  if (!report.assertions.reportSnapshots.includes('--- PASS: TestReportRuntimeSnapshotsTheMonthItsJobNames')) throw new Error('TestReportRuntimeSnapshotsTheMonthItsJobNames did not run');

  // energy_interval_facts_visible keeps only a fact's latest revision, and filters on it after
  // choosing that revision: a fact corrected from SITE to BRANCH leaves the Site total. The caller's
  // Site and period filters must still reach the table's index, or every query reads the whole table.
  const visibleFact = (revision, scope, energyKWh, projectedAt) => `(generateUUIDv4(), toUUID('01990000-2a00-7000-8000-000000000001'), toUUID('01990000-2a10-7000-8000-000000000001'),
     toUUID('01990000-2a20-7000-8000-000000000001'), 'electricity', '${scope}', 'VALID', toDateTime64('2026-06-10 01:59:00', 3, 'UTC'), toDateTime64('2026-06-10 02:00:00', 3, 'UTC'), ${energyKWh},
     toUUID('01990000-2a30-7000-8000-000000000001'), ${revision}, toDateTime64('${projectedAt}', 3, 'UTC'))`;
  clickHouse(`INSERT INTO analytics.energy_interval_facts (fact_id, tenant_id, site_id, meter_binding_id, energy_type, accounting_scope, quality, period_start, period_end, energy_kwh, source_current_observation_id, fact_revision, projected_at) VALUES ${[
    visibleFact(0, 'SITE', 5, '2026-06-10 02:01:00'),
    visibleFact(1, 'BRANCH', 6, '2026-06-11 00:00:00'),
  ].join(',\n')}`);
  const visibleScope = `FROM analytics.energy_interval_facts_visible WHERE tenant_id = toUUID('01990000-2a00-7000-8000-000000000001') AND site_id = toUUID('01990000-2a10-7000-8000-000000000001')
    AND period_end >= toDateTime64('2026-06-10 00:00:00', 3, 'UTC') AND period_end < toDateTime64('2026-06-11 00:00:00', 3, 'UTC')`;
  report.assertions.visibleLatestRevision = clickHouse(`SELECT toString(count()) || '|' || toString(sumIf(energy_kwh, accounting_scope = 'SITE')) || '|' || toString(sumIf(energy_kwh, accounting_scope = 'BRANCH')) ${visibleScope} FORMAT TSVRaw`);
  if (report.assertions.visibleLatestRevision !== '1|0|6') throw new Error(`visible facts did not keep only the latest revision: ${report.assertions.visibleLatestRevision}`);
  report.assertions.visibleIndexCondition = clickHouse(`EXPLAIN indexes = 1 SELECT sum(energy_kwh) ${visibleScope}`);
  const primaryKeyCondition = /PrimaryKey\n[\s\S]*?Condition: (.*)/.exec(report.assertions.visibleIndexCondition)?.[1] ?? '';
  if (!primaryKeyCondition.includes('site_id') || !primaryKeyCondition.includes('period_end')) throw new Error(`visible facts read the whole table: PrimaryKey condition ${primaryKeyCondition || 'missing'}`);

  const forecastJobId = '01990000-1700-7000-8000-000000000001';
  report.assertions.forecastSeriesTraceability = clickHouse(`SELECT
    toString(count()) || '|' || toString(min(horizon_minutes)) || '|' || toString(max(horizon_minutes)) || '|'
    || any(quality) || '|' || toString(any(model_version)) || '|' || toString(any(feature_set_version)) || '|'
    || toString(any(deployment_id)) || '|' || toString(any(model_version_id)) || '|' || toString(any(input_snapshot_id)) || '|'
    || toString(any(topology_version_id)) || '|' || toString(countIf(forecast_for > forecast_origin))
    FROM analytics.forecast_series WHERE forecast_job_id = toUUID('${forecastJobId}') FORMAT TSVRaw`);
  const expectedForecastTraceability = '4|15|60|VALID|3|7|01990000-1720-7000-8000-000000000001|01990000-1740-7000-8000-000000000001|01990000-1760-7000-8000-000000000001|01990000-1770-7000-8000-000000000001|4';
  if (report.assertions.forecastSeriesTraceability !== expectedForecastTraceability) throw new Error(`unexpected Forecast traceability ${report.assertions.forecastSeriesTraceability}`);

  report.assertions.readerCanSelectCanonical = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'analytics_projector_reader', '--query', "SELECT count() FROM telemetry_history.counter_deltas_from(points = [toUUID('01990000-1000-7000-8000-000000000010')], since = '2026-08-11 11:01:00.000', until = '2026-08-11 11:02:00.000')"]);
  report.assertions.readerCannotSelectRaw = clickHouseMustFail('SELECT count() FROM telemetry_history.observations', 'analytics_projector_reader');
  report.assertions.historyQueryCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_query_history_reader', '--query', 'SELECT count() FROM telemetry_history.observations']);
  report.assertions.cubeCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'cube_analytics_reader', '--query', 'SELECT count() FROM analytics.energy_interval_facts']);
  report.assertions.metricReaderCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'metric_engine_reader', '--query', 'SELECT count() FROM analytics.metric_result_facts']);
  report.assertions.cubeCanSelectMetric = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'cube_analytics_reader', '--query', 'SELECT count() FROM analytics.metric_result_facts']);
  report.assertions.forecastReaderCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'forecast_service_reader', '--query', 'SELECT count() FROM analytics.forecast_series']);
  report.assertions.cubeCanSelectForecast = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'cube_analytics_reader', '--query', 'SELECT count() FROM analytics.forecast_series']);
  report.assertions.readerCannotInsert = clickHouseMustFail('INSERT INTO analytics.energy_interval_facts (fact_id) VALUES (generateUUIDv4())', 'analytics_projector_reader');
  report.assertions.metricReaderCannotInsert = clickHouseMustFail("INSERT INTO analytics.metric_result_facts (result_id) VALUES (generateUUIDv4())", 'metric_engine_reader');
  report.assertions.metricWriterCannotSelect = clickHouseMustFail('SELECT count() FROM analytics.metric_result_facts', 'metric_engine_writer');
  report.assertions.metricWriterCanDelete = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'metric_engine_writer', '--query', "ALTER TABLE analytics.metric_result_facts DELETE WHERE result_id = toUUID('00000000-0000-0000-0000-000000000000') SETTINGS mutations_sync=1"]);
  report.assertions.forecastReaderCannotInsert = clickHouseMustFail('INSERT INTO analytics.forecast_series (forecast_id) VALUES (generateUUIDv4())', 'forecast_service_reader');
  report.assertions.forecastWriterCannotSelect = clickHouseMustFail('SELECT count() FROM analytics.forecast_series', 'forecast_service_writer');
  report.assertions.historyQueryCannotInsert = clickHouseMustFail('INSERT INTO telemetry_history.observations (observation_id) VALUES (generateUUIDv4())', 'telemetry_query_history_reader');
  report.assertions.historyQueryCannotSelectAnalytics = clickHouseMustFail('SELECT count() FROM analytics.energy_interval_facts', 'telemetry_query_history_reader');
  report.assertions.writerCannotSelect = clickHouseMustFail('SELECT count() FROM analytics.energy_interval_facts', 'analytics_projector_writer');
  report.assertions.fddReaderCanSelectObservations = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'fdd_service_reader', '--query', 'SELECT count() FROM telemetry_history.observations']);
  report.assertions.fddReaderCannotSelectAnalytics = clickHouseMustFail('SELECT count() FROM analytics.energy_interval_facts', 'fdd_service_reader');
  report.assertions.fddReaderCannotInsert = clickHouseMustFail('INSERT INTO telemetry_history.observations (observation_id) VALUES (generateUUIDv4())', 'fdd_service_reader');
  report.assertions.mvReaderCanSelectMetricResults = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'mv_service_reader', '--query', 'SELECT count() FROM analytics.metric_result_facts']);
  report.assertions.mvReaderCannotSelectObservations = clickHouseMustFail('SELECT count() FROM telemetry_history.observations', 'mv_service_reader');
  report.assertions.mvReaderCannotInsert = clickHouseMustFail('INSERT INTO analytics.metric_result_facts (result_id) VALUES (generateUUIDv4())', 'mv_service_reader');
  report.assertions.cubeCannotInsert = clickHouseMustFail('INSERT INTO analytics.energy_interval_facts (fact_id) VALUES (generateUUIDv4())', 'cube_analytics_reader');

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Analytics history evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  try { registryCompose(['down', '--volumes', '--remove-orphans']); } catch {}
}
