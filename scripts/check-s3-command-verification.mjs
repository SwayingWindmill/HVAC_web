import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const [plan, ownership, model, service, serviceTests, postgres, postgresTests, migration, worker, workerTests, mqttConnector, mqttEdge, routes] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/ownership/s3-command-ownership.v1.json'),
  read('libs/commandmodel/model.go'),
  read('services/command-service/pkg/commandservice/service.go'),
  read('services/command-service/pkg/commandservice/service_test.go'),
  read('services/command-service/pkg/commandservice/postgres_verification.go'),
  read('services/command-service/pkg/commandservice/postgres_dispatch_integration_test.go'),
  read('services/command-service/migrations/001_s3_command_runtime.sql'),
  read('services/command-dispatcher/pkg/commanddispatcher/verification.go'),
  read('services/command-dispatcher/pkg/commanddispatcher/verification_test.go'),
  read('services/command-dispatcher/pkg/mqttconnector/connector.go'),
  read('tools/eg8200-simulator/internal/simulator/mqtt_command.go'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
]);

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(plan.completedTickets?.includes('S3-07'), 'S3-07 is not marked complete');
assert((plan.currentFrontier ?? []).some((ticket) => ['S3-08', 'S3-09'].includes(ticket)), 'S3 frontier regressed before S3-08');
assert(plan.productionTrafficPercent === 0, 'S3 enabled production traffic');
assert(plan.firstTracerBullet?.publicRoutesEnabled === false, 'Command public routes enabled');
assert(plan.firstTracerBullet?.productionProviderEnabled === false, 'Production provider enabled before certification');

const dispatcherOwner = ownership.restrictedWorkers?.find((workerItem) => workerItem.service === 'command-dispatcher');
const verifierOwner = ownership.restrictedWorkers?.find((workerItem) => workerItem.service === 'command-verifier');
assert(dispatcherOwner?.activationStatus === 'active-native-mqtt', 'Command Dispatcher is not on native MQTT');
assert(dispatcherOwner?.directDatabaseAccess === false, 'Command Dispatcher received command_runtime credentials');
assert(verifierOwner?.directDatabaseAccess === false, 'Command Verifier received command_runtime credentials');
assert(ownership.businessOwner === 'command-service', 'Command Service lost verification business ownership');
assert(ownership.ownedResources?.includes('command-reported-state-verification'), 'Verification authority resource is not registered');
assert(ownership.transport?.type === 'MQTT' && ownership.transport?.authority === 'transport-only', 'Command transport is not native MQTT transport-only');
assert(ownership.firstCapability?.connector === 'NATIVE_MQTT', 'First capability is not routed through native MQTT');
assert(ownership.nativeEdgeContract?.provider === 'EG8200_MQTT', 'Native Edge command contract is missing');
assert(ownership.nativeEdgeContract?.connectorMayDeclareVerified === false, 'MQTT connector may not declare VERIFIED');

for (const route of (routes.routes ?? []).filter((route) => route.owner === 'command-service')) {
  assert(route.rollout?.mode === 'all', `${route.method} ${route.path} canonical ownership is not active`);
  assert(route.compatibilityMode === 'native', `${route.method} ${route.path} is not native`);
}

for (const token of [
  'AttemptAcknowledged', 'AttemptVerified', 'VerificationEnvelope', 'ReportedStateEvidence',
  'VerificationSucceeded', 'VerificationInconclusive', 'VerificationMismatch', 'VerificationTimedOut',
  'BaselineBusinessRevision', 'VerificationDeadline', 'ConnectorEvidenceID',
]) {
  assert(model.includes(token), `Command verification model is missing ${token}`);
}

for (const token of [
  'PROVIDER_ACKNOWLEDGED_AWAITING_REPORTED_STATE', 'PrepareVerification', 'ResolveVerification',
  'ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED', 'REPORTED_STATE_VERIFICATION_NOT_PROVEN',
  'reported.BusinessRevision > envelope.BaselineBusinessRevision', 'reported.ObservedAt.After(envelope.AcknowledgedAt)',
  'CapabilityProfileFor(intent.Capability)', 'profile.VerificationTolerance', 'result.Verified',
]) {
  assert(service.includes(token), `In-memory Command verification invariant is missing ${token}`);
}
for (const test of [
  'TestAcknowledgedCommandRequiresReportedStateVerification',
  'TestReportedStateMismatchBecomesOutcomeUnknown',
  'TestConnectorCannotDeclareReportedStateVerified',
]) {
  assert(serviceTests.includes(test), `Command verification unit test is missing ${test}`);
}

for (const token of [
  'ClaimVerification', 'ResolveVerification', 'reconcileExpiredAcknowledgedAttempts',
  "a.status = 'ACKNOWLEDGED'", 'verification_lease_owner', 'verification_deadline',
  'ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED', 'REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED',
  'frozen_control_groups', 'validReportedState',
]) {
  assert(postgres.includes(token), `PostgreSQL verification invariant is missing ${token}`);
}
for (const token of ['acknowledged_at', 'verification_deadline', 'verification_lease_owner', 'verification_lease_until', 'verification_evidence_id', 'command_attempts_verification_ready_idx']) {
  assert(migration.includes(token), `Command migration is missing ${token}`);
}
for (const test of [
  'TestPostgresReportedStateMismatchFreezesOutcomeUnknown',
  'TestPostgresExpiredReportedStateVerificationFreezesOutcomeUnknown',
  'TestPostgresConnectorCannotSelfVerify',
]) {
  assert(postgresTests.includes(test), `PostgreSQL verification test is missing ${test}`);
}

for (const token of [
  'DurableVerificationStore', 'ReportedStateReader', 'AuthoritativeReportedStateVerifier',
  'DurableVerificationWorker', 'ClaimVerification', 'ResolveVerification',
  'CapabilityProfileFor(envelope.Capability)', 'profile.VerificationTolerance',
  'REPORTED_STATE_VERIFICATION_TIMED_OUT', 'REPORTED_VALUE_MISMATCH',
]) {
  assert(worker.includes(token), `Command Verifier worker invariant is missing ${token}`);
}
assert(!worker.includes('pgx') && !worker.includes('command_runtime'), 'Command Verifier has direct database coupling');
for (const test of [
  'TestSetpointVerifierRequiresNewFreshReportedState',
  'TestSetpointVerifierClassifiesMismatch',
  'TestDurableVerificationWorkerPreservesClaimBoundary',
  'TestDurableVerificationWorkerDoesNotResolveReadFailure',
]) {
  assert(workerTests.includes(test), `Command Verifier worker test is missing ${test}`);
}

for (const token of [
  'command/reply', 'QoS: 1', 'ExecutionFence', 'PayloadHash', 'Prepare(ctx',
  'ConnectorAcknowledged', 'Verified: false', 'MQTT_COMMAND_REPLY_TIMEOUT',
]) {
  assert(mqttConnector.includes(token), `Cloud MQTT command connector is missing ${token}`);
}
for (const token of [
  'commandId', 'STALE_FENCE', 'EXPIRED', 'COMMAND_MAPPING_INVALID', 'SubmitCommand', 'command/reply',
]) {
  assert(mqttEdge.includes(token), `Edge MQTT command handler is missing ${token}`);
}

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log('S3 command verification checks passed: native MQTT transport is bounded, ACK remains non-terminal, and S2 reported-state evidence independently determines VERIFIED.');
