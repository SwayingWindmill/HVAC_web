import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const [openapi, routes, data, lock, model, migration001, migration002, migration003, migration004] = await Promise.all([
  readJSON('contracts/http/s5-work-order-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readText('libs/workordermodel/model.go'),
  readText('services/work-order-service/migrations/001_s5_work_order_runtime.sql'),
  readText('services/work-order-service/migrations/002_s5_work_order_create_assignment.sql'),
  readText('services/work-order-service/migrations/003_s5_work_order_lifecycle.sql'),
  readText('services/work-order-service/migrations/004_s5_work_order_task_checklist.sql'),
]);
const migration = migration001 + '\n' + migration002 + '\n' + migration003 + '\n' + migration004;

assert(openapi.openapi === '3.1.0', 'Work Order OpenAPI baseline is invalid');
const listPath = '/api/v1/sites/{siteId}/work-orders';
const detailPath = '/api/v1/sites/{siteId}/work-orders/{workOrderId}';
const assignPath = '/api/v1/sites/{siteId}/work-orders/{workOrderId}:assign';
const phasePolicies = {
  'S5-R0-contract-only': {
    infoVersion: '0.1.0-contract-only',
    traffic: 0,
    revision: 1,
    activation: 'expand-baseline',
    rolloutMode: 'disabled',
    percentage: 0,
  },
  'S5-R1-internal-read-only': {
    infoVersion: '0.3.0-governed-task-checklist',
    traffic: 1,
    revision: 2,
    activation: 'internal-canary',
    rolloutMode: 'percentage',
    percentage: 1,
  },
  'S5-R2-site-canary': {
    infoVersion: '0.3.0-site-canary',
    traffic: 5,
    revision: 3,
    activation: 'site-canary',
    rolloutMode: 'percentage',
    percentage: 5,
  },
  'S5-R3-operationally-certified': {
    infoVersion: '1.0.0',
    traffic: 100,
    revision: 4,
    activation: 'primary',
    rolloutMode: 'all',
    percentage: 0,
  },
};
let observedPhase = '';

const declaredPaths = Object.keys(openapi.paths ?? {}).sort();
const lifecyclePaths = ["/api/v1/sites/{siteId}/work-orders/{workOrderId}:plan", "/api/v1/sites/{siteId}/work-orders/{workOrderId}:start", "/api/v1/sites/{siteId}/work-orders/{workOrderId}:block", "/api/v1/sites/{siteId}/work-orders/{workOrderId}:resume", "/api/v1/sites/{siteId}/work-orders/{workOrderId}:complete", "/api/v1/sites/{siteId}/work-orders/{workOrderId}:cancel", "/api/v1/sites/{siteId}/work-orders/{workOrderId}:reopen"];
const taskPaths = ["/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks", "/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks/{taskId}:status", "/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks:reorder"];
assert(JSON.stringify(declaredPaths) === JSON.stringify([listPath, detailPath, assignPath, ...lifecyclePaths, ...taskPaths].sort()), 'Work Order contract exposes undeclared routes');
assert(openapi.paths?.[listPath]?.post?.['x-iam-action'] === 'work-order:create', 'Work Order create route is missing from the governed public contract');
assert(openapi.paths?.[assignPath]?.post?.['x-iam-action'] === 'work-order:assign', 'Work Order assignment route is missing from the governed public contract');
for (const [path, action] of [[listPath, 'work-order:list'], [detailPath, 'work-order:read']]) {
  const pathItem = openapi.paths?.[path];
  assert(pathItem?.get, 'Work Order read operation is missing for ' + path);
  const expectedMethods = path === listPath ? ['get', 'post'] : ['get'];
  assert(JSON.stringify(Object.keys(pathItem).sort()) === JSON.stringify(expectedMethods), 'Work Order ' + path + ' exposes an undeclared method');
  const operation = pathItem.get;
  assert(operation['x-owner'] === 'work-order-service' && operation['x-public-ingress'] === 'platform-gateway', 'Work Order owner or ingress is invalid for ' + path);
  const phase = operation['x-migration-phase'];
  const phasePolicy = phasePolicies[phase];
  assert(phasePolicy, 'Work Order route ' + path + ' has an unsupported S5 phase');
  assert(operation['x-production-traffic-percent'] === phasePolicy.traffic, 'Work Order route ' + path + ' traffic does not match ' + phase);
  assert(openapi.info?.version === phasePolicy.infoVersion, 'Work Order OpenAPI version does not match ' + phase);
  assert(observedPhase === '' || observedPhase === phase, 'Work Order routes are in different migration phases');
  observedPhase = phase;
  assert(operation['x-iam-action'] === action && operation['x-no-fallback'] === true, 'Work Order action or no-fallback marker is invalid for ' + path);
  for (const status of ['200', '401', '403', '404', '503']) {
    assert(operation.responses?.[status], 'Work Order response ' + status + ' is missing for ' + path);
  }

  const route = routes.routes?.find((entry) => entry.method === 'GET' && entry.path === path);
  assert(route?.owner === 'work-order-service' && route?.publicIngress === 'platform-gateway', 'Route Ownership Registry is missing ' + path);
  assert(route?.revision === phasePolicy.revision && route?.activationStatus === phasePolicy.activation && route?.rollout?.mode === phasePolicy.rolloutMode, 'Work Order route ' + path + ' does not match ' + phase);
  assert((route?.rollout?.percentage ?? 0) === phasePolicy.percentage, 'Work Order route ' + path + ' has the wrong rollout percentage');
  if (phasePolicy.rolloutMode === 'percentage') {
    assert(typeof route?.rollout?.cohortSalt === 'string' && route.rollout.cohortSalt.length >= 8, 'Work Order route ' + path + ' lacks a stable cohort salt');
  }
  assert(route?.migrationPhase === phase && route?.cohortGroup === 's5-work-order-read-v1', 'Work Order route ' + path + ' has the wrong S5 phase or group');
  assert(route?.readOnlyFallback === false && route?.readFallbackOwner === undefined && route?.rollout?.fallbackOwner === undefined, 'Work Order route ' + path + ' permits fallback');
  assert(route?.shadowSideEffectPolicy === 'NONE', 'Work Order route ' + path + ' is not side-effect-free');
  for (const scope of ['organization', 'site', 'principal']) {
    assert(route?.allowedScopeDimensions?.includes(scope), 'Work Order route ' + path + ' lacks scope ' + scope);
  }
  if (path === detailPath) {
    assert(route?.allowedScopeDimensions?.includes('work-order'), 'Work Order detail lacks identity scope');
  }
  for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND']) {
    assert(route?.fallbackForbiddenResults?.includes(result), 'Work Order route ' + path + ' does not forbid fallback for ' + result);
  }
  assert(lock.routes?.['GET ' + path]?.owner === 'work-order-service' && lock.routes?.['GET ' + path]?.revision === phasePolicy.revision, 'Work Order compatibility lock is missing ' + path);
}

const schema = openapi.components?.schemas?.WorkOrder;
for (const field of ['workOrderId', 'organizationId', 'siteId', 'title', 'description', 'priority', 'status', 'sourceReferences', 'tasks', 'noteCount', 'attachmentCount', 'completionEvidence', 'timeline', 'version', 'createdAt', 'updatedAt']) {
  assert(schema?.required?.includes(field), 'Work Order contract does not require ' + field);
}
assert(JSON.stringify(openapi.components.schemas.WorkOrderStatus.enum) === JSON.stringify(['DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED', 'COMPLETED', 'CANCELLED']), 'Work Order status vocabulary drifted');
assert(JSON.stringify(openapi.components.schemas.WorkOrderSourceDomain.enum) === JSON.stringify(['MANUAL', 'ALARM', 'ASSET', 'EQUIPMENT', 'INVESTIGATION', 'EXTERNAL']), 'Work Order source authority vocabulary drifted');
assert(!openapi.components.schemas.WorkOrderSourceDomain.enum.includes('TELEMETRY') && !openapi.components.schemas.WorkOrderSourceDomain.enum.includes('FDD'), 'Telemetry or FDD was admitted as Work Order authority');
assert(openapi.components.schemas.WorkOrderSourceRelationship.enum.includes('ORIGIN'), 'Work Order origin relationship is missing');
assert(openapi.components.schemas.WorkOrderListResponse.properties.nextCursor?.$ref === '#/components/schemas/OpaqueCursor', 'Work Order cursor is not opaque');
assert(model.includes('func (workOrder WorkOrder) Validate() error') && model.includes('func (response ListResponse) Validate('), 'Work Order model validation is missing');
assert(model.includes('originCount != 1') && model.includes('completed work order requires completion evidence'), 'Work Order origin or completion invariants are missing');
assert(model.includes('SourceAlarm') && model.includes('SourceAsset') && model.includes('SourceEquipment') && !model.includes('SourceTelemetry') && !model.includes('SourceFDD'), 'Work Order source authority vocabulary is invalid');
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
  assert(resource?.writer === 'work-order-service' && resource?.revision === 1, 'Work Order ownership resource ' + kind + ':' + name + ' is missing');
  assert(lock.resources?.[kind + ':' + name]?.writer === 'work-order-service', 'Work Order lock resource ' + kind + ':' + name + ' is missing');
}
const workOrderDatabaseAccess = data.databaseAccess?.find((entry) => entry.service === 'work-order-service' && entry.schema === 'work_order_runtime');
assert(workOrderDatabaseAccess && ['read', 'write'].includes(workOrderDatabaseAccess.mode), 'Work Order database access is missing');
if (workOrderDatabaseAccess.mode === 'write') {
  const expectedMutationTables = ['work_order_current', 'work_order_source_reference', 'work_order_timeline', 'work_order_idempotency', 'work_order_mutation_audit', 'work_order_completion_evidence', 'work_order_task'];
  assert(JSON.stringify([...(workOrderDatabaseAccess.restrictedTo ?? [])].sort()) === JSON.stringify(expectedMutationTables.sort()), 'Work Order write access is not restricted to create/assignment persistence');
}
assert(data.databaseIdentities?.some((entry) => entry.schema === 'work_order_runtime' && entry.migrationRole === 's5_work_order_migrator' && entry.runtimeRole === 's5_work_order_runtime' && entry.runtimeBypassRls === false && entry.accessMode === 'read'), 'Work Order database identity is invalid');
assert(data.databaseIdentities?.some((entry) => entry.schema === 'work_order_runtime' && entry.migrationRole === 's5_work_order_migrator' && entry.runtimeRole === 's5_work_order_service' && entry.activationRole === 's5_work_order_runtime' && entry.runtimeBypassRls === false && entry.accessMode === 'read'), 'Work Order service database identity is invalid');

for (const table of ['work_order_current', 'work_order_source_reference', 'work_order_timeline', 'work_order_task', 'work_order_note', 'work_order_attachment_metadata', 'work_order_completion_evidence']) {
  assert(migration.includes('CREATE TABLE IF NOT EXISTS work_order_runtime.' + table), 'Work Order migration lacks ' + table);
}
assert(migration.includes('CREATE UNIQUE INDEX IF NOT EXISTS work_order_one_origin_idx') && migration.includes("WHERE relationship = 'ORIGIN'"), 'Work Order durable origin uniqueness is missing');
assert(migration.includes('PRIMARY KEY (organization_id, site_id, work_order_id, source_domain, source_resource_id)'), 'Work Order source identity can carry conflicting relationships');
assert(migration.includes('ENABLE ROW LEVEL SECURITY') && migration.includes('FORCE ROW LEVEL SECURITY'), 'Work Order FORCE RLS is missing');
assert(migration.includes("current_setting(''app.organization_id''"), 'Work Order Organization RLS binding is missing');
assert(migration001.includes('GRANT SELECT ON ALL TABLES IN SCHEMA work_order_runtime TO s5_work_order_runtime'), 'Work Order read runtime SELECT grant is missing');
assert(migration002.includes('TO s5_work_order_writer;'), 'Work Order writer SELECT grant is missing');
const runtimeGrantLines = migration.split(/\r?\n/).filter((line) => line.includes('TO s5_work_order_runtime'));
for (const forbidden of ['GRANT INSERT', 'GRANT UPDATE', 'GRANT DELETE', 'GRANT ALL']) {
  assert(!runtimeGrantLines.some((line) => line.includes(forbidden)), 'Work Order read runtime has forbidden grant ' + forbidden);
}
assert(migration002.includes('GRANT INSERT ON work_order_runtime.work_order_current TO s5_work_order_writer') && migration002.includes('GRANT UPDATE (assignee_id, team_id, version, updated_at)'), 'Work Order writer current projection grants are missing or too broad');
assert(migration003.includes('GRANT INSERT ON work_order_runtime.work_order_completion_evidence TO s5_work_order_writer'), 'Work Order lifecycle evidence grant is missing');
assert(migration004.includes('GRANT INSERT ON work_order_runtime.work_order_task TO s5_work_order_writer') && migration004.includes('GRANT UPDATE (position, status, version, updated_at)'), 'Work Order task grants are missing or too broad');
assert(migration.includes('GRANT INSERT ON work_order_runtime.work_order_idempotency TO s5_work_order_writer') && migration.includes('GRANT INSERT ON work_order_runtime.work_order_mutation_audit TO s5_work_order_writer'), 'Work Order writer durable mutation evidence grants are missing');
assert(!migration.split(/\r?\n/).some((line) => line.includes('GRANT DELETE') || line.includes('GRANT ALL')), 'Work Order authority grants include delete or all privileges');
assert(!migration.includes('ON DELETE CASCADE'), 'Work Order authoritative evidence can be cascade-deleted');

console.log('S5 Work Order ' + observedPhase + ' baseline passed: independent owner, strict read model, governed no-fallback rollout and read-only FORCE RLS persistence are present.');