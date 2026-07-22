import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const readJSON = async (relativePath) => JSON.parse(await readFile(path.join(root, relativePath), 'utf8'));

const routeRegistry = await readJSON('contracts/ownership/route-ownership.v1.json');
const dataRegistry = await readJSON('contracts/ownership/data-ownership.v1.json');
const lock = await readJSON('contracts/ownership/ownership.v1.lock.json');
const phaseRegistries = await Promise.all([
  'contracts/ownership/s1-registry-phases/01-legacy-primary-go-shadow.json',
  'contracts/ownership/s1-registry-phases/02-go-canary-legacy-shadow.json',
  'contracts/ownership/s1-registry-phases/03-go-primary-legacy-read-fallback.json',
  'contracts/ownership/s1-registry-phases/04-go-primary.json',
].map(readJSON));

const errors = [];
const allowedOwners = new Set(['platform-gateway', 'legacy-hvac-backend', 'platform-core-service']);
const s1RegistryPaths = new Set([
  '/api/v1/organizations',
  '/api/v1/organizations/{organizationId}',
  '/api/v1/organizations/{organizationId}/sites',
  '/api/v1/sites/{siteId}',
  '/api/v1/sites/{siteId}/equipment',
  '/api/v1/equipment/{equipmentId}',
  '/api/v1/sites/{siteId}/devices',
  '/api/v1/devices/{deviceId}',
]);
const expectedMigrationPhases = [
  'LEGACY_PRIMARY_GO_SHADOW',
  'GO_CANARY_LEGACY_SHADOW',
  'GO_PRIMARY_LEGACY_READ_FALLBACK',
  'GO_PRIMARY',
];
const allowedScopes = new Set(['organization', 'principal', 'site']);
const allowedCompatibility = new Set(['native', 'legacy-read']);
const allowedMethods = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE']);

if (routeRegistry.registryVersion !== 1) errors.push('route registry version must be 1');
if (!Number.isInteger(routeRegistry.registryRevision) || routeRegistry.registryRevision < lock.routeRegistryRevision) {
  errors.push('route registry revision regressed');
}

const routeKeys = new Set();
for (const route of routeRegistry.routes ?? []) {
  const key = `${route.method} ${route.path}`;
  if (!allowedMethods.has(route.method)) errors.push(`${key}: unsupported method`);
  if (typeof route.path !== 'string' || !route.path.startsWith('/api/v1/')) errors.push(`${key}: public path must start with /api/v1/`);
  if (routeKeys.has(key)) errors.push(`${key}: duplicate route owner`);
  routeKeys.add(key);
  if (!allowedOwners.has(route.owner)) errors.push(`${key}: unknown owner ${route.owner}`);
  if (!Number.isInteger(route.revision) || route.revision < 1) errors.push(`${key}: invalid revision`);
  if (!allowedCompatibility.has(route.compatibilityMode)) errors.push(`${key}: invalid compatibility mode`);
  for (const scope of route.allowedScopeDimensions ?? []) {
    if (!allowedScopes.has(scope)) errors.push(`${key}: invalid scope dimension ${scope}`);
  }
  const rollout = route.rollout ?? {};
  if (rollout.mode === 'all') {
    for (const forbidden of ['percentage', 'fallbackOwner', 'cohortSalt']) {
      if (rollout[forbidden] !== undefined) errors.push(`${key}: all rollout cannot declare ${forbidden}`);
    }
  } else if (rollout.mode === 'percentage') {
    if (!Number.isInteger(rollout.percentage) || rollout.percentage < 0 || rollout.percentage > 100) errors.push(`${key}: rollout percentage must be 0..100`);
    if (!allowedOwners.has(rollout.fallbackOwner) || rollout.fallbackOwner === route.owner) errors.push(`${key}: invalid fallback owner`);
    if (typeof rollout.cohortSalt !== 'string' || rollout.cohortSalt.length < 8) errors.push(`${key}: cohort salt is required`);
    if (!(route.allowedScopeDimensions ?? []).includes('organization') || !(route.allowedScopeDimensions ?? []).includes('principal')) {
      errors.push(`${key}: percentage rollout requires organization and principal scope dimensions`);
    }
  } else {
    errors.push(`${key}: rollout mode must be all or percentage`);
  }
  if (s1RegistryPaths.has(route.path)) {
    if (route.method !== 'GET') errors.push(`${key}: S1 Registry route must be read-only`);
    if (route.owner !== 'platform-core-service' || rollout.mode !== 'all' || route.compatibilityMode !== 'native') {
      errors.push(`${key}: final S1 route must be Core primary without cohort routing`);
    }
    if (route.migrationPhase !== 'GO_PRIMARY' || route.readFallbackOwner !== undefined) {
      errors.push(`${key}: active S1 route must finish in GO_PRIMARY without Legacy fallback`);
    }
    if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedMigrationPhases.join('|')) {
      errors.push(`${key}: S1 migration phases are incomplete or reordered`);
    }
    if (route.shadowSideEffectPolicy !== 'NONE') errors.push(`${key}: S1 shadow must be side-effect free`);
    if (route.readOnlyFallback !== true) errors.push(`${key}: S1 fallback must be explicitly read-only`);
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    if (!forbiddenResults.includes('AUTHORIZATION_DENIED') || !forbiddenResults.includes('RESOURCE_NOT_FOUND')) {
      errors.push(`${key}: S1 fallback must be forbidden after denial or resource invisibility`);
    }
  }
  const locked = lock.routes?.[key];
  if (!locked) {
    errors.push(`${key}: route is missing from compatibility lock`);
  } else {
    if (route.revision < locked.revision) errors.push(`${key}: owner revision regressed`);
    if (route.owner !== locked.owner && route.revision === locked.revision) errors.push(`${key}: owner changed without revision increase`);
  }
}
for (const key of Object.keys(lock.routes ?? {})) {
  if (!routeKeys.has(key)) errors.push(`${key}: locked route was removed`);
}

const phaseExpectations = [
  { revision: 3, routeRevision: 2, phase: 'LEGACY_PRIMARY_GO_SHADOW', owner: 'legacy-hvac-backend', compatibility: 'legacy-read', rolloutMode: 'percentage', percentage: 100, fallbackOwner: 'platform-core-service' },
  { revision: 4, routeRevision: 3, phase: 'GO_CANARY_LEGACY_SHADOW', owner: 'platform-core-service', compatibility: 'native', rolloutMode: 'percentage', percentage: 10, fallbackOwner: 'legacy-hvac-backend' },
  { revision: 5, routeRevision: 4, phase: 'GO_PRIMARY_LEGACY_READ_FALLBACK', owner: 'platform-core-service', compatibility: 'native', rolloutMode: 'all', readFallbackOwner: 'legacy-hvac-backend' },
  { revision: 6, routeRevision: 5, phase: 'GO_PRIMARY', owner: 'platform-core-service', compatibility: 'native', rolloutMode: 'all' },
];
for (let index = 0; index < phaseRegistries.length; index += 1) {
  const registry = phaseRegistries[index];
  const expected = phaseExpectations[index];
  if (registry.registryRevision !== expected.revision) errors.push(`S1 phase ${expected.phase}: registry revision mismatch`);
  const phaseRoutes = (registry.routes ?? []).filter((route) => s1RegistryPaths.has(route.path));
  if (phaseRoutes.length !== s1RegistryPaths.size) errors.push(`S1 phase ${expected.phase}: route set is incomplete`);
  for (const route of phaseRoutes) {
    const key = `${route.method} ${route.path}`;
    if (route.revision !== expected.routeRevision || route.migrationPhase !== expected.phase || route.owner !== expected.owner || route.compatibilityMode !== expected.compatibility) errors.push(`${key}: phase ${expected.phase} ownership drifted`);
    if (route.rollout?.mode !== expected.rolloutMode || route.rollout?.percentage !== expected.percentage || route.rollout?.fallbackOwner !== expected.fallbackOwner) errors.push(`${key}: phase ${expected.phase} rollout drifted`);
    if ((route.readFallbackOwner ?? undefined) !== expected.readFallbackOwner) errors.push(`${key}: phase ${expected.phase} read fallback drifted`);
    if (route.shadowSideEffectPolicy !== 'NONE' || route.readOnlyFallback !== true) errors.push(`${key}: phase ${expected.phase} safety policy drifted`);
  }
}
if (JSON.stringify(routeRegistry) !== JSON.stringify(phaseRegistries.at(-1))) errors.push('active Route Ownership Registry is not the final GO_PRIMARY phase asset');

if (dataRegistry.registryVersion !== 1) errors.push('data registry version must be 1');
if (!Number.isInteger(dataRegistry.registryRevision) || dataRegistry.registryRevision < lock.dataRegistryRevision) {
  errors.push('data registry revision regressed');
}
const resources = new Map();
for (const resource of dataRegistry.resources ?? []) {
  const key = `${resource.kind}:${resource.name}`;
  if (resources.has(key)) errors.push(`${key}: duplicate writer`);
  resources.set(key, resource);
  if (typeof resource.writer !== 'string' || resource.writer.length === 0) errors.push(`${key}: writer is required`);
  if (!Number.isInteger(resource.revision) || resource.revision < 1) errors.push(`${key}: invalid revision`);
  const locked = lock.resources?.[key];
  if (!locked) {
    errors.push(`${key}: resource is missing from compatibility lock`);
  } else {
    if (resource.revision < locked.revision) errors.push(`${key}: writer revision regressed`);
    if (resource.writer !== locked.writer && resource.revision === locked.revision) errors.push(`${key}: writer changed without revision increase`);
  }
}
for (const key of Object.keys(lock.resources ?? {})) {
  if (!resources.has(key)) errors.push(`${key}: locked resource was removed`);
}

for (const access of dataRegistry.databaseAccess ?? []) {
  const schema = resources.get(`schema:${access.schema}`);
  if (!schema) {
    errors.push(`${access.service}:${access.schema}: schema has no declared writer`);
    continue;
  }
  if (access.mode === 'write' && access.service !== schema.writer) {
    errors.push(`${access.service}:${access.schema}: forbidden cross-service writer`);
  } else if (access.mode === 'relay') {
    if (access.service !== 'outbox-relay' || access.schema !== 'gateway') errors.push(`${access.service}:${access.schema}: invalid relay access`);
  } else if (access.mode === 'migration') {
    if (access.service !== 'legacy-migration-service' || access.schema !== 'core_registry') errors.push(`${access.service}:${access.schema}: invalid migration access`);
  } else if (access.mode === 'reconciliation') {
    if (access.service !== 'iam-reconciler' || access.schema !== 'iam') errors.push(`${access.service}:${access.schema}: invalid reconciliation access`);
  } else if (access.mode !== 'write' && access.mode !== 'read' && access.mode !== 'relay' && access.mode !== 'migration' && access.mode !== 'reconciliation') {
    errors.push(`${access.service}:${access.schema}: invalid access mode`);
  }
}

const requiredIdentities = new Map([
  ['iam:s1_iam_runtime', { migrationRole: 's1_iam_migrator' }],
  ['iam:s1_iam_reconciler', { migrationRole: 's1_iam_migrator' }],
  ['core_registry:s1_core_runtime', { migrationRole: 's1_core_migrator' }],
  ['core_registry:s1_core_service', { migrationRole: 's1_core_migrator', activationRole: 's1_core_runtime' }],
  ['core_registry:s1_migration_operator', { migrationRole: 's1_core_migrator' }],
  ['core_registry:s1_legacy_migration_service', { migrationRole: 's1_core_migrator', activationRole: 's1_migration_operator' }],
]);
for (const identity of dataRegistry.databaseIdentities ?? []) {
  const key = `${identity.schema}:${identity.runtimeRole}`;
  const expected = requiredIdentities.get(key);
  if (!expected) errors.push(`${key}: unexpected database identity`);
  if (identity.migrationRole !== expected?.migrationRole) errors.push(`${key}: migration role mismatch`);
  if ((identity.activationRole ?? null) !== (expected?.activationRole ?? null)) errors.push(`${key}: activation role mismatch`);
  if (identity.runtimeBypassRls !== false) errors.push(`${key}: runtime identity must not bypass RLS`);
  requiredIdentities.delete(key);
}
for (const key of requiredIdentities.keys()) errors.push(`${key}: required database identity is missing`);

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('Route and Data Ownership Registry checks passed.');
