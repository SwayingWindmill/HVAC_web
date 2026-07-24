import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));

function assert(condition, message) {
  if (!condition) throw new Error(`Invalid S2 Ticket 03 Telemetry Runtime Snapshot: ${message}`);
}

function includesAll(source, markers, label) {
  for (const marker of markers) assert(source.includes(marker), `${label} is missing ${marker}`);
}

const [
  evaluatorGo,
  storeGo,
  authorizationGo,
  serverGo,
  mainGo,
  evaluatorTests,
  authorizationTests,
  serverTests,
  postgresTests,
  runtimeSQL,
  fixtureSQL,
  packageJSON,
  dataRegistry,
  ownershipLock,
  routeRegistry,
  workflow,
] = await Promise.all([
  readText('services/telemetry-runtime-service/internal/telemetry/evaluator.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/store.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/authorization.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/server.go'),
  readText('services/telemetry-runtime-service/cmd/telemetry-runtime-service/main.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/evaluator_test.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/authorization_test.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/server_test.go'),
  readText('services/telemetry-runtime-service/internal/telemetry/postgres_integration_test.go'),
  readText('infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql'),
  readText('infra/s2-telemetry/postgres/init/003-s2-telemetry-fixtures.sql'),
  readJSON('package.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readText('.github/workflows/s2-telemetry-runtime-snapshot.yml'),
]);

includesAll(evaluatorGo, [
  'type DeviceFacts struct',
  'type CanonicalEvaluation struct',
  'func EvaluateCanonical',
  'func ProjectSnapshot',
  'DevicePresenceStateUnknown',
  'DevicePresenceStateOffline',
  'EXPLICIT_DISCONNECT',
  'AcceptedSignalTypes',
  'EvaluationAvailabilityUnavailable',
  'TelemetryFreshnessStale',
  'TelemetryQualitySuspect',
  'contractTelemetryValue',
  'canonicalQualityReasons',
  'LastKnownPresence',
  'func stateDigest',
], 'Presence/Freshness evaluator');
includesAll(storeGo, [
  'pgx.Serializable',
  'SET LOCAL ROLE s2_telemetry_runtime',
  'FOR UPDATE',
  'state_sha256',
  'candidateRevision = previousRevision + 1',
  'persistCurrentState',
  'insertOutboxIntent',
  'subscription_id',
  "'hvac.telemetry.device-snapshot.v1'",
  'telemetry runtime database identity must be s2_telemetry_service',
], 'PostgreSQL Snapshot transaction');
includesAll(authorizationGo, [
  'telemetryauth.VerifyGrant',
  'ScopeDigest',
  '/internal/v1/telemetry/grants:consume',
  'ErrGrantRejected',
  'ErrAuthorizationUnavailable',
  'ensureJSONEOF(decoder)',
], 'IAM grant adapter');
includesAll(serverGo, [
  'InternalDeviceSnapshotPrefix',
  'InternalBatchSnapshotPath',
  'verifiedPeerSPIFFE',
  'hasForgedIdentityHeader',
  'TELEMETRY_FORGED_IDENTITY_HEADER',
  'TELEMETRY_WORKLOAD_IDENTITY_INVALID',
  'TELEMETRY_AUTHORIZATION_UNAVAILABLE',
  'problemTraceID',
  'traceIDPattern',
  'RESOURCE_NOT_FOUND',
  'BatchObservationFailure',
], 'internal Snapshot HTTP seam');
assert(!serverGo.includes('/api/v1/'), 'Telemetry Runtime handler must not activate public S2 routes');
const batchHandler = serverGo.slice(serverGo.indexOf('func (h *handler) handleBatch'));
assert(batchHandler.indexOf('if !h.authorize') < batchHandler.indexOf('commit, err := h.store.EvaluateAndRead'), 'batch authorization must be all-or-nothing before item reads');
includesAll(mainGo, [
  'TELEMETRY_DATABASE_URL',
  'TELEMETRY_IAM_ENDPOINT',
  'TELEMETRY_ALLOWED_GATEWAY_SPIFFE',
  'tls.RequireAndVerifyClientCert',
  'tls.VersionTLS13',
  'OpenPostgresStore',
  'NewHTTPGrantAuthorizer',
  'DisableCompression: true',
], 'Telemetry Runtime service entrypoint');

includesAll(evaluatorTests, [
  'TestEvaluateCanonicalPresenceFreshnessAndQuality',
  'offline requires continuous coverage',
  'coverage interruption is unavailable not offline',
  'ONLY_REJECTED_CANDIDATES',
  'TestEvaluateCanonicalDigestIgnoresRefreshTimeButIncludesPolicyChange',
  'TestProjectSnapshotPreservesExactRequestedKeyOrderAndPresenceOnly',
  'TestEvaluatePresencePreservesLastKnownAndRequiresNamedDisconnect',
], 'evaluator conformance tests');
includesAll(authorizationTests, [
  'TestHTTPGrantAuthorizerVerifiesExactScopeBeforeSingleUseConsumption',
  'wrong presenter',
  'altered scope',
  'TestHTTPGrantAuthorizerFailsClosedWhenIAMOrAcceptanceIsInvalid',
  'acceptance has trailing JSON',
], 'grant conformance tests');
includesAll(serverTests, [
  'TestInternalSingleSnapshotRequiresGatewayIdentityAndPreservesSelection',
  'TestInternalSnapshotRejectsForgedIdentityAndWrongWorkload',
  'TestInternalBatchPreservesOrderAndReturnsTypedNotFound',
  'TestInternalSnapshotFailsClosedForGrantAndStoreDependencies',
  'TestProblemDetailsAlwaysUseContractTraceID',
], 'internal API tests');
includesAll(postgresTests, [
  'TestPostgresSnapshotTransactionRevisionAndRollback',
  'duplicate outbox event unexpectedly committed',
  'TestPostgresSnapshotTwoOrganizationIsolationAndRuntimeRole',
  'runtime login read without activation',
], 'PostgreSQL integration tests');

includesAll(runtimeSQL, [
  'ADD COLUMN IF NOT EXISTS presence_applicability',
  'ADD COLUMN IF NOT EXISTS accepted_signal_types',
  'presence_policies_accepted_signal_types_check',
  'ADD COLUMN IF NOT EXISTS state_sha256',
  'CREATE TABLE IF NOT EXISTS telemetry_runtime.presence_signals',
  'CREATE TABLE IF NOT EXISTS telemetry_runtime.observation_coverage',
  'ALTER TABLE telemetry_runtime.presence_signals FORCE ROW LEVEL SECURITY',
  'ALTER TABLE telemetry_runtime.observation_coverage FORCE ROW LEVEL SECURITY',
  'CREATE POLICY presence_signals_runtime_all',
  'CREATE POLICY observation_coverage_runtime_all',
], 'runtime Snapshot migration');
for (const forbidden of ['DROP TABLE', 'DROP COLUMN', 'ALTER COLUMN TYPE', 'TRUNCATE']) {
  assert(!runtimeSQL.toUpperCase().includes(forbidden), `runtime migration is not expand-only: ${forbidden}`);
}
includesAll(fixtureSQL, [
  'telemetry_runtime.presence_signals',
  'telemetry_runtime.observation_coverage',
  "'OBSERVATION_COVERAGE_GAP'",
  'state_sha256',
], 'deterministic Snapshot fixtures');

assert(dataRegistry.registryRevision === 8 && ownershipLock.dataRegistryRevision === 8, 'data ownership revision must be 8');
const resources = new Map((dataRegistry.resources ?? []).map((resource) => [`${resource.kind}:${resource.name}`, resource]));
for (const name of ['presence-signal', 'observation-coverage', 'device-presence', 'device-observation-snapshot']) {
  assert(resources.get(`projection:${name}`)?.writer === 'telemetry-runtime-service', `${name} writer drifted`);
}
assert(resources.get('outbox:telemetry-publication-outbox')?.writer === 'telemetry-runtime-service', 'Snapshot outbox writer drifted');
const publicRoutes = (routeRegistry.routes ?? []).filter((route) => route.owner === 'telemetry-runtime-service');
assert(publicRoutes.length === 4, 'public S2 route count drifted');
for (const route of publicRoutes) {
  assert(route.publicIngress === 'platform-gateway', `${route.method} ${route.path} bypasses Gateway`);
  assert(route.activationStatus === 'expand-baseline' && route.rollout?.mode === 'disabled' && route.migrationPhase === 'R0-contract-only', `${route.method} ${route.path} activated production traffic`);
  assert(route.readOnlyFallback === false && route.readFallbackOwner === undefined, `${route.method} ${route.path} gained request fallback`);
}

const expectedScripts = {
  'build:telemetry-runtime': 'node scripts/run-go.mjs build -o out/telemetry-runtime-service ./services/telemetry-runtime-service/cmd/telemetry-runtime-service',
  's2:runtime:check': 'node scripts/check-s2-telemetry-runtime-snapshot.mjs',
  's2:runtime:postgres': 'node scripts/run-s2-telemetry-runtime-postgres-tests.mjs',
};
for (const [name, command] of Object.entries(expectedScripts)) {
  assert(packageJSON.scripts?.[name] === command, `${name} is not wired`);
}
assert(packageJSON.scripts?.['test:identity']?.includes('./services/telemetry-runtime-service/...'), 'global identity tests omit Telemetry Runtime');
assert(packageJSON.scripts?.['test:security-negative']?.includes('./services/telemetry-runtime-service/...'), 'global security-negative tests omit Telemetry Runtime');
const ticketCommand = packageJSON.scripts?.['s2:ticket-03'];
assert(typeof ticketCommand === 'string', 's2:ticket-03 is missing');
for (const command of [
  'npm run s2:runtime:check',
  'test ./services/telemetry-runtime-service/...',
  'vet ./services/telemetry-runtime-service/...',
  'npm run build:telemetry-runtime',
  'npm run ownership:check',
  'npm run s2:baseline:check',
  'npm run s2:iam:check',
  'npm run s2:contracts:check',
  'npm run contracts:check',
  'npm run release:evidence-assets',
  'npm run lint',
  'npm run build',
  'npm run s2:runtime:postgres',
]) {
  assert(ticketCommand.includes(command), `s2:ticket-03 omits ${command}`);
}
includesAll(workflow, ['runs-on: ubuntu-24.04', 'go-version: "1.25.12"', 'node-version: "20.19.4"', 'npm run s2:ticket-03', 's2-telemetry-runtime-snapshot-evidence'], 'Ticket 03 workflow');

const runtimeSources = `${evaluatorGo}\n${storeGo}\n${authorizationGo}\n${serverGo}\n${mainGo}`.toLowerCase();
for (const forbidden of ['thingsboard', 'centrifugo', 'legacy-hvac-backend', 'mock fallback', 'redis']) {
  assert(!runtimeSources.includes(forbidden), `Ticket 03 crossed an out-of-scope boundary: ${forbidden}`);
}

console.log('S2 Ticket 03 Telemetry Runtime Snapshot passed: authoritative semantics, atomic revision/outbox, exact internal reads and zero public traffic.');
