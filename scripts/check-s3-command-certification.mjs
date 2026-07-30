import { readFile, readdir } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));
const [
  plan,
  envelope,
  schema,
  routes,
  providerContract,
  packageJSON,
  runner,
  validatorTests,
  verifier,
  workflow,
  authorityWorkflow,
  docs,
  certificationTests,
  postgresSubmissionTests,
  postgresDispatchTests,
  dispatcherTests,
  connectorTests,
] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('deploy/s3/certification-envelope.v1.json'),
  readJSON('deploy/s3/full-certification-attestation.schema.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/thingsboard/s3-set-temperature-setpoint.local.v1.json'),
  readJSON('package.json'),
  read('scripts/run-s3-command-certification.mjs'),
  read('scripts/test-s3-command-certification.mjs'),
  read('scripts/verify-s3-command-certification.mjs'),
  read('.github/workflows/s3-command-certification.yml'),
  read('.github/workflows/s3-command-authority.yml'),
  read('docs/operations/s3-implementation-plan.md'),
  read('services/command-service/pkg/commandservice/certification_test.go'),
  read('services/command-service/pkg/commandservice/postgres_integration_test.go'),
  read('services/command-service/pkg/commandservice/postgres_dispatch_integration_test.go'),
  read('services/command-dispatcher/pkg/commanddispatcher/dispatcher_test.go'),
  read('services/thingsboard-connector-control/pkg/controlconnector/thingsboard_test.go'),
]);

const failures = [];
const assert = (condition, message) => { if (!condition) failures.push(message); };

assert(plan.productionTrafficPercent === 0, 'S3-09 repository plan enabled production traffic');
assert(JSON.stringify(plan.currentFrontier) === JSON.stringify(['S3-09']), 'S3-09 is not the active frontier');
assert(!plan.completedTickets?.includes('S3-09'), 'configuration work incorrectly marked S3-09 complete');
assert(plan.certificationEnvelope === 'deploy/s3/certification-envelope.v1.json', 'S3 plan does not bind the certification envelope');
const ticket = plan.tickets?.find((candidate) => candidate.id === 'S3-09');
assert(ticket?.status === 'formal-certification-pending', 'S3-09 is not formal-certification-pending');
assert(ticket?.formalCompletionRequires?.length >= 6, 'S3-09 formal completion conditions are incomplete');
assert(plan.firstTracerBullet?.publicRoutesEnabled === false && plan.firstTracerBullet?.productionProviderEnabled === false, 'S3-09 enabled a public route or production provider');

assert(envelope.schemaVersion === 1 && envelope.ticket === 'S3-09', 'S3-09 certification envelope metadata is invalid');
assert(envelope.status === 'formal-certification-pending' && envelope.formalCertificationRequired === true, 'certification envelope permits non-formal completion');
assert(envelope.productionTrafficPercent === 0, 'certification envelope enabled production traffic');
assert(envelope.capacity?.steadyState?.commandsPerSecond === 100 && envelope.capacity?.steadyState?.minimumDurationSeconds === 3600, 'steady capacity is not 100 commands/s for 60 minutes');
assert(envelope.capacity?.burst?.commandsPerSecond === 1000 && envelope.capacity?.burst?.durationSeconds === 60, 'burst capacity is not 1000 commands/s for one minute');
assert(envelope.capacity?.minimumHeadroomFraction === 0.3, 'capacity headroom is not 30%');
assert(envelope.capacity?.latencySlo?.acceptedToSubmittedP95MillisecondsMaximum === 300, 'Command acceptance p95 drifted');
assert(envelope.capacity?.latencySlo?.queuedReadyToSendStartedP99MillisecondsMaximum === 3000, 'ready-to-send p99 drifted');
assert(envelope.recoveryObjectives?.singlePodSecondsMaximum === 30, 'single-Pod recovery objective drifted');
assert(envelope.recoveryObjectives?.consumerRebalanceSecondsMaximum === 60, 'Consumer Rebalance recovery objective drifted');
assert(envelope.recoveryObjectives?.connectorInstanceSecondsMaximum === 120, 'Connector recovery objective drifted');
assert(envelope.recoveryObjectives?.singleAvailabilityZoneSecondsMaximum === 300, 'single-AZ recovery objective drifted');
const expectedCrashPoints = [
  'command-postgres-transaction-failure',
  'concurrent-idempotency-race',
  'dispatcher-crash-after-claim-before-connector-result',
  'consumer-rebalance',
  'pre-send-rejection-safe-retry',
  'request-committed-timeout-no-reissue',
  'connector-crash-after-request-commit',
  'late-old-fence-result',
  'reported-state-mismatch',
  'reported-state-verification-deadline',
  'audit-intent-write-failure',
  'single-availability-zone-loss',
];
assert(JSON.stringify(envelope.requiredCrashPoints) === JSON.stringify(expectedCrashPoints), 'crash-point certification matrix drifted');
for (const counter of [
  'lostAcceptedCommands', 'duplicateDeviceSideEffects', 'oldFenceExecutions',
  'blindRetriesAfterRequestCommitted', 'crossTenantDispatches', 'unapprovedHighRiskDispatches',
  'unverifiedCapabilityDispatches', 'unauditedControlActions',
  'syntheticEvidenceUsedAsProductionAttestation', 'secretLeaks',
]) assert(envelope.zeroCounters?.includes(counter), `zero-tolerance counter is missing: ${counter}`);
const canary = envelope.internalCanary ?? {};
assert(canary.phase === 'S3-R2-internal-low-risk' && canary.organizationCount === 1 && canary.siteCount === 1 && canary.deviceCount === 1, 'internal canary scope is not exactly one Organization/Site/Device');
assert(canary.risk === 'LOW' && canary.maximumSetpointDeltaC === 1, 'internal canary is not bounded to LOW risk');
assert(canary.mappingStatusRequired === 'VERIFIED' && canary.s2CurrentStateProductionCertificationRequired === true, 'canary lacks VERIFIED mapping or S2 certification');
assert(canary.minimumCommands === 6 && canary.maximumCommands === 12 && canary.minimumHoldMinutes === 240, 'canary command count or hold window drifted');
assert(canary.distinctApproversRequired === 2 && canary.manualPromotionApprovalRequired === true, 'canary approval boundary is incomplete');
assert(canary.publicRoutesEnabled === false && canary.productionTrafficPercent === 0 && canary.automaticRetryAfterRequestCommitted === false, 'canary enables public traffic or forbidden retry');
assert(canary.rollback?.futureCommandsOnly === true && canary.rollback?.acceptedCommandsRemainWithOriginalOwner === true, 'canary rollback violates accepted-command ownership');
assert(new Set(envelope.requiredEvidence).size === envelope.requiredEvidence.length && envelope.requiredEvidence.includes('SHA256SUMS'), 'formal evidence inventory is incomplete or duplicated');

assert(schema?.additionalProperties === false, 'formal attestation schema is not closed');
for (const field of ['workflowRunId', 'load', 'observed', 'zeroCounters', 'crashPoints', 'canary', 'rollback', 'approval']) {
  assert(schema.required?.includes(field), `formal attestation schema does not require ${field}`);
}
const schemaCanary = schema.properties?.canary;
assert(schemaCanary?.additionalProperties === false, 'formal canary schema is not closed');
for (const field of ['organizationCount', 'siteCount', 'deviceCount', 'maximumObservedSetpointDeltaC', 'credentialReference', 'operatorConfirmedEachCommand', 'automaticReissuesAfterRequestCommitted']) {
  assert(schemaCanary?.required?.includes(field), `formal canary schema does not require ${field}`);
}

const commandRoutes = (routes.routes ?? []).filter((route) => route.owner === 'command-service');
assert(commandRoutes.length === 3, 'Command Route Ownership must contain exactly three public routes');
assert(commandRoutes.every((route) => route.rollout?.mode === 'disabled' && route.shadowSideEffectPolicy === 'SYNTHETIC_ONLY'), 'Command public route became enabled or non-Synthetic');
assert(providerContract.verificationStatus === 'LOCAL_VERIFIED' && providerContract.productionEligible === false, 'repository provider mapping became production eligible without formal promotion');

for (const token of [
  "assert(['preflight', 'formal'].includes(profile)",
  "!plan.completedTickets?.includes('S3-09')",
  "formal S3 certification requires --attestation=<json>",
  'blindRetriesAfterRequestCommitted',
  'maximumObservedSetpointDeltaC',
  'canary cohort is not exactly one Organization, one Site and one Device',
  'canary time window is in the future, reversed, or shorter than four hours',
  'credential evidence must be an opaque secret:// or workload:// reference',
  'approvedInternalLowRiskCanaryPassed: true',
  "publicProductionRoutesRemainDisabled: true",
]) assert(runner.includes(token), `formal certification validator invariant is missing: ${token}`);
for (const token of [
  'preflight report incorrectly claims formal eligibility',
  'formal profile accepted a missing attestation',
  'formal validator accepted a non-zero blind retry counter',
  'formal validator accepted a future-dated canary hold',
  'offline verifier accepted a tampered evidence file',
  'validator-fixture-only',
]) assert(validatorTests.includes(token), `certification validator test is missing: ${token}`);
for (const token of [
  'SHA256SUMS subject inventory does not match the certification envelope',
  'S3 certification digest mismatch',
  'Command Fence report violates a zero invariant',
  'S3 in-toto subject digest mismatch',
]) assert(verifier.includes(token), `offline certification verifier invariant is missing: ${token}`);

for (const test of [
  'TestS309ConcurrentDuplicateSubmissionProducesOneIntent',
  'TestS309InProcessCapacitySmokeMeetsCommandLatencyEnvelope',
]) assert(certificationTests.includes(test), `S3-09 deterministic test is missing: ${test}`);
for (const test of [
  'TestPostgresConcurrentIdempotencyConvergesToOneIntent',
  'TestPostgresSubmissionRollsBackEveryOwnedWrite',
]) assert(postgresSubmissionTests.includes(test), `PostgreSQL submission crash-point test is missing: ${test}`);
for (const test of [
  'TestPostgresConcurrentDispatchClaimHasOneWinner',
  'TestPostgresPreSendRetryAdvancesFenceAndRejectsOldAttempt',
  'TestPostgresExpiredPreparedLeaseFreezesOutcomeUnknown',
  'TestPostgresDeviceControlLaneIsSerial',
  'TestPostgresReportedStateMismatchFreezesOutcomeUnknown',
  'TestPostgresExpiredReportedStateVerificationFreezesOutcomeUnknown',
]) assert(postgresDispatchTests.includes(test), `PostgreSQL dispatch certification test is missing: ${test}`);
for (const test of ['TestCommittedTimeoutBecomesOutcomeUnknown', 'TestPreSendFailureCanBeRetriedByAnotherDispatcher']) {
  assert(dispatcherTests.includes(test), `Dispatcher certification test is missing: ${test}`);
}
for (const test of [
  'TestThingsBoardHTTPTimeoutIsCommittedUnknown',
  'TestThingsBoardTransportFailureBeforeWriteIsSafePreSend',
  'TestThingsBoardEvidenceCompletionFailureAfterWriteFreezesUnknown',
  'TestThingsBoardRequiresVerifiedMappingAndRejectsOldFence',
]) assert(connectorTests.includes(test), `Connector certification test is missing: ${test}`);

const workflowFiles = await readdir(resolve(root, '.github/workflows'));
assert(workflowFiles.includes('s3-command-certification.yml'), 'stable S3 Command Certification workflow is missing');
assert(!workflowFiles.includes('s3-ticket-09.yml'), 'retired S3 Ticket 09 workflow must not return');
for (const script of ['s3:certification:check', 's3:certification:preflight', 's3:certification:verify', 'test:s3-certification', 'test:s3-target-image-evidence', 's3:certification:pr']) {
  assert(packageJSON.scripts?.[script], `package script is missing: ${script}`);
}
assert(!packageJSON.scripts?.['s3:ticket-09'], 'retired s3:ticket-09 package script must not return');
const certificationPr = packageJSON.scripts?.['s3:certification:pr'] ?? '';
for (const marker of ['s3:local:check', 's3:target-runtime:test', 's3:certification:check', 'test:s3-certification', 'test:s3-target-image-evidence', 's3:certification:preflight', 'ownership:check']) {
  assert(certificationPr.includes(marker), `S3 certification PR gate is missing ${marker}`);
}
for (const duplicate of ['s3:postgres', 'npm run lint', 'npm run build']) {
  assert(!certificationPr.includes(duplicate), `S3 certification PR gate duplicates ${duplicate}`);
}
assert(authorityWorkflow.includes('npm run s3:command-authority'), 'S3 Command Authority no longer owns the stable authority command');
for (const marker of [
  'name: S3 Command Certification',
  '.github/workflows/s3-command-certification.yml',
  'certification-preflight:',
  'npm run s3:certification:pr',
  'needs: [certification-preflight]',
  'verify-s3-command-certification.mjs',
  's3-command-certification.yml@refs/(heads|tags)/.+',
]) {
  assert(workflow.includes(marker), `S3 Command Certification workflow is missing ${marker}`);
}
assert(!workflow.includes('s3-ticket-09'), 'S3 Command Certification workflow still emits Ticket 09 topology');
assert(runner.includes("out/s3-command-certification"), 'S3 certification preflight default output path is not stable');
assert(!runner.includes("out/s3-ticket-09"), 'S3 certification runner still defaults to Ticket 09 evidence topology');
for (const token of [
  '100 commands/s for a 60-minute steady-state certification',
  '1,000 commands/s for a one-minute burst',
  'formal-certification-pending',
  'tsenart/vegeta',
  'fortio/fortio',
  'Shopify/toxiproxy',
  '--profile=formal',
  'verify-s3-command-certification.mjs',
]) assert(docs.includes(token), `S3 operations documentation is missing: ${token}`);

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log('S3 Command certification checks passed: frozen capacity/SLO envelope, deterministic crash semantics, fail-closed formal evidence, bounded approved canary and zero public production traffic.');
