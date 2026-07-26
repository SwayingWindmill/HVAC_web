import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const [plan, contract, compose, connector, unitTests, integrationTests, runner, ownership] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/thingsboard/s3-set-temperature-setpoint.local.v1.json'),
  read('infra/s3-thingsboard/compose.yaml'),
  read('services/thingsboard-connector-control/pkg/controlconnector/thingsboard.go'),
  read('services/thingsboard-connector-control/pkg/controlconnector/thingsboard_test.go'),
  read('services/thingsboard-connector-control/pkg/controlconnector/thingsboard_integration_test.go'),
  read('scripts/run-s3-thingsboard-local-tests.mjs'),
  readJSON('contracts/ownership/s3-command-ownership.v1.json'),
]);

const errors = [];
const assert = (condition, message) => {
  if (!condition) errors.push(message);
};

assert(plan.completedTickets?.includes('S3-06'), 'S3-06 is not marked complete');
assert((plan.currentFrontier ?? []).some((ticket) => ['S3-07', 'S3-08', 'S3-09'].includes(ticket)), 'S3 frontier regressed before S3-07');
assert(plan.productionTrafficPercent === 0, 'S3 ThingsBoard baseline enabled production traffic');
assert(plan.firstTracerBullet?.connector === 'SYNTHETIC_ONLY', 'Production execution is no longer Synthetic-only');
assert(plan.firstTracerBullet?.localProviderContract === 'THINGSBOARD_CE_4.3.1.3_LOCAL_VERIFIED', 'Local ThingsBoard contract marker is missing');
assert(plan.firstTracerBullet?.productionProviderEnabled === false, 'Production ThingsBoard provider enabled too early');
assert(ownership.restrictedWorkers?.some((worker) => worker.service === 'command-dispatcher' && worker.activationStatus === 'active-S3-05-synthetic-only'), 'Dispatcher ownership is no longer Synthetic-only');

assert(contract.provider === 'THINGSBOARD_CE' && contract.providerVersion === '4.3.1.3', 'ThingsBoard contract version is not pinned');
assert(contract.verificationStatus === 'LOCAL_VERIFIED' && contract.productionEligible === false, 'Local contract is incorrectly production eligible');
assert(contract.transport?.operation === 'TWO_WAY' && contract.transport?.pathTemplate === '/api/rpc/twoway/{deviceId}', 'Two-way RPC endpoint contract is incorrect');
assert(contract.request?.method === 'setTemperatureSetpoint', 'Canonical setpoint mapping is missing');
assert(contract.acknowledgement?.businessSuccess === false && contract.acknowledgement?.reportedStateVerificationRequired === true, 'ACK incorrectly implies business success');
assert(contract.failureClassification?.requestNotWritten === 'PRE_SEND_REJECTED', 'Pre-send classification is missing');
assert(contract.failureClassification?.requestWrittenTransportFailure === 'REQUEST_COMMITTED', 'After-write transport classification is missing');
assert(contract.failureClassification?.automaticRetryAfterRequestWritten === false, 'After-write automatic retry is enabled');
assert(contract.providerObservedContract?.httpDeviceRpcRequestId?.minimum === 0, 'Observed ThingsBoard HTTP RPC request ID zero contract is missing');
assert(contract.evidence?.credentialPersistenceAllowed === false && contract.evidence?.rawCredentialLoggingAllowed === false, 'Provider credential persistence is allowed');

assert(compose.includes('thingsboard/tb-node:4.3.1.3'), 'ThingsBoard image is not pinned to 4.3.1.3');
assert(compose.includes('127.0.0.1:18080:8080'), 'Local ThingsBoard is not loopback-only');
assert(compose.includes('TB_QUEUE_TYPE: in-memory'), 'Local ThingsBoard queue mode is not deterministic');

for (const token of [
  'MappingLocalVerified', 'MappingProductionVerified', 'AllowLocalVerified', 'AllowProductionVerified',
  '/api/rpc/twoway/', 'setpointC', 'httptrace.ClientTrace', 'WroteRequest',
  'ConnectorPreSendRejected', 'ConnectorRequestCommitted', 'ConnectorAcknowledged',
  'THINGSBOARD_RPC_TIMEOUT', 'CONNECTOR_EVIDENCE_COMPLETION_FAILED', 'Verified:     false',
  'EvidenceStore', 'RequestSHA256', 'ResponseSHA256', 'ErrOldFence', 'ErrPayloadMismatch',
]) {
  assert(connector.includes(token), `ThingsBoard Connector invariant is missing: ${token}`);
}
assert(!connector.includes('Persistent RPC'), 'Persistent RPC was enabled in S3-06');
for (const test of [
  'TestThingsBoardTwoWayRPCReturnsAcknowledgedButNotVerified',
  'TestThingsBoardHTTPTimeoutIsCommittedUnknown',
  'TestThingsBoardTransportFailureBeforeWriteIsSafePreSend',
  'TestThingsBoardEvidenceCompletionFailureAfterWriteFreezesUnknown',
  'TestThingsBoardRequiresVerifiedMappingAndRejectsOldFence',
]) {
  assert(unitTests.includes(test), `ThingsBoard unit test is missing: ${test}`);
}
assert(integrationTests.includes('TestLocalThingsBoardTwoWaySetpointContract'), 'Local ThingsBoard integration test is missing');
assert(integrationTests.includes('*int64') && integrationTests.includes('*command.ID < 0'), 'HTTP RPC request ID zero is not accepted by the local Device emulator');
assert(runner.includes('INSTALL_TB=true') && runner.includes('LOAD_DEMO=true'), 'Local ThingsBoard installation runner is incomplete');
assert(runner.includes('credentialsPersisted: false') && runner.includes('productionEligible: false'), 'Local evidence could be mistaken for production certification');
assert(runner.includes("down', '-v'"), 'Local ThingsBoard containers and volumes are not cleaned');

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('S3 ThingsBoard Connector checks passed: pinned local CE contract, fixed canonical mapping, evidence-before-send, after-write uncertainty and no production eligibility.');
