import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const readJSON = async (relativePath) => JSON.parse(await readFile(path.join(root, relativePath), 'utf8'));

const routeRegistry = await readJSON('contracts/ownership/route-ownership.v1.json');
const dataRegistry = await readJSON('contracts/ownership/data-ownership.v1.json');
const backendArchitecture = await readJSON('contracts/architecture/backend-architecture.v2.json');
const errors = [];

const allowedOwners = new Set([
  'platform-gateway', 'platform-core-service', 'telemetry-runtime-service', 'command-service',
  'telemetry-query-service', 'operations-agent-service', 'alarm-service', 'work-order-service',
  'forecast-service', 'optimization-service', 'metric-engine-service', 'settlement-service',
]);
const allowedScopes = new Set(['tenant', 'principal', 'site', 'device', 'key', 'alarm', 'work-order', 'asset', 'space', 'point', 'command']);
const allowedMethods = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE']);
const canonicalMustExist = new Set([
  'POST /api/v1/auth/login',
  'GET /api/v1/sites',
  'GET /api/v1/sites/{siteId}',
  'GET /api/v1/assets/{assetId}',
  'GET /api/v1/devices/{deviceId}',
  'POST /api/v1/commands',
  'GET /api/v1/commands/{commandId}',
  'POST /api/v1/commands/{commandId}/approve',
  'GET /api/v1/alarms',
  'GET /api/v1/alarms/{alarmId}',
  'POST /api/v1/alarms/{alarmId}/ack',
]);

if (routeRegistry.registryVersion !== 1) errors.push('route registry version must be 1');
if (!Number.isInteger(routeRegistry.registryRevision) || routeRegistry.registryRevision < 1) errors.push('route registry revision must be positive');

const routeKeys = new Set();
for (const route of routeRegistry.routes ?? []) {
  const key = `${route.method} ${route.path}`;
  if (!allowedMethods.has(route.method)) errors.push(`${key}: unsupported method`);
  if (typeof route.path !== 'string' || !route.path.startsWith('/api/v1/')) errors.push(`${key}: public path must start with /api/v1/`);
  if (routeKeys.has(key)) errors.push(`${key}: duplicate route owner`);
  routeKeys.add(key);
  if (!allowedOwners.has(route.owner)) errors.push(`${key}: unknown current owner ${route.owner}`);
  if (route.publicIngress !== undefined && route.publicIngress !== 'platform-gateway') errors.push(`${key}: public ingress must be platform-gateway`);
  if (!Number.isInteger(route.revision) || route.revision < 1) errors.push(`${key}: invalid revision`);
  if (route.compatibilityMode !== 'native') errors.push(`${key}: only native compatibility mode is allowed in the current registry`);
  if (route.readOnlyFallback === true) errors.push(`${key}: request fallback is forbidden in the current registry`);
  if (route.readFallbackOwner !== undefined) errors.push(`${key}: read fallback owner is forbidden`);
  for (const scope of route.allowedScopeDimensions ?? []) {
    if (!allowedScopes.has(scope)) errors.push(`${key}: invalid scope dimension ${scope}`);
  }
  if ((route.allowedScopeDimensions ?? []).includes('organization')) errors.push(`${key}: Organization scope is forbidden; use Tenant`);
  if (route.path.includes('/organizations') || route.path.includes('{organizationId}') || route.path.includes('/equipment') || route.path.includes('{equipmentId}')) {
    errors.push(`${key}: legacy Organization/Equipment public path is forbidden`);
  }
  if (route.path.includes(':approve') || route.path.includes(':acknowledge')) errors.push(`${key}: legacy colon Command/Alarm action path is forbidden`);

  const rollout = route.rollout ?? {};
  if (!['all', 'disabled', 'percentage'].includes(rollout.mode)) errors.push(`${key}: rollout mode is invalid`);
  if (rollout.fallbackOwner !== undefined) errors.push(`${key}: rollout fallback owner is forbidden`);
  if (rollout.mode === 'percentage') {
    if (!Number.isInteger(rollout.percentage) || rollout.percentage < 0 || rollout.percentage > 100) errors.push(`${key}: rollout percentage must be 0..100`);
    if (typeof rollout.cohortSalt !== 'string' || rollout.cohortSalt.length < 8) errors.push(`${key}: percentage rollout requires a cohort salt`);
    if (!(route.allowedScopeDimensions ?? []).includes('tenant') || !(route.allowedScopeDimensions ?? []).includes('principal')) {
      errors.push(`${key}: percentage rollout requires Tenant + Principal scope`);
    }
  } else {
    for (const field of ['percentage', 'fallbackOwner', 'cohortSalt']) {
      if (rollout[field] !== undefined) errors.push(`${key}: ${rollout.mode} rollout cannot declare ${field}`);
    }
  }
}
for (const key of canonicalMustExist) {
  if (!routeKeys.has(key)) errors.push(`${key}: required current canonical route is missing`);
}

if (dataRegistry.registryVersion !== 1) errors.push('data registry version must be 1');
if (!Number.isInteger(dataRegistry.registryRevision) || dataRegistry.registryRevision < 1) errors.push('data registry revision must be positive');

const resources = new Map();
for (const resource of dataRegistry.resources ?? []) {
  const key = `${resource.kind}:${resource.name}`;
  if (resources.has(key)) errors.push(`${key}: duplicate writer`);
  resources.set(key, resource);
  if (typeof resource.writer !== 'string' || resource.writer.length === 0) errors.push(`${key}: writer is required`);
  if (!Number.isInteger(resource.revision) || resource.revision < 1) errors.push(`${key}: invalid revision`);
  if (resource.name === 'legacy' || resource.writer === 'legacy-hvac-backend') errors.push(`${key}: legacy compatibility ownership is forbidden`);
}

for (const access of dataRegistry.databaseAccess ?? []) {
  const schema = resources.get(`schema:${access.schema}`);
  if (!schema) {
    errors.push(`${access.service}:${access.schema}: schema has no declared writer`);
    continue;
  }
  if (access.service === 'legacy-migration-service' || access.service === 'legacy-hvac-backend') errors.push(`${access.service}:${access.schema}: legacy compatibility access is forbidden`);
  if (access.mode === 'write' && access.service !== schema.writer) errors.push(`${access.service}:${access.schema}: forbidden cross-service writer`);
  if (access.mode === 'relay') {
    const validGatewayRelay = access.service === 'outbox-relay' && access.schema === 'gateway';
    const validTelemetryRelay = access.service === 'outbox-relay' && access.schema === 'telemetry_runtime'
      && Array.isArray(access.restrictedTo) && access.restrictedTo.join('|') === 'telemetry_publication_outbox';
    if (!validGatewayRelay && !validTelemetryRelay) errors.push(`${access.service}:${access.schema}: invalid relay access`);
  } else if (access.mode === 'reconciliation') {
    if (access.service !== 'iam-reconciler' || access.schema !== 'iam') errors.push(`${access.service}:${access.schema}: invalid reconciliation access`);
  } else if (!['write', 'read', 'relay', 'reconciliation', 'grant-state', 'connect-only'].includes(access.mode)) {
    errors.push(`${access.service}:${access.schema}: invalid current access mode`);
  }
}

for (const identity of dataRegistry.databaseIdentities ?? []) {
  const key = `${identity.schema}:${identity.runtimeRole}`;
  if (identity.runtimeBypassRls !== false) errors.push(`${key}: runtime identity must not bypass RLS`);
  if (identity.runtimeRole === 's1_migration_operator' || identity.runtimeRole === 's1_legacy_migration_service') errors.push(`${key}: legacy migration identity is forbidden in the active registry`);
  for (const table of identity.restrictedTo ?? []) {
    if (table === 'organizations' || table === 'equipment' || table === 'organization_memberships') errors.push(`${key}: legacy Organization/Equipment table scope is forbidden`);
  }
}

const routeText = JSON.stringify(routeRegistry);
const dataText = JSON.stringify(dataRegistry);
if (/organization/i.test(routeText)) errors.push('active route ownership registry still contains Organization vocabulary');
if (/organization/i.test(dataText)) errors.push('active data ownership registry still contains Organization vocabulary');

if (backendArchitecture.document?.version !== '2.1.2') errors.push('ownership registry checker requires SE-ARCH-004 V2.1.2 baseline');
if (backendArchitecture.authorityOverrides?.tenantModel?.includes('Tenant') !== true) errors.push('Tenant authority override is missing');

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log(`Current ownership registries are V2.1.2-consistent: routes=${routeKeys.size}; resources=${resources.size}; compatibilityLocks=disabled; releaseGate=false`);
