import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const [plan, routes, openapi, gateway, gatewayTests, gatewayMain, gatewayServer, iam, iamServer, iamTests, commandHTTP, commandHTTPTests, commandModel] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/http/s3-command-public.openapi.json'),
  read('services/platform-gateway/internal/gateway/command.go'),
  read('services/platform-gateway/internal/gateway/command_test.go'),
  read('services/platform-gateway/cmd/platform-gateway/main.go'),
  read('services/platform-gateway/internal/gateway/server.go'),
  read('services/iam-service/internal/iam/command_server.go'),
  read('services/iam-service/internal/iam/server.go'),
  read('services/iam-service/internal/iam/command_server_test.go'),
  read('services/command-service/pkg/commandservice/http.go'),
  read('services/command-service/pkg/commandservice/http_test.go'),
  read('libs/commandmodel/model.go'),
]);

const errors = [];
const assert = (condition, message) => {
  if (!condition) errors.push(message);
};

assert(plan.completedTickets?.includes('S3-04'), 'S3-04 is not marked complete');
assert((plan.currentFrontier ?? []).some((ticket) => ['S3-07', 'S3-08', 'S3-09'].includes(ticket)), 'S3 frontier regressed before S3-07');
assert(plan.productionTrafficPercent === 0, 'S3 Gateway baseline must keep zero production traffic');
assert(plan.firstTracerBullet?.publicRoutesEnabled === false, 'S3 public Command routes enabled too early');

for (const route of (routes.routes ?? []).filter((item) => item.owner === 'command-service')) {
  assert(route.rollout?.mode === 'disabled', `${route.method} ${route.path} is not disabled`);
  assert(route.shadowSideEffectPolicy === 'SYNTHETIC_ONLY', `${route.method} ${route.path} is not Synthetic-only`);
}
const createRoute = routes.routes?.find((item) => item.method === 'POST' && item.path === '/api/v1/commands');
const getRoute = routes.routes?.find((item) => item.method === 'GET' && item.path === '/api/v1/commands/{commandId}');
assert(createRoute?.owner === 'command-service' && getRoute?.owner === 'command-service', 'Command public route ownership is missing');

assert(openapi.info?.version === '0.3.0-disabled-command-ux-baseline', 'Command OpenAPI is not the implemented disabled Command UX baseline');
assert(openapi.paths?.['/api/v1/commands']?.post?.['x-production-traffic-percent'] === 0, 'Command POST OpenAPI enabled production traffic');
assert(openapi.paths?.['/api/v1/commands/{commandId}']?.get?.['x-production-traffic-percent'] === 0, 'Command GET OpenAPI enabled production traffic');
for (const forbidden of ['organizationId', 'siteId', 'principalId', 'providerMethod', 'providerParams', 'thingsBoardDeviceId', 'executionFence']) {
  assert(openapi.paths?.['/api/v1/commands']?.post?.['x-client-forbidden-fields']?.includes(forbidden), `Command OpenAPI no longer forbids ${forbidden}`);
}
for (const status of ['AWAITING_APPROVAL', 'APPROVED', 'OUTCOME_UNKNOWN']) {
  assert(openapi.components?.schemas?.Command?.properties?.status?.enum?.includes(status), `Command OpenAPI is missing ${status}`);
}
for (const risk of ['LOW', 'MEDIUM', 'HIGH']) {
  assert(openapi.components?.schemas?.Command?.properties?.risk?.enum?.includes(risk), `Command OpenAPI is missing risk ${risk}`);
}

for (const token of [
  'X-CSRF-Token', 'Origin', 'Idempotency-Key', 'resolveCommandDevice', 'commandRegistryDecision', 'readCommandCurrentState',
  'EvaluationAvailability', 'ONLINE', 'CURRENT', 'FRESH', 'GOOD', 'AuthorizationCommandSubmit',
  'structurallyValidCommandGrant', 'X-Command-Read-Context', 'organization:', 'command:', 'Cache-Control',
]) {
  assert(gateway.includes(token), `Gateway Command invariant is missing: ${token}`);
}
assert(gateway.includes('Registry could not resolve the Command Device'), 'Gateway does not resolve Device ownership through Registry');
assert(gateway.includes('Telemetry Runtime returned an invalid current-state projection'), 'Gateway does not fail closed on S2 projection drift');
assert(!gateway.includes('providerMethod') && !gateway.includes('providerParams'), 'Gateway accepts provider-specific Command fields');
for (const header of ['X-Command-Grant', 'X-Command-Read-Context', 'X-Acting-Organization-ID']) {
  assert(gatewayServer.includes(header), `Gateway edge header deny list is missing ${header}`);
}
assert(gatewayMain.includes('COMMAND_SERVICE_URL') && gatewayMain.includes('COMMAND_SERVICE_SERVER_CA') && gatewayMain.includes('workloadTransport'), 'Gateway Command Service mTLS configuration is missing');

for (const token of ['CommandDecisionPath', 'AuthorizationCommandSubmit', 'MaximumGrantLifetime', 'Transitive: false', 'command:authorize']) {
  assert(iam.includes(token) || commandModel.includes(token) || iamServer.includes(token), `IAM Command invariant is missing: ${token}`);
}
for (const test of ['TestIAMCommandDecisionIssuesExactPurposeBoundGrant', 'TestIAMCommandDecisionDoesNotReuseSubmitPermissionForApproval', 'TestIAMCommandExplicitDenyOverridesAllow']) {
  assert(iamTests.includes(test), `IAM Command test is missing: ${test}`);
}

for (const token of ['VerifyGrant', 'AuthorizationCommandSubmit', 'CommandGrantUseChecker', 'VerifyDelegation', 'command:read', 'organization:', 'command:', 'http.StatusAccepted']) {
  assert(commandHTTP.includes(token), `Command Service HTTP invariant is missing: ${token}`);
}
for (const test of ['TestCommandHTTPCreateRequiresExactIAMGrant', 'TestCommandHTTPCreateRejectsApprovalGrantAndScopeDrift', 'TestCommandHTTPReadRequiresSignedOrganizationAndCommandScopes']) {
  assert(commandHTTPTests.includes(test), `Command Service HTTP test is missing: ${test}`);
}
for (const test of ['TestGatewayCreateCommandDerivesAuthorityAndCurrentState', 'TestGatewayCreateCommandFailsBeforeUpstreamsWithoutCSRF', 'TestGatewayRejectsBrowserCommandAuthorityHeaders', 'TestGatewayUnsafeCurrentStateStopsBeforeCommandAuthorization', 'TestGatewayGetCommandUsesScopedReadContext', 'TestCommandRegistryDecisionUsesRegistryRouteOwnership']) {
  assert(gatewayTests.includes(test), `Gateway Command route test is missing: ${test}`);
}

assert(iamServer.includes('CommandDecisionPath') && iamServer.includes('commandAuthorizeAction'), 'IAM server does not register Command authorization');

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('S3 Gateway Command checks passed: Session and CSRF, Registry ownership, S2 current-state, exact IAM Grant, Command Service authority and disabled public routes.');
