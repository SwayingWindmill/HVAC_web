import { readFile } from 'node:fs/promises';

const publicContractPath = 'contracts/http/operations-investigations-public.openapi.yaml';
const internalContractPath = 'contracts/http/operations-agent-internal.openapi.yaml';
const toolAuthorizationContractPath = 'contracts/http/operations-tool-authorization-internal.openapi.yaml';
const ownershipPath = 'contracts/ownership/route-ownership.v1.json';
const serviceHandlerPath = 'services/operations-agent-service/src/transport-http/internal/operations-agent-http.ts';
const gatewayPath = 'services/platform-gateway/internal/gateway/operations_agent.go';
const iamPath = 'services/iam-service/internal/iam/server.go';
const registryAuthPath = 'libs/registryauth/registry.go';
const corePath = 'services/platform-core-service/internal/core/server.go';
const queryPath = 'services/telemetry-query-service/internal/query/server.go';

const [
  publicContract,
  internalContract,
  toolAuthorizationContract,
  ownershipSource,
  serviceHandler,
  gateway,
  iam,
  registryAuth,
  core,
  query,
] = await Promise.all([
  readFile(publicContractPath, 'utf8'),
  readFile(internalContractPath, 'utf8'),
  readFile(toolAuthorizationContractPath, 'utf8'),
  readFile(ownershipPath, 'utf8'),
  readFile(serviceHandlerPath, 'utf8'),
  readFile(gatewayPath, 'utf8'),
  readFile(iamPath, 'utf8'),
  readFile(registryAuthPath, 'utf8'),
  readFile(corePath, 'utf8'),
  readFile(queryPath, 'utf8'),
]);
const ownership = JSON.parse(ownershipSource);
const failures = [];

const publicRoutes = [
  ['POST', '/api/v1/sites/{siteId}/operations/investigations'],
  ['GET', '/api/v1/sites/{siteId}/operations/investigations/{investigationId}'],
  ['GET', '/api/v1/sites/{siteId}/operations/investigations/{investigationId}/events'],
  ['POST', '/api/v1/sites/{siteId}/operations/investigations/{investigationId}:advance'],
  ['POST', '/api/v1/sites/{siteId}/operations/investigations/{investigationId}:cancel'],
];
const internalRoutes = [
  '/internal/v1/sites/{siteId}/operations/investigations',
  '/internal/v1/sites/{siteId}/operations/investigations/{investigationId}',
  '/internal/v1/sites/{siteId}/operations/investigations/{investigationId}/events',
  '/internal/v1/sites/{siteId}/operations/investigations/{investigationId}:advance',
  '/internal/v1/sites/{siteId}/operations/investigations/{investigationId}:cancel',
];

for (const [method, path] of publicRoutes) {
  if (!publicContract.includes(`  ${path}:`)) failures.push(`Public OpenAPI is missing ${method} ${path}.`);
  const route = ownership.routes.find((candidate) => candidate.method === method && candidate.path === path);
  if (route?.owner !== 'operations-agent-service' || route.publicIngress !== 'platform-gateway') {
    failures.push(`Route Ownership does not assign ${method} ${path} to Operations Agent through Gateway.`);
  }
}
for (const path of internalRoutes) {
  if (!internalContract.includes(`  ${path}:`)) failures.push(`Internal OpenAPI is missing ${path}.`);
}
for (const required of [
  'X-CSRF-Token',
  'SiteNightEnergyInvestigationView',
  'SUPPORTED_SITE_FINDING',
  'UNABLE_TO_CONCLUDE',
  'TOOL_EXECUTION_RECEIPT',
  'text/event-stream',
  'RUN_STARTED',
  'STATE_SNAPSHOT',
  'RUN_FINISHED',
]) {
  if (!publicContract.includes(required)) failures.push(`Public OpenAPI is missing ${required}.`);
}
for (const required of [
  'X-Acting-Organization-ID',
  'X-Delegation-Grant',
  'X-Route-Policy-Revision',
  'Cache-Control',
]) {
  if (!internalContract.includes(required)) failures.push(`Internal OpenAPI is missing ${required}.`);
}
for (const required of [
  '/internal/v1/operations/tool-authorization',
  'type: mutualTLS',
  'X-Request-ID',
  'application/json',
  'registry.getSite',
  'registry.listSiteEquipment',
  'analytics.getEnergySeries',
  'additionalProperties: false',
  'delegationGrant',
  'policyRevision',
  "'415':",
]) {
  if (!toolAuthorizationContract.includes(required)) {
    failures.push(`Tool Authorization OpenAPI is missing ${required}.`);
  }
}
for (const forbidden of ['LangGraph', 'opaqueState', 'runtimeRevision', 'providerMessage', 'points:']) {
  if (publicContract.includes(forbidden)
    || internalContract.includes(forbidden)
    || toolAuthorizationContract.includes(forbidden)) {
    failures.push(`HTTP contracts expose forbidden runtime or raw-series field ${forbidden}.`);
  }
}
for (const required of [
  'maximumRequestBytes',
  'AUTHORIZATION_DENIED',
  'RESOURCE_NOT_FOUND',
  'streamPattern',
  'createAgUiEventStreamResponse',
]) {
  if (!serviceHandler.includes(required)) failures.push(`Internal HTTP handler is missing ${required}.`);
}
for (const required of [
  'commandSession',
  'authorizeRegistryForPresenter',
  'authorizeAnalyticsForPresenter',
  'authorizeOperationsTool',
  'decodeStrictOperationsJSON',
  'ValidateDelegationAnyScope',
  'Content-Type',
  'context.WithTimeout',
  'RateLimitPerMinute',
  'validateOperationsSnapshot',
  'validateOperationsEventStream',
  'text/event-stream',
  'no-store, no-transform',
  'X-Accel-Buffering',
]) {
  if (!gateway.includes(required)) failures.push(`Gateway implementation is missing ${required}.`);
}
for (const required of [
  'AllowedRegistryGrantPresenters',
  'GrantPresenter',
  'IAM_REGISTRY_GRANT_PRESENTER_REJECTED',
]) {
  if (!iam.includes(required)) failures.push(`IAM presenter authorization is missing ${required}.`);
}
for (const required of [
  'allowedPresenterSPIFFEs',
  'Presenter:             peerSPIFFE',
  'registryauth.ValidateGrant',
]) {
  if (!core.includes(required)) failures.push(`Core presenter enforcement is missing ${required}.`);
}
if (!registryAuth.includes('if claims.Transitive')) {
  failures.push('Registry grant validation no longer rejects transitive grants.');
}
for (const required of [
  'delegationIssuerSPIFFE',
  'allowedPresenterSPIFFEs',
  'ValidateDelegationFromIssuer',
]) {
  if (!query.includes(required)) failures.push(`Query presenter enforcement is missing ${required}.`);
}
for (const forbidden of ['ClickHouse', 'Cube', 'ThingsBoard', 'Command Intent', 'physical execution']) {
  if (serviceHandler.includes(forbidden) || gateway.includes(forbidden)) {
    failures.push(`Operations HTTP path contains forbidden direct-path concept ${forbidden}.`);
  }
}

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exitCode = 1;
} else {
  console.log('Operations Agent internal and Gateway public contracts passed.');
}
