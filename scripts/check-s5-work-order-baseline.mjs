import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const [openapi, routes, data, lock, model, migration] = await Promise.all([
  readJSON('contracts/http/s5-work-order-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readText('libs/workordermodel/model.go'),
  readText('services/work-order-service/migrations/001_s5_work_order_runtime.sql'),
]);

assert(openapi.openapi === '3.1.0' && openapi.info?.version === '0.1.0-contract-only', 'Work Order OpenAPI baseline is invalid');
const listPath = '/api/v1/sites/{siteId}/work-orders';
const detailPath = '/api/v1/sites/{siteId}/work-orders/{workOrderId}';
assert(Object.keys(openapi.paths ?? {}).length === 2, 'Work Order contract exposes undeclared routes');
for (const [path, action] of [[listPath, 'work-order:list'], [detailPath, 'work-order:read']]) {
  const pathItem = openapi.paths?.[path];
  assert(pathItem?.get && Object.keys(pathItem).every((method) => method === 'get'), `Work Order ${path} is not GET-only`);
  const operation = pathItem.get;
  assert(operation['x-owner'] === 'work-order-service' && operation['x-public-ingress'] === 'platform-gateway', `Work Order owner or ingress is invalid for ${path}`);
  assert(operation['x-production-traffic-percent'] === 0 && operation['x-migration-phase'] === 'S5-R0-contract-only', `Work Order route ${path} is not contract-only at 0%`);
  assert(operation['x-iam-action'] === action && operation['x-no-fallback'] === true, `Work Order action or no-fallback marker is invalid for ${path}`);
  for (const status of ['200', '401', '403', '404', '503']) assert(operation.responses?.[status], `Work Order response ${status} is missing for ${path}`);

  const route = routes.routes?.find((entry) => entry.method === 'GET' && entry.path === path);
  assert(route?.owner === 'work-order-service' && route?.publicIngress === 'platform-gateway', `Route Ownership Registry is missing ${path}`);
  assert(route?.revision === 1 && route?.activationStatus === 'expand-baseline' && route?.rollout?.mode === 'disabled', `Work Order route ${path} is not disabled revision 1`);
  assert(route?.migrationPhase === 'S5-R0-contract-only' && route?.cohortGroup === 's5-work-order-read-v1', `Work Order route ${path} has the wrong S5 phase or group`);
  assert(route?.readOnlyFallback === false && route?.readFallbackOwner === undefined && route?.rollout?.fallbackOwner === undefined, `Work Order route ${path} permits fallback`);
  assert(route?.shadowSideEffectPolicy === 'NONE', `Work Order route ${path} is not side-effect-free`);
  for (const scope of ['organization', 'site', 'principal']) assert(route?.allowedScopeDimensions?.includes(scope), `Work Order route ${path} lacks scope ${scope}`);
  if (path === detailPath) assert(route?.allowedScopeDimensions?.includes('work-order'), 'Work Order detail lacks identity scope');
  for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND']) assert(route?.fallbackForbiddenResults?.includes(result), `Work Order route ${path} does not forbid fallback for ${result}`);
  assert(lock.routes?.[`GET ${path}`]?.owner === 'work-order-service' && lock.routes?.[`GET ${path}`]?.revision === 1, `Work Order compatibility lock is missing ${path}`);
}

const schema = openapi.components?.schemas?.WorkOrder;
for (const field of ['workOrderId', 'organizationId', 'siteId', 'title', 'description', 'priority', 'status', 'sourceReferences', 'tasks', 'noteCount', 'attachmentCount', 'completionEvidence', 'timeline', 'version', 'createdAt', 'updatedAt']) {
  assert(schema?.required?.includes(field), `Work Order contract does not require ${field}`);
}
assert(JSON.stringify(openapi.components.schemas.WorkOrderStatus.enum) === JSON.stringify(['DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED', 'COMPLETED', 'CANCELLED']), 'Work Order status vocabulary drifted');
assert(!openapi.components.schemas.WorkOrderSourceDomain.enum.includes('TELEMETRY'), 'Telemetry was admitted as Work Order authority');
assert(openapi.components.schemas.WorkOrderSourceRelationship.enum.includes('ORIGIN'), 'Work Order origin relationship is missing');
assert(openapi.components.schemas.WorkOrderListResponse.properties.nextCursor?.$ref === '#/components/schemas/OpaqueCursor', 'Work Order cursor is not opaque');
assert(model.includes('func (workOrder WorkOrder) Validate() error') && model.includes('func (response ListResponse) Validate('), 'Work Order model validation is missing');
assert(model.includes('originCount != 1') && model.includes('completed work order requires completion evidence'), 'Work Order origin or completion invariants are missing');
assert(model.includes('SourceAlarm') && !model.includes('SourceTelemetry'), 'Work Order source authority vocabulary is invalid');
assert(!model.includes('/telemetry/'), 'Work Order model depends on Telemetry HTTP');

assert(data.registryRevision >= 17 && lock.dataRegistryRevision >= 17, 'Work Order data ownership revision was not advanced');
const expectedResources = [
  ['schema', 'work_order_runtime'],
  ['event-family', 'hvac.work-order.lifecycle.v1'],
  ['projection', 'work-order-current'],
  ['projection', 'work-order-source-reference'],
  ['projection', 'work-order-timeline'],
  ['projection', 'work-order-task'],
  ['projection', 'work-order-note'],
  ['projection', 'work-order-attachment-metadata'],
  ['projection', 'work-order-completion-evidence'],
];
for (const [kind, name] of expectedResources) {
  const resource = data.resources?.find((entry) => entry.kind === kind && entry.name === name);
  assert(resource?.writer === 'work-order-service' && resource?.revision === 1, `Work Order ownership resource ${kind}:${name} is missing`);
  assert(lock.resources?.[`${kind}:${name}`]?.writer === 'work-order-service', `Work Order lock resource ${kind}:${name} is missing`);
}
assert(data.databaseAccess?.some((entry) => entry.service === 'work-order-service' && entry.schema === 'work_order_runtime' && entry.mode === 'read'), 'Work Order read-only database access is missing');
assert(data.databaseIdentities?.some((entry) => entry.schema === 'work_order_runtime' && entry.migrationRole === 's5_work_order_migrator' && entry.runtimeRole === 's5_work_order_runtime' && entry.runtimeBypassRls === false && entry.accessMode === 'read'), 'Work Order database identity is invalid');

for (const table of ['work_order_current', 'work_order_source_reference', 'work_order_timeline', 'work_order_task', 'work_order_note', 'work_order_attachment_metadata', 'work_order_completion_evidence']) {
  assert(migration.includes(`CREATE TABLE IF NOT EXISTS work_order_runtime.${table}`), `Work Order migration lacks ${table}`);
}
assert(migration.includes('CREATE UNIQUE INDEX IF NOT EXISTS work_order_one_origin_idx') && migration.includes("WHERE relationship = 'ORIGIN'"), 'Work Order durable origin uniqueness is missing');
assert(migration.includes('ENABLE ROW LEVEL SECURITY') && migration.includes('FORCE ROW LEVEL SECURITY'), 'Work Order FORCE RLS is missing');
assert(migration.includes("current_setting(''app.organization_id''"), 'Work Order Organization RLS binding is missing');
assert(migration.includes('GRANT SELECT ON ALL TABLES IN SCHEMA work_order_runtime TO s5_work_order_runtime'), 'Work Order runtime SELECT grant is missing');
for (const forbidden of ['GRANT INSERT', 'GRANT UPDATE', 'GRANT DELETE', 'GRANT ALL']) assert(!migration.includes(forbidden), `Work Order runtime has forbidden grant ${forbidden}`);
assert(!migration.includes('ON DELETE CASCADE'), 'Work Order authoritative evidence can be cascade-deleted');

console.log('S5 Work Order contract-only baseline passed: independent owner, strict read model, 0% public routes, no fallback and read-only FORCE RLS persistence are present.');
