import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const [
  ddl,
  historyDDL,
  counterDDL,
  rollupDDL,
  domain,
  clickHouseClient,
  projector,
  projectorModule,
  queryModule,
  cubeModel,
  cubeViews,
  queryClient,
  compose,
  integration,
  cubeIntegration,
  ownership,
  hourlyDDL,
  hourlyBackfill,
] = await Promise.all([
  read('infra/telemetry/clickhouse/init/002-analytics-energy-interval.sql'),
  read('infra/telemetry/clickhouse/init/001-telemetry-history.sql'),
  read('infra/telemetry/clickhouse/init/004-counter-semantics.sql'),
  read('infra/telemetry/clickhouse/init/003-telemetry-rollups.sql'),
  read('modules/energy/internal/energy/projector.go'),
  read('modules/energy/internal/clickhouse/client.go'),
  read('modules/energy/cmd/energy-projector/main.go'),
  read('modules/energy/go.mod'),
  read('modules/telemetry/go.mod'),
  read('semantic/cube/model/cubes/energy_facts.yml'),
  read('semantic/cube/model/views/energy.yml'),
  read('modules/telemetry/internal/cube/client.go'),
  read('infra/telemetry/compose.yaml'),
  read('scripts/run-analytics-history-tests.mjs'),
  read('scripts/run-analytics-cube-tests.mjs'),
  read('contracts/ownership/data-ownership.v1.json'),
  read('infra/telemetry/clickhouse/init/015-energy-hourly-rollup.sql'),
  read('infra/telemetry/clickhouse/ops/backfill-energy-hourly-rollup.sh'),
]);

for (const marker of [
  'analytics.energy_interval_facts',
  'tenant_id UUID',
  'meter_id UUID',
  'meter_binding_id UUID',
  'topology_version_id UUID',
  'energy_type_id UUID',
  'meter_role LowCardinality(String)',
  'accounting_scope LowCardinality(String)',
  'load_device_id Nullable(UUID)',
  'transition_type LowCardinality(String)',
  'point_id UUID',
  'sensor_id Nullable(UUID)',
  'energy_kwh Float64',
  'source_current_observation_id UUID',
  'dataset_revision UInt64',
  'fact_revision UInt64',
  'analytics_projector_reader',
  'analytics_projector_writer',
  'cube_analytics_reader',
  'telemetry_query_history_reader',
]) {
  assert(ddl.includes(marker), `missing analytics DDL marker ${marker}`);
}
assert(!ddl.includes('observation_count'), 'obsolete observation_count column remains in the Energy fact schema');
for (const source of [ddl, historyDDL, rollupDDL]) {
  assert(!source.includes('owning_organization_id') && !source.includes('organization_id'), 'Organization remains a ClickHouse telemetry fact dimension');
  assert(!/\bTTL\b/u.test(source), 'ClickHouse DDL hard-codes retention TTL instead of using Governance policy');
}
assert(!rollupDDL.includes('numeric_daily') && !rollupDDL.includes('toStartOfDay(sampled_at)'), 'UTC daily rollup is incorrectly treated as a Site business day');
assert(historyDDL.includes('tenant_id Nullable(UUID)') && historyDDL.includes('site_id Nullable(UUID)'), 'raw history Tenant/Site fact scope is missing');
assert(counterDDL.includes('SQL SECURITY DEFINER'), 'counter delta view must not expose raw observations through invoker permissions');
assert(ddl.includes('GRANT INSERT ON analytics.energy_interval_facts TO analytics_projector_writer'), 'analytics writer must only insert facts');
assert(ddl.includes('GRANT SELECT ON analytics.energy_interval_facts TO cube_analytics_reader'), 'Cube must use a read-only analytics identity');
assert(ddl.includes('GRANT SELECT ON telemetry_history.observations TO telemetry_query_history_reader'), 'Telemetry Query Service must use a raw-history read-only identity');

// 015: the hourly rollup follows every fact revision through two materialized views, and the
// bounded counter deltas filter points on the observations key.
for (const marker of [
  'raw.point_id IN {points:Array(UUID)}',
  'analytics.energy_hourly_states',
  'ENGINE = AggregatingMergeTree',
  'analytics.energy_facts_to_hourly',
  'analytics.energy_fact_corrections_to_hourly',
  'prior.fact_revision + 1 = correction.fact_revision',
  'HAVING interval_count != 0',
  'GRANT SELECT ON analytics.energy_hourly TO cube_analytics_reader',
]) {
  assert(hourlyDDL.includes(marker), `missing hourly rollup DDL marker ${marker}`);
}
// The ClickHouse entrypoint runs every init script, so an operations script there would run on a fresh volume.
assert(!(await readdir(resolve(root, 'infra/telemetry/clickhouse/init'))).some((name) => !name.endsWith('.sql')), 'only SQL belongs in the ClickHouse init directory');
for (const marker of ['--resume', '--verify', 'insert_deduplication_token', 'energy_interval_facts_visible', 'telemetry-worker']) {
  assert(hourlyBackfill.includes(marker), `missing hourly rollup backfill marker ${marker}`);
}
assert(cubeModel.includes('FROM analytics.energy_hourly') && cubeModel.includes('interval_count'), 'Site and equipment energy must read whole hours from the hourly rollup');

for (const marker of [
  'counter_deltas',
  'BindingResolver',
  'TransitionIncrease',
  'FactRevision',
  'SOURCE_QUALITY_INVALID',
  'DatasetRevision',
  'DataWatermark',
]) {
  assert(domain.includes(marker), `missing energy-domain marker ${marker}`);
}
for (const marker of ['counterDeltaQuery', "delta.transition_type IN ('INCREASE', 'UNCHANGED', 'RECOVERY', 'RESET', 'ROLLOVER')", 'source_event_id', 'insert_deduplication_token', 'JSONEachRow', 'observability.InjectHTTP']) {
  assert(clickHouseClient.includes(marker), `missing ClickHouse adapter marker ${marker}`);
}
for (const marker of [
  'ANALYTICS_CLICKHOUSE_READER_USERNAME',
  'ANALYTICS_CLICKHOUSE_WRITER_USERNAME',
  'ANALYTICS_CORE_REGISTRY_URL',
  'ANALYTICS_IAM_URL',
  'ANALYTICS_PROJECTOR_DIAGNOSTICS_ADDR',
  '127.0.0.1:19089',
]) {
  assert(projector.includes(marker), `missing analytics projector runtime marker ${marker}`);
}
assert(!projectorModule.includes('telemetry-runtime-service'), 'analytics projector must not import Telemetry Runtime implementation');
assert(!queryModule.includes('analytics-read-model-projector'), 'Query Service must not import Analytics Projector implementation');

for (const marker of ['max_data_watermark', 'max_dataset_revision', 'period_end', 'access_policy']) {
  assert(cubeModel.includes(marker), `missing Cube semantic marker ${marker}`);
}
assert(cubeViews.includes('access_policy'), 'Cube views must grant their members through an access policy');
for (const marker of ['buildMetadataQuery', 'maximumCubeQueryDuration', 'Add(-time.Millisecond)', 'coversRequestedBuckets', '"site_energy"', '"equipment_energy"', 'partial = watermark.Before']) {
  assert(queryClient.includes(marker), `missing Query Service metadata marker ${marker}`);
}
for (const marker of ['analytics-read-model-projector:', 'analytics_projector_reader', 'analytics_projector_writer', ':19089']) {
  assert(compose.includes(marker), `missing analytics compose marker ${marker}`);
}
for (const marker of ['TestCanonicalCounterDeltaProjectsEnergyFactsIdempotently', 'TestLateCounterObservationCorrectsSuccessorFactFromCheckpoint', 'TestClickHouseHistoryClientQueriesBoundedRealProjection', 'deviceHistoryQuery', "factCount !== '3'", 'readerCanSelectCanonical', 'readerCannotSelectRaw', 'historyQueryCanSelect', 'historyQueryCannotInsert', 'historyQueryCannotSelectAnalytics', 'readerCannotInsert', 'writerCannotSelect', 'cubeCannotInsert', 'boundedCounterDeltasIndex', 'hourlyRollupCorrection', 'hourlyRollupDrift', 'cubeCanSelectHourly']) {
  assert(integration.includes(marker), `missing analytics integration marker ${marker}`);
}
for (const marker of [
  'deniedSiteRows',
  'corrected rollup',
  'randomBytes(32)',
  "CUBEJS_DB_HOST: 'clickhouse'",
  "CUBEJS_DB_PORT: '8123'",
  'waitForClickHouseHTTP',
  "'network', 'connect', sourceNetwork, cubeContainer",
]) {
  assert(cubeIntegration.includes(marker), `missing Cube integration marker ${marker}`);
}
const ownershipRegistry = JSON.parse(ownership);
for (const expected of [
  { kind: 'schema', name: 'telemetry_history', writer: 'telemetry-history-projector' },
  { kind: 'schema', name: 'analytics', writer: 'analytics-read-model-projector' },
  { kind: 'projection', name: 'analytics-energy-interval-fact', writer: 'analytics-read-model-projector' },
]) {
  assert(
    (ownershipRegistry.resources ?? []).some((resource) => resource.kind === expected.kind && resource.name === expected.name && resource.writer === expected.writer),
    `missing analytics ownership resource ${expected.kind}:${expected.name}`,
  );
}

console.log('Analytics history modular architecture check passed.');
