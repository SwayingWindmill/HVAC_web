import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
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
  queryClient,
  compose,
  integration,
  cubeIntegration,
  ownership,
] = await Promise.all([
  read('infra/s2-telemetry/clickhouse/init/002-analytics-energy-interval.sql'),
  read('infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql'),
  read('infra/s2-telemetry/clickhouse/init/004-counter-semantics.sql'),
  read('infra/s2-telemetry/clickhouse/init/003-telemetry-rollups.sql'),
  read('services/analytics-read-model-projector/internal/energy/projector.go'),
  read('services/analytics-read-model-projector/internal/clickhouse/client.go'),
  read('services/analytics-read-model-projector/cmd/analytics-read-model-projector/main.go'),
  read('services/analytics-read-model-projector/go.mod'),
  read('services/telemetry-query-service/go.mod'),
  read('semantic/cube/model/cubes/energy_usage.yml'),
  read('services/telemetry-query-service/internal/cube/client.go'),
  read('infra/s2-telemetry/compose.yaml'),
  read('scripts/run-analytics-history-tests.mjs'),
  read('scripts/run-analytics-cube-tests.mjs'),
  read('contracts/ownership/data-ownership.v1.json'),
]);

for (const marker of [
  'analytics.energy_interval_facts',
  'tenant_id UUID',
  'meter_id UUID',
  'meter_binding_id UUID',
  'topology_version_id UUID',
  'energy_type_id UUID',
  'meter_role LowCardinality(String)',
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
assert(ddl.includes('GRANT SELECT ON telemetry_history.counter_deltas TO analytics_projector_reader'), 'analytics reader must have canonical counter-delta read access');
assert(counterDDL.includes('SQL SECURITY DEFINER'), 'counter delta view must not expose raw observations through invoker permissions');
assert(ddl.includes('GRANT INSERT ON analytics.energy_interval_facts TO analytics_projector_writer'), 'analytics writer must only insert facts');
assert(ddl.includes('GRANT SELECT ON analytics.energy_interval_facts TO cube_analytics_reader'), 'Cube must use a read-only analytics identity');
assert(ddl.includes('GRANT SELECT ON telemetry_history.observations TO telemetry_query_history_reader'), 'Telemetry Query Service must use a raw-history read-only identity');

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
for (const marker of ['counterDeltaQuery', 'fact.source_previous_observation_id = delta.previous_observation_id', "delta.transition_type IN ('INCREASE', 'UNCHANGED', 'RECOVERY', 'RESET', 'ROLLOVER')", 'source_event_id', 'LEFT ANTI JOIN', 'insert_deduplication_token', 'JSONEachRow', 'observability.InjectHTTP']) {
  assert(clickHouseClient.includes(marker), `missing ClickHouse adapter marker ${marker}`);
}
for (const marker of [
  'ANALYTICS_CLICKHOUSE_READER_USERNAME',
  'ANALYTICS_CLICKHOUSE_WRITER_USERNAME',
  'ANALYTICS_CORE_REGISTRY_URL',
  'ANALYTICS_CORE_REGISTRY_GRANT_FILE',
  'ANALYTICS_PROJECTOR_DIAGNOSTICS_ADDR',
  '127.0.0.1:19089',
]) {
  assert(projector.includes(marker), `missing analytics projector runtime marker ${marker}`);
}
assert(!projectorModule.includes('telemetry-runtime-service'), 'analytics projector must not import Telemetry Runtime implementation');
assert(!queryModule.includes('analytics-read-model-projector'), 'Query Service must not import Analytics Projector implementation');

for (const marker of ['max_data_watermark', 'max_dataset_revision', 'tenant_id', 'device_id', 'point_id', 'sensor_id', 'telemetry_key', 'period_end', 'access_policy']) {
  assert(cubeModel.includes(marker), `missing Cube semantic marker ${marker}`);
}
for (const marker of ['buildMetadataQuery', 'maximumCubeQueryDuration', 'Add(-time.Millisecond)', 'coversRequestedBuckets', 'energy_usage.max_data_watermark', 'energy_usage.max_dataset_revision', 'partial = watermark.Before']) {
  assert(queryClient.includes(marker), `missing Query Service metadata marker ${marker}`);
}
for (const marker of ['analytics-read-model-projector:', 'analytics_projector_reader', 'analytics_projector_writer', ':19089']) {
  assert(compose.includes(marker), `missing analytics compose marker ${marker}`);
}
for (const marker of ['TestCanonicalCounterDeltaProjectsEnergyFactsIdempotently', 'TestClickHouseHistoryClientQueriesBoundedRealProjection', 'deviceHistoryQuery', "factCount !== '3'", 'readerCanSelectCanonical', 'readerCannotSelectRaw', 'historyQueryCanSelect', 'historyQueryCannotInsert', 'historyQueryCannotSelectAnalytics', 'readerCannotInsert', 'writerCannotSelect', 'cubeCannotInsert']) {
  assert(integration.includes(marker), `missing analytics integration marker ${marker}`);
}
for (const marker of [
  'energy_usage.energy_valid_kwh',
  'energy_usage.max_data_watermark',
  'deniedSiteRows',
  'randomBytes(32)',
  "CUBEJS_DB_HOST: 'clickhouse'",
  "CUBEJS_DB_PORT: '8123'",
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
