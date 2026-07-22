import { createHmac, timingSafeEqual } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readJSON = async (path) => JSON.parse(await readFile(resolve(root, path), 'utf8'));
const [model, openapi, routeRegistry, dataRegistry, ownershipLock, ddl, fixtures, iamRuntimeResolver, iamReconciliation, coreReadService, legacyMigrationExecution, legacyMigrationSource, legacyMigrationTypes, sqlcQueries, sqlcGenerated] = await Promise.all([
  readJSON('contracts/registry/s1-registry-model.v1.json'),
  readJSON('contracts/http/platform-gateway.openapi.yaml'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readFile(resolve(root, 'infra/s1-registry/postgres/init/001-s1-registry-baseline.sql'), 'utf8'),
  readFile(resolve(root, 'infra/s1-registry/postgres/init/002-s1-registry-fixtures.sql'), 'utf8'),
  readFile(resolve(root, 'infra/s1-registry/postgres/init/003-iam-runtime-identity-resolution.sql'), 'utf8'),
  readFile(resolve(root, 'infra/s1-registry/postgres/init/004-iam-reconciliation.sql'), 'utf8'),
  readFile(resolve(root, 'infra/s1-registry/postgres/init/005-core-read-service.sql'), 'utf8'),
  readFile(resolve(root, 'infra/s1-registry/postgres/init/006-legacy-migration-execution.sql'), 'utf8'),
  readFile(resolve(root, 'services/legacy-migration-service/internal/migration/postgres.go'), 'utf8'),
  readFile(resolve(root, 'services/legacy-migration-service/internal/migration/types.go'), 'utf8'),
  readFile(resolve(root, 'pocs/s1-sqlc/queries.sql'), 'utf8'),
  readFile(resolve(root, 'pocs/s1-sqlc/generated/queries.sql.go'), 'utf8'),
]);

function assert(condition, message) {
  if (!condition) throw new Error(`S1 Registry baseline check failed: ${message}`);
}

const expectedRoutes = [
  ['GET', '/api/v1/organizations', 'listOrganizations'],
  ['GET', '/api/v1/organizations/{organizationId}', 'getOrganization'],
  ['GET', '/api/v1/organizations/{organizationId}/sites', 'listOrganizationSites'],
  ['GET', '/api/v1/sites/{siteId}', 'getSite'],
  ['GET', '/api/v1/sites/{siteId}/equipment', 'listSiteEquipment'],
  ['GET', '/api/v1/equipment/{equipmentId}', 'getEquipment'],
  ['GET', '/api/v1/sites/{siteId}/devices', 'listSiteDevices'],
  ['GET', '/api/v1/devices/{deviceId}', 'getDevice'],
];
assert(model.schemaVersion === 1 && model.contractRevision === 1, 'model revision is not fixed');
assert(model.publicId.type === 'uuidv7' && model.publicId.immutable === true, 'public IDs are not immutable UUIDv7');
assert(Object.keys(model.resources).join('|') === 'Organization|Site|Equipment|Device|DeviceBinding|ExternalBinding', 'resource set drifted');
assert(model.resources.Equipment.identityRule.includes('not interchangeable with Device'), 'Equipment and Device are not separated');
assert(model.resources.Device.identityRule.includes('not interchangeable with Equipment'), 'Device and Equipment are not separated');
assert(model.resources.ExternalBinding.activeUniqueness.join('|') === 'integrationInstanceId|externalEntityType|externalId', 'ExternalBinding active key drifted');

for (const [method, path, operationId] of expectedRoutes) {
  const operation = openapi.paths?.[path]?.[method.toLowerCase()];
  assert(operation?.operationId === operationId, `${method} ${path} is missing from OpenAPI`);
  const modelRoute = model.http.routes.find((route) => route.method === method && route.path === path);
  assert(modelRoute?.operationId === operationId, `${method} ${path} is missing from the model lock`);
  const owner = routeRegistry.routes.find((route) => route.method === method && route.path === path);
  assert(owner?.owner === 'legacy-hvac-backend', `${method} ${path} initial owner must remain Legacy`);
  assert(owner?.rollout?.fallbackOwner === 'platform-core-service', `${method} ${path} Core candidate owner is missing`);
  assert(owner?.readOnlyFallback === true && owner?.shadowSideEffectPolicy === 'NONE', `${method} ${path} migration safety is incomplete`);
  assert(owner?.fallbackForbiddenResults?.includes('AUTHORIZATION_DENIED'), `${method} ${path} could fallback after denial`);
  assert(owner?.fallbackForbiddenResults?.includes('RESOURCE_NOT_FOUND'), `${method} ${path} could leak resource existence`);
  assert(ownershipLock.routes?.[`${method} ${path}`]?.owner === 'legacy-hvac-backend', `${method} ${path} lock drifted`);
}

const stableCodes = openapi.components.schemas.ProblemDetails.properties.code['x-stable-codes'];
assert(stableCodes.join('|') === Object.keys(model.problemCodes).join('|'), 'Problem Details stable codes drifted');
assert(openapi.components.schemas.UUIDv7.pattern === model.publicId.pattern, 'UUIDv7 regex drifted');
assert(openapi.components.schemas.Instant.pattern === model.instant.pattern, 'Instant format drifted from RFC3339 UTC milliseconds');
assert(model.http.collections.exactCount === false, 'default exact counts are forbidden');
assert(model.cursor.authorizationRecheckedPerPage === true, 'cursor incorrectly replaces authorization');
assert(model.cursor.requiredClaims.join('|') === 'v|route|scopeHash|filterHash|order|last|queryRevision', 'cursor claims drifted');

const secret = Buffer.from('s1-ticket-01-cursor-integrity-key');
const payload = Buffer.from(JSON.stringify({
  v: 1,
  route: '/api/v1/sites/{siteId}/devices',
  scopeHash: 'scope-a',
  filterHash: 'filter-a',
  order: ['displayName', 'id'],
  last: ['Controller 1', '018f1e00-4000-7000-8000-000000000001'],
  queryRevision: 1,
})).toString('base64url');
const signature = createHmac('sha256', secret).update(payload).digest('base64url');
const cursor = `${payload}.${signature}`;
const verify = (candidate, expectedScope) => {
  const parts = candidate.split('.');
  if (parts.length !== 2) return false;
  const expected = createHmac('sha256', secret).update(parts[0]).digest();
  const actual = Buffer.from(parts[1], 'base64url');
  if (expected.length !== actual.length || !timingSafeEqual(expected, actual)) return false;
  const decoded = JSON.parse(Buffer.from(parts[0], 'base64url').toString('utf8'));
  return decoded.scopeHash === expectedScope;
};
assert(verify(cursor, 'scope-a'), 'valid cursor was rejected');
assert(!verify(`${payload}.${signature.slice(0, -1)}A`, 'scope-a'), 'tampered cursor was accepted');
assert(!verify(cursor, 'scope-b'), 'cursor was reusable across authorization Scope');

const bootstrap = await readFile(resolve(root, 'infra/s1-registry/postgres/init/000-bootstrap-identities.sql'), 'utf8');
for (const role of ['s1_iam_migrator', 's1_iam_runtime', 's1_iam_reconciler']) {
  assert(bootstrap.includes(`CREATE ROLE ${role} LOGIN`) && bootstrap.includes('NOBYPASSRLS'), `${role} is not a login-capable, RLS-bound IAM identity`);
}
for (const role of ['s1_core_migrator', 's1_core_runtime', 's1_migration_operator']) {
  assert(bootstrap.includes(`CREATE ROLE ${role} NOLOGIN`) && bootstrap.includes('NOBYPASSRLS'), `${role} is not locked down`);
}
assert(coreReadService.includes('CREATE ROLE s1_core_service LOGIN') && coreReadService.includes('NOBYPASSRLS'), 'Core service login identity is not RLS-bound');
assert(coreReadService.includes('GRANT s1_core_runtime TO s1_core_service'), 'Core service cannot activate the restricted runtime role');
assert(legacyMigrationExecution.includes('CREATE ROLE s1_legacy_migration_service LOGIN') && legacyMigrationExecution.includes('NOBYPASSRLS'), 'Legacy migration login identity is not RLS-bound');
assert(legacyMigrationExecution.includes('GRANT s1_migration_operator TO s1_legacy_migration_service'), 'Legacy migration service cannot activate the operator role');
for (const table of ['organizations', 'sites', 'equipment', 'devices']) {
  assert(legacyMigrationExecution.includes(`CREATE POLICY ${table}_migration_scope`), `${table} migration policy is missing`);
}
assert(legacyMigrationExecution.includes('migration_quarantine_open_source_uidx'), 'open quarantine source uniqueness is missing');
assert(legacyMigrationExecution.includes('GRANT UPDATE (status, revision, updated_at)'), 'Legacy migration retire grant is too broad or missing');
for (const marker of ['SET LOCAL ROLE s1_migration_operator', 'pg_advisory_xact_lock', "mapping_state='VERIFIED'", "mapping_state='QUARANTINED'", 'finalState = "RETIRED"', 'SOURCE_HASH_CONFLICT']) {
  assert(legacyMigrationSource.includes(marker), `Legacy migration execution marker is missing: ${marker}`);
}
for (const marker of ['AMBIGUOUS_ASSET_EQUIPMENT_RELATION', 'DisallowUnknownFields', 'maxRecordBytes', 'relationEvidence key']) {
  assert(legacyMigrationTypes.includes(marker), `Legacy migration input marker is missing: ${marker}`);
}
for (const marker of ['CREATE TABLE IF NOT EXISTS iam.registry_grant_revocations', 'ALTER TABLE iam.registry_grant_revocations ENABLE ROW LEVEL SECURITY', 'ALTER TABLE iam.registry_grant_revocations FORCE ROW LEVEL SECURITY', 'FOR ALL TO s1_iam_migrator', 'TO s1_iam_runtime']) {
  assert(coreReadService.includes(marker), `Core read service security asset is missing: ${marker}`);
}
assert(iamRuntimeResolver.includes('SECURITY DEFINER'), 'IAM identity resolver is not security definer');
assert(iamRuntimeResolver.includes('SET search_path = pg_catalog, iam'), 'IAM identity resolver search_path is unsafe');
assert(iamRuntimeResolver.includes('REVOKE ALL ON FUNCTION iam.resolve_principal_identity(text, text) FROM PUBLIC'), 'IAM identity resolver remains executable by PUBLIC');
assert(iamRuntimeResolver.includes('GRANT EXECUTE ON FUNCTION iam.resolve_principal_identity(text, text) TO s1_iam_runtime'), 'IAM runtime cannot execute the exact identity resolver');
assert(iamRuntimeResolver.includes('policies_one_active_key_uidx'), 'active IAM policy uniqueness is not enforced');
for (const table of ['reconciliation_state', 'reconciliation_events', 'reconciliation_quarantine']) {
  assert(iamReconciliation.includes(`CREATE TABLE IF NOT EXISTS iam.${table}`), `${table} DDL is missing`);
  assert(iamReconciliation.includes(`ALTER TABLE iam.${table} ENABLE ROW LEVEL SECURITY`), `${table} RLS is missing`);
  assert(iamReconciliation.includes(`ALTER TABLE iam.${table} FORCE ROW LEVEL SECURITY`), `${table} forced RLS is missing`);
}
assert(iamReconciliation.includes('TO s1_iam_reconciler'), 'IAM reconciliation grants are missing');
assert(iamReconciliation.includes('current_source_system text') && iamReconciliation.includes('current_source_key text'), 'IAM reconciliation quarantine does not retain the conflicting source');
assert(iamReconciliation.includes('GRANT UPDATE (display_name, email, status, revision, updated_at)'), 'IAM reconciler mutable Principal update grant is missing');
assert(!iamReconciliation.includes('GRANT SELECT, INSERT, UPDATE ON iam.principals'), 'IAM reconciler can update immutable Principal identity columns');
assert(iamReconciliation.includes("'STALE_SOURCE_VERSION'") && iamReconciliation.includes("'SOURCE_VERSION_CONFLICT'") && iamReconciliation.includes("'IMMUTABLE_IDENTITY_CONFLICT'"), 'IAM reconciliation quarantine reasons are incomplete');
for (const table of ['organizations', 'sites', 'equipment', 'devices', 'device_bindings', 'external_bindings', 'legacy_resource_maps', 'migration_provenance', 'migration_quarantine']) {
  assert(ddl.includes(`CREATE TABLE IF NOT EXISTS core_registry.${table}`), `${table} DDL is missing`);
  assert(ddl.includes(`ALTER TABLE core_registry.${table} ENABLE ROW LEVEL SECURITY`), `${table} RLS is missing`);
  assert(ddl.includes(`ALTER TABLE core_registry.${table} FORCE ROW LEVEL SECURITY`), `${table} forced RLS is missing`);
}
assert(ddl.includes('FOREIGN KEY (organization_id) REFERENCES core_registry.organizations(id)'), 'Site owning Organization foreign key is missing');
assert(ddl.includes('external_bindings_active_external_key_uidx'), 'ExternalBinding active uniqueness is missing');
assert(ddl.includes('equipment_registry_page_idx') && ddl.includes('devices_registry_page_idx'), 'tenant-leading keyset indexes are missing');
assert(ddl.includes('pg_timezone_names'), 'IANA timezone enforcement is missing');
for (const state of model.migration.mappingStates) assert(ddl.includes(`'${state}'`), `mapping state ${state} is missing`);
assert(fixtures.includes("'QUARANTINED'") && fixtures.includes("'ambiguous-asset-1'"), 'ambiguous Legacy fixture is missing');
assert(fixtures.includes("'018f1e00-2000-7000-8000-000000000004'"), 'no-access Principal fixture is missing');

const schemaWriters = Object.fromEntries(dataRegistry.resources.filter((resource) => resource.kind === 'schema').map((resource) => [resource.name, resource.writer]));
assert(schemaWriters.iam === 'iam-service' && schemaWriters.core_registry === 'platform-core-service', 'IAM/Core schema writers drifted');
assert(dataRegistry.databaseAccess.some((access) => access.service === 'legacy-migration-service' && access.schema === 'core_registry' && access.mode === 'migration'), 'Legacy migration service access is missing');
assert(dataRegistry.databaseIdentities.some((identity) => identity.runtimeRole === 's1_legacy_migration_service' && identity.activationRole === 's1_migration_operator'), 'Legacy migration database identity is missing');
assert(dataRegistry.databaseIdentities.every((identity) => identity.runtimeBypassRls === false), 'a runtime database identity can bypass RLS');

for (const queryName of ['ListOrganizations', 'GetOrganization', 'ListSites', 'GetSite', 'ListEquipment', 'GetEquipment', 'ListDevices', 'GetDevice']) {
  assert(sqlcQueries.includes(`-- name: ${queryName}`), `sqlc POC query ${queryName} is missing`);
  assert(sqlcGenerated.includes(`const ${queryName.charAt(0).toLowerCase()}${queryName.slice(1)}`) || sqlcGenerated.includes(`func (q *Queries) ${queryName}`), `sqlc output for ${queryName} is missing`);
}
assert(sqlcQueries.includes('authorized_organization_ids') && sqlcQueries.includes('authorized_site_ids'), 'sqlc POC omits application Scope predicates');

console.log(`S1 Registry baseline passed: ${expectedRoutes.length} routes, ${Object.keys(model.resources).length} resources, cursor integrity and ownership/RLS assets verified.`);
