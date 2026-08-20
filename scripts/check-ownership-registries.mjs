import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const readJSON = async (relativePath) => JSON.parse(await readFile(path.join(root, relativePath), 'utf8'));

const routeRegistry = await readJSON('contracts/ownership/route-ownership.v1.json');
const dataRegistry = await readJSON('contracts/ownership/data-ownership.v1.json');
const targetDomainModel = await readJSON('contracts/architecture/target-domain-model.v1.json');
const errors = [];

const allowedOwners = new Set([
  'platform-gateway', 'platform-core-service', 'telemetry-runtime-service', 'command-service',
  'telemetry-query-service', 'operations-agent-service', 'alarm-service', 'notification-service', 'work-order-service', 'presentation-service',
  'forecast-service', 'optimization-service', 'metric-engine-service', 'settlement-service',
]);
const allowedScopes = new Set(['tenant', 'principal', 'site', 'device', 'key', 'alarm', 'notification', 'work-order', 'asset', 'space', 'point', 'command']);
const allowedMethods = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE']);
const allowedRouteFields = new Set(['method', 'path', 'owner', 'publicIngress', 'revision', 'rollout', 'compatibilityMode', 'allowedScopeDimensions']);
const allowedRolloutFields = new Set(['mode']);
const canonicalOwners = new Map([
  ['POST /api/v1/auth/login', 'platform-gateway'],
  ['GET /api/v1/sites', 'platform-core-service'],
  ['GET /api/v1/sites/{siteId}', 'platform-core-service'],
  ['GET /api/v1/assets/{assetId}', 'platform-core-service'],
  ['GET /api/v1/devices/{deviceId}', 'platform-core-service'],
  ['POST /api/v1/commands', 'command-service'],
  ['GET /api/v1/commands/{commandId}', 'command-service'],
  ['POST /api/v1/commands/{commandId}/approve', 'command-service'],
  ['GET /api/v1/alarms', 'alarm-service'],
  ['GET /api/v1/alarms/{alarmId}', 'alarm-service'],
  ['POST /api/v1/alarms/{alarmId}/ack', 'alarm-service'],
]);

if (routeRegistry.registryVersion !== 1) errors.push('route registry version must be 1');
if (!Number.isInteger(routeRegistry.registryRevision) || routeRegistry.registryRevision < 1) errors.push('route registry revision must be positive');

const routeKeys = new Set();
for (const route of routeRegistry.routes ?? []) {
  const key = `${route.method} ${route.path}`;
  for (const field of Object.keys(route)) {
    if (!allowedRouteFields.has(field)) errors.push(`${key}: obsolete or unknown route field ${field}`);
  }
  if (!allowedMethods.has(route.method)) errors.push(`${key}: unsupported method`);
  if (typeof route.path !== 'string' || !route.path.startsWith('/api/v1/')) errors.push(`${key}: public path must start with /api/v1/`);
  if (routeKeys.has(key)) errors.push(`${key}: duplicate route owner`);
  routeKeys.add(key);
  if (!allowedOwners.has(route.owner)) errors.push(`${key}: unknown current owner ${route.owner}`);
  if (route.owner === 'platform-gateway') {
    if (route.publicIngress !== undefined) errors.push(`${key}: Gateway-owned route must not declare a second ingress owner`);
  } else if (route.publicIngress !== 'platform-gateway') {
    errors.push(`${key}: non-Gateway public route must enter through platform-gateway`);
  }
  if (!Number.isInteger(route.revision) || route.revision < 1) errors.push(`${key}: invalid revision`);
  if (route.compatibilityMode !== 'native') errors.push(`${key}: only native compatibility mode is allowed in the current registry`);
  for (const scope of route.allowedScopeDimensions ?? []) {
    if (!allowedScopes.has(scope)) errors.push(`${key}: invalid scope dimension ${scope}`);
  }
  if ((route.allowedScopeDimensions ?? []).includes('organization')) errors.push(`${key}: Organization scope is forbidden; use Tenant`);
  if (route.path.includes('/organizations') || route.path.includes('{organizationId}') || route.path.includes('/equipment') || route.path.includes('{equipmentId}')) {
    errors.push(`${key}: legacy Organization/Equipment public path is forbidden`);
  }
  if (route.path.includes(':approve') || route.path.includes(':acknowledge')) errors.push(`${key}: legacy colon Command/Alarm action path is forbidden`);
  if (route.path.includes('/alarms/') && (route.path.endsWith(':close') || route.path.endsWith(':reopen'))) errors.push(`${key}: legacy single-status Alarm lifecycle path is forbidden`);

  const rollout = route.rollout ?? {};
  for (const field of Object.keys(rollout)) {
    if (!allowedRolloutFields.has(field)) errors.push(`${key}: obsolete or unknown rollout field ${field}`);
  }
  if (!['all', 'disabled'].includes(rollout.mode)) errors.push(`${key}: only deterministic all-or-disabled activation is allowed`);
}
for (const [key, expectedOwner] of canonicalOwners) {
  const route = (routeRegistry.routes ?? []).find((entry) => `${entry.method} ${entry.path}` === key);
  if (!route) errors.push(`${key}: required current canonical route is missing`);
  else if (route.owner !== expectedOwner) errors.push(`${key}: owner must be ${expectedOwner}, got ${route.owner}`);
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
    const validNotificationRelay = access.service === 'notification-service' && access.schema === 'alarm_runtime'
      && Array.isArray(access.restrictedTo) && access.restrictedTo.join('|') === 'notification_outbox';
    if (!validGatewayRelay && !validTelemetryRelay && !validNotificationRelay) errors.push(`${access.service}:${access.schema}: invalid relay access`);
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

if (targetDomainModel.status !== 'TARGET_DOMAIN_MODEL_FINAL') errors.push('ownership checker requires the final ADR 0013 target Domain model');
if (targetDomainModel.ownershipRules?.singleAuthoritativeOwner !== true) errors.push('target Domain model must require one authoritative owner');
if (targetDomainModel.ownershipRules?.crossOwnerDirectDatabaseWritesForbidden !== true) errors.push('target Domain model must forbid cross-owner direct database writes');
const requiredMechanisms = ['owner-command-port', 'owner-query-port', 'immutable-owner-event', 'rebuildable-projection', 'identity-and-revision-reference'];
const declaredMechanisms = new Set(targetDomainModel.ownershipRules?.allowedCrossDomainMechanisms ?? []);
for (const mechanism of requiredMechanisms) {
  if (!declaredMechanisms.has(mechanism)) errors.push(`target Domain model is missing cross-domain mechanism ${mechanism}`);
}
const requiredContexts = ['iam', 'registry', 'telemetry-runtime', 'telemetry-query', 'metric', 'command', 'edge-control', 'rule-runtime', 'alarm', 'notification', 'outbound-delivery', 'work-order', 'intelligence', 'presentation', 'platform-operations'];
const declaredContexts = new Set((targetDomainModel.boundedContexts ?? []).map((context) => context.id));
for (const context of requiredContexts) {
  if (!declaredContexts.has(context)) errors.push(`target Domain model is missing bounded context ${context}`);
}
if ((targetDomainModel.canonicalLanguage?.forbiddenCanonicalTerms ?? []).join('|') !== 'Organization|Area|Equipment') {
  errors.push('target Domain model canonical vocabulary lock is missing');
}

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log(`Current ownership registries are ADR-0013-consistent: routes=${routeKeys.size}; resources=${resources.size}; singleOwner=true; crossOwnerDirectWrites=forbidden; cohortFallback=disabled`);
