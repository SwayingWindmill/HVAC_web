import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const [
  bootstrap, baseline, migration, ingestStore, relay, repository, sink, projector, clickHouseDDL, compose, integration,
  historyModel, aggregateModel, historyClient, aggregateClient, publicContract, internalContract,
] = await Promise.all([
  read('infra/s2-telemetry/postgres/init/000-bootstrap-identities.sql'),
  read('infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql'),
  read('infra/s2-telemetry/postgres/init/004-s2-telemetry-history-outbox.sql'),
  read('services/telemetry-runtime-service/internal/telemetry/ingest_store.go'),
  read('services/telemetry-runtime-service/internal/telemetry/history.go'),
  read('services/telemetry-runtime-service/internal/telemetry/history_postgres.go'),
  read('services/telemetry-runtime-service/internal/telemetry/history_clickhouse.go'),
  read('services/telemetry-runtime-service/cmd/telemetry-history-projector/main.go'),
  read('infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql'),
  read('infra/s2-telemetry/compose.yaml'),
  read('scripts/run-s2-telemetry-history-tests.mjs'),
  read('libs/telemetryhistorymodel/model.go'),
  read('libs/telemetryhistorymodel/aggregation.go'),
  read('services/telemetry-query-service/internal/history/client.go'),
  read('services/telemetry-query-service/internal/history/aggregate.go'),
  read('contracts/http/s2-telemetry-public.openapi.json'),
  read('contracts/http/telemetry-query-internal.openapi.yaml'),
]);

for (const marker of ['s2_telemetry_history', 's2_telemetry_history_service', 'NOINHERIT', 'NOBYPASSRLS']) {
  assert(bootstrap.includes(marker), `missing history identity marker ${marker}`);
}
assert(
  baseline.includes('GRANT EXECUTE ON FUNCTION telemetry_runtime.is_uuid_v7(uuid) TO s2_telemetry_runtime, s2_telemetry_relay, s2_telemetry_history;'),
  'history projector must be able to validate UUIDv7 lease identifiers',
);
for (const marker of [
  'telemetry_history_outbox', "'PENDING', 'IN_FLIGHT', 'PUBLISHED', 'DEAD'", 'FOR INSERT TO s2_telemetry_runtime',
  'FOR SELECT TO s2_telemetry_history', 'FOR UPDATE TO s2_telemetry_history', 'leased_until', 'outbox_payload_sha256',
]) {
  assert(migration.includes(marker), `missing history outbox marker ${marker}`);
}
const observationIndex = ingestStore.indexOf('insertSourceObservation');
const historyIndex = ingestStore.indexOf('insertHistoryOutboxIntent', observationIndex);
const commitIndex = ingestStore.indexOf('tx.Commit', historyIndex);
assert(observationIndex >= 0 && historyIndex > observationIndex && commitIndex > historyIndex, 'history outbox must be written after source observation and before commit');
assert(!ingestStore.toLowerCase().includes('clickhouse'), 'S2 ingest transaction must not directly call ClickHouse');

for (const marker of ['ClaimHistoryBatch', 'MarkHistoryBatchPublished', 'RetryHistoryBatch', 'CLICKHOUSE_INSERT_FAILED']) {
  assert(relay.includes(marker), `missing history relay marker ${marker}`);
}
for (const marker of ['FOR UPDATE SKIP LOCKED', "delivery_state = 'IN_FLIGHT'", 'attempts = outbox.attempts + 1', "THEN 'DEAD' ELSE 'PENDING'"]) {
  assert(repository.includes(marker), `missing PostgreSQL history repository marker ${marker}`);
}
for (const marker of ['insert_deduplication_token', 'async_insert', 'wait_for_async_insert', 'async_insert_deduplicate', 'JSONEachRow']) {
  assert(sink.includes(marker), `missing ClickHouse sink marker ${marker}`);
}
for (const marker of ['TELEMETRY_HISTORY_DATABASE_URL', 'TELEMETRY_CLICKHOUSE_HTTP_URL', 'telemetry-history-projector', 'RelayOnce']) {
  assert(projector.includes(marker), `missing projector marker ${marker}`);
}
for (const marker of [
  'telemetry_history.observations', 'PARTITION BY toYYYYMM(sampled_at)',
  'non_replicated_deduplication_window', 'AggregatingMergeTree', 'observations_to_numeric_hourly', 'countState()', 'avgState', 'numeric_hourly',
]) {
  assert(clickHouseDDL.includes(marker), `missing ClickHouse DDL marker ${marker}`);
}
assert(!clickHouseDDL.includes('TTL '), 'ClickHouse history must not encode retention as a table TTL; retention is governed by the data lifecycle policy');
assert.equal(
  clickHouseDDL.match(/non_replicated_deduplication_window/g)?.length,
  2,
  'raw and hourly materialized-view target tables must both retain deduplication tokens',
);
assert(compose.includes('clickhouse/clickhouse-server:26.3.12.3@sha256:1f7cd090d5c4e2b8bfe0ea5d8ae6125937e1d932c6371b4d25fbd6088829dc9c'), 'ClickHouse image must be version and digest pinned');
assert(compose.includes('./clickhouse/init:/docker-entrypoint-initdb.d:ro'), 'ClickHouse init must be mounted read-only');
assert(integration.includes('pullDockerImageWithRetry'), 'history integration must use bounded immutable-image pull retries');
assert(integration.includes("'--pull=never'"), 'history integration compose startup must not repull images');
for (const marker of ['TestPostgresOutboxProjectsClickHouseHistoryExactlyOnce', 'PUBLISHED|2|true', "'1|1|24.75'", "'1|24.75'"]) {
  assert(integration.includes(marker), `missing ClickHouse integration evidence marker ${marker}`);
}

for (const marker of ['AcceptanceOutOfOrder', 'ValueTypeNumber', 'ValueTypeString', 'ValueTypeBoolean', 'ValueTypeJSON', 'PointRevision', 'SourcePosition', 'NextCursor', 'ProjectionWatermark']) {
  assert(historyModel.includes(marker), `missing typed History model marker ${marker}`);
}
for (const marker of ['PointTypeTelemetry', 'PointTypeCounter', 'PointTypeState', 'AggregateGranularityDay', 'AggregateQualityValidOnly', 'AggregateQualityUsable', 'CounterAggregate', 'StateAggregate']) {
  assert(aggregateModel.includes(marker), `missing typed aggregate model marker ${marker}`);
}
for (const marker of ["acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')", "value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')", 'ORDER BY telemetry_key, sampled_at, toString(observation_id)', 'max(projected_at)', 'LastObservationID']) {
  assert(historyClient.includes(marker), `missing stable typed History query marker ${marker}`);
}
for (const marker of ['RESET_TO_ZERO', 'ROLLOVER', 'REVISION_BOUNDARY', 'UNIT_BOUNDARY', 'previous_quality', 'toStartOfDay', 'toStartOfMonth']) {
  assert(aggregateClient.includes(marker), `missing Point-type/calendar aggregate marker ${marker}`);
}
for (const marker of ['"/api/v1/telemetry/device-series:aggregate"', '"DeviceHistoryObservation"', '"projectionWatermark"', '"pointRevision"', '"sourcePosition"', '"OUT_OF_ORDER"']) {
  assert(publicContract.includes(marker), `missing public History v2 contract marker ${marker}`);
}
for (const marker of ['/internal/v1/telemetry/device-history:aggregate', 'DeviceHistoryAggregateQuery', 'DeviceHistoryObservation', 'projectionWatermark', 'pointRevision', 'sourcePosition']) {
  assert(internalContract.includes(marker), `missing internal History v2 contract marker ${marker}`);
}
const historyOnlyContracts = `${publicContract.slice(publicContract.indexOf('"DeviceHistoryRequest"'))}\n${internalContract.slice(internalContract.indexOf('DeviceHistoryQuery:'))}`;
for (const forbidden of ['maxPointsPerKey', 'datasetRevision', 'dataWatermark', 'returnedPoints', 'truncatedKeys']) {
  assert(!historyOnlyContracts.includes(forbidden), `legacy pseudo History field remains active: ${forbidden}`);
}

console.log('S2 ClickHouse telemetry history architecture check passed.');
