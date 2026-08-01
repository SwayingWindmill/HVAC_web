import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const [packageRaw, routeRaw, dataRaw, openapi, capabilities, ownership, workOrderAuth, iamMainServer, iamServer, iamAuthorization, iamPostgres, gateway, gatewayTests, sql, browser, workflow, postgresWorkflow, postgresChecker, releaseEvidenceChecker] = await Promise.all([
  read('package.json'), read('contracts/ownership/route-ownership.v1.json'), read('contracts/ownership/data-ownership.v1.json'),
  read('contracts/http/platform-gateway.openapi.yaml'), read('libs/identitycontext/capabilities.go'),
  read('libs/ownershipregistry/registry.go'), read('libs/workorderauth/authorization.go'),
  read('services/iam-service/internal/iam/server.go'), read('services/iam-service/internal/iam/work_order_server.go'), read('services/iam-service/internal/iam/work_order_authorization.go'),
  read('services/iam-service/internal/iam/postgres_work_order_authorization.go'), read('services/platform-gateway/internal/gateway/work_order.go'),
  read('services/platform-gateway/internal/gateway/work_order_test.go'), read('infra/s1-registry/postgres/init/008-s5-work-order-authorization.sql'),
  read('scripts/run-s5-work-order-read-browser-audit.mjs'), read('.github/workflows/s5-work-order-read-canary.yml'),
  read('.github/workflows/s5-work-order-postgres.yml'), read('scripts/check-s5-work-order-postgres.mjs'), read('scripts/check-s0-release-evidence-assets.mjs'),
]);
const packageJSON = JSON.parse(packageRaw);
const registry = JSON.parse(routeRaw);
const dataRegistry = JSON.parse(dataRaw);
assert(typeof packageJSON.scripts?.['s5:work-order:read-canary:check'] === 'string', 'package.json is missing s5:work-order:read-canary:check');
assert(typeof packageJSON.scripts?.['s5:work-order:read-canary:browser'] === 'string', 'package.json is missing s5:work-order:read-canary:browser');
assert(typeof packageJSON.scripts?.['s5:work-order:read-canary'] === 'string', 'package.json is missing s5:work-order:read-canary');
const routes = registry.routes.filter((route) => route.owner === 'work-order-service' && route.method === 'GET');
assert(routes.length === 2, 'Work Order must expose exactly the list/detail GET pair');
for (const route of routes) {
  assert(route.method === 'GET', `${route.path} is not GET-only`);
  assert(route.publicIngress === 'platform-gateway', `${route.path} bypasses Gateway`);
  assert(route.revision === 2 && route.migrationPhase === 'S5-R1-internal-read-only', `${route.path} is outside S5 R1`);
  assert(route.activationStatus === 'internal-canary' && route.rollout?.mode === 'percentage' && route.rollout?.percentage === 1, `${route.path} is not the exact 1% read canary`);
  assert(route.rollout?.cohortSalt === 's5-work-order-read-canary-v1' && route.cohortGroup === 's5-work-order-read-v1', `${route.path} changed cohort identity`);
  assert(!route.rollout?.fallbackOwner && !route.readFallbackOwner && route.shadowSideEffectPolicy === 'NONE', `${route.path} gained fallback or shadow behavior`);
}
const mutations = registry.routes.filter((route) => route.owner === 'work-order-service' && route.migrationPhase === 'S5-R1-internal-create-assign');
assert(mutations.length === 2 && mutations.every((route) => route.method === 'POST'), 'Work Order exposes an undeclared non-read route');
assert(mutations.some((route) => route.path === '/api/v1/sites/{siteId}/work-orders') && mutations.some((route) => route.path === '/api/v1/sites/{siteId}/work-orders/{workOrderId}:assign'), 'Work Order create/assign route pair is incomplete');
assert(mutations.every((route) => route.migrationPhase === 'S5-R1-internal-create-assign' && route.cohortGroup === 's5-work-order-write-v1' && route.rollout?.percentage === 1 && !route.rollout?.fallbackOwner), 'Work Order mutation routes escaped their governed 1% no-fallback cohort');
for (const name of ['iam-work-order-permission', 'iam-work-order-authorization-decision']) {
  assert(dataRegistry.resources.some((resource) => resource.kind === 'projection' && resource.name === name && resource.writer === 'iam-service' && resource.revision === 1), 'IAM ownership projection is missing: ' + name);
}
assert(openapi.includes('"const": 6') && openapi.includes('"work-order.list"') && openapi.includes('"work-order.read"') && openapi.includes('"work-order.create"') && openapi.includes('"work-order.assign"') && openapi.includes('"maxItems": 19'), 'public capability v6 contract is incomplete');
for (const marker of ['CapabilitySetVersion = 6', 'CapabilityWorkOrderList', 'CapabilityWorkOrderRead', 'CapabilityWorkOrderCreate', 'CapabilityWorkOrderAssign', 'CapabilityWorkOrderLifecycle']) assert(capabilities.includes(marker), `identity capability contract is missing ${marker}`);
for (const marker of ['PhaseS5InternalReadOnly', 's5-work-order-read-v1', 'Percentage != 1']) assert(ownership.includes(marker), `ownership validator is missing ${marker}`);
for (const marker of ['ActionList', 'ActionRead', 'ReasonDenyExplicit', 'ALLOW_EXACT_SCOPE']) assert(workOrderAuth.includes(marker), `Work Order authorization contract is missing ${marker}`);
for (const marker of ['WorkOrderDecisionPath', 'work-order:authorize', 'handleWorkOrderDecision']) assert(iamMainServer.includes(marker), 'IAM Work Order route is missing ' + marker);
for (const marker of ['handleWorkOrderDecision', 'RecordWorkOrderDecision']) assert(iamServer.includes(marker), 'IAM Work Order handler is missing ' + marker);
for (const marker of ['BindingEffectDeny', 'ReasonDenyMembership', 'ReasonDenyExplicit', 'ReasonDenyScope', 'ReasonAllowExactScope']) assert(iamAuthorization.includes(marker), `IAM Work Order evaluator is missing ${marker}`);
for (const marker of ['iam.work_order_permissions', 'iam.work_order_authorization_decisions', 'setIAMAuthorizationContext']) assert(iamPostgres.includes(marker), `IAM PostgreSQL owner is missing ${marker}`);
for (const marker of ['X-Work-Order-Read-Context', 'work-order:authorize', 'work-order-service', 'SignDelegation', '/internal/v1/sites/']) assert(gateway.includes(marker), `Gateway Work Order boundary is missing ${marker}`);
for (const marker of ['ExactSignedReadContext', 'RejectsCrossSiteProjection', 'RejectsBrowserWorkOrderAuthorityHeaders']) assert(gatewayTests.includes(marker), `Gateway Work Order tests are missing ${marker}`);
for (const marker of ['FORCE ROW LEVEL SECURITY', 'work-order:list', 'work-order:read', 'DENY_EXPLICIT']) assert(sql.includes(marker), `Work Order IAM migration is missing ${marker}`);
assert(browser.includes('public-gateway-get-only') && browser.includes('authorization-denial-no-data') && browser.includes('non-selected-session-route-absence') && browser.includes('session-loss-purge'), 'Work Order browser evidence is incomplete');
assert(workflow.includes('npm run s5:work-order:read-canary') && workflow.includes('npm run s1:registry:postgres'), 'Work Order read canary workflow omits required gates');
assert(!workflow.includes("- 'package.json'") && !workflow.includes("- 'package-lock.json'") && !workflow.includes("- 'go.work'") && !workflow.includes("- 'go.work.sum'"), 'S5 Read Canary workflow directly watches a broad root manifest');
assert((postgresWorkflow.match(/^\s+- 'go\.work'$/gm) ?? []).length === 2 && (postgresWorkflow.match(/^\s+- 'go\.work\.sum'$/gm) ?? []).length === 2, 'P2 PostgreSQL workflow lost root Go workspace triggers');
assert(postgresChecker.includes('goWorkTriggers === 2 && goWorkSumTriggers === 2'), 'P2 checker no longer enforces root Go workspace triggers');
for (const name of ['iam-work-order-permission', 'iam-work-order-authorization-decision']) assert(releaseEvidenceChecker.includes(name), 'S0 release evidence checker is missing ' + name);
console.log('S5 Work Order exact IAM/Gateway 1% read canary assets passed.');
