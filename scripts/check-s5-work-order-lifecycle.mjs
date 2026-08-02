import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const [openapi, routes, data, model, store, postgres, serviceHTTP, gatewayRoute, gatewayMutation, gatewayLifecycle, auth, capabilities, migration, browser, workflow] = await Promise.all([
  readJSON('contracts/http/s5-work-order-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readText('libs/workordermodel/model.go'),
  readText('services/work-order-service/pkg/workorderservice/store.go'),
  readText('services/work-order-service/pkg/workorderservice/postgres_mutation.go'),
  readText('services/work-order-service/pkg/workorderservice/http.go'),
  readText('services/platform-gateway/internal/gateway/work_order.go'),
  readText('services/platform-gateway/internal/gateway/work_order_mutation.go'),
  readText('services/platform-gateway/internal/gateway/work_order_lifecycle.go'),
  readText('libs/workorderauth/authorization.go'),
  readText('libs/identitycontext/capabilities.go'),
  readText('services/work-order-service/migrations/003_s5_work_order_lifecycle.sql'),
  readText('scripts/run-s5-work-order-lifecycle-browser-audit.mjs'),
  readText('.github/workflows/s5-work-order-lifecycle.yml'),
]);
const actions = ['plan', 'start', 'block', 'resume', 'complete', 'cancel', 'reopen'];
const operations = ['OperationSchedule', 'OperationStart', 'OperationBlock', 'OperationResume', 'OperationComplete', 'OperationCancel', 'OperationReopen'];
for (const action of actions) {
  const path = '/api/v1/sites/{siteId}/work-orders/{workOrderId}:' + action;
  assert(openapi.paths?.[path]?.post?.['x-iam-action'] === 'work-order:' + action, 'public lifecycle contract lacks ' + action);
}
for (const forbidden of ['/notes', '/attachments', ':link-alarm', ':unlink-alarm', ':add-note', ':attach', ':open', ':draft']) {
  assert(!Object.keys(openapi.paths ?? {}).some((path) => path.includes(forbidden)), 'unreviewed Work Order route leaked: ' + forbidden);
}
const reviewedTaskPaths = new Set([
  '/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks',
  '/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks/{taskId}:status',
  '/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks:reorder',
]);
assert(Object.keys(openapi.paths ?? {}).filter((path) => path.includes('/tasks')).every((path) => reviewedTaskPaths.has(path)), 'unreviewed Work Order task route leaked');
const lifecycleRoutes = routes.routes.filter((route) => route.owner === 'work-order-service' && route.migrationPhase === 'S5-R1-internal-lifecycle');
assert(lifecycleRoutes.length === 7, 'expected exactly seven lifecycle routes');
assert(lifecycleRoutes.every((route) => route.method === 'POST' && route.rollout?.mode === 'percentage' && route.rollout?.percentage === 1 && route.rollout?.fallbackOwner === undefined && route.shadowSideEffectPolicy === 'NONE' && route.cohortGroup === 's5-work-order-lifecycle-v1'), 'lifecycle cohort is not stable no-fallback/no-shadow 1%');
assert(new Set(lifecycleRoutes.map((route) => route.rollout.cohortSalt)).size === 1, 'lifecycle routes do not share one cohort salt');
for (const scope of ['organization', 'site', 'principal', 'work-order', 'key']) assert(lifecycleRoutes.every((route) => route.allowedScopeDimensions?.includes(scope)), 'lifecycle scope missing ' + scope);
assert(data.databaseIdentities.some((identity) => identity.runtimeRole === 's5_work_order_mutation_service' && identity.restrictedTo?.includes('work_order_completion_evidence')), 'writer identity lacks completion evidence boundary');
for (const marker of ['ApplyLifecycle', 'ErrInvalidLifecycle', 'validLifecycleTransition', 'normalizeLifecycleEvidence', 'completed work order requires completion evidence and converged tasks']) assert(model.includes(marker), 'model lacks ' + marker);
for (const operation of operations) assert(model.includes(operation), 'model lacks lifecycle operation ' + operation);
for (const marker of ['Transition(', 'lifecycleMutationDigest', 'IdempotencyKey', 'ExpectedVersion']) assert(store.includes(marker), 'Store lacks ' + marker);
for (const marker of ['work_order_completion_evidence', 'completion_version', 'insertPostgresTimeline', 'insertPostgresMutationEvidence', 'mutationPool']) assert(postgres.includes(marker), 'PostgreSQL lifecycle owner lacks ' + marker);
for (const action of actions) assert(serviceHTTP.includes('work-order:' + action), 'service HTTP lacks action ' + action);
for (const marker of ['WorkOrderWriteContextHeader', 'mutationKeyScope', 'handleLifecycle', 'handleLifecyclePrecondition', 'last.Reason != reason', 'last.PolicyRevision', 'last.CorrelationID', 'CompletionEvidence']) assert(serviceHTTP.includes(marker), 'service lifecycle boundary lacks ' + marker);
for (const action of actions) assert(gatewayRoute.includes('workorderauth.Action' + action[0].toUpperCase() + action.slice(1)), 'Gateway route lacks action ' + action);
assert(gatewayMutation.includes('X-CSRF-Token'), 'Gateway mutation session boundary lacks CSRF enforcement');
for (const marker of ['workOrderMutationKeyScope', 'executeWorkOrderLifecyclePrecondition', 'validPublicLifecycleProjection', 'validPublicLifecycleChange', 'last.Reason != mutation.reason', 'last.PolicyRevision', 'last.CorrelationID', 'evidenceSuffixMatches', 'Idempotency-Key']) assert(gatewayLifecycle.includes(marker), 'Gateway lifecycle boundary lacks ' + marker);
assert(auth.includes('func MutationKeyScope') && auth.includes('return "key:"'), 'shared Work Order key scope contract is missing');
for (const action of actions) assert(auth.includes('Action' + action[0].toUpperCase() + action.slice(1)), 'authorization contract lacks ' + action);
assert(capabilities.includes('CapabilitySetVersion = 7') && capabilities.includes('CapabilityWorkOrderLifecycle'), 'capability v7 lifecycle/task contract is missing');
assert(migration.includes("'LIFECYCLE'") && migration.includes('completion_version') && migration.includes('GRANT UPDATE (status, scheduled_start, due_at, version, updated_at)') && migration.includes('GRANT INSERT ON work_order_runtime.work_order_completion_evidence') && !migration.includes('GRANT DELETE') && !migration.includes('GRANT ALL'), 'lifecycle SQL authority is incomplete or broad');
for (const marker of ['legal-lifecycle-graph', 'exact-idempotent-retry', 'cross-action-idempotency-conflict', 'illegal-transition', 'stale-version-conflict', 'missing-completion-evidence', 'authorization-denial-no-data', 'non-selected-session-route-absence', 'cross-site-nondiscovery', 'session-loss-purge', 'unreviewed-collaboration-absence', 'public-gateway-lifecycle-only']) assert(browser.includes(marker), 'browser audit lacks ' + marker);
assert(workflow.includes('npm run s5:work-order:lifecycle') && workflow.includes('npm run contracts:check'), 'lifecycle workflow omits required gates');
for (const forbidden of ["- 'package.json'", "- 'package-lock.json'", "- 'go.work'", "- 'go.work.sum'"]) assert(!workflow.includes(forbidden), 'lifecycle workflow watches broad root manifest ' + forbidden);
console.log('S5 Work Order governed lifecycle assets passed.');
