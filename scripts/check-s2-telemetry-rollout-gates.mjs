import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());

function assert(condition, message) {
  if (!condition) throw new Error(`Invalid S2 rollout gate: ${message}`);
}

async function readText(path) {
  return readFile(resolve(root, path), 'utf8');
}

async function readJSON(path) {
  return JSON.parse(await readText(path));
}

function exact(actual, expected) {
  return Array.isArray(actual)
    && actual.length === expected.length
    && actual.every((value, index) => value === expected[index]);
}

function includesAll(actual, expected) {
  return Array.isArray(actual) && expected.every((value) => actual.includes(value));
}

const [gate, ownership, publicContract, operations, research, context, packageJSON] = await Promise.all([
  readJSON('deploy/s2/release-gates.v1.json'),
  readJSON('contracts/ownership/s2-telemetry-ownership.v1.json'),
  readJSON('contracts/http/s2-telemetry-public.openapi.json'),
  readText('docs/operations/s2-telemetry-rollout-gates.md'),
  readText('docs/research/s2-observability-capacity-rollout-sources.md'),
  readText('CONTEXT.md'),
  readJSON('package.json'),
]);

assert(gate.schemaVersion === 1, 'schemaVersion must be 1');
assert(gate.activationStatus === 'expand-baseline', 'release gates must be activated only as an expand baseline');
assert(gate.issue === 52, 'issue trace must remain #52');
assert(gate.name === 's2-telemetry-release-gates-v1', 'gate name drifted');

const authority = gate.authority ?? {};
assert(authority.businessOwner === 'telemetry-runtime-service', 'Telemetry Runtime must remain the business owner');
assert(authority.businessOwner === ownership.ownerService, 'rollout and ownership owner differ');
assert(authority.publicSeam === 'platform-gateway', 'Gateway must remain the public seam');
assert(authority.authorizationOwner === 'iam-service', 'IAM must remain the authorization owner');
assert(authority.registryOwner === 'platform-core-service', 'Core must remain the Registry owner');
assert(authority.transport === 'centrifugo-v6.8.1-oss', 'locked transport version drifted');
assert(authority.transportBroker === 'dedicated-redis', 'transport broker must remain dedicated Redis');
assert(authority.businessStore === 'postgresql:telemetry_runtime', 'PostgreSQL business authority drifted');
assert(authority.legacyFallback === false && authority.mockFallback === false, 'Legacy and Mock fallback must remain forbidden');
assert(publicContract['x-activation-status'] === 'expand-baseline', 'public S2 contract must remain an expand baseline');
assert(ownership.activationStatus === 'expand-baseline', 'ownership contract must remain an expand baseline');

const envelope = gate.releaseEnvelope ?? {};
const expectedEnvelope = {
  sustainedActiveConnections: 5000,
  reconnectBurstConnections: 10000,
  reconnectBurstPerSecond: 1000,
  activeSubscriptions: 50000,
  activeHistoryChannels: 10000,
  averageSubscriptionsPerConnection: 10,
  p95SubscriptionsPerConnection: 25,
  hardSubscriptionsPerConnection: 100,
  peakDeviceBusinessRevisionsPerSecond: 2000,
  averageSubscribersPerPublication: 3,
  peakPublicationDeliveriesPerSecond: 6000,
  averageEncodedPublicationBytes: 900,
  p99EncodedPublicationBytes: 4096,
  maximumEncodedPublicationBytes: 65536,
  steadyStateDurationMinutes: 60,
  shortPeakMultiplier: 2,
  shortPeakDurationMinutes: 15,
  nPlusOneLoadFraction: 0.7,
  minimumCapacityHeadroomFraction: 0.3,
};
assert(envelope.name === 'initial-production-v1', 'Release Envelope name drifted');
for (const [field, value] of Object.entries(expectedEnvelope)) {
  assert(envelope[field] === value, `Release Envelope ${field} drifted`);
}
assert(
  envelope.peakPublicationDeliveriesPerSecond
    === envelope.peakDeviceBusinessRevisionsPerSecond * envelope.averageSubscribersPerPublication,
  'publication-delivery derivation is inconsistent',
);
assert(
  envelope.activeSubscriptions
    === envelope.sustainedActiveConnections * envelope.averageSubscriptionsPerConnection,
  'subscription derivation is inconsistent',
);
assert(envelope.p99EncodedPublicationBytes <= envelope.maximumEncodedPublicationBytes, 'p99 publication exceeds hard payload limit');

const transport = gate.transportBounds ?? {};
assert(transport.clientQueueMaxBytes === 262144, 'client queue bound drifted');
assert(transport.historySizePublications === 256, 'history size drifted');
assert(transport.historyTtlSeconds === 180, 'history TTL drifted');
assert(transport.historyMetaTtlSeconds === 86400, 'history metadata TTL drifted');
assert(transport.recoveryMaxPublicationLimit === transport.historySizePublications, 'recovery limit must match retained publication count');
assert(transport.maximumRecoveryCursorLifetimeSeconds === 120, 'Recovery Cursor lifetime drifted');
assert(transport.checkpointIntervalSeconds === 30, 'checkpoint interval drifted');
assert(transport.connectionTokenLifetimeSeconds === 300, 'connection token lifetime drifted');
assert(transport.subscriptionTokenLifetimeSeconds === 300, 'subscription token lifetime drifted');
assert(transport.guaranteedRecoveryWindowSeconds === 120, 'guaranteed recovery window drifted');
assert(transport.maximumRecoveryCursorLifetimeSeconds <= transport.guaranteedRecoveryWindowSeconds, 'Cursor lifetime exceeds guaranteed recovery window');
assert(transport.guaranteedRecoveryWindowSeconds <= transport.historyTtlSeconds, 'guaranteed recovery window exceeds history TTL');
assert(
  transport.historySizePublications / transport.maximumSustainedDeviceRevisionsPerSecondForGuaranteedWindow
    >= transport.guaranteedRecoveryWindowSeconds,
  'history size cannot cover the declared recovery window',
);
assert(transport.historyIsAuthoritative === false, 'transport history cannot become authoritative');
assert(transport.snapshotFallbackRequired === true, 'Snapshot fallback must remain mandatory');

const slo = gate.slo ?? {};
assert(slo.windowDays === 30, 'SLO window drifted');
assert(slo.snapshotHttp?.availabilityPercent === 99.9, 'Snapshot availability objective drifted');
assert(slo.snapshotHttp?.singleP95Milliseconds === 250, 'single Snapshot p95 drifted');
assert(slo.snapshotHttp?.singleP99Milliseconds === 750, 'single Snapshot p99 drifted');
assert(slo.snapshotHttp?.batchP95Milliseconds === 1000, 'batch p95 drifted');
assert(slo.snapshotHttp?.batchP99Milliseconds === 2000, 'batch p99 drifted');
assert(slo.snapshotHttp?.maximumCurrentSnapshotAgeSeconds === 30, 'current Snapshot age bound drifted');
assert(slo.ingest?.acceptanceP95Milliseconds === 2000 && slo.ingest?.acceptanceP99Milliseconds === 5000, 'ingest acceptance objectives drifted');
assert(slo.ingest?.presenceSignalToEvaluationP95Milliseconds === 3000, 'Presence p95 drifted');
assert(slo.ingest?.presenceSignalToEvaluationP99Milliseconds === 10000, 'Presence p99 drifted');
assert(slo.ingest?.offlineDeadlineLatenessP95Seconds === 15, 'offline deadline p95 drifted');
assert(slo.ingest?.offlineDeadlineLatenessP99Seconds === 60, 'offline deadline p99 drifted');
assert(slo.publication?.withinFiveSecondsPercent === 99.95, 'publication success objective drifted');
assert(slo.publication?.undetectedRevisionGaps === 0, 'undetected revision gaps must be zero');
assert(slo.recovery?.eligibleTransportRecoverySuccessPercent === 99, 'eligible recovery objective drifted');
assert(slo.recovery?.recoveryOrSnapshotSuccessPercent === 99.9, 'recovery-or-Snapshot objective drifted');
assert(slo.authorization?.revocationPropagationMaximumMilliseconds === 10000, 'revocation maximum drifted');
assert(slo.authorization?.revocationPropagationP99Milliseconds <= slo.authorization?.revocationPropagationMaximumMilliseconds, 'revocation p99 exceeds maximum');
assert(slo.slowConsumer?.warningDisconnectFractionPerFiveMinutes === 0.001, 'slow-consumer warning threshold drifted');
assert(slo.slowConsumer?.rollbackDisconnectFractionPerFiveMinutes === 0.005, 'slow-consumer rollback threshold drifted');

const zeroInvariants = [
  'cross_organization_successes',
  'cross_site_successes',
  'hidden_device_disclosures',
  'forged_scope_successes',
  'unauthorized_key_disclosures',
  'post_revocation_publications',
  'cursor_scope_expansions',
  'cursor_replay_authorization_bypasses',
  'raw_token_log_findings',
  'raw_cursor_log_findings',
  'raw_channel_log_findings',
  'telemetry_value_log_findings',
  'legacy_or_mock_fallbacks',
  'non_owner_s2_business_writes',
  'undetected_revision_gaps',
];
assert(exact(gate.securityZeroInvariants, zeroInvariants), 'security zero-invariant set drifted');

const negativeIds = [
  'cross-organization-single-read',
  'cross-site-batch-read',
  'forged-browser-identity-headers',
  'hidden-device-subscription',
  'forged-channel-subscription',
  'revocation-stops-delivery',
  'cursor-cross-principal-replay',
  'cursor-cross-device-or-key-replay',
  'key-enumeration-visible-device',
  'key-enumeration-hidden-device',
];
assert(exact(gate.negativeTests?.map((test) => test.id), negativeIds), 'negative-test matrix drifted');
const byId = new Map(gate.negativeTests.map((test) => [test.id, test]));
assert(byId.get('cross-organization-single-read')?.expectedCode === 'RESOURCE_NOT_FOUND', 'cross-Organization invisibility drifted');
assert(byId.get('cross-site-batch-read')?.expectedItemCode === 'RESOURCE_NOT_FOUND', 'cross-Site batch invisibility drifted');
assert(byId.get('hidden-device-subscription')?.expectedCode === 'RESOURCE_NOT_FOUND', 'hidden bootstrap result drifted');
assert(byId.get('revocation-stops-delivery')?.expectedMaximumMilliseconds === 10000, 'revocation test maximum drifted');
assert(byId.get('cursor-cross-principal-replay')?.expectedCode === 'RECOVERY_CURSOR_INVALID', 'Cursor replay result drifted');
assert(byId.get('key-enumeration-visible-device')?.expectedCode === 'TELEMETRY_KEY_INVALID', 'visible Device key nondisclosure drifted');
assert(byId.get('key-enumeration-hidden-device')?.expectedCode === 'RESOURCE_NOT_FOUND', 'hidden Device must be resolved before key validation');

const observability = gate.observability ?? {};
const requiredMetricFamilies = [
  's2_telemetry_ingest_acceptance_duration_seconds',
  's2_telemetry_source_lag_seconds',
  's2_telemetry_presence_evaluation_duration_seconds',
  's2_telemetry_offline_deadline_lateness_seconds',
  's2_telemetry_snapshot_request_duration_seconds',
  's2_telemetry_snapshot_age_seconds',
  's2_telemetry_publication_lag_seconds',
  's2_telemetry_publication_revision_gap_total',
  's2_telemetry_recovery_attempts_total',
  's2_telemetry_snapshot_fallback_total',
  's2_telemetry_subscribe_decision_duration_seconds',
  's2_telemetry_revocation_propagation_seconds',
  's2_telemetry_outbox_oldest_unpublished_timestamp_seconds',
  's2_telemetry_quarantine_candidates_total',
  's2_telemetry_upstream_errors_total',
  's2_telemetry_active_subscriptions',
  's2_telemetry_publication_bytes_total',
];
assert(exact(observability.requiredMetricFamilies, requiredMetricFamilies), 'required metric family set drifted');
for (const metric of observability.requiredMetricFamilies) {
  assert(/^s2_telemetry_[a-z0-9_]+$/.test(metric), `metric name is invalid: ${metric}`);
  if (metric.includes('duration') || metric.includes('lag') || metric.includes('age') || metric.includes('lateness') || metric.includes('propagation')) {
    assert(metric.endsWith('_seconds'), `time metric lacks seconds suffix: ${metric}`);
  }
  if (metric.includes('bytes')) assert(metric.endsWith('_bytes_total'), `byte counter lacks bytes_total suffix: ${metric}`);
}
const allowedLabels = new Set(observability.allowedMetricLabels ?? []);
for (const forbidden of observability.forbiddenMetricLabels ?? []) {
  assert(!allowedLabels.has(forbidden), `metric label is both allowed and forbidden: ${forbidden}`);
}
for (const required of ['organization_id', 'site_id', 'device_id', 'subscription_id', 'recovery_cursor', 'business_revision', 'telemetry_key', 'telemetry_value', 'token']) {
  assert(observability.forbiddenMetricLabels.includes(required), `high-cardinality or sensitive metric label not forbidden: ${required}`);
}
for (const required of ['connection_token', 'subscription_token', 'raw_recovery_cursor', 'raw_channel', 'raw_telemetry_value', 'authorization_header', 'cookie', 'csrf_token', 'source_credential']) {
  assert(observability.forbiddenLogsAndTraces.includes(required), `sensitive log/trace field not forbidden: ${required}`);
}
assert(exact(observability.baggageAllowed, ['none']), 'S2 sensitive context must not use Baggage');
assert(observability.identifierReferenceMethod === 'hmac-sha256-environment-key-truncated-16-bytes', 'identifier reference method drifted');

const scenarioIds = [
  'steady-state-envelope',
  'two-x-short-peak',
  'reconnect-storm',
  'slow-consumer-one-percent',
  'history-overflow',
  'centrifugo-node-loss',
  'redis-failover',
  'postgres-failover',
  'authorization-revocation-storm',
  'upstream-observation-outage',
];
assert(exact(gate.capacityScenarios?.map((scenario) => scenario.id), scenarioIds), 'capacity/failure scenario set drifted');
const scenarioMap = new Map(gate.capacityScenarios.map((scenario) => [scenario.id, scenario]));
assert(scenarioMap.get('steady-state-envelope')?.connections === envelope.sustainedActiveConnections, 'steady-state connection target differs from envelope');
assert(scenarioMap.get('steady-state-envelope')?.subscriptions === envelope.activeSubscriptions, 'steady-state subscription target differs from envelope');
assert(scenarioMap.get('two-x-short-peak')?.multiplier === envelope.shortPeakMultiplier, 'short-peak multiplier differs from envelope');
assert(scenarioMap.get('reconnect-storm')?.connections === envelope.reconnectBurstConnections, 'reconnect population differs from envelope');
assert(scenarioMap.get('reconnect-storm')?.connectionsPerSecond === envelope.reconnectBurstPerSecond, 'reconnect rate differs from envelope');
assert(scenarioMap.get('slow-consumer-one-percent')?.slowConsumerFraction === 0.01, 'slow-consumer scenario drifted');
assert(scenarioMap.get('centrifugo-node-loss')?.loadFraction === envelope.nPlusOneLoadFraction, 'N+1 scenario load differs from envelope');
assert(scenarioMap.get('authorization-revocation-storm')?.revocationsPerSecond === 100, 'revocation storm rate drifted');

const resources = gate.resourcePassCriteria ?? {};
assert(resources.steadyStateCpuMaximumFraction === 0.6, 'CPU headroom gate drifted');
assert(resources.steadyStateMemoryMaximumFraction === 0.7, 'memory headroom gate drifted');
assert(resources.redisMemoryMaximumFraction === 0.65, 'Redis memory gate drifted');
assert(resources.networkMaximumFraction === 0.6, 'network headroom gate drifted');
assert(resources.postgresConnectionPoolMaximumFraction === 0.7, 'PostgreSQL pool gate drifted');
assert(resources.noUnboundedQueueGrowth && resources.noProcessRestart && resources.noOomKill, 'resource safety gates must remain true');

const phaseIds = [
  'R0-contract-only',
  'R1-dark-ingest',
  'R2-shadow-compare',
  'R3-internal-canary',
  'R4-external-canary-5',
  'R5-ramp-25',
  'R6-ramp-50',
  'R7-primary-100',
  'R8-legacy-current-state-retired',
];
const traffic = [0, 0, 0, 1, 5, 25, 50, 100, 100];
assert(exact(gate.rolloutPhases?.map((phase) => phase.id), phaseIds), 'rollout phase set drifted');
assert(exact(gate.rolloutPhases?.map((phase) => phase.trafficPercent), traffic), 'rollout traffic progression drifted');
const allowedPhaseFallbacks = new Set([
  'route-level-legacy-remains-primary',
  'none-within-request',
  'route-revision-rollback-only',
  'none',
]);
for (const phase of gate.rolloutPhases.slice(1)) {
  assert(phase.writer.startsWith('telemetry-runtime-service'), `${phase.id} introduces a non-owner S2 writer`);
  assert(allowedPhaseFallbacks.has(phase.fallback), `${phase.id} has an unsupported fallback mode`);
}
assert(gate.rolloutPhases[2].shadow === true, 'shadow comparison phase must enable shadow evidence');
assert(gate.rolloutPhases[2].fallback === 'none-within-request', 'shadow compare must not use fallback');
assert(gate.rolloutPhases[3].minimumSnapshotRequests === 10000, 'internal canary sample floor drifted');
assert(gate.rolloutPhases[4].minimumRecoveryAttempts === 100, 'external canary recovery sample floor drifted');
assert(gate.rolloutPhases[8].minimumHoldMinutes === 10080, 'Legacy retirement observation window drifted');

const shadow = gate.shadowComparison ?? {};
assert(shadow.sideEffectFree === true, 'shadow comparison must be side-effect-free');
assert(shadow.writesAllowed === false && shadow.publishesAllowed === false && shadow.authorizationEffectsAllowed === false, 'shadow comparison gained side effects');
assert(shadow.mappingMismatchMaximum === 0 && shadow.missingOrExtraDeviceMaximum === 0, 'identity/mapping shadow differences must remain zero');
assert(shadow.overlappingAcceptedValueAgreementPercent === 99.9, 'value parity threshold drifted');
assert(shadow.sampleTimestampWithinOneExpectedIntervalPercent === 99.5, 'sample timestamp parity threshold drifted');
assert(shadow.semanticDifferencesMustBeClassified === true, 'semantic differences must remain classified');

const promotion = gate.promotionGates ?? {};
assert(promotion.allSecurityZeroInvariants === true, 'promotion must require all security invariants');
assert(promotion.allRequiredEvidencePresent === true, 'promotion must require all evidence');
assert(promotion.sloBurnRateMaximum === 1, 'promotion burn-rate gate drifted');
assert(promotion.unclassifiedShadowDifferences === 0, 'unclassified shadow differences must be zero');
assert(promotion.contractDrift === 0 && promotion.ownershipDrift === 0, 'contract/ownership drift must be zero');
assert(promotion.capacityHeadroomMinimumFraction === envelope.minimumCapacityHeadroomFraction, 'promotion headroom differs from envelope');
assert(promotion.manualApprovalRequired === true, 'production promotion must remain manual');

const rollback = gate.rollbackTriggers ?? {};
assert(includesAll(rollback.immediate, [
  'any-security-zero-invariant-nonzero',
  'business-revision-corruption',
  'non-owner-s2-write',
  'wrong-device-or-key-publication',
  'token-cursor-channel-or-value-leak',
  'snapshot-authority-bypass',
]), 'immediate rollback trigger set is incomplete');
assert(includesAll(rollback.sustained, [
  'snapshot-5xx-error-rate-over-one-percent-for-five-minutes',
  'single-snapshot-p99-over-1500ms-for-ten-minutes',
  'publication-p99-over-5000ms-for-ten-minutes',
  'recovery-or-snapshot-failure-over-half-percent-for-five-minutes',
  'slow-consumer-disconnect-fraction-over-half-percent-for-five-minutes',
]), 'sustained rollback trigger set is incomplete');
assert(rollback.maximumRollbackDecisionMinutes === 5, 'rollback decision objective drifted');
assert(rollback.maximumRouteRollbackMinutes === 15, 'route rollback objective drifted');
assert(rollback.forceFreshSnapshotAfterRollback === true, 'rollback must require a fresh Snapshot');
assert(rollback.disconnectOrExpireLiveSessions === true, 'rollback must invalidate live sessions');
assert(rollback.databaseMigrationRollback === 'expand-contract-no-down-migration-during-compatibility-window', 'database rollback policy drifted');

const existingCommands = [
  'npm run s2:rollout-gates:check',
  'npm run s2:ownership:check',
  'npm run s2:public-contract:check',
  'npm run s2:centrifugo:check',
  'npm run contracts:check',
  'npm run lint',
  'npm run build',
];
assert(exact(gate.requiredExistingCommands, existingCommands), 'existing command set drifted');
for (const command of existingCommands) {
  const scriptName = command.replace(/^npm run /, '');
  assert(typeof packageJSON.scripts?.[scriptName] === 'string', `required existing command is not wired: ${command}`);
}
const futureCommands = [
  'npm run build:telemetry-runtime',
  'npm run build:telemetry-runtime-migrator',
  'npm run s2:security-negative',
  'npm run s2:postgres-integration',
  'npm run s2:transport-integration',
  'npm run s2:capacity',
  'npm run audit:s2-browser',
  'npm run audit:s2-kind-rollout',
  'npm run s2:release-evidence',
];
assert(exact(gate.requiredFutureCommands, futureCommands), 'future implementation command set drifted');
assert(gate.requiredCI?.workflow === '.github/workflows/s2-telemetry-release.yml', 'formal CI workflow path drifted');
assert(gate.requiredCI?.cleanRunnerRequired === true, 'formal CI must use clean runners');
assert(gate.requiredCI?.artifactName === 's2-telemetry-release-evidence', 'formal CI artifact name drifted');
assert(exact(gate.requiredCI?.jobs, [
  'contracts-and-static',
  'security-negative',
  'postgres-integration',
  'transport-integration',
  'capacity-and-failure',
  'browser-real-mode',
  'production-images',
  'kind-rollout-rollback',
  'release-evidence',
]), 'formal CI job set drifted');
assert(gate.requiredEvidence?.length === 20, 'required evidence set size drifted');
for (const path of gate.requiredEvidence) {
  assert(path.startsWith('out/s2-release-evidence/'), `evidence path is outside the S2 bundle: ${path}`);
}
for (const path of [
  'out/s2-release-evidence/workflow-jobs.json',
  'out/s2-release-evidence/alert-rule-validation-report.json',
  'out/s2-release-evidence/production-image-report.json',
  'out/s2-release-evidence/sbom-provenance-report.json',
]) {
  assert(gate.requiredEvidence.includes(path), `required clean-CI or production-build evidence missing: ${path}`);
}
assert(gate.releaseChecklist?.length === 18, 'release checklist size drifted');

for (const phrase of [
  'Initial Production Release Envelope v1',
  'Zero-tolerance security invariants',
  'Negative-security acceptance matrix',
  'Metrics and alerting contract',
  'Trace, log and audit correlation',
  'Capacity and failure suite',
  'Shadow comparison',
  'Rollout phases and single-writer rule',
  'Promotion gates',
  'Rollback triggers',
  'Required local, build and implementation commands',
  'Required evidence bundle',
  'Release checklist',
]) {
  assert(operations.includes(phrase), `operations specification is missing: ${phrase}`);
}
for (const url of [
  'https://centrifugal.dev/docs/server/observability',
  'https://centrifugal.dev/docs/server/configuration',
  'https://centrifugal.dev/docs/server/history_and_recovery',
  'https://centrifugal.dev/docs/server/engines',
  'https://prometheus.io/docs/practices/instrumentation/',
  'https://opentelemetry.io/docs/concepts/signals/baggage/',
]) {
  assert(research.includes(url), `primary-source review is missing ${url}`);
}
for (const term of ['## Release Envelope', '## Shadow Comparison', '## Rollout Cohort']) {
  assert(context.includes(term), `CONTEXT.md is missing domain term: ${term}`);
}
assert(packageJSON.scripts?.['s2:rollout-gates:check'] === 'node scripts/check-s2-telemetry-rollout-gates.mjs', 's2:rollout-gates:check is not wired');

console.log('S2 rollout gates passed: zero-tolerance security, measurable SLOs, bounded capacity, single-writer phases and deterministic rollback.');
