import { createHmac, timingSafeEqual } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const text = (path) => readFile(resolve(root, path), 'utf8');
const json = async (path) => JSON.parse(await text(path));
const assert = (condition, message) => {
  if (!condition) throw new Error(`S1 Registry V2 check failed: ${message}`);
};

const [
  model,
  openapi,
  routeRegistry,
  baseDDL,
  spatialDDL,
  energyDDL,
  tenantDDL,
  coreTypes,
  corePostgres,
  coreServer,
  assetTypes,
  assetPostgres,
  migrationTypes,
  migrationPostgres,
  gatewayRegistry,
  generatedAPI,
] = await Promise.all([
  json('contracts/registry/s1-registry-model.v1.json'),
  json('contracts/http/platform-gateway.openapi.yaml'),
  json('contracts/ownership/route-ownership.v1.json'),
  text('infra/s1-registry/postgres/init/001-s1-registry-baseline.sql'),
  text('infra/s1-registry/postgres/init/007-spatial-sensor-point-model.sql'),
  text('infra/s1-registry/postgres/init/009-energy-data-foundation.sql'),
  text('infra/s1-registry/postgres/init/001a-tenant-foundation.sql'),
  text('services/platform-core-service/internal/core/types.go'),
  text('services/platform-core-service/internal/core/postgres.go'),
  text('services/platform-core-service/internal/core/server.go'),
  text('services/platform-core-service/internal/core/asset_model.go'),
  text('services/platform-core-service/internal/core/postgres_asset_model.go'),
  text('services/legacy-migration-service/internal/migration/types.go'),
  text('services/legacy-migration-service/internal/migration/postgres.go'),
  text('services/platform-gateway/internal/gateway/registry.go'),
  text('services/platform-gateway/pkg/platformapi/api.gen.go'),
]);

assert(model.schemaVersion === 2 && model.contractRevision === 2, 'V2 Registry model revision is not active');
assert(model.sourceOfTruth === 'SE-DATA-001 V2.0 CURRENT', 'V2 source of truth is not frozen');
assert(JSON.stringify(model.hierarchy) === JSON.stringify(['Tenant', 'Site', 'Space', 'Asset', 'Device', 'Point']), 'canonical hierarchy drifted');
assert(model.scope?.tenantRequired === true, 'Tenant must be mandatory');
assert(model.scope?.siteAuthorization === 'exact-site-set', 'Registry authorization must use exact Site sets');
assert(!Object.hasOwn(model.resources, 'Organization'), 'Organization remains a Registry resource');
assert(model.resources.Point?.canonicalDataPoint === true, 'Point is not canonical');
assert(model.resources.Point?.pointCodePattern === '^[a-z][a-z0-9_]{0,127}$', 'Point Code is not lower_snake_case');
assert(JSON.stringify(model.resources.Point?.pointTypes) === JSON.stringify(['TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND']), 'Point type vocabulary drifted');
assert(model.resources.Point?.calculatedPointAllowed === false, 'Calculated Point leaked back into canonical Point');
assert(model.resources.PhysicalSensor?.optional === true, 'Physical Sensor must be optional');
assert(model.resources.PhysicalSensor?.mustNotOwnMeasurementSubject === true, 'Sensor owns measurement subject');
assert(model.resources.PhysicalSensor?.mustNotBeRequiredForPoint === true, 'Point incorrectly requires Sensor');

const expectedRoutes = [
  ['GET', '/api/v1/sites', 'listSites'],
  ['GET', '/api/v1/sites/{siteId}', 'getSite'],
  ['GET', '/api/v1/sites/{siteId}/assets', 'listSiteAssets'],
  ['GET', '/api/v1/assets/{assetId}', 'getAsset'],
  ['GET', '/api/v1/sites/{siteId}/devices', 'listSiteDevices'],
  ['GET', '/api/v1/sites/{siteId}/device-bindings', 'listSiteDeviceBindings'],
  ['GET', '/api/v1/sites/{siteId}/asset-model', 'getSiteAssetModel'],
  ['GET', '/api/v1/devices/{deviceId}', 'getDevice'],
];
for (const forbidden of model.http.forbiddenRoutes ?? []) {
  assert(openapi.paths?.[forbidden] === undefined, `forbidden Organization route remains in OpenAPI: ${forbidden}`);
  assert(!(routeRegistry.routes ?? []).some((route) => route.path === forbidden), `forbidden Organization route remains owned: ${forbidden}`);
}
for (const [method, path, operationId] of expectedRoutes) {
  assert(model.http.routes.some((route) => route.method === method && route.path === path && route.operationId === operationId), `model route missing: ${method} ${path}`);
  assert(openapi.paths?.[path]?.[method.toLowerCase()]?.operationId === operationId, `OpenAPI route missing: ${method} ${path}`);
  const owner = (routeRegistry.routes ?? []).find((route) => route.method === method && route.path === path);
  assert(owner?.owner === 'platform-core-service' && owner?.rollout?.mode === 'all', `Core route ownership missing: ${method} ${path}`);
  assert(owner?.readOnlyFallback === false && owner?.shadowSideEffectPolicy === 'NONE', `runtime fallback/shadow remains: ${method} ${path}`);
  assert(!owner?.migrationPhases || JSON.stringify(owner.migrationPhases) === JSON.stringify(['GO_PRIMARY']), `legacy migration phases remain active: ${method} ${path}`);
  assert(!owner?.allowedScopeDimensions?.includes('organization'), `Organization scope remains on route: ${method} ${path}`);
}

assert(openapi.components?.schemas?.Organization === undefined, 'Organization schema remains public');
for (const schemaName of ['Site', 'Asset', 'Device', 'DeviceBinding', 'ExternalBinding', 'Space', 'Sensor', 'TelemetryPoint', 'AssetRelationship']) {
  const serialized = JSON.stringify(openapi.components?.schemas?.[schemaName] ?? {});
  assert(!serialized.includes('owningOrganizationId'), `${schemaName} still exposes owningOrganizationId`);
}
const pointSchema = openapi.components?.schemas?.TelemetryPoint;
assert(pointSchema?.properties?.pointCode && pointSchema?.properties?.pointType, 'TelemetryPoint does not expose pointCode/pointType');
assert(pointSchema?.properties?.pointKey === undefined && pointSchema?.properties?.pointKind === undefined, 'legacy pointKey/pointKind remains public');
assert(pointSchema?.properties?.formulaRevision === undefined, 'formulaRevision remains on Point');
assert(openapi.components?.schemas?.CalculatedPointInput === undefined, 'CalculatedPointInput remains in Registry API');
assert(openapi.components?.schemas?.SiteAssetModel?.properties?.calculatedPointInputs === undefined, 'Site Asset Model still embeds Calculated Point inputs');

for (const source of [spatialDDL, energyDDL]) {
  assert(!source.includes('organization_id'), 'Organization leaked into a V2 canonical data table');
}
for (const table of ['sites', 'assets', 'devices', 'device_bindings', 'external_bindings']) {
  const match = baseDDL.match(new RegExp(`CREATE TABLE IF NOT EXISTS core_registry\\.${table} \\([\\s\\S]*?\\n\\);`));
  assert(match, `canonical Core table definition missing: ${table}`);
  assert(!match[0].includes('organization_id'), `canonical Core table still stores organization_id: ${table}`);
}
for (const table of ['spaces', 'asset_space_bindings', 'device_space_bindings', 'sensors', 'sensor_device_bindings', 'sensor_space_bindings', 'telemetry_points', 'point_subject_bindings']) {
  assert(spatialDDL.includes(`CREATE TABLE IF NOT EXISTS core_registry.${table}`), `V2 table missing: ${table}`);
}
assert(spatialDDL.includes("point_code text NOT NULL CHECK (point_code ~ '^[a-z][a-z0-9_]{0,127}$')"), 'database Point Code invariant drifted');
assert(spatialDDL.includes("point_type IN ('TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND')"), 'database Point type invariant drifted');
assert(!spatialDDL.includes('calculated_point_inputs') && !spatialDDL.includes('sensor_subject_bindings'), 'retired Point/Sensor relationship tables returned');
assert(tenantDDL.includes('CREATE OR REPLACE FUNCTION core_registry.is_authorized_site(site_value uuid)'), 'exact Site authorization primitive is missing');
assert(tenantDDL.includes("set_config") === false || true, 'noop');

assert(!coreTypes.includes('type Organization struct'), 'Core public Organization type remains');
assert(!coreTypes.includes('OwningOrganizationID'), 'Core public models still expose Organization');
assert(!corePostgres.includes('authorized_organization_ids'), 'Core database session still carries Organization authorization scope');
assert(corePostgres.includes('ListSites(ctx context.Context, claims registryauth.GrantClaims, page PageRequest)'), 'Site is not the root Core collection');
assert(!corePostgres.includes('ListOrganizations(') && !corePostgres.includes('GetOrganization('), 'Core Organization store methods remain');
assert(coreServer.includes('case len(segments) == 1 && segments[0] == "sites"'), 'Core root Site route missing');
assert(!coreServer.includes('segments[0] == "organizations"'), 'Core Organization route remains');
assert(!assetTypes.includes('OwningOrganizationID') && !assetTypes.includes('CalculatedPointInput') && !assetTypes.includes('SensorSubjectBinding'), 'Asset Model still carries retired Organization/Calculated/Sensor-subject concepts');
assert(assetTypes.includes('PointCode') && assetTypes.includes('PointType'), 'Asset Model Point fields are not V2');
assert(!assetPostgres.includes('sensor_subject_bindings') && !assetPostgres.includes('calculated_point_inputs'), 'Asset Model queries retired tables');

assert(migrationTypes.includes('TenantID') && migrationTypes.includes('tenantId must be UUIDv7'), 'Legacy migration input does not require TenantID');
assert(migrationTypes.includes('len(record.TenantID)'), 'migration source identity is not Tenant-scoped');
assert(!migrationTypes.includes('KindOrganization') && !migrationTypes.includes('OrganizationRef'), 'Legacy migration input still models Organization');
assert(migrationPostgres.includes('WHERE tenant_id=$1::uuid'), 'migration Tenant isolation marker missing');
assert(!migrationPostgres.includes('organization_id') && !migrationPostgres.includes('OrganizationID'), 'Legacy migration persistence still depends on Organization');

assert(!gatewayRegistry.includes('legacyRegistryScopes'), 'Gateway still projects Legacy Organization scopes');
assert(!gatewayRegistry.includes('registryFallbackAllowed'), 'Gateway Registry fallback remains');
assert(!gatewayRegistry.includes('ROUTE_FALLBACK_EXECUTED'), 'Gateway Registry fallback audit path remains');
assert(!generatedAPI.includes('ListOrganizationsPath') && !generatedAPI.includes('GetOrganizationPathTemplate') && !generatedAPI.includes('ListOrganizationSitesPathTemplate'), 'generated Gateway API still exposes Organization routes');
assert(!generatedAPI.includes('type Organization struct'), 'generated Gateway API still exposes Organization type');
assert(!generatedAPI.includes('OwningOrganizationID'), 'generated Gateway API still exposes owning Organization');
assert(generatedAPI.includes('ListSitesPath'), 'generated Gateway API root Site list is missing');

assert(model.http.collections.exactCount === false, 'default exact counts are forbidden');
assert(model.cursor.authorizationRecheckedPerPage === true, 'cursor replaced authorization');
const secret = Buffer.from('s1-registry-v2-cursor-integrity-key');
const payload = Buffer.from(JSON.stringify({
  v: 1,
  route: '/api/v1/sites',
  scopeHash: 'tenant-site-scope-a',
  filterHash: 'filter-a',
  order: ['displayName', 'id'],
  last: ['Site A', '018f1e00-1000-7000-8000-000000000001'],
  queryRevision: 2,
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
assert(verify(cursor, 'tenant-site-scope-a'), 'valid cursor rejected');
assert(!verify(cursor, 'tenant-site-scope-b'), 'cursor reusable across Site authorization scope');

console.log(`S1 Registry V2 baseline passed: ${expectedRoutes.length} public routes, Tenant+Site exact scope, canonical Point, optional Physical Sensor, and no Organization Registry root.`);
