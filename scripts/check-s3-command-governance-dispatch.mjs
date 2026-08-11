import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const [plan, ownership, dataOwnership, routes, model, grant, governance, approval, dispatch, durable, migration, governanceTests, dispatchTests] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/ownership/s3-command-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  read('libs/commandmodel/model.go'),
  read('libs/commandauth/grant.go'),
  read('services/command-service/pkg/commandservice/governance.go'),
  read('services/command-service/pkg/commandservice/postgres_approval.go'),
  read('services/command-service/pkg/commandservice/postgres_dispatch.go'),
  read('services/command-dispatcher/pkg/commanddispatcher/durable.go'),
  read('services/command-service/migrations/001_s3_command_runtime.sql'),
  read('services/command-service/pkg/commandservice/postgres_governance_integration_test.go'),
  read('services/command-service/pkg/commandservice/postgres_dispatch_integration_test.go'),
]);

const errors = [];
const assert = (condition, message) => {
  if (!condition) errors.push(message);
};

for (const ticket of ['S3-03', 'S3-05']) {
  assert(plan.completedTickets?.includes(ticket), `${ticket} is not marked complete`);
}
assert((plan.currentFrontier ?? []).some((ticket) => ['S3-04', 'S3-06', 'S3-07', 'S3-08', 'S3-09'].includes(ticket)), 'S3 frontier must not regress before S3-04/S3-06');
assert(plan.productionTrafficPercent === 0, 'S3 governance/dispatch baseline must keep zero production traffic');
assert(ownership.businessOwner === 'command-service', 'Command Service must remain the single business owner');
assert(ownership.restrictedWorkers?.every((worker) => worker.directDatabaseAccess === false), 'Dispatcher must not receive command_runtime credentials');
const dispatcherOwnership = ownership.restrictedWorkers?.find((worker) => worker.service === 'command-dispatcher');
assert(dispatcherOwnership?.activationStatus === 'active-S3-05-synthetic-only', 'Dispatcher activation must remain Synthetic-only');
assert(!(dataOwnership.databaseAccess ?? []).some((access) => access.service === 'command-dispatcher'), 'Data Ownership Registry grants Dispatcher direct database access');
for (const name of ['command-authorization-snapshot', 'command-risk-snapshot', 'command-approval-snapshot']) {
  const resource = (dataOwnership.resources ?? []).find((item) => item.kind === 'projection' && item.name === name);
  assert(resource?.writer === 'command-service', `${name} must be Command Service-owned`);
}
for (const route of routes.routes ?? []) {
  if (route.owner !== 'command-service') continue;
  assert(route.rollout?.mode === 'disabled', `${route.method} ${route.path} enabled control traffic too early`);
  assert(route.shadowSideEffectPolicy === 'SYNTHETIC_ONLY', `${route.method} ${route.path} is no longer Synthetic-only`);
}

for (const token of ['AuthorizationPurpose', 'AuthorizationCommandSubmit', 'AuthorizationCommandApprove', 'AuthorizationSnapshot', 'RiskSnapshot', 'ApprovalEvidence', 'IntentAwaitingApproval', 'IntentApproved', 'ApprovalTwoPerson']) {
  assert(model.includes(token), `Command model is missing ${token}`);
}
for (const token of ['MaximumGrantLifetime    = 30 * time.Second', 'Purpose', 'PolicyRevision', 'EmergencyRevocationRevision', 'risk ceiling', 'scope is invalid', 'revoked or replayed']) {
  assert(grant.includes(token), `Command Grant invariant is missing: ${token}`);
}
for (const token of ['command-risk:equipment-capability:v1', 'CapabilityProfileFor(request.Capability)', 'profile.LowRiskDelta', 'profile.MediumRiskDelta', 'profile.MaximumDelta', 'ApprovalSingleApprover', 'ApprovalTwoPerson', 'approval.ApproverID == intent.PrincipalID', 'AuthorizationCommandApprove', 'approval.ApproverID, intent.OrganizationID', 'validateAuthorizationScope(approval.Authorization']) {
  assert(governance.includes(token), `Governance invariant is missing: ${token}`);
}
for (const table of ['command_authorization_snapshots', 'command_risk_snapshots', 'command_approval_snapshots']) {
  assert(migration.includes(`command_runtime.${table}`), `Governance table is missing: ${table}`);
  assert(migration.includes(`ALTER TABLE command_runtime.${table} FORCE ROW LEVEL SECURITY`), `${table} does not force RLS`);
}
assert(approval.includes("if finalStatus == commandmodel.IntentQueued"), 'Approval transaction does not gate Dispatch Outbox on the final approval threshold');
assert(approval.includes('COMMAND_APPROVAL_CAPTURED'), 'Approval Audit Intent is missing');
assert(approval.includes('authorization_grant_id'), 'Fresh approval authorization is not persisted');
assert(approval.includes('authorization_purpose'), 'Approval authorization purpose is not persisted');
assert(migration.includes('authorization_purpose text NOT NULL'), 'Authorization purpose is not an immutable PostgreSQL field');

for (const token of ['pgx.Serializable', 'FOR UPDATE OF o SKIP LOCKED', 'active_execution_fence = active_execution_fence + 1', 'LEASE_EXPIRED_WITHOUT_SEND_PROOF', 'IntentOutcomeUnknown', 'PROVABLY_NOT_SENT_REQUEUE', 'ErrStaleFence', 'frozen_control_groups', 'earlier.device_command_sequence < i.device_command_sequence']) {
  assert(dispatch.includes(token), `Durable dispatch invariant is missing: ${token}`);
}
assert(durable.includes('Dispatcher never owns') && durable.includes('ClaimDispatch') && durable.includes('ResolveDispatch'), 'Durable Dispatcher does not use Command Service Claim/Resolve');
assert(!durable.includes('pgx') && !durable.includes('database/sql'), 'Durable Dispatcher imports a database client');

for (const test of ['TestPostgresApprovalUsesFreshAuthorizationAndCreatesOutboxOnlyAfterThreshold', 'TestPostgresHighRiskRequiresTwoDistinctApprovals', 'TestPostgresExecutionExpiresWhenAnyApprovalAuthorizationExpires']) {
  assert(governanceTests.includes(test), `Governance integration test is missing: ${test}`);
}
for (const test of ['TestPostgresConcurrentDispatchClaimHasOneWinner', 'TestPostgresPreSendRetryAdvancesFenceAndRejectsOldAttempt', 'TestPostgresExpiredPreparedLeaseFreezesOutcomeUnknown', 'TestPostgresDeviceControlLaneIsSerial']) {
  assert(dispatchTests.includes(test), `Dispatch integration test is missing: ${test}`);
}

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('S3 governance and durable dispatch checks passed: short-lived exact grants, risk-bound approvals, single-writer Claim/Resolve, monotonic fences, serial device lanes and no blind lease takeover.');
