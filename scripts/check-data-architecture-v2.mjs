import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const baselinePath = resolve(root, 'contracts/data/data-architecture.v2.json');

const baseline = JSON.parse(await readFile(baselinePath, 'utf8'));

function invariant(condition, message) {
  if (!condition) throw new Error(`Data Architecture V2 check failed: ${message}`);
}

function exact(actual, expected) {
  return Array.isArray(actual)
    && actual.length === expected.length
    && actual.every((value, index) => value === expected[index]);
}

const expectedDomains = [
  'Object Model',
  'Device Model',
  'Time Series Model',
  'Energy Topology Model',
  'Metric Model',
  'Metering & Settlement Model',
  'Control Model',
  'Forecast Model',
  'Optimization Model',
  'Governance Model',
];

const expectedGateIds = [
  'SOT_BOUNDARIES',
  'TIMESERIES_OLD_BASELINE_REMOVED',
  'POINT_STANDARD',
  'UNIT_DIRECTION_STANDARD',
  'EVENT_INGEST_TIME',
  'DUPLICATE',
  'OUT_OF_ORDER_HISTORY_LATEST',
  'REDIS_LATEST',
  'UNKNOWN_POINT_QUARANTINE',
  'COUNTER_RESET_ROLLOVER',
  'METER_BINDING',
  'CT_PT_APPLY_ONCE',
  'TOPOLOGY_VERSION',
  'PRIMARY_METER_UNIQUENESS',
  'VIRTUAL_METER_DAG',
  'METRIC_VERSIONING',
  'SETTLEMENT_LOCK',
  'SETTLEMENT_REVISION',
  'CORRECTION_NO_OVERWRITE',
  'FORECAST_TRACEABILITY',
  'OPTIMIZATION_INPUT_SNAPSHOT',
  'LIFECYCLE_POLICY',
  'LEGAL_HOLD',
  'ARCHIVE_BACKUP_SEPARATION',
  'RESTORE_TOMBSTONE',
  'TENANT_ISOLATION',
];

const allowedStatuses = new Set(['PASS', 'PARTIAL', 'MISSING']);

invariant(baseline.schemaVersion === 2, 'schemaVersion must be 2');
invariant(baseline.document?.documentId === 'SE-DATA-001', 'documentId must be SE-DATA-001');
invariant(baseline.document?.version === '2.0', 'document version must be 2.0');
invariant(baseline.document?.status === 'CURRENT', 'document status must be CURRENT');
invariant(baseline.document?.authority === 'source-of-truth', 'V2 must be the data architecture source of truth');

invariant(exact(baseline.canonicalChain, ['Tenant', 'Site', 'Space', 'Asset', 'Device', 'Point', 'Telemetry']), 'canonical chain drifted');
invariant(exact(baseline.approvedRemovals, ['Organization', 'ThingsBoard']), 'approved removals drifted');

invariant(baseline.storageAuthorities?.postgresql === 'business-master-metadata-and-state-machine', 'PostgreSQL authority drifted');
invariant(baseline.storageAuthorities?.clickhouse === 'historical-telemetry-and-analytics', 'ClickHouse authority drifted');
invariant(baseline.storageAuthorities?.redis === 'rebuildable-latest-cache-and-realtime', 'Redis authority drifted');
invariant(baseline.storageAuthorities?.mqtt === 'transport-only', 'MQTT authority drifted');

invariant(baseline.pointModel?.canonicalEntity === 'Point', 'Point must remain canonical');
invariant(baseline.pointModel?.codePattern === '^[a-z][a-z0-9_]{0,127}$', 'Point Code lower_snake_case rule drifted');
invariant(exact(baseline.pointModel?.types, ['TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND']), 'Point types drifted');
invariant(baseline.pointModel?.physicalSensor?.canonical === false, 'Physical Sensor must not become canonical');
invariant(baseline.pointModel?.physicalSensor?.requiredBetweenDeviceAndPoint === false, 'Sensor must not be required between Device and Point');
invariant(baseline.pointModel?.physicalSensor?.allowedOnlyForIndependentPhysicalLifecycle === true, 'Physical Sensor lifecycle rule drifted');

invariant(baseline.telemetryModel?.eventTime === true && baseline.telemetryModel?.ingestTime === true, 'dual-time telemetry semantics drifted');
invariant(baseline.telemetryModel?.historicalOutOfOrder === 'INSERT', 'historical out-of-order policy must INSERT');
invariant(baseline.telemetryModel?.latestOutOfOrder === 'NO_UPDATE', 'Latest out-of-order policy must NO_UPDATE');
invariant(exact(baseline.telemetryModel?.quality, ['GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL', 'STALE', 'INVALID']), 'V2 quality values drifted');
invariant(baseline.telemetryModel?.qualityIndependentFromAcceptance === true, 'quality must remain independent from ingest acceptance');

invariant(exact(baseline.domains?.map((domain) => domain.name), expectedDomains), 'ten-domain model drifted');
for (const domain of baseline.domains ?? []) {
  invariant(allowedStatuses.has(domain.status), `invalid domain status for ${domain.name}`);
}
invariant(exact(baseline.productionGate?.map((gate) => gate.id), expectedGateIds), 'Production Gate inventory drifted');
for (const gate of baseline.productionGate ?? []) {
  invariant(allowedStatuses.has(gate.status), `invalid Production Gate status for ${gate.id}`);
}
invariant(baseline.acceptanceEligible === false, 'acceptance must remain false while V2 blockers exist');
invariant(Array.isArray(baseline.currentBlockers) && baseline.currentBlockers.length > 0, 'current blockers must be explicit while acceptance is false');

const gateStatus = Object.fromEntries(baseline.productionGate.map((gate) => [gate.id, gate.status]));

const registryPointSQL = await readFile(resolve(root, 'infra/registry/postgres/init/007-spatial-sensor-point-model.sql'), 'utf8');
invariant(registryPointSQL.includes("point_code text NOT NULL CHECK (point_code ~ '^[a-z][a-z0-9_]{0,127}$')"), 'Registry Point Code must enforce V2 lower_snake_case');
invariant(registryPointSQL.includes("point_type IN ('TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND')"), 'Registry Point types do not match V2');
invariant(registryPointSQL.includes("source_key text NOT NULL CHECK (source_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$')"), 'vendor/source key must remain separate from canonical Point Code');
invariant(!registryPointSQL.includes('calculated_point_inputs'), 'Calculated Point DAG must not live in canonical Point model');
invariant(!registryPointSQL.includes('sensor_subject_bindings'), 'Physical Sensor must not own canonical measured-subject bindings');
invariant(registryPointSQL.includes('sensor_id uuid'), 'optional Physical Sensor attribution is missing from Point');
invariant(gateStatus.POINT_STANDARD === 'PASS', 'POINT_STANDARD must be PASS when canonical Point SQL evidence is present');

const energyFoundationSQL = await readFile(resolve(root, 'infra/registry/postgres/init/009-energy-data-foundation.sql'), 'utf8');
invariant(energyFoundationSQL.includes("('m3', 'cubic metre', 'VOLUME', 'm3'"), 'Unit Registry must contain canonical m3');
invariant(energyFoundationSQL.includes("('m3/h', 'cubic metre per hour', 'FLOW', 'm3/h'"), 'Unit Registry must contain canonical m3/h');
invariant(!energyFoundationSQL.includes("('m³'") && !energyFoundationSQL.includes("('m³/h'"), 'legacy Unicode cubic metre unit codes must not remain canonical');
invariant(energyFoundationSQL.includes("'IMPORT','EXPORT','GENERATE','CONSUME','CHARGE','DISCHARGE'"), 'Energy Direction inventory drifted');

const topologyMeteringSQL = await readFile(resolve(root, 'infra/registry/postgres/init/009a-energy-topology-metering-v2.sql'), 'utf8');
invariant(topologyMeteringSQL.includes('validate_energy_edge_direction_semantics'), 'Energy Edge direction sign validator is missing');
invariant(topologyMeteringSQL.includes("from_type = 'GRID'") && topologyMeteringSQL.includes("NEW.direction <> 'IMPORT'"), 'Grid import sign rule is missing');
invariant(topologyMeteringSQL.includes("to_type = 'GRID'") && topologyMeteringSQL.includes("NEW.direction <> 'EXPORT'"), 'Grid export sign rule is missing');
invariant(topologyMeteringSQL.includes("from_type = 'ESS'") && topologyMeteringSQL.includes("NEW.direction <> 'DISCHARGE'"), 'ESS discharge sign rule is missing');
invariant(topologyMeteringSQL.includes("to_type = 'ESS'") && topologyMeteringSQL.includes("NEW.direction <> 'CHARGE'"), 'ESS charge sign rule is missing');
invariant(gateStatus.UNIT_DIRECTION_STANDARD === 'PASS', 'UNIT_DIRECTION_STANDARD must be PASS when Unit/Direction SQL evidence is present');

const telemetryIngest = await readFile(resolve(root, 'modules/telemetry/internal/telemetry/ingest.go'), 'utf8');
invariant(telemetryIngest.includes('QualityPartial') && telemetryIngest.includes('QualityInvalid'), 'Telemetry runtime V2 quality model is incomplete');
invariant(!telemetryIngest.includes('QualitySuspect') && !telemetryIngest.includes('QualityRejected'), 'legacy principal quality values remain in runtime');

const historyPostgres = await readFile(resolve(root, 'modules/telemetry/internal/telemetry/history_postgres.go'), 'utf8');
invariant(historyPostgres.includes('decision.Status == ObservationOutOfOrder && decision.PointID != ""'), 'mapped out-of-order facts are not preserved in history');

const telemetryOwnership = JSON.parse(await readFile(resolve(root, 'contracts/ownership/s2-telemetry-ownership.v1.json'), 'utf8'));
invariant(telemetryOwnership.storageAuthorities?.historicalTelemetry?.engine === 'clickhouse', 'Historical Telemetry must remain ClickHouse-owned');
invariant(telemetryOwnership.storageAuthorities?.historicalTelemetry?.authority === 'historical-telemetry-source-of-truth', 'ClickHouse historical authority drifted');
invariant(telemetryOwnership.storageAuthorities?.latestTelemetry?.engine === 'redis', 'Latest Telemetry must be Redis-backed');
invariant(telemetryOwnership.storageAuthorities?.latestTelemetry?.implementationStatus === 'PASS', 'Redis Latest implementation must be complete before REDIS_LATEST passes');
invariant(telemetryOwnership.storageAuthorities?.latestTelemetry?.writeSemantics === 'business-revision-cas', 'Redis Latest must use business revision CAS');
invariant(telemetryOwnership.storageAuthorities?.latestTelemetry?.rebuildFrom === 'postgresql-business-state-machine-snapshot', 'Redis Current Snapshot rebuild source drifted');
invariant(telemetryOwnership.storageAuthorities?.latestTelemetry?.historicalFactAuthority === 'clickhouse-historical-telemetry', 'Redis rebuild metadata must not displace ClickHouse historical fact authority');
invariant(telemetryOwnership.storageAuthorities?.postgresLatestProjection?.authority === 'internal-ingest-evaluator-working-projection', 'PostgreSQL latest projection must remain internal-only');
invariant(telemetryOwnership.storageAuthorities?.postgresLatestProjection?.implementationStatus === 'INTERNAL_ONLY', 'PostgreSQL latest projection status drifted');
invariant(telemetryOwnership.storageAuthorities?.postgresLatestProjection?.publicRead === false, 'PostgreSQL latest projection must not be public read authority');
invariant(telemetryOwnership.storageAuthorities?.mqtt?.authority === 'transport-only', 'MQTT must remain transport-only');

const latestCacheCode = await readFile(resolve(root, 'modules/telemetry/internal/telemetry/latest_cache.go'), 'utf8');
invariant(latestCacheCode.includes('redisLatestCAS') && latestCacheCode.includes('PutIfNewer'), 'Redis Latest revision CAS implementation is missing');
invariant(latestCacheCode.includes('RebuildLatestCache') && latestCacheCode.includes('LatestCacheRebuildSource'), 'Redis Latest rebuild implementation is missing');
const latestCachePostgres = await readFile(resolve(root, 'modules/telemetry/internal/telemetry/latest_cache_postgres.go'), 'utf8');
invariant(latestCachePostgres.includes('device_observation_snapshots') && latestCachePostgres.includes('latest_cache_state = \'PENDING\''), 'Redis Latest rebuild/outbox persistence evidence is missing');
const latestCacheMigration = await readFile(resolve(root, 'infra/telemetry/postgres/init/004d-s2-redis-latest.sql'), 'utf8');
invariant(latestCacheMigration.includes("'NOT_APPLICABLE', 'PENDING', 'MATERIALIZED'"), 'Redis Latest outbox state model is incomplete');
invariant(latestCacheMigration.includes('subscription_id IS NULL') && latestCacheMigration.includes('latest_cache_materialized_at'), 'Redis Latest canonical snapshot materialization boundary is missing');
const telemetryServer = await readFile(resolve(root, 'modules/telemetry/internal/telemetry/server.go'), 'utf8');
invariant(telemetryServer.includes('readLatestSnapshot') && telemetryServer.includes('LatestCache'), 'Current Snapshot read path is not Redis Latest-aware');
const telemetryMain = await readFile(resolve(root, 'cmd/telemetry-worker/main.go'), 'utf8');
invariant(telemetryMain.includes('TELEMETRY_LATEST_CACHE_ENABLED') && telemetryMain.includes('RebuildLatestCache') && telemetryMain.includes('runLatestCacheRelay'), 'Redis Latest production startup/relay path is incomplete');
invariant(gateStatus.REDIS_LATEST === 'PASS', 'REDIS_LATEST must be PASS when Redis CAS, rebuild, materialization and read-path evidence are present');
invariant(gateStatus.SOT_BOUNDARIES === 'PASS', 'SOT_BOUNDARIES must be PASS when PostgreSQL/ClickHouse/Redis/MQTT ownership is V2-aligned');

const objectStorageGovernance = await readFile(resolve(root, 'infra/registry/postgres/init/009g-object-storage-governance-v2.sql'), 'utf8');
invariant(objectStorageGovernance.includes('CREATE TABLE IF NOT EXISTS core_registry.object_storage_buckets'), 'Object Storage bucket purpose registry is missing');
invariant(objectStorageGovernance.includes("purpose IN ('BACKUP','ARCHIVE','COLD_DATA','EVIDENCE','DATASET','MODEL_ARTIFACT','REPORT','OTA')"), 'Object Storage purpose inventory drifted');
invariant(objectStorageGovernance.includes('CREATE TABLE IF NOT EXISTS core_registry.archive_manifests'), 'Archive Manifest ledger is missing');
invariant(objectStorageGovernance.includes('CREATE TABLE IF NOT EXISTS core_registry.backup_manifests'), 'Backup Manifest ledger is missing');
invariant(objectStorageGovernance.includes('Archive Manifest requires an ACTIVE ARCHIVE bucket'), 'Archive bucket-purpose enforcement is missing');
invariant(objectStorageGovernance.includes('Backup Manifest requires an ACTIVE BACKUP bucket'), 'Backup bucket-purpose enforcement is missing');
invariant(objectStorageGovernance.includes('archive-required deletion needs a VERIFIED Archive Manifest; Backup is not Archive'), 'archive-required deletion is not bound to VERIFIED Archive evidence');
invariant(objectStorageGovernance.includes('Restore Run requires a VERIFIED Backup Manifest; Archive is not Backup'), 'Restore is not bound to VERIFIED Backup evidence');
invariant(objectStorageGovernance.includes('DROP COLUMN IF EXISTS backup_id') && objectStorageGovernance.includes('backup_manifest_id uuid NOT NULL'), 'legacy free-form Restore backup identity remains in the CURRENT model');
invariant(gateStatus.ARCHIVE_BACKUP_SEPARATION === 'PASS', 'ARCHIVE_BACKUP_SEPARATION must be PASS when bucket purpose and separate Manifest ledgers are enforced');

const counts = Object.fromEntries([...allowedStatuses].map((status) => [status, baseline.productionGate.filter((gate) => gate.status === status).length]));
console.log(`Data Architecture V2 baseline passed: domains=${baseline.domains.length}; gates=${baseline.productionGate.length}; PASS=${counts.PASS}; PARTIAL=${counts.PARTIAL}; MISSING=${counts.MISSING}; acceptanceEligible=${baseline.acceptanceEligible}`);
