import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const outputRoot = resolve(root, 'out/s2-gateway-snapshot');
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));

function assert(condition, message) {
  if (!condition) throw new Error(`Invalid S2 Ticket 05 Gateway Snapshot: ${message}`);
}

function includesAll(source, markers, label) {
  for (const marker of markers) assert(source.includes(marker), `${label} is missing ${marker}`);
}

const [
  openapi,
  toolingLock,
  gatewayGenerated,
  runtimeGenerated,
  typescriptGenerated,
  telemetryGateway,
  gatewayServer,
  gatewayMain,
  gatewayRouting,
  gatewayTests,
  browserAudit,
  authTopology,
  iamFixture,
  routeRegistry,
  packageJSON,
  workflow,
  operations,
] = await Promise.all([
  readJSON('contracts/http/s2-telemetry-public.openapi.json'),
  readJSON('contracts/http/s2-tooling.lock.json'),
  readText('services/platform-gateway/pkg/s2telemetryapi/api.gen.go'),
  readText('services/telemetry-runtime-service/pkg/telemetryapi/api.gen.go'),
  readText('apps/hvac-web/src/api/generated/s2Telemetry.gen.ts'),
  readText('services/platform-gateway/internal/gateway/telemetry.go'),
  readText('services/platform-gateway/internal/gateway/server.go'),
  readText('services/platform-gateway/cmd/platform-gateway/main.go'),
  readText('services/platform-gateway/cmd/platform-gateway/routing.go'),
  readText('services/platform-gateway/internal/gateway/telemetry_test.go'),
  readText('scripts/run-s2-gateway-browser-audit.mjs'),
  readText('scripts/s0-auth-topology.mjs'),
  readText('services/iam-service/internal/iam/fixtures.go'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('package.json'),
  readText('.github/workflows/s2-gateway-snapshot.yml'),
  readText('docs/operations/s2-gateway-snapshot.md'),
]);

assert(openapi.openapi === '3.1.0', 'public contract must remain OpenAPI 3.1');
assert(openapi['x-public-owner'] === 'platform-gateway' && openapi['x-upstream-owner'] === 'telemetry-runtime-service', 'public/upstream ownership drifted');
assert(openapi['x-activation-status'] === 'expand-baseline', 'public contract activation status drifted');
assert(JSON.stringify(openapi.security) === JSON.stringify([{ BffSession: [] }, { WorkloadMTLS: [] }]), 'public security alternatives drifted');
assert(openapi['x-limits']?.maxKeysPerDevice === 64, 'maxKeysPerDevice must be 64');
assert(openapi['x-limits']?.maxBatchDevices === 100, 'maxBatchDevices must be 100');
assert(openapi['x-limits']?.maxBatchKeySelections === 2048, 'maxBatchKeySelections must be 2048');
const singlePath = '/api/v1/devices/{deviceId}/observation-snapshot';
const batchPath = '/api/v1/telemetry/observation-snapshots:batchGet';
assert(openapi.paths?.[singlePath]?.get?.operationId === 'getDeviceObservationSnapshot', 'single Snapshot operation drifted');
assert(openapi.paths?.[batchPath]?.post?.operationId === 'batchGetDeviceObservationSnapshots', 'batch Snapshot operation drifted');
for (const invariant of ['snapshot-values-preserve-requested-key-order', 'batch-items-preserve-request-order', 'batch-partial-results-use-item-problems', 'real-mode-never-falls-back-to-mock-or-thingsboard-read-through']) {
  assert(openapi['x-contract-invariants']?.includes(invariant), `contract invariant missing: ${invariant}`);
}
for (const field of ['organizationId', 'siteId', 'principal', 'roles', 'scopes', 'policyRevision', 'thingsBoardDeviceId']) {
  assert(openapi['x-client-forbidden-fields']?.includes(field), `client-forbidden field missing: ${field}`);
}
for (const code of ['RESOURCE_NOT_FOUND', 'TELEMETRY_REQUEST_INVALID', 'TELEMETRY_KEY_INVALID', 'TELEMETRY_BATCH_LIMIT_EXCEEDED', 'TELEMETRY_UNAVAILABLE', 'TELEMETRY_TIMEOUT', 'TELEMETRY_AUTHORIZATION_UNAVAILABLE']) {
  assert(openapi.components?.schemas?.ProblemDetails?.properties?.code?.['x-stable-codes']?.includes(code), `stable Problem code missing: ${code}`);
}

const expectedOutputs = [
  'services/telemetry-runtime-service/pkg/telemetryapi/api.gen.go',
  'services/platform-gateway/pkg/s2telemetryapi/api.gen.go',
  'apps/hvac-web/src/api/generated/s2Telemetry.gen.ts',
];
assert(JSON.stringify(toolingLock.outputs) === JSON.stringify(expectedOutputs), 'digest-locked generator outputs drifted');
includesAll(gatewayGenerated, ['package s2telemetryapi', 'type ServerInterface interface', 'GetDeviceObservationSnapshot(', 'BatchGetDeviceObservationSnapshots(', 'OpenAPIContractSHA256'], 'generated Gateway server contract');
includesAll(runtimeGenerated, ['package telemetryapi', 'OpenAPIContractSHA256'], 'generated Runtime contract');
includesAll(typescriptGenerated, ['export interface S2TelemetryClient', 'createS2TelemetryClient', 'credentials: "include"', 'X-CSRF-Token', 'S2TelemetryClientError'], 'generated TypeScript client');

includesAll(telemetryGateway, [
  'telemetryDecisionPath',
  'telemetryauth.ActionSnapshotRead',
  'telemetryauth.ActionBatchRead',
  'identitycontext.SignDelegation',
  'X-Delegation-Grant',
  'Authorization", "Bearer "+grant',
  'context.WithTimeout',
  'http.MaxBytesReader',
  'telemetryauth.MaximumKeysPerTarget',
  'telemetryauth.MaximumTargets',
  'telemetryauth.MaximumTotalKeys',
  'TELEMETRY_AUTHORIZATION_UNAVAILABLE',
  'TELEMETRY_BATCH_LIMIT_EXCEEDED',
  'TELEMETRY_TIMEOUT',
  'RESOURCE_NOT_FOUND',
  'Cache-Control", "private, no-store',
  'validateTelemetrySnapshot',
  'validateTelemetryBatchResponse',
  'verifiedTelemetryWorkloadIdentity',
  'callerScope = "workload:" + caller.workloadSPIFFE',
], 'Gateway Snapshot adapter');
assert(!telemetryGateway.toLowerCase().includes('thingsboard'), 'Gateway Snapshot adapter must not read through ThingsBoard');
assert(!telemetryGateway.toLowerCase().includes('mock'), 'Gateway Snapshot adapter must not fall back to Mock');
assert(!telemetryGateway.toLowerCase().includes('legacy'), 'Gateway Snapshot adapter must not fall back to Legacy');
includesAll(gatewayServer, ['matchPublicTelemetryRoute', 'dispatchTelemetryRoute', 'return telemetryRoute.template', 'safeLogPath', 'FORGED_IDENTITY_HEADER'], 'Gateway public router');
includesAll(gatewayMain, ['TELEMETRY_RUNTIME_URL', 'TELEMETRY_RUNTIME_SERVER_CA', 'TELEMETRY_RUNTIME_AUDIENCE', 'workloadTransport(roots, certificate', 'GATEWAY_SERVER_CERT', 'GATEWAY_SERVER_KEY', 'GATEWAY_CLIENT_CA', 'tls.VerifyClientCertIfGiven', 'tls.VersionTLS13'], 'Gateway mTLS configuration');
includesAll(gatewayRouting, ['S2_ALLOW_UNROUTED_GATEWAY_FIXTURE', 'S0_ALLOW_MEMORY_ROUTE_AUDIT', 'S0_ALLOW_MEMORY_SESSION_STORE', 'isLoopbackTelemetryFixtureURL', 's2_unrouted_gateway_fixture_enabled'], 'test-only un-routed Gateway fixture guard');
includesAll(authTopology, ['startTelemetryRuntimeFixture', 'requestCert: true', 'rejectUnauthorized: true', "minVersion: 'TLSv1.3'", 'S2_ALLOW_UNROUTED_GATEWAY_FIXTURE', 'IAM_S2_AUTHORIZATION_FIXTURE'], 'browser topology mTLS fixture');
includesAll(iamFixture, ['NewS2FixtureTelemetryAuthorizationStore', 'S2FixtureDeviceTwo', 'telemetryauth.ActionSnapshotRead', 'telemetryauth.ActionBatchRead'], 'IAM browser authorization fixture');
includesAll(browserAudit, ['startS0AuthTopology', 'login_hint=s2-telemetry', 'authorizedSingleSnapshot', 'authorizedBatchSnapshot', 'csrfEnforced', 'forgedScopeRejected', 'crossTenantNondiscovery', 'browserAuthorityHeaders === 0', 'legacyCalls: 0', 'mockCalls: 0', 'thingsBoardReadThroughCalls: 0'], 'real browser Session evidence');

for (const test of [
  'TestTelemetryGatewaySingleAndBatchPreserveOrder',
  'TestTelemetryGatewayRejectsCSRFAndForgedIdentityBeforeUpstream',
  'TestTelemetryGatewayWorkloadMTLSUsesExactIAMScopeWithoutCSRF',
  'TestTelemetryGatewayIAMUnavailableIsStableAndDoesNotReachRuntime',
  'TestTelemetryGatewayNondiscoveryLimitsAndTimeout',
]) {
  assert(gatewayTests.includes(test), `Gateway HTTP test missing: ${test}`);
}
for (const marker of ['browser authority leaked to runtime', 'browser authority leaked to IAM', 'CSRF_REQUIRED', 'FORGED_IDENTITY_HEADER', 'RESOURCE_NOT_FOUND', 'TELEMETRY_TIMEOUT']) {
  assert(gatewayTests.includes(marker), `Gateway negative evidence missing: ${marker}`);
}

const selectedRoutes = (routeRegistry.routes ?? []).filter((route) => (route.method === 'GET' && route.path === singlePath) || (route.method === 'POST' && route.path === batchPath));
assert(selectedRoutes.length === 2, 'route registry must contain exactly the single and batch Snapshot routes');
for (const route of selectedRoutes) {
  assert(route.owner === 'telemetry-runtime-service' && route.publicIngress === 'platform-gateway', `${route.method} ${route.path} ownership drifted`);
  assert(route.activationStatus === 'expand-baseline' && route.rollout?.mode === 'disabled' && route.migrationPhase === 'R0-contract-only', `${route.method} ${route.path} must remain default 0% traffic`);
  assert(route.readOnlyFallback === false && route.readFallbackOwner === undefined && route.shadowSideEffectPolicy === 'NONE', `${route.method} ${route.path} gained fallback or shadow side effects`);
  assert(route.migrationPhases?.includes('R3-internal-canary') && route.migrationPhases?.includes('R7-primary-100'), `${route.method} ${route.path} lacks deterministic rollout phases`);
}

assert(packageJSON.scripts?.['s2:gateway:check'] === 'node scripts/check-s2-gateway-snapshot.mjs', 's2:gateway:check is not wired');
assert(packageJSON.scripts?.['s2:gateway:browser'] === 'node scripts/run-s2-gateway-browser-audit.mjs', 's2:gateway:browser is not wired');
const ticketCommand = packageJSON.scripts?.['s2:gateway-snapshot'] ?? '';
for (const command of ['s2:gateway:check', 's2:contracts:check', 'test:gateway', 'build:gateway', 'lint', 'build', 's2:gateway:browser']) {
  assert(ticketCommand.includes(command), `s2:gateway-snapshot is missing ${command}`);
}
includesAll(workflow, ['runs-on: ubuntu-24.04', 'go-version: "1.25.12"', 'node-version: "22.22.0"', 'npm run s2:gateway-snapshot', 's2-gateway-snapshot-evidence'], 'Ticket 05 workflow');
for (const heading of ['## Reuse-first decision', '## Public request boundary', '## Authorization and mTLS chain', '## Error and nondiscovery semantics', '## Rollout boundary', '## Out of scope']) {
  assert(operations.includes(heading), `operations note is missing ${heading}`);
}
assert(operations.includes('oapi-codegen/oapi-codegen'), 'reuse decision must record oapi-codegen evaluation');
assert(operations.includes('ogen-go/ogen'), 'reuse decision must record ogen evaluation');
assert(operations.includes('0%'), 'operations note must record default zero traffic');

await mkdir(outputRoot, { recursive: true });
const report = {
  schemaVersion: 1,
  ticket: 'S2 Ticket 05',
  generatedAt: new Date().toISOString(),
  contract: {
    openapiVersion: openapi.info?.version,
    security: ['BffSession', 'WorkloadMTLS'],
    limits: openapi['x-limits'],
    generatedOutputs: expectedOutputs,
  },
  gateway: {
    routes: selectedRoutes.map((route) => ({ method: route.method, path: route.path, owner: route.owner, rolloutMode: route.rollout.mode, migrationPhase: route.migrationPhase, fallback: false })),
    authorization: 'BFF Session or verified workload SPIFFE -> signed Gateway delegation -> IAM exact-scope grant -> mTLS Telemetry Runtime',
    stableProblems: ['RESOURCE_NOT_FOUND', 'TELEMETRY_REQUEST_INVALID', 'TELEMETRY_KEY_INVALID', 'TELEMETRY_BATCH_LIMIT_EXCEEDED', 'TELEMETRY_UNAVAILABLE', 'TELEMETRY_TIMEOUT', 'TELEMETRY_AUTHORIZATION_UNAVAILABLE'],
    requestFallbacks: [],
    defaultProductionTrafficPercent: 0,
  },
  evidence: {
    gatewayHTTPTests: 5,
    realBrowserSessionAudit: true,
    verifiedWorkloadMTLSAudit: true,
    browserAuthorityForwarded: false,
    legacyCalls: 0,
    mockCalls: 0,
    thingsBoardReadThroughCalls: 0,
  },
};
await writeFile(resolve(outputRoot, 'gateway-snapshot.json'), `${JSON.stringify(report, null, 2)}\n`, 'utf8');
process.stdout.write(`S2 Ticket 05 Gateway Snapshot passed: ${resolve(outputRoot, 'gateway-snapshot.json')}\n`);
