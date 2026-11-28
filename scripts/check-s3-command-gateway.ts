import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

interface RouteOwnershipDoc {
  routes?: Array<{ method: string; path: string; owner?: string }>;
}

interface CommandOpenApiDoc {
  info?: { version?: string };
  paths?: {
    '/api/v1/commands'?: {
      post?: {
        'x-production-traffic-percent'?: number;
        'x-client-forbidden-fields'?: string[];
      };
    };
    '/api/v1/commands/{commandId}'?: {
      get?: {
        'x-production-traffic-percent'?: number;
      };
    };
  };
  components?: {
    schemas?: {
      Command?: {
        required?: string[];
        properties?: {
          pointId?: { format?: string };
          status?: { enum?: string[] };
          risk?: { enum?: string[] };
        };
      };
    };
  };
}

const root = resolve(process.cwd());
const read = (relPath: string): Promise<string> => readFile(resolve(root, relPath), 'utf8');
const readJSON = async <T>(relPath: string): Promise<T> => JSON.parse(await read(relPath)) as T;

const [
  routes,
  openapi,
  gateway,
  gatewayTests,
  gatewayMain,
  gatewayServer,
  iam,
  iamServer,
  iamTests,
  commandHTTP,
  commandHTTPTests,
  controlAuthority,
  adaptersHTTP,
  operatorHTTP,
  controlAuthorityTests,
  commandModel,
  controlGate,
] = await Promise.all([
  readJSON<RouteOwnershipDoc>('contracts/ownership/route-ownership.v1.json'),
  readJSON<CommandOpenApiDoc>('contracts/http/s3-command-public.openapi.json'),
  read('cmd/energy-api/internal/gateway/command.go'),
  read('cmd/energy-api/internal/gateway/command_test.go'),
  read('cmd/energy-api/main.go'),
  read('cmd/energy-api/internal/gateway/server.go'),
  read('modules/iam/internal/iam/command_server.go'),
  read('modules/iam/internal/iam/server.go'),
  read('modules/iam/internal/iam/command_server_test.go'),
  read('modules/command/pkg/commandservice/http.go'),
  read('modules/command/pkg/commandservice/http_test.go'),
  read('modules/command/pkg/commandservice/control_authority.go'),
  read('modules/command/pkg/commandservice/adapters_http.go'),
  read('modules/command/pkg/commandservice/operator_http.go'),
  read('modules/command/pkg/commandservice/control_authority_test.go'),
  read('libs/commandmodel/model.go'),
  read('libs/commandmodel/control_gate.go'),
]);

const errors: string[] = [];
const assert = (condition: unknown, message: string): void => {
  if (!condition) errors.push(message);
};

for (const [method, routePath] of [
  ['POST', '/api/v1/commands'],
  ['GET', '/api/v1/commands/{commandId}'],
  ['GET', '/api/v1/sites/{siteId}/control-state'],
  ['PUT', '/api/v1/sites/{siteId}/control-mode'],
  ['PUT', '/api/v1/assets/{assetId}/control-lock'],
  ['POST', '/api/v1/sites/{siteId}/control-shutdown'],
]) {
  const route = routes.routes?.find((item) => item.method === method && item.path === routePath);
  assert(route?.owner === 'command-service', `Command public route ownership missing or wrong for ${method} ${routePath}`);
}

assert(openapi.info?.version === '0.5.0-command-point-identity', 'Command OpenAPI is not the implemented canonical Point identity baseline');
assert(openapi.paths?.['/api/v1/commands']?.post?.['x-production-traffic-percent'] === 100, 'Command POST OpenAPI must keep 100% production traffic');
assert(openapi.paths?.['/api/v1/commands/{commandId}']?.get?.['x-production-traffic-percent'] === 100, 'Command GET OpenAPI must keep 100% production traffic');
for (const forbidden of ['tenantId', 'siteId', 'deviceId', 'pointId', 'principalId', 'providerMethod', 'providerParams', 'executionFence']) {
  assert(openapi.paths?.['/api/v1/commands']?.post?.['x-client-forbidden-fields']?.includes(forbidden), `Command OpenAPI no longer forbids ${forbidden}`);
}
assert(openapi.components?.schemas?.Command?.required?.includes('pointId'), 'Command projection is missing canonical pointId');
assert(openapi.components?.schemas?.Command?.properties?.pointId?.format === 'uuid', 'Command pointId is not a UUID contract');
for (const status of ['AWAITING_APPROVAL', 'APPROVED', 'OUTCOME_UNKNOWN']) {
  assert(openapi.components?.schemas?.Command?.properties?.status?.enum?.includes(status), `Command OpenAPI is missing ${status}`);
}
for (const risk of ['LOW', 'MEDIUM', 'HIGH']) {
  assert(openapi.components?.schemas?.Command?.properties?.risk?.enum?.includes(risk), `Command OpenAPI is missing risk ${risk}`);
}

for (const token of [
  'X-CSRF-Token', 'Origin', 'Idempotency-Key', 'X-Command-Control-Context',
  'signCommandControlContext', 'verifySubmitStepUpTOTP', 'tenant:', 'session:', 'Cache-Control',
]) {
  assert(gateway.includes(token), `Gateway Command edge invariant is missing: ${token}`);
}
assert(!gateway.includes('providerMethod') && !gateway.includes('providerParams'), 'Gateway accepts provider-specific Command fields');
for (const header of ['X-Command-Grant', 'X-Command-Read-Context', 'X-Command-Control-Context', 'X-Acting-Organization-ID']) {
  assert(gatewayServer.includes(header), `Gateway edge header deny list is missing ${header}`);
}
assert(gatewayMain.includes('COMMAND_SERVICE_URL') && gatewayMain.includes('COMMAND_SERVICE_SERVER_CA') && gatewayMain.includes('workloadTransport'), 'Gateway Command Service mTLS configuration is missing');

for (const token of ['EvaluateControlGate', 'GatePhaseSubmit', 'GatePhasePreDispatch', 'AuthorizationCommandSubmit', 'ClaimDispatch', 'maxPreDispatchClaimScans']) {
  assert(controlAuthority.includes(token), `ControlAuthority invariant is missing: ${token}`);
}
for (const token of ['ONLINE', 'CURRENT', 'FRESH', 'GOOD', 'validateSubmitLiveState', 'preDispatchStateFailureCode', 'evaluatePeerInterlocks']) {
  assert(controlGate.includes(token), `CommandModel ControlGate invariant is missing: ${token}`);
}
for (const token of ['EvaluationAvailability', 'telemetryReportedStatePath', 'registryDispatchContextPath', 'ReadLivePoint', 'GetDispatchContext']) {
  assert(adaptersHTTP.includes(token), `Command Service HTTPAdapter invariant is missing: ${token}`);
}
for (const token of ['VerifyDelegation', 'tenant:', 'session:', 'http.StatusAccepted']) {
  assert(operatorHTTP.includes(token), `Command Service OperatorHTTP invariant is missing: ${token}`);
}

for (const token of ['CommandDecisionPath', 'AuthorizationCommandSubmit', 'MaximumGrantLifetime', 'Transitive: false', 'command:authorize']) {
  assert(iam.includes(token) || commandModel.includes(token) || iamServer.includes(token), `IAM Command invariant is missing: ${token}`);
}
for (const testName of ['TestIAMCommandDecisionIssuesExactPurposeBoundGrant', 'TestIAMCommandDecisionDoesNotReuseSubmitPermissionForApproval', 'TestIAMCommandExplicitDenyOverridesAllow']) {
  assert(iamTests.includes(testName), `IAM Command test is missing: ${testName}`);
}

for (const token of ['VerifyGrant', 'AuthorizationCommandSubmit', 'CommandGrantUseChecker', 'VerifyDelegation', 'command:read', 'tenant:', 'command:', 'http.StatusAccepted']) {
  assert(commandHTTP.includes(token), `Command Service HTTP invariant is missing: ${token}`);
}
for (const testName of ['TestCommandHTTPCreateRequiresExactIAMGrant', 'TestCommandHTTPCreateRejectsApprovalGrantAndScopeDrift', 'TestCommandHTTPReadRequiresSignedOrganizationAndCommandScopes']) {
  assert(commandHTTPTests.includes(testName), `Command Service HTTP test is missing: ${testName}`);
}
for (const testName of [
  'TestControlAuthorityOrderInvariantLocksBeforeStepUpBeforeGrant',
  'TestControlAuthorityShutdownEmergencyBypassAndOrdering',
  'TestControlAuthorityClaimDispatchPreDispatchGateScanAndTransientHandling',
  'TestOperatorHTTPHandlerAuthAndRoutes',
]) {
  assert(controlAuthorityTests.includes(testName), `ControlAuthority test is missing: ${testName}`);
}
for (const testName of [
  'TestGatewayCreateCommandDerivesAuthorityAndCurrentState',
  'TestGatewayCreateCommandFailsBeforeUpstreamsWithoutCSRF',
  'TestGatewayRejectsBrowserCommandAuthorityHeaders',
  'TestGatewayUnsafeCurrentStateStopsBeforeCommandAuthorization',
  'TestGatewayGetCommandUsesScopedReadContext',
  'TestCommandRegistryDecisionUsesRegistryRouteOwnership',
]) {
  assert(gatewayTests.includes(testName), `Gateway Command route test is missing: ${testName}`);
}

assert(iamServer.includes('CommandDecisionPath') && iamServer.includes('commandAuthorizeAction'), 'IAM server does not register Command authorization');

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('S3 Gateway & ControlAuthority Command checks passed: Session and CSRF, Registry dispatch context, S2 current-state, exact IAM Grant, ControlAuthority pre-dispatch gate and tenant-scoped delegation.');
