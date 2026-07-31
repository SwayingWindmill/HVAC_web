import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const [
  plan,
  routes,
  ownership,
  openapi,
  commandApi,
  commandContract,
  realCommands,
  commandPage,
  permissions,
  app,
  sidebar,
  gateway,
  gatewayTests,
  commandHTTP,
  commandHTTPTests,
] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/s3-command-ownership.v1.json'),
  readJSON('contracts/http/s3-command-public.openapi.json'),
  read('apps/hvac-web/src/api/commands.ts'),
  read('apps/hvac-web/src/api/command-contract.ts'),
  read('apps/hvac-web/src/real/RealCommands.tsx'),
  read('apps/hvac-web/src/pages/Commands/index.tsx'),
  read('apps/hvac-web/src/auth/permissions.ts'),
  read('apps/hvac-web/src/App.tsx'),
  read('apps/hvac-web/src/layout/Sidebar.tsx'),
  read('services/platform-gateway/internal/gateway/command.go'),
  read('services/platform-gateway/internal/gateway/command_test.go'),
  read('services/command-service/pkg/commandservice/http.go'),
  read('services/command-service/pkg/commandservice/http_test.go'),
]);

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(plan.completedTickets?.includes('S3-08'), 'S3-08 is not marked complete');
assert(JSON.stringify(plan.currentFrontier) === JSON.stringify(['S3-09']), 'S3 frontier must advance to S3-09');
assert(plan.productionTrafficPercent === 0, 'S3-08 enabled production traffic');
assert(plan.firstTracerBullet?.publicRoutesEnabled === false, 'Command public routes enabled during S3-08');
assert(plan.firstTracerBullet?.productionProviderEnabled === false, 'Production provider enabled during S3-08');

const commandRoutes = (routes.routes ?? []).filter((route) => route.owner === 'command-service');
for (const route of commandRoutes) {
  assert(route.rollout?.mode === 'disabled', `${route.method} ${route.path} is not disabled`);
  assert(route.shadowSideEffectPolicy === 'SYNTHETIC_ONLY', `${route.method} ${route.path} is not Synthetic-only`);
  assert(route.readOnlyFallback === false, `${route.method} ${route.path} allows fallback`);
}
for (const [method, path] of [
  ['POST', '/api/v1/commands'],
  ['GET', '/api/v1/commands/{commandId}'],
  ['POST', '/api/v1/commands/{commandId}:approve'],
]) {
  assert(commandRoutes.some((route) => route.method === method && route.path === path), `Command route is missing: ${method} ${path}`);
  assert(ownership.routes?.some((route) => route.method === method && route.path === path && route.rollout === 'disabled'), `Command ownership is missing: ${method} ${path}`);
}

assert(openapi.info?.version === '0.4.0-disabled-real-site-scope', 'Command OpenAPI version is not the Real Site scope baseline');
const approvalOperation = openapi.paths?.['/api/v1/commands/{commandId}:approve']?.post;
assert(approvalOperation?.['x-production-traffic-percent'] === 0, 'Approval OpenAPI enabled production traffic');
assert(openapi.components?.schemas?.ApproveCommandRequest?.maxProperties === 0, 'Public approval request is not empty by contract');
for (const forbidden of ['organizationId', 'siteId', 'deviceId', 'principalId', 'approverRole', 'payloadHash', 'risk', 'riskRuleRevision', 'providerMethod', 'providerParams']) {
  assert(approvalOperation?.['x-client-forbidden-fields']?.includes(forbidden), `Approval OpenAPI no longer forbids ${forbidden}`);
}
for (const field of ['organizationId', 'siteId', 'requiredApprovalCount', 'setpointC', 'transitions']) {
  assert(openapi.components?.schemas?.Command?.required?.includes(field), `Command detail is missing required ${field}`);
}
assert(openapi.components?.schemas?.CommandTransition?.properties?.actorType?.enum?.join('|') === 'PRINCIPAL|WORKLOAD', 'Timeline exposes unsupported actor identity');

for (const token of [
  'COMMAND_PUBLIC_ROUTES_ENABLED = false as const',
  'createScopedCommand',
  'getScopedCommand',
  'approveScopedCommand',
  'trustedOrganizationId',
  'trustedSiteId',
  "capability: 'SET_TEMPERATURE_SETPOINT'",
  'parameters: { setpointC }',
  'body: JSON.stringify({})',
  'Command 控制路由已登记，但尚未启用生产流量',
]) {
  assert(commandApi.includes(token), `HVAC Web Command API invariant is missing: ${token}`);
}
for (const token of ['organizationId', 'siteId', 'superRefine', 'Command timeline does not converge', 'validateCommandScope', 'RESOURCE_NOT_FOUND']) {
  assert(commandContract.includes(token), `HVAC Web Command contract invariant is missing: ${token}`);
}
for (const forbidden of ['principalId', 'approverRole', 'providerMethod', 'providerParams']) {
  assert(!commandApi.includes(forbidden), `HVAC Web public Command client contains forbidden authority field ${forbidden}`);
}

for (const token of [
  '生产控制保持禁用',
  '生产流量为 0%',
  'LOCAL / NON-FORMAL / PRODUCTION DISABLED',
  '不表示设备已经成功执行',
  'S2 Snapshot Revision',
  '状态时间线',
  '批准 Command',
]) {
  assert(realCommands.includes(token), `Real Command UX invariant is missing: ${token}`);
}
for (const token of [
  '设备结果待确认',
  '不会自动重发',
  "can(role, 'create', 'command')",
  "can(role, 'approve', 'command')",
  'PRODUCTION DISABLED',
  'Route Ownership Registry 仍为 disabled',
  'S2 Snapshot Revision',
  '状态时间线',
]) {
  assert(commandPage.includes(token), `Demo Command UX invariant is missing: ${token}`);
}
assert(permissions.includes("| 'commands'") && permissions.includes("| 'command'"), 'Command permission subjects are missing');
assert(permissions.includes("{ actions: ['approve'], subjects: ['command'] }") || permissions.includes("{ actions: ['create', 'approve'], subjects: ['command'] }"), 'Command approval permission is missing');
assert(app.includes("'/commands': Commands") && app.includes('path="/commands/:commandId"'), 'Command routes are not registered in HVAC Web');
assert(sidebar.includes("'/commands'") && sidebar.includes('ControlOutlined'), 'Command navigation is not registered');

for (const token of [
  'commandRouteApproval',
  'approveCommand',
  'trustedCommandApproverRole',
  'AuthorizationCommandApprove',
  'The public approval request must be an empty JSON object',
  'validCommandTimeline',
]) {
  assert(gateway.includes(token), `Gateway Command UX invariant is missing: ${token}`);
}
for (const test of [
  'TestGatewayApproveCommandDerivesIdentityRoleAndExactGrant',
  'TestGatewayApprovalRejectsBrowserAuthorityBeforeUpstreams',
  'TestGatewayApprovalRequiresCSRFBeforeCommandRead',
]) {
  assert(gatewayTests.includes(test), `Gateway approval test is missing: ${test}`);
}
for (const token of ['internalApproveCommandRequest', 'AuthorizationCommandApprove', 'RequiredApprovalCount', 'CommandTransitionView']) {
  assert(commandHTTP.includes(token), `Command Service UX invariant is missing: ${token}`);
}
assert(commandHTTPTests.includes('TestCommandHTTPApprovalUsesExactGrantAndServerDerivedEvidence'), 'Command Service approval test is missing');

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log('S3 Command UX checks passed: canonical submit, Session-derived approval, strict timeline projection, disabled production routes and no provider-specific browser authority.');
