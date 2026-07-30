import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const [
  ddl,
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
  'energy_kwh Float64',
  'source_current_observation_id UUID',
  'dataset_revision UInt64',
  "TTL period_end + INTERVAL 36 MONTH DELETE",
  'analytics_projector_reader',
  'analytics_projector_writer',
  'cube_analytics_reader',
]) {
  assert(ddl.includes(marker), `missing analytics DDL marker ${marker}`);
}
assert(ddl.includes('GRANT SELECT ON telemetry_history.observations TO analytics_projector_reader'), 'analytics reader must have raw-history read access');
assert(ddl.includes('GRANT INSERT ON analytics.energy_interval_facts TO analytics_projector_writer'), 'analytics writer must only insert facts');
assert(ddl.includes('GRANT SELECT ON analytics.energy_interval_facts TO cube_analytics_reader'), 'Cube must use a read-only analytics identity');

for (const marker of [
  'hvac_meter.energy',
  'METER_RESET_OR_ROLLBACK',
  'NEGATIVE_CUMULATIVE_VALUE',
  'SOURCE_QUALITY_INVALID',
  'DatasetRevision',
  'DataWatermark',
]) {
  assert(domain.includes(marker), `missing energy-domain marker ${marker}`);
}
for (const marker of ['lagInFrame', 'ORDER BY sampled_at, source_offset, observation_id', 'isFinite(value_number)', 'LEFT ANTI JOIN', 'insert_deduplication_token', 'JSONEachRow', 'observability.InjectHTTP']) {
  assert(clickHouseClient.includes(marker), `missing ClickHouse adapter marker ${marker}`);
}
for (const marker of [
  'ANALYTICS_CLICKHOUSE_READER_USERNAME',
  'ANALYTICS_CLICKHOUSE_WRITER_USERNAME',
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
for (const marker of ['buildMetadataQuery', 'maximumCubeQueryDuration', 'Add(-time.Millisecond)', 'coversRequestedBuckets', 'energy_usage.max_data_watermark', 'energy_usage.max_dataset_revision', 'partial = watermark.Before']) {
  assert(queryClient.includes(marker), `missing Query Service metadata marker ${marker}`);
}
for (const marker of ['analytics-read-model-projector:', 'analytics_projector_reader', 'analytics_projector_writer', ':19089']) {
  assert(compose.includes(marker), `missing analytics compose marker ${marker}`);
}
for (const marker of ['TestCumulativeMeterProjectsAdditiveEnergyFactsIdempotently', "factCount !== '2'", 'readerCannotInsert', 'writerCannotSelect', 'cubeCannotInsert']) {
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
for (const marker of [
  '"schema", "name": "telemetry_history", "writer": "telemetry-history-projector"',
  '"schema", "name": "analytics", "writer": "analytics-read-model-projector"',
  '"projection", "name": "analytics-energy-interval-fact", "writer": "analytics-read-model-projector"',
]) {
  assert(ownership.includes(marker), `missing analytics ownership marker ${marker}`);
}

console.log('Analytics history modular architecture check passed.');
