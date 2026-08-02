import { readFile } from 'node:fs/promises';

const readText = (path) => readFile(path, 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const [openapi, routes, data, model, tasks, store, postgres, http, gatewayBase, gatewayMutation, gatewayTasks, auth, capabilities, webPrincipalTemplate, webPrincipalGenerated, iamSQL, migration, browser, workflow, packageJSON, matrix] = await Promise.all([
  readText('contracts/http/s5-work-order-public.openapi.json'),
  readText('contracts/ownership/route-ownership.v1.json'),
  readText('contracts/ownership/data-ownership.v1.json'),
  readText('libs/workordermodel/model.go'),
  readText('libs/workordermodel/tasks.go'),
  readText('services/work-order-service/pkg/workorderservice/tasks_store.go'),
  readText('services/work-order-service/pkg/workorderservice/postgres_tasks.go'),
  readText('services/work-order-service/pkg/workorderservice/http_tasks.go'),
  readText('services/platform-gateway/internal/gateway/work_order.go'),
  readText('services/platform-gateway/internal/gateway/work_order_mutation.go'),
  readText('services/platform-gateway/internal/gateway/work_order_tasks.go'),
  readText('libs/workorderauth/authorization.go'),
  readText('libs/identitycontext/capabilities.go'),
  readText('contracts/http/templates/platformGateway.ts.tmpl'),
  readText('apps/hvac-web/src/api/generated/platformGateway.gen.ts'),
  readText('infra/s1-registry/postgres/init/009-s5-work-order-task-authorization.sql'),
  readText('services/work-order-service/migrations/004_s5_work_order_task_checklist.sql'),
  readText('scripts/run-s5-work-order-task-browser-audit.mjs'),
  readText('.github/workflows/s5-work-order-tasks.yml'),
  readText('package.json'),
  readText('scripts/domain-task-matrix.mjs'),
]);

const spec = JSON.parse(openapi);
const registry = JSON.parse(routes);
const ownership = JSON.parse(data);
const packageScripts = JSON.parse(packageJSON).scripts ?? {};
const gateway = gatewayBase + '\n' + gatewayMutation + '\n' + gatewayTasks;
const taskPaths = [
  '/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks',
  '/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks/{taskId}:status',
  '/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks:reorder',
];
assert(spec.info.version === '0.3.0-governed-task-checklist', 'P6 Work Order task contract version drifted');
for (const path of taskPaths) assert(spec.paths[path], `P6 Work Order task path missing: ${path}`);
assert(spec.paths[taskPaths[0]].get?.['x-iam-action'] === 'work-order:task:list' && spec.paths[taskPaths[0]].post?.['x-iam-action'] === 'work-order:task:append', 'P6 task collection actions drifted');
assert(spec.paths[taskPaths[1]].post?.['x-iam-action'] === 'work-order:task:status' && spec.paths[taskPaths[2]].post?.['x-iam-action'] === 'work-order:task:reorder', 'P6 task mutation actions drifted');
for (const schema of ['WorkOrderTask', 'WorkOrderTaskChecklist', 'AppendWorkOrderTaskRequest', 'SetWorkOrderTaskStatusRequest', 'ReorderWorkOrderTasksRequest']) assert(spec.components.schemas[schema]?.additionalProperties === false, `P6 schema ${schema} is not closed`);
assert(spec.components.schemas.ReorderWorkOrderTasksRequest.properties.taskIds.uniqueItems === true && spec.components.schemas.WorkOrderTask.properties.position.maximum === 511, 'P6 task order contract is not bounded and exact');
assert(!Object.keys(spec.paths).some((path) => /tasks.*:(delete|title)/.test(path)), 'P6 exposes unreviewed task delete or title edit');

const expectedRoutes = new Map([
  [`GET ${taskPaths[0]}`, ['organization', 'site', 'principal', 'work-order']],
  [`POST ${taskPaths[0]}`, ['organization', 'site', 'principal', 'work-order', 'key']],
  [`POST ${taskPaths[1]}`, ['organization', 'site', 'principal', 'work-order', 'task', 'key']],
  [`POST ${taskPaths[2]}`, ['organization', 'site', 'principal', 'work-order', 'key']],
]);
for (const [key, scopes] of expectedRoutes) {
  const route = registry.routes.find((candidate) => `${candidate.method} ${candidate.path}` === key);
  assert(route?.owner === 'work-order-service' && route?.publicIngress === 'platform-gateway', `${key}: owner or ingress drifted`);
  assert(route?.activationStatus === 'internal-canary' && route?.rollout?.mode === 'percentage' && route?.rollout?.percentage === 1 && !route?.rollout?.fallbackOwner, `${key}: no-fallback 1% canary drifted`);
  assert(route?.migrationPhase === 'S5-R1-internal-task-checklist' && route?.cohortGroup === 's5-work-order-task-v1' && route?.readOnlyFallback === false && route?.shadowSideEffectPolicy === 'NONE', `${key}: P6 task governance drifted`);
  assert(scopes.every((scope) => route.allowedScopeDimensions.includes(scope)), `${key}: exact scope dimensions are incomplete`);
}
assert(ownership.databaseAccess.some((entry) => entry.service === 'work-order-service' && entry.restrictedTo.includes('work_order_task')), 'P6 data ownership omits work_order_task');

for (const marker of ['OperationTaskAppend', 'OperationTaskStatus', 'OperationTaskReorder', 'TaskID']) assert(model.includes(marker), `P6 model marker missing: ${marker}`);
for (const marker of ['ApplyTaskAppend', 'ApplyTaskStatus', 'ApplyTaskReorder', 'ExpectedTaskVersion', 'len(input.TaskIDs) != len(tasks)', 'workOrder.Status != StatusOpen && workOrder.Status != StatusInProgress && workOrder.Status != StatusBlocked']) assert(tasks.includes(marker), `P6 task graph marker missing: ${marker}`);
for (const marker of ['taskIdempotencyOperation = "TASK"', 'ListTasks', 'AppendTask', 'SetTaskStatus', 'ReorderTasks', 'Replayed: true']) assert(store.includes(marker), `P6 memory store marker missing: ${marker}`);
for (const marker of ['postgresTaskOperation', 'loadPostgresTasks', 'work_order_task', 'position = position + 1000000', 'readPostgresTaskReplay']) assert(postgres.includes(marker), `P6 PostgreSQL marker missing: ${marker}`);
for (const marker of ['WorkOrderTaskListAction', 'WorkOrderTaskAppendAction', 'WorkOrderTaskStatusAction', 'WorkOrderTaskReorderAction', 'sameTaskOrder']) assert(http.includes(marker), `P6 internal HTTP marker missing: ${marker}`);
for (const marker of ['matchPublicWorkOrderTaskRoute', 'dispatchWorkOrderTaskRoute', 'publicWorkOrderTaskStatus', 'gatewayTaskOrderMatches', '"task:"+route.taskID']) assert(gateway.includes(marker), `P6 Gateway marker missing: ${marker}`);
for (const marker of ['ActionTaskList', 'ActionTaskAppend', 'ActionTaskStatus', 'ActionTaskReorder', 'TaskID']) assert(auth.includes(marker), `P6 authorization marker missing: ${marker}`);
assert(capabilities.includes('CapabilitySetVersion = 7') && capabilities.includes('CapabilityWorkOrderTask'), 'P6 capability v7 contract is missing');
for (const webPrincipal of [webPrincipalTemplate, webPrincipalGenerated]) {
  assert(webPrincipal.includes("'work-order.task'") && webPrincipal.includes('capabilitySetVersion: z.literal(7)') && webPrincipal.includes('z.array(capabilitySchema).max(20)'), 'P6 Web Principal contract is not aligned to capability set v7');
}

for (const marker of ['task_id uuid', 'work-order:task:list', 'work-order:task:append', 'work-order:task:status', 'work-order:task:reorder']) assert(iamSQL.includes(marker), `P6 IAM migration marker missing: ${marker}`);
for (const marker of ['GRANT INSERT ON work_order_runtime.work_order_task', 'GRANT UPDATE (position, status, version, updated_at)', 'DROP POLICY IF EXISTS work_order_task_writer_insert_org', 'DROP POLICY IF EXISTS work_order_task_writer_update_org']) assert(migration.includes(marker), `P6 task migration marker missing: ${marker}`);
assert(!migration.includes('GRANT DELETE') && !migration.includes('UPDATE (title'), 'P6 task migration grants delete or title edit');

for (const marker of ['exact-snapshot-replay', 'dual-version-conflict', 'unified-task-idempotency-domain', 'exact-full-permutation', 'authorization-denial-no-data', 'non-selected-route-absence', 'unreviewedTitleEdit', 'delete-and-title-edit-absence']) assert(browser.includes(marker), `P6 browser audit marker missing: ${marker}`);
assert(workflow.includes('runs-on: ubuntu-latest') && workflow.includes('npm run s5:work-order:tasks') && workflow.includes('npm run contracts:check'), 'P6 workflow omits Linux, contract or capability gate');
for (const forbidden of ["- 'package.json'", "- 'package-lock.json'", "- 'go.work'", "- 'go.work.sum'"]) assert(!workflow.includes(forbidden), `P6 workflow watches broad root manifest ${forbidden}`);
assert(packageScripts['s5:work-order:tasks:check'] && packageScripts['s5:work-order:tasks:browser'] && packageScripts['s5:work-order:tasks'], 'P6 package task entry points are missing');
assert(matrix.includes("'s5:work-order:tasks'"), 'P6 domain task matrix entry is missing');

console.log('S5 Work Order governed task checklist assets passed.');
