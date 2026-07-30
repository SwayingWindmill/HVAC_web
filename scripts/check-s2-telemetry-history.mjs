import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const [bootstrap, baseline, migration, ingestStore, relay, repository, sink, projector, clickHouseDDL, compose, integration] = await Promise.all([
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
  'telemetry_history.observations', 'PARTITION BY toYYYYMM(sampled_at)', 'TTL sampled_at + INTERVAL 36 MONTH DELETE',
  'non_replicated_deduplication_window', 'AggregatingMergeTree', 'observations_to_numeric_hourly', 'countState()', 'avgState', 'numeric_hourly',
]) {
  assert(clickHouseDDL.includes(marker), `missing ClickHouse DDL marker ${marker}`);
}
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

console.log('S2 ClickHouse telemetry history architecture check passed.');
