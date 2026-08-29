import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const telemetryComposePath = resolve(root, 'infra/telemetry/compose.yaml');
const registryComposePath = resolve(root, 'infra/registry/compose.yaml');
const telemetryProject = `hvac-replay-intelligence-telemetry-${process.pid}`;
const registryProject = `hvac-replay-intelligence-registry-${process.pid}`;
const reportPath = resolve(root, process.env.HISTORICAL_REPLAY_INTELLIGENCE_REPORT_PATH ?? 'out/historical-replay/intelligence-acceptance.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('acceptance port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const [telemetryPostgresPort, clickHousePort, registryPostgresPort] = await Promise.all([
  findAvailablePort(), findAvailablePort(), findAvailablePort(),
]);
const telemetryEnvironment = {
  ...process.env,
  S2_POSTGRES_HOST_PORT: String(telemetryPostgresPort),
  S2_CLICKHOUSE_HTTP_HOST_PORT: String(clickHousePort),
};
const registryEnvironment = { ...process.env, S1_POSTGRES_HOST_PORT: String(registryPostgresPort) };
const telemetryRuntimeURL = `postgres://s2_telemetry_service:s2-telemetry-runtime-local-only@127.0.0.1:${telemetryPostgresPort}/hvac_s2?sslmode=disable`;
const telemetryHistoryURL = `postgres://s2_telemetry_history_service:s2-telemetry-history-local-only@127.0.0.1:${telemetryPostgresPort}/hvac_s2?sslmode=disable`;
const telemetryAdminURL = `postgres://postgres:postgres-local-only@127.0.0.1:${telemetryPostgresPort}/hvac_s2?sslmode=disable`;
const metricRegistryURL = `postgres://metric_engine_runtime:metric-engine-local-only@127.0.0.1:${registryPostgresPort}/hvac_s1?sslmode=disable`;
const forecastRegistryURL = `postgres://forecast_runtime:forecast-runtime-local-only@127.0.0.1:${registryPostgresPort}/hvac_s1?sslmode=disable`;
const optimizationRegistryURL = `postgres://optimization_runtime:optimization-runtime-local-only@127.0.0.1:${registryPostgresPort}/hvac_s1?sslmode=disable`;
const clickHouseURL = `http://127.0.0.1:${clickHousePort}`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function telemetryCompose(args) {
  return runDockerCompose(run, ['-p', telemetryProject, '-f', telemetryComposePath, ...args], { env: telemetryEnvironment });
}
function registryCompose(args) {
  return runDockerCompose(run, ['-p', registryProject, '-f', registryComposePath, ...args], { env: registryEnvironment });
}
function telemetryContainer(service) { return telemetryCompose(['ps', '-q', service]); }
function registryContainer(service) { return registryCompose(['ps', '-q', service]); }
function telemetryPsql(sql) {
  return run('docker', ['exec', telemetryContainer('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}
function registryPsql(sql) {
  return run('docker', ['exec', registryContainer('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}
function clickHouse(sql) {
  return run('docker', ['exec', telemetryContainer('clickhouse'), 'clickhouse-client', '--user', 'telemetry_history', '--query', sql]);
}

async function waitForServices() {
  let stable = 0;
  for (let attempt = 0; attempt < 360; attempt += 1) {
    try {
      const telemetryReady = telemetryPsql(`SELECT (to_regclass('telemetry_runtime.telemetry_history_outbox') IS NOT NULL)::text`);
      const registryReady = registryPsql(`SELECT (to_regclass('core_registry.optimization_input_snapshots') IS NOT NULL)::text || '|' || (to_regclass('core_registry.job_instances') IS NOT NULL)::text`);
      const clickHouseReady = clickHouse(`SELECT count() FROM system.tables WHERE (database='telemetry_history' AND name IN ('observations','counter_deltas')) OR (database='analytics' AND name IN ('energy_interval_facts','metric_result_facts','forecast_series','optimization_evaluations'))`);
      if (telemetryReady === 'true' && registryReady === 'true|true' && clickHouseReady === '6') {
        stable += 1;
        if (stable >= 3) return;
      } else stable = 0;
    } catch {
      stable = 0;
    }
    await pause(250);
  }
  throw new Error('Historical Replay intelligence acceptance services did not initialize');
}

function seedRegistry() {
  registryPsql(`
    INSERT INTO core_registry.unit_registry(unit_code,display_name,quantity_kind,canonical_unit_code,multiplier,conversion_offset,status,revision,created_at,updated_at)
    VALUES ('CNY','Chinese yuan','OTHER','CNY',1,0,'ACTIVE',1,now(),now()) ON CONFLICT DO NOTHING;

    INSERT INTO core_registry.metrics(id,tenant_id,metric_code,metric_name,category,status,revision,created_at,updated_at) VALUES
      ('01990000-3483-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','site_load_15min','Site Load 15min','LOAD','ACTIVE',1,now(),now()),
      ('01990000-3483-7000-8000-000000000004','018f1d00-0000-7000-8000-000000000001','daily_energy','Daily Energy','ENERGY','ACTIVE',1,now(),now()),
      ('01990000-3483-7000-8000-000000000007','018f1d00-0000-7000-8000-000000000001','energy_cost','Energy Cost','FINANCIAL','ACTIVE',1,now(),now());
    INSERT INTO core_registry.metric_versions(id,tenant_id,metric_id,version,unit_code,data_type,subject_type,time_granularity,aggregation,calculation_method,formula,quality_policy,effective_from,status,metadata,revision,created_at,updated_at) VALUES
      ('01990000-3483-7000-8000-000000000002','018f1d00-0000-7000-8000-000000000001','01990000-3483-7000-8000-000000000001',1,'kW','NUMBER','SITE','15MIN','AVG','IDENTITY',NULL,'STRICT','2026-08-01T00:00:00Z','DRAFT','{}',1,now(),now()),
      ('01990000-3483-7000-8000-000000000005','018f1d00-0000-7000-8000-000000000001','01990000-3483-7000-8000-000000000004',1,'kWh','NUMBER','SITE','DAY','LAST','DELTA',NULL,'STRICT','2026-08-01T00:00:00Z','DRAFT','{}',1,now(),now()),
      ('01990000-3483-7000-8000-000000000008','018f1d00-0000-7000-8000-000000000001','01990000-3483-7000-8000-000000000007',1,'CNY','NUMBER','SITE','DAY','SUM','EXPRESSION','daily_energy * 1.5','STRICT','2026-08-01T00:00:00Z','DRAFT','{}',1,now(),now());
    INSERT INTO core_registry.metric_dependencies(id,tenant_id,metric_version_id,dependency_type,dependency_code,dependency_metric_id,sort_order,required,metadata,revision,created_at,updated_at) VALUES
      ('01990000-3483-7000-8000-00000000000a','018f1d00-0000-7000-8000-000000000001','01990000-3483-7000-8000-000000000002','POINT','site_load',NULL,0,true,'{}',1,now(),now()),
      ('01990000-3483-7000-8000-00000000000b','018f1d00-0000-7000-8000-000000000001','01990000-3483-7000-8000-000000000005','POINT','grid_import_energy_total',NULL,0,true,'{}',1,now(),now()),
      ('01990000-3483-7000-8000-00000000000c','018f1d00-0000-7000-8000-000000000001','01990000-3483-7000-8000-000000000008','METRIC','daily_energy','01990000-3483-7000-8000-000000000004',0,true,'{}',1,now(),now());
    UPDATE core_registry.metric_versions
    SET status='RELEASED',revision=revision+1,updated_at=now()+interval '1 second'
    WHERE id IN ('01990000-3483-7000-8000-000000000002','01990000-3483-7000-8000-000000000005','01990000-3483-7000-8000-000000000008');
    INSERT INTO core_registry.metric_bindings(id,tenant_id,site_id,metric_version_id,metric_id,metric_version,binding_version,subject_type,subject_id,time_granularity,source_definition,effective_from,status,revision,created_at,updated_at) VALUES
      ('01990000-3483-7000-8000-000000000003','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-3483-7000-8000-000000000002','01990000-3483-7000-8000-000000000001',1,1,'SITE','018f1e00-1000-7000-8000-000000000001','15MIN','{"points":{"site_load":"01990000-3481-7000-8000-000000000001"}}','2026-08-01T00:00:00Z','RELEASED',1,now(),now()),
      ('01990000-3483-7000-8000-000000000006','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-3483-7000-8000-000000000005','01990000-3483-7000-8000-000000000004',1,1,'SITE','018f1e00-1000-7000-8000-000000000001','DAY','{"points":{"grid_import_energy_total":"01990000-3481-7000-8000-000000000002"},"counter":{"decreaseMode":"RESET"}}','2026-08-01T00:00:00Z','RELEASED',1,now(),now()),
      ('01990000-3483-7000-8000-000000000009','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-3483-7000-8000-000000000008','01990000-3483-7000-8000-000000000007',1,1,'SITE','018f1e00-1000-7000-8000-000000000001','DAY','{"expression":"daily_energy * 1.5"}','2026-08-01T00:00:00Z','RELEASED',1,now(),now());

    INSERT INTO core_registry.energy_topology_versions(id,tenant_id,site_id,version,status,effective_from,revision,created_at,updated_at)
    VALUES ('01990000-2300-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001',1,'VALIDATING','2026-08-01T00:00:00Z',1,now(),now());
    INSERT INTO core_registry.energy_nodes(id,tenant_id,site_id,topology_version_id,node_type,name,status,revision,created_at,updated_at) VALUES
      ('01990000-2310-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','GRID','Replay Grid','ACTIVE',1,now(),now()),
      ('01990000-2310-7000-8000-000000000002','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','LOAD','Replay Site Load','ACTIVE',1,now(),now());
    INSERT INTO core_registry.energy_edges(id,tenant_id,site_id,topology_version_id,from_node_id,to_node_id,energy_type_id,direction,enabled,revision,created_at,updated_at)
    VALUES ('01990000-2320-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','01990000-2310-7000-8000-000000000001','01990000-2310-7000-8000-000000000002','01990000-0000-7000-8000-000000000001','IMPORT',true,1,now(),now());
    UPDATE core_registry.energy_topology_versions SET status='ACTIVE',released_at=now(),revision=revision+1,updated_at=now()+interval '1 second' WHERE id='01990000-2300-7000-8000-000000000001';

    INSERT INTO core_registry.forecast_feature_sets(id,tenant_id,feature_set_code,target,status,revision,created_at,updated_at)
    VALUES ('01990000-2400-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','replay_site_load','SITE_LOAD','ACTIVE',1,now(),now());
    INSERT INTO core_registry.forecast_feature_set_versions(id,tenant_id,feature_set_id,version,feature_schema,fallback_schema,status,revision,created_at,updated_at)
    VALUES ('01990000-2410-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','01990000-2400-7000-8000-000000000001',1,'{"targetMetricVersionId":"01990000-3483-7000-8000-000000000002","features":["load_history"]}',NULL,'RELEASED',1,now(),now());
    INSERT INTO core_registry.forecast_dataset_snapshots(id,tenant_id,site_id,target,subject_type,subject_id,train_from,train_to,feature_set_version_id,topology_version_id,metric_version_refs,weather_source,data_quality_summary,manifest_uri,manifest_checksum,created_at)
    VALUES ('01990000-2420-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','SITE_LOAD','SITE','018f1e00-1000-7000-8000-000000000001','2026-08-01T00:00:00Z','2026-08-28T00:00:00Z','01990000-2410-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','["01990000-3483-7000-8000-000000000002"]',NULL,'{"goodRatio":1}','s3://replay-intelligence/manifest.json',repeat('a',64),now());
    INSERT INTO core_registry.forecast_models(id,tenant_id,model_code,target,subject_type,horizon_minutes,granularity,status,revision,created_at,updated_at)
    VALUES ('01990000-2430-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','replay_site_load_day_ahead','SITE_LOAD','SITE',1440,'15MIN','ACTIVE',1,now(),now());
    INSERT INTO core_registry.forecast_training_runs(id,tenant_id,site_id,model_id,dataset_snapshot_id,feature_set_version_id,topology_version_id,algorithm,hyperparameters,code_version,evaluation,status,started_at,finished_at,revision,created_at,updated_at)
    VALUES ('01990000-2440-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2430-7000-8000-000000000001','01990000-2420-7000-8000-000000000001','01990000-2410-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','BASELINE','{"method":"LINEAR_TREND"}','historical-replay-intelligence',NULL,'PENDING',NULL,NULL,1,now(),now());
    UPDATE core_registry.forecast_training_runs SET status='RUNNING',started_at=now(),revision=revision+1,updated_at=now()+interval '1 second' WHERE id='01990000-2440-7000-8000-000000000001';
    UPDATE core_registry.forecast_training_runs SET status='SUCCEEDED',evaluation='{"acceptance":true}',finished_at=now(),revision=revision+1,updated_at=now()+interval '2 seconds' WHERE id='01990000-2440-7000-8000-000000000001';
    INSERT INTO core_registry.forecast_model_versions(id,tenant_id,site_id,model_id,model_version,training_run_id,dataset_snapshot_id,feature_set_version_id,topology_version_id,artifact_uri,artifact_checksum,evaluation,compatibility,status,revision,created_at,updated_at)
    VALUES ('01990000-2450-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2430-7000-8000-000000000001',1,'01990000-2440-7000-8000-000000000001','01990000-2420-7000-8000-000000000001','01990000-2410-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','s3://replay-intelligence/model.bin',repeat('b',64),'{"acceptance":true}','{"runtime":"go"}','CANDIDATE',1,now(),now());
    UPDATE core_registry.forecast_model_versions SET status='VALIDATED',revision=revision+1,updated_at=now()+interval '1 second' WHERE id='01990000-2450-7000-8000-000000000001';
    INSERT INTO core_registry.forecast_deployments(id,tenant_id,site_id,target,subject_type,subject_id,model_version_id,model_id,feature_set_version_id,topology_version_id,status,effective_from,revision,created_at,updated_at)
    VALUES ('01990000-2460-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','SITE_LOAD','SITE','018f1e00-1000-7000-8000-000000000001','01990000-2450-7000-8000-000000000001','01990000-2430-7000-8000-000000000001','01990000-2410-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','ACTIVE','2026-08-01T00:00:00Z',1,now(),now());

    INSERT INTO core_registry.optimization_policies(id,tenant_id,policy_code,subject_type,resource_type,status,revision,created_at,updated_at)
    VALUES ('01990000-2700-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','replay_hvac_cost','SITE','HVAC','ACTIVE',1,now(),now());
    INSERT INTO core_registry.optimization_policy_versions(id,tenant_id,policy_id,version,objective,weights,constraints,dispatch_mode,fallback_policy,risk_level,horizon,horizon_minutes,granularity,effective_from,status,revision,created_at,updated_at)
    VALUES ('01990000-2710-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','01990000-2700-7000-8000-000000000001',1,'COST','{"cost":1}','{"comfort":{"zoneTempMinC":21,"zoneTempMaxC":25},"safety":{"supplyTempMinC":6,"supplyTempMaxC":10,"maxSupplyTempStepC":1},"inputMapping":{"supplyTemperatureKey":"btu_meter.supply_water_temperature","zoneTemperatureKey":"zone.temperature"},"maintenanceConstraints":{"outOfService":[]},"manualLocks":{"resources":[]},"responseModel":{"dailyEnergyDeltaKWhPerSupplyTempC":-5,"zoneTempDeltaCPerSupplyTempC":0.4,"energyUncertaintyP90KWh":2,"zoneTempUncertaintyP90C":0.2}}','SHADOW','RULE_STRATEGY','LOW','DAY_AHEAD',1440,'15MIN','2026-08-01T00:00:00Z','RELEASED',1,now(),now());
    INSERT INTO core_registry.settlement_boundaries(id,tenant_id,site_id,topology_version_id,boundary_code,display_name,boundary_type,energy_type_id,direction,definition_mode,node_id,effective_from,status,revision,created_at,updated_at)
    VALUES ('01990000-2720-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2300-7000-8000-000000000001','replay_grid_import','Replay Grid Import','GRID_CONNECTION','01990000-0000-7000-8000-000000000001','IMPORT','NODE','01990000-2310-7000-8000-000000000001','2026-08-01T00:00:00Z','DRAFT',1,now(),now());
    UPDATE core_registry.settlement_boundaries SET status='ACTIVE',revision=revision+1,updated_at=now()+interval '1 second' WHERE id='01990000-2720-7000-8000-000000000001';
    INSERT INTO core_registry.tariffs(id,tenant_id,site_id,tariff_code,display_name,energy_type_id,status,revision,created_at,updated_at)
    VALUES ('01990000-2730-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','replay_import_tariff','Replay Import Tariff','01990000-0000-7000-8000-000000000001','ACTIVE',1,now(),now());
    INSERT INTO core_registry.tariff_versions(id,tenant_id,site_id,tariff_id,version,effective_from,timezone,currency,billing_cycle,status,revision,created_at,updated_at)
    VALUES ('01990000-2740-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2730-7000-8000-000000000001',1,'2026-08-01T00:00:00Z','Asia/Shanghai','CNY','CALENDAR_MONTH','RELEASED',1,now(),now());
    INSERT INTO core_registry.tariff_assignments(id,tenant_id,site_id,boundary_id,tariff_id,effective_from,status,revision,created_at,updated_at)
    VALUES ('01990000-2750-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','01990000-2720-7000-8000-000000000001','01990000-2730-7000-8000-000000000001','2026-08-01T00:00:00Z','RELEASED',1,now(),now());
    INSERT INTO core_registry.ai_model_definitions(id,tenant_id,name,provider,model_id,capabilities,status,revision,created_at,updated_at)
    VALUES ('01990000-2760-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','Replay Optimization','LOCAL','hvac-recommendation-v1',ARRAY['optimization'],'ACTIVE',1,now(),now());
    INSERT INTO core_registry.ai_deployment_revisions(id,tenant_id,model_definition_id,use_case,revision,output_schema_version,enabled,created_at)
    VALUES ('01990000-2770-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','01990000-2760-7000-8000-000000000001','OPTIMIZATION',1,'optimization-recommendation-v1',true,now());
    INSERT INTO core_registry.ai_deployment_bindings(id,tenant_id,site_id,use_case,deployment_revision_id,status,revision,created_at,updated_at)
    VALUES ('01990000-2780-7000-8000-000000000001','018f1d00-0000-7000-8000-000000000001','018f1e00-1000-7000-8000-000000000001','OPTIMIZATION','01990000-2770-7000-8000-000000000001','ACTIVE',1,now(),now());
  `);
}

function runIsolated(module, testName, env) {
  return run(process.execPath, [
    'scripts/run-isolated-go.mjs', `--module=${module}`,
    'test', '-count=1', '-run', `^${testName}$`, '-v', './...',
  ], { env: { ...process.env, ...env } });
}

const report = {
  schemaVersion: 1,
  ticket: 348,
  capability: 'historical-replay-through-intelligence-inputs',
  status: 'failed',
  startedAt: new Date().toISOString(),
  assertions: {},
};

try {
  try { telemetryCompose(['down', '--volumes', '--remove-orphans']); } catch {}
  try { registryCompose(['down', '--volumes', '--remove-orphans']); } catch {}
  telemetryCompose(['up', '-d', '--pull=never', 'postgres', 'clickhouse']);
  registryCompose(['up', '-d', '--pull=never', 'postgres']);
  await waitForServices();
  seedRegistry();

  report.assertions.plantScenario = runIsolated('tools/eg8200-simulator', 'TestRunReplayReusesCanonicalPlantAndProducesDeterministicRequests', {});
  report.assertions.telemetry = runIsolated('modules/telemetry', 'TestHistoricalReplayIntelligenceAcceptancePublishesEventTimeHistory', {
    S2_TELEMETRY_TEST_DATABASE_URL: telemetryRuntimeURL,
    S2_TELEMETRY_ADMIN_DATABASE_URL: telemetryAdminURL,
    S2_TELEMETRY_HISTORY_DATABASE_URL: telemetryHistoryURL,
    S2_CLICKHOUSE_HTTP_URL: clickHouseURL,
    S2_CLICKHOUSE_USERNAME: 'telemetry_history',
    S2_CLICKHOUSE_PASSWORD: '',
  });
  report.assertions.energy = runIsolated('modules/energy', 'TestHistoricalReplayCounterHistoryProjectsEnergyFacts', {
    ANALYTICS_CLICKHOUSE_TEST_URL: clickHouseURL,
    ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME: 'telemetry_history',
  });
  report.assertions.metric = runIsolated('modules/metric', 'TestHistoricalReplayHistoryProjectsForecastMetricFacts', {
    HISTORICAL_REPLAY_REGISTRY_DSN: metricRegistryURL,
    HISTORICAL_REPLAY_CLICKHOUSE_URL: clickHouseURL,
  });
  report.assertions.forecast = runIsolated('services/forecast-service', 'TestHistoricalReplayMetricFactsPrepareAndPublishDayAheadForecast', {
    HISTORICAL_REPLAY_FORECAST_DSN: forecastRegistryURL,
    HISTORICAL_REPLAY_CLICKHOUSE_URL: clickHouseURL,
  });
  report.assertions.optimization = runIsolated('services/optimization-service', 'TestHistoricalReplayForecastAndCurrentStatePrepareSealedOptimization', {
    HISTORICAL_REPLAY_OPTIMIZATION_DSN: optimizationRegistryURL,
    HISTORICAL_REPLAY_CLICKHOUSE_URL: clickHouseURL,
  });

  report.assertions.eventTime = clickHouse(`SELECT toString(count()) || '|' || toString(countIf(source_path='HISTORY_REPLAY')) || '|' || toString(countIf(source_offset=9 AND sampled_at=toDateTime64('2026-08-28 10:25:00',3,'UTC'))) || '|' || toString(countIf(source_offset=7 AND sampled_at=toDateTime64('2026-08-28 10:50:00',3,'UTC'))) FROM telemetry_history.observations WHERE device_id=toUUID('018f1e00-4000-7000-8000-000000000001') AND telemetry_key='site.load_kw'`);
  if (report.assertions.eventTime !== '5|5|1|1') throw new Error(`unexpected event-time evidence ${report.assertions.eventTime}`);
  report.assertions.energyFacts = clickHouse(`SELECT arrayStringConcat(arrayMap(x -> toString(x), groupArray(energy_kwh ORDER BY period_end)), ',') FROM analytics.energy_interval_facts WHERE device_id=toUUID('018f1e00-4000-7000-8000-000000000001') AND point_id=toUUID('01990000-3481-7000-8000-000000000002')`);
  if (report.assertions.energyFacts !== '10,15,15,20') throw new Error(`unexpected energy facts ${report.assertions.energyFacts}`);
  report.assertions.metricFacts = clickHouse(`SELECT arrayStringConcat(arrayMap(x -> toString(x), groupArray(value_number ORDER BY period_end)), ',') FROM analytics.metric_result_facts WHERE metric_version_id=toUUID('01990000-3483-7000-8000-000000000002')`);
  if (report.assertions.metricFacts !== '760,790,800,820') throw new Error(`unexpected metric facts ${report.assertions.metricFacts}`);
  report.assertions.forecastPoints = clickHouse(`SELECT toString(count()) || '|' || toString(countIf(quality='VALID')) FROM analytics.forecast_series WHERE tenant_id=toUUID('018f1d00-0000-7000-8000-000000000001') AND site_id=toUUID('018f1e00-1000-7000-8000-000000000001')`);
  if (report.assertions.forecastPoints !== '96|96') throw new Error(`unexpected Forecast owner evidence ${report.assertions.forecastPoints}`);
  report.assertions.currentTruth = telemetryPsql(`SELECT (SELECT count(*) FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id='018f1e00-4000-7000-8000-000000000001'::uuid)::text || '|' || (SELECT business_revision FROM telemetry_runtime.device_observation_snapshots WHERE device_id='018f1e00-4000-7000-8000-000000000001'::uuid)::text || '|' || (SELECT count(*) FROM telemetry_runtime.presence_signals WHERE device_id='018f1e00-4000-7000-8000-000000000001'::uuid)::text`);
  if (report.assertions.currentTruth !== '2|2|2') throw new Error(`unexpected Current truth evidence ${report.assertions.currentTruth}`);
  report.assertions.optimizationStore = registryPsql(`SELECT s.status || '|' || (s.load_forecast_snapshot_id IS NOT NULL)::text || '|' || r.status FROM core_registry.optimization_input_snapshots s JOIN core_registry.optimization_runs r ON r.input_snapshot_id=s.id WHERE s.tenant_id='018f1d00-0000-7000-8000-000000000001'::uuid ORDER BY s.created_at DESC LIMIT 1`);
  if (!report.assertions.optimizationStore.startsWith('SEALED|true|')) throw new Error(`unexpected Optimization owner evidence ${report.assertions.optimizationStore}`);

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Historical Replay intelligence acceptance passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { telemetryCompose(['down', '--volumes', '--remove-orphans']); } catch {}
  try { registryCompose(['down', '--volumes', '--remove-orphans']); } catch {}
}
