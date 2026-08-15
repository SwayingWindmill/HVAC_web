import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const text = (path) => readFile(resolve(root, path), 'utf8');
const json = async (path) => JSON.parse(await text(path));
const assert = (condition, message) => {
  if (!condition) throw new Error(`S2 telemetry ownership check failed: ${message}`);
};

const [contract, dataArchitecture, clickhouse, ingest, ingestStore, history, mqttRuntime, context] = await Promise.all([
  json('contracts/ownership/s2-telemetry-ownership.v1.json'),
  json('contracts/data/data-architecture.v2.json'),
  text('infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql'),
  text('services/telemetry-runtime-service/internal/telemetry/ingest.go'),
  text('services/telemetry-runtime-service/internal/telemetry/ingest_store.go'),
  text('services/telemetry-runtime-service/internal/telemetry/history_postgres.go'),
  text('services/mqtt-telemetry-adapter/internal/adapter/envelope.go'),
  text('CONTEXT.md'),
]);

assert(contract.schemaVersion === 1, 'contract schemaVersion must be 1');
assert(contract.decisionRevision === 3, 'decisionRevision must be 3 for V2 convergence');
assert(contract.activationStatus === 'v2-convergence', 'activationStatus must be v2-convergence');
assert(contract.sourceOfTruth === 'SE-DATA-001 V2.0 CURRENT', 'V2 must be the ownership source of truth');
assert(contract.ownerService === 'telemetry-runtime-service', 'Telemetry Runtime owner drifted');

const stores = contract.storageAuthorities ?? {};
assert(stores.operationalIngest?.engine === 'postgresql', 'PostgreSQL operational ingest store missing');
assert(stores.operationalIngest?.authority === 'ingest-transaction-dedup-quarantine-outbox', 'PostgreSQL authority exceeded V2 boundary');
assert(stores.historicalTelemetry?.engine === 'clickhouse', 'historical telemetry must use ClickHouse');
assert(stores.historicalTelemetry?.authority === 'historical-telemetry-source-of-truth', 'ClickHouse historical authority drifted');
assert(stores.latestTelemetry?.engine === 'redis', 'V2 Latest target must be Redis');
assert(stores.latestTelemetry?.authority === 'rebuildable-latest-cache', 'Redis Latest must be rebuildable cache authority');
assert(stores.latestTelemetry?.implementationStatus === 'PARTIAL', 'Redis Latest must remain visibly partial until implemented');
assert(stores.postgresLatestProjection?.mustNotBeFinalV2Authority === true, 'PostgreSQL Latest must not be final V2 authority');
assert(stores.mqtt?.authority === 'transport-only', 'MQTT must remain transport-only');
assert(stores.transportHistory?.authority === 'ephemeral-continuity-cache', 'transport history authority drifted');

assert(dataArchitecture.storageAuthorities?.clickhouse === 'historical-telemetry-and-analytics', 'V2 baseline ClickHouse boundary drifted');
assert(dataArchitecture.storageAuthorities?.redis === 'rebuildable-latest-cache-and-realtime', 'V2 baseline Redis boundary drifted');
assert(dataArchitecture.storageAuthorities?.mqtt === 'transport-only', 'V2 baseline MQTT boundary drifted');

assert(contract.ingestSources?.length === 1 && contract.ingestSources[0]?.service === 'mqtt-telemetry-adapter', 'MQTT adapter must be the canonical S2 ingest source');
assert(contract.ingestSources[0]?.unknownMajorVersion === 'REJECT', 'unknown MQTT schema major must reject');

const semantics = contract.telemetrySemantics ?? {};
assert(semantics.canonicalPoint === true, 'Point must remain canonical');
assert(semantics.eventTimeField === 'sampled_at' && semantics.ingestTimeField === 'received_at', 'event/ingest dual time drifted');
assert(semantics.outOfOrder?.history === 'INSERT_FULL_FACT', 'out-of-order history policy must preserve full fact');
assert(semantics.outOfOrder?.latest === 'NO_UPDATE', 'out-of-order Latest policy must not regress');
assert(JSON.stringify(semantics.quality) === JSON.stringify(['GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL', 'STALE', 'INVALID']), 'V2 quality values drifted');
assert(semantics.qualityIndependentFromAcceptance === true, 'quality must be independent from ingest acceptance');
assert(semantics.unknownPoint === 'QUARANTINE', 'unknown Point must quarantine');

assert(clickhouse.includes('CREATE TABLE IF NOT EXISTS telemetry_history.observations'), 'ClickHouse historical observations table missing');
assert(clickhouse.includes('sampled_at') && clickhouse.includes('received_at'), 'ClickHouse dual-time fields missing');
assert(ingest.includes('ObservationOutOfOrder'), 'runtime out-of-order decision missing');
assert(ingest.includes('QualityPartial') && ingest.includes('QualityInvalid'), 'runtime V2 quality model incomplete');
assert(!ingest.includes('QualitySuspect') && !ingest.includes('QualityRejected'), 'legacy principal quality values remain');
assert(ingestStore.includes('decision.Status == ObservationOutOfOrder && decision.PointID != ""'), 'mapped out-of-order value is not persisted');
assert(history.includes('decision.Status == ObservationOutOfOrder && decision.PointID != ""'), 'mapped out-of-order fact is not projected to history');
assert(mqttRuntime.includes('TelemetryEnvelopeSchemaVersion'), 'MQTT adapter schema-version gate missing');

assert(contract.historicalTimeseries?.insideS2Slice === true, 'historical telemetry must be inside current S2 architecture');
assert(contract.historicalTimeseries?.authority === 'clickhouse.telemetry_history.observations', 'historical authority must be ClickHouse observations');
assert(contract.historicalTimeseries?.postgresOperationalEvidenceIsAuthority === false, 'PostgreSQL source_observations must not be historical authority');
assert(contract.currentState?.v2Authority === 'redis-rebuildable-latest-cache', 'current-state target must be Redis');
assert(contract.currentState?.implementationStatus === 'PARTIAL', 'Redis Latest incomplete work must remain explicit');
assert(contract.currentState?.temporaryPostgresProjectionExists === true, 'current migration reality must remain explicit until Redis Latest is complete');

const serialized = JSON.stringify(contract).toLowerCase();
assert(!serialized.includes('thingsboard'), 'ThingsBoard remains in current S2 ownership contract');
assert(!serialized.includes('legacy-hvac-backend'), 'legacy telemetry compatibility remains in current S2 ownership contract');

for (const forbidden of [
  'browser-to-mqtt',
  'mqtt-direct-to-latest-without-ingest-validation',
  'postgres-operational-ingest-evidence-as-historical-authority',
  'out-of-order-to-latest-regression',
  'transport-history-to-latest-authority',
  'browser-direct-to-clickhouse',
]) {
  assert(contract.forbiddenFlows?.includes(forbidden), `forbidden flow missing: ${forbidden}`);
}

for (const term of ['## Telemetry Runtime', '## Device Observation Snapshot', '## Business Revision', '## Source Position', '## Transport Position', '## Recovery Cursor', '## Ingest Quarantine']) {
  assert(context.includes(term), `CONTEXT.md is missing domain term: ${term}`);
}

console.log('S2 telemetry ownership checks passed: MQTT is transport-only, ClickHouse owns history, Redis is the explicit V2 Latest target, and PostgreSQL Latest remains a visible migration projection only.');
