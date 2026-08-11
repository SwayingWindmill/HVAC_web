import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { resolveCapabilityTask } from './domain-task-matrix.mjs';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));

function assert(condition, message) {
  if (!condition) throw new Error(`Invalid S2 Ticket 04 trusted telemetry ingest: ${message}`);
}

function includesAll(source, markers, label) {
  for (const marker of markers) assert(source.includes(marker), `${label} is missing ${marker}`);
}

const aggregateCommandLabels = (task) => {
  const packageCommand = packageJSON.scripts?.[task];
  const taskMatch = packageCommand?.match(/^node scripts\/run-capability-task\.mjs --task=([^\s]+)$/u);
  if (taskMatch) {
    if (taskMatch[1] !== task) return [];
    try {
      return resolveCapabilityTask(task).map(({ label }) => label);
    } catch {
      return [];
    }
  }
  return typeof packageCommand === 'string'
    ? packageCommand.split(/\s*&&\s*/u).map((command) => command.trim()).filter(Boolean)
    : [];
};

const [
  decisionGo,
  ingestStoreGo,
  coverageGo,
  sourceServerGo,
  snapshotStoreGo,
  mainGo,
  decisionTests,
  sourceServerTests,
  postgresTests,
  httpPostgresTests,
  migrationSQL,
  fixtureSQL,
  packageJSON,
  dataRegistry,
  ownershipLock,
  routeRegistry,
  workflow,
  runner,
] = await Promise.all([
  readText('services/telemetry-runtime-service/internal/telemetry/ingest.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/ingest_store.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/coverage.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/source_server.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/store.go'),
  readText('services/telemetry-runtime-service/cmd/telemetry-runtime-service/main.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/ingest_test.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/source_server_test.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/ingest_postgres_integration_test.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/ingest_http_postgres_test.go'),
  readText('infra/s2-telemetry/postgres/init/003-s2-telemetry-ingest.sql'),
  readText('infra/s2-telemetry/postgres/init/004-s2-telemetry-fixtures.sql'),
  readJSON('package.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readText('.github/workflows/s2-telemetry-ingest.yml'),
  readText('scripts/run-s2-telemetry-ingest-postgres-tests.mjs'),
]);

includesAll(decisionGo, [
  'SourcePathWebhook',
  'SourcePathPush',
  'SourcePathPoll',
  'SourcePathReconciliation',
  'ObservationAccepted',
  'ObservationRejected',
  'ObservationQuarantined',
  'ObservationDuplicate',
  'ObservationOutOfOrder',
  'QualityReasonSourceLagExceeded',
  'QualityReasonDuplicate',
  'QualityReasonOutOfOrder',
  'QualityReasonReplayed',
  'QuarantineMappingNotFound',
  'QuarantineMappingConflict',
  'QuarantineMappingQuarantined',
  'QuarantineMappingRetired',
  'QuarantinePolicyNotConfigured',
  'binding.ValidFrom',
  'binding.ValidTo',
  'func EvaluateObservation',
  'decision.ReplaceLatest = true',
  'decision.EmitPresenceSignal = true',
  'decision.ReevaluateSnapshot = true',
], 'source acceptance decision');

includesAll(ingestStoreGo, [
  'pgx.Serializable',
  'SET LOCAL ROLE s2_telemetry_runtime',
  ':partition:',
  ':event:',
  'source_positions',
  'source_observations',
  'source_delivery_evidence',
  'ON CONFLICT (',
  'LEAST(telemetry_runtime.source_delivery_evidence.detected_at',
  'ingest_quarantine',
  'latest_accepted_telemetry',
  'presence_signals',
  'evaluateAndPersistDevice',
  'if decision.Status == ObservationAccepted',
  'payloadSha256',
  'retryableTelemetryTransaction',
  'pgconn.SafeToRetry',
  '57P01',
], 'PostgreSQL source acceptance transaction');
assert(!ingestStoreGo.includes('legacy'), 'source acceptance must not write Legacy state');
assert(!ingestStoreGo.includes('thingsboard_ts'), 'source acceptance must not write ThingsBoard current-state storage');
const rawEvidenceBlock = ingestStoreGo.slice(ingestStoreGo.indexOf('func insertSourceObservation'), ingestStoreGo.indexOf('func (store *PostgresStore) insertQuarantine'));
assert(rawEvidenceBlock.indexOf('if decision.Status == ObservationAccepted') < rawEvidenceBlock.indexOf('INSERT INTO telemetry_runtime.source_observations'), 'only accepted observations may retain raw values');

includesAll(coverageGo, [
  'type CoverageReport struct',
  'SourceRevision',
  'FOR UPDATE',
  'observation_coverage',
  'report.SourceRevision <= currentRevision',
  'evaluateAndPersistDevice',
  'AvailabilityReasonCodeSourceUnavailable',
  'AvailabilityReasonCodeObservationCoverageGap',
  'insertCoverageQuarantine',
  'OBSERVATION_COVERAGE_REPORT',
], 'coverage outage/recovery transaction');
includesAll(snapshotStoreGo, ['func (store *PostgresStore) evaluateAndPersistDevice', 'candidateRevision = previousRevision + 1', 'persistCurrentState', 'insertOutboxIntent'], 'shared Snapshot transaction helper');

includesAll(sourceServerGo, [
  'InternalSourceObservationPath',
  'InternalThingsBoardCoveragePath',
  'ParseSourceAuthenticatorJSON',
  'spiffe',
  'uuidV7Pattern',
  'verifiedPeerSPIFFE',
  'hasForgedIdentityHeader',
  'trustedSourcePeer',
  'decodeSourceRequest',
  'mime.ParseMediaType',
  'mediaType != "application/json"',
  'AllowsSource(peer, candidate.IntegrationInstanceID)',
  'normalizeSourceObservation',
  'TELEMETRY_SOURCE_IDENTITY_INVALID',
  'TELEMETRY_SOURCE_UNAVAILABLE',
], 'internal ThingsBoard source seam');
assert(!sourceServerGo.includes('/api/v1/'), 'source seam must not activate public Gateway routes');
assert(!sourceServerGo.includes('/api/telemetry/ingest'), 'source seam must not expose Legacy ingest');
includesAll(mainGo, [
  'TELEMETRY_SOURCE_BINDINGS_JSON',
  'TELEMETRY_SOURCE_BINDINGS_INVALID',
  'ParseSourceAuthenticatorJSON',
  'ObservationAcceptor:',
  'CoverageReporter:',
  'SourceAuthenticator:',
  'tls.RequireAndVerifyClientCert',
], 'Telemetry Runtime source startup configuration');

includesAll(decisionTests, [
  'TestEvaluateObservationFailsClosedOnMapping',
  'TestEvaluateObservationFreezesPositionSemantics',
  'TestEvaluateObservationValidationAndQuality',
  'TestEvaluateObservationAcceptsGoodCandidate',
], 'source acceptance conformance tests');
includesAll(sourceServerTests, [
  'TestParseSourceAuthenticatorJSONRequiresExactSPIFFEAndIntegrationBindings',
  'TestThingsBoardSourceModesReuseOneAcceptancePath',
  'TestThingsBoardSourceAuthenticationAndScopeFailClosed',
  'TestThingsBoardSourceFailsClosedOnMalformedAndDependencyFailure',
  'TestThingsBoardCoverageReportsOutageAndRecovery',
  'TestLegacyIngestPathDoesNotExist',
  'JSON prefix bypass',
], 'source HTTP tests');
includesAll(httpPostgresTests, [
  'acceptObservationViaHTTP',
  'InternalSourceObservationPath',
  'verifiedTLSState',
], 'source HTTP to PostgreSQL helper');
includesAll(postgresTests, [
  'TestPostgresIngestEndToEnd',
  'ONLY_REJECTED_CANDIDATES',
  'QualityReasonSourceLagExceeded',
  'AvailabilityReasonCodeSourceUnavailable',
  'OBSERVATION_COVERAGE_GAP',
  'outbox failure unexpectedly committed observation',
  'Organization B isolation failed',
  'OBSERVATION_COVERAGE_REPORT',
  'pg_terminate_backend',
  'restart duplicate',
  'CENTRIFUGO_UNAVAILABLE',
  '48*time.Hour',
  'QualityReasonClockAhead',
], 'PostgreSQL ingest integration tests');

includesAll(migrationSQL, [
  'max_future_clock_skew_seconds',
  'max_source_lag_seconds',
  'expected_sample_interval_seconds',
  'value_type text',
  'expected_unit',
  'minimum_number',
  'maximum_number',
  'source_path',
  'quality text',
  'source_delivery_evidence',
  'source_delivery_evidence_identity_uidx',
  'ingest_quarantine_open_coverage_identity_uidx',
  'FORCE ROW LEVEL SECURITY',
  'source_delivery_evidence_runtime_all',
  'POLICY_NOT_CONFIGURED',
  'drop_future_clock_guard',
  "ILIKE '%received_at%sampled_at%24%'",
], 'trusted ingest expand migration');
assert(migrationSQL.includes('NOT VALID') && migrationSQL.includes('later contract phase may validate'), 'configured policy constraint must remain expand-safe');
for (const forbidden of ['DROP TABLE', 'DROP COLUMN', 'ALTER COLUMN TYPE', 'TRUNCATE']) {
  assert(!migrationSQL.toUpperCase().includes(forbidden), `ingest migration is not expand-only: ${forbidden}`);
}
includesAll(fixtureSQL, [
  'max_future_clock_skew_seconds',
  'expected_sample_interval_seconds',
  "'RECONCILIATION'",
  "'REJECTED', 'REJECTED'",
], 'deterministic ingest fixtures');
assert(!fixtureSQL.includes('"invalid"'), 'rejected fixture must not retain raw telemetry');

assert(dataRegistry.registryRevision >= 9 && ownershipLock.dataRegistryRevision === dataRegistry.registryRevision, 'data ownership revision must remain monotonic and locked');
const resources = new Map((dataRegistry.resources ?? []).map((resource) => [`${resource.kind}:${resource.name}`, resource]));
for (const [name, revision] of [['source-observation-evidence', 1], ['ingest-deduplication', 2], ['ingest-quarantine', 2]]) {
  const resource = resources.get(`projection:${name}`);
  assert(resource?.writer === 'telemetry-runtime-service' && resource?.revision === revision, `${name} ownership drifted`);
  assert(ownershipLock.resources?.[`projection:${name}`]?.writer === 'telemetry-runtime-service' && ownershipLock.resources?.[`projection:${name}`]?.revision === revision, `${name} lock drifted`);
}
for (const name of ['observation-coverage', 'latest-accepted-telemetry', 'presence-signal', 'device-observation-snapshot']) {
  assert(resources.get(`projection:${name}`)?.writer === 'telemetry-runtime-service', `${name} writer drifted`);
}
const publicRoutes = (routeRegistry.routes ?? []).filter((route) => route.owner === 'telemetry-runtime-service');
assert(publicRoutes.length === 4, 'public S2 route count drifted');
for (const route of publicRoutes) {
  assert(route.activationStatus === 'expand-baseline' && route.rollout?.mode === 'disabled' && route.migrationPhase === 'R0-contract-only', `${route.method} ${route.path} activated production traffic`);
  assert(route.readOnlyFallback === false && route.readFallbackOwner === undefined, `${route.method} ${route.path} gained request fallback`);
}

const expectedScripts = {
  's2:ingest:check': 'node scripts/check-s2-telemetry-ingest.mjs',
  's2:ingest:postgres': 'node scripts/run-s2-telemetry-ingest-postgres-tests.mjs',
};
for (const [name, command] of Object.entries(expectedScripts)) assert(packageJSON.scripts?.[name] === command, `${name} is not wired`);
const ticketCommands = aggregateCommandLabels('s2:telemetry-ingest');
assert(ticketCommands.length > 0, 's2:telemetry-ingest is missing');
for (const command of [
  'npm run s2:ingest:check',
  'test ./services/telemetry-runtime-service/...',
  'vet ./services/telemetry-runtime-service/...',
  'npm run build:telemetry-runtime',
  'npm run ownership:check',
  'npm run s2:baseline:check',
  'npm run s2:runtime:check',
  'npm run s2:iam:check',
  'npm run s2:contracts:check',
  'npm run contracts:check',
  'npm run release:evidence-assets',
  'npm run lint',
  'npm run build',
  'npm run s2:ingest:postgres',
]) assert(ticketCommands.some((label) => label.includes(command)), `s2:telemetry-ingest omits ${command}`);
includesAll(workflow, ['runs-on: ubuntu-24.04', 'go-version: "1.25.12"', 'node-version: "22.22.0"', 'npm run s2:telemetry-ingest', 's2-telemetry-ingest-evidence'], 'Ticket 04 workflow');
includesAll(runner, ['TestPostgresIngestEndToEnd', 'docker', 'restart', 'restartDurability', 'deliveryEvidence', 'rejectedAndQuarantined', 'coverageQuarantine', 'twoOrganizationIsolation'], 'Ticket 04 PostgreSQL runner');

const allSources = `${decisionGo}\n${ingestStoreGo}\n${coverageGo}\n${sourceServerGo}\n${mainGo}`.toLowerCase();
for (const forbidden of ['centrifugo client', 'redis client', 'legacy-hvac-backend', 'reverse sync', 'historical warehouse']) {
  assert(!allSources.includes(forbidden), `Ticket 04 crossed an out-of-scope boundary: ${forbidden}`);
}

console.log('S2 Ticket 04 trusted telemetry ingest passed: exact source scope, one acceptance path, bounded evidence, atomic Snapshot/outbox and restart durability.');
