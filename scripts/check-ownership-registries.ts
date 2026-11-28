import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

interface RouteEntry {
  method: string;
  path: string;
  owner: string;
  publicIngress?: string;
  revision: number;
  rollout?: { mode?: string };
  compatibilityMode?: string;
  allowedScopeDimensions?: string[];
}

interface RouteRegistry {
  registryVersion: number;
  registryRevision: number;
  routes?: RouteEntry[];
}

interface DataResource {
  kind: string;
  name: string;
  writer: string;
  revision: number;
}

interface DataAccess {
  service: string;
  schema: string;
  mode: string;
  restrictedTo?: string[];
}

interface DatabaseIdentity {
  schema: string;
  runtimeRole: string;
  runtimeBypassRls: boolean;
  restrictedTo?: string[];
}

interface DataRegistry {
  registryVersion: number;
  registryRevision: number;
  resources?: DataResource[];
  databaseAccess?: DataAccess[];
  databaseIdentities?: DatabaseIdentity[];
}

interface TargetDomainModel {
  status?: string;
  ownershipRules?: {
    singleAuthoritativeOwner?: boolean;
    crossOwnerDirectDatabaseWritesForbidden?: boolean;
    allowedCrossDomainMechanisms?: string[];
  };
  boundedContexts?: Array<{ id: string }>;
  canonicalLanguage?: {
    forbiddenCanonicalTerms?: string[];
  };
}

interface OpenApiSpec {
  paths?: Record<string, Record<string, unknown>>;
}

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const readJSON = async <T>(p: string): Promise<T> => JSON.parse(await readFile(path.join(root, p), 'utf8')) as T;

const routeRegistry = await readJSON<RouteRegistry>('contracts/ownership/route-ownership.v1.json');
const dataRegistry = await readJSON<DataRegistry>('contracts/ownership/data-ownership.v1.json');
const targetDomainModel = await readJSON<TargetDomainModel>('contracts/architecture/target-domain-model.v1.json');
const errors: string[] = [];
const fail = (m: string): void => {
  errors.push(m);
};

const allowedOwners = new Set([
  'connectivity',
  'platform-gateway', 'platform-core-service', 'telemetry-runtime-service', 'command-service',
  'telemetry-query-service', 'operations-agent-service', 'alarm-service', 'notification-service', 'work-order-service',
  'forecast-service', 'fdd-service', 'mv-service', 'report-service', 'optimization-service', 'metric-engine-service',
]);
const allowedScopes = new Set(['tenant', 'principal', 'site', 'device', 'key', 'alarm', 'work-order', 'asset', 'space', 'point', 'command']);
const allowedMethods = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE']);
const allowedRouteFields = new Set(['method', 'path', 'owner', 'publicIngress', 'revision', 'rollout', 'compatibilityMode', 'allowedScopeDimensions']);
const allowedRolloutFields = new Set(['mode']);
const canonicalOwners = new Map<string, string>([
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
  ['POST /api/v1/alarms/{alarmId}/assign', 'alarm-service'],
]);

if (routeRegistry.registryVersion !== 1) fail('route registry version must be 1');
if (!Number.isInteger(routeRegistry.registryRevision) || routeRegistry.registryRevision < 1) fail('route registry revision must be positive');

const routeKeys = new Set<string>();
for (const route of routeRegistry.routes ?? []) {
  const key = `${route.method} ${route.path}`;
  for (const field of Object.keys(route)) {
    if (!allowedRouteFields.has(field)) fail(`${key}: obsolete or unknown route field ${field}`);
  }
  if (!allowedMethods.has(route.method)) fail(`${key}: unsupported method`);
  if (typeof route.path !== 'string' || !route.path.startsWith('/api/v1/')) fail(`${key}: public path must start with /api/v1/`);
  if (routeKeys.has(key)) fail(`${key}: duplicate route owner`);
  routeKeys.add(key);
  if (!allowedOwners.has(route.owner)) fail(`${key}: unknown current owner ${route.owner}`);
  if (route.owner === 'platform-gateway') {
    if (route.publicIngress !== undefined) fail(`${key}: Gateway-owned route must not declare a second ingress owner`);
  } else if (route.publicIngress !== 'platform-gateway') {
    fail(`${key}: non-Gateway public route must enter through platform-gateway`);
  }
  if (!Number.isInteger(route.revision) || route.revision < 1) fail(`${key}: invalid revision`);
  if (route.compatibilityMode !== 'native') fail(`${key}: only native compatibility mode is allowed in the current registry`);
  for (const scope of route.allowedScopeDimensions ?? []) {
    if (!allowedScopes.has(scope)) fail(`${key}: invalid scope dimension ${scope}`);
  }
  if ((route.allowedScopeDimensions ?? []).includes('organization')) fail(`${key}: Organization scope is forbidden; use Tenant`);
  if (route.path.includes('/organizations') || route.path.includes('{organizationId}') || route.path.includes('/equipment') || route.path.includes('{equipmentId}')) {
    fail(`${key}: legacy Organization/Equipment public path is forbidden`);
  }
  if (route.path.includes(':approve') || route.path.includes(':acknowledge')) fail(`${key}: legacy colon Command/Alarm action path is forbidden`);
  if (route.path.includes('/alarms/') && (route.path.endsWith(':close') || route.path.endsWith(':reopen'))) fail(`${key}: legacy single-status Alarm lifecycle path is forbidden`);

  const rollout = route.rollout ?? {};
  for (const field of Object.keys(rollout)) {
    if (!allowedRolloutFields.has(field)) fail(`${key}: obsolete or unknown rollout field ${field}`);
  }
  if (rollout.mode !== 'all' && rollout.mode !== 'disabled') fail(`${key}: only deterministic all-or-disabled activation is allowed`);
}
for (const [key, expectedOwner] of canonicalOwners) {
  const route = (routeRegistry.routes ?? []).find((entry) => `${entry.method} ${entry.path}` === key);
  if (!route) fail(`${key}: required current canonical route is missing`);
  else if (route.owner !== expectedOwner) fail(`${key}: owner must be ${expectedOwner}, got ${route.owner}`);
}
// The Gateway answers 404 for any public route without an owner here, while its handler
// tests skip the registry, so every contracted Gateway route must be registered, and every
// public route in route-ownership must carry an OpenAPI contract outside the frozen baseline.
const publicOpenApiSpecs = [
  'contracts/http/platform-gateway.openapi.yaml',
  'contracts/http/operations-agent-sessions-public.openapi.json',
  'contracts/http/s05-registry-writer.openapi.json',
  'contracts/http/s2-telemetry-public.openapi.json',
  'contracts/http/s3-command-public.openapi.json',
  'contracts/http/s4-alarm-public.openapi.json',
];
const uncontractedPublicRoutesBaseline = new Set([
  'GET /api/v1/provisioning/batches',
  'POST /api/v1/provisioning/batches',
  'GET /api/v1/provisioning/batches/{batchId}',
  'POST /api/v1/provisioning/batches/{batchId}/devices',
  'POST /api/v1/provisioning/batches/{batchId}/revoke',
  'GET /api/v1/sites/{siteId}/work-orders',
  'GET /api/v1/sites/{siteId}/work-orders/{workOrderId}',
  'POST /api/v1/sites/{siteId}/work-orders',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:assign',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:plan',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:start',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:block',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:resume',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:complete',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:cancel',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:reopen',
  'POST /api/v1/sites/{siteId}/optimization/auto-commands',
]);
const openApiRoutes = new Set<string>();
for (const specPath of publicOpenApiSpecs) {
  const contract = await readJSON<OpenApiSpec>(specPath);
  for (const [contractPath, operations] of Object.entries(contract.paths ?? {})) {
    for (const method of Object.keys(operations ?? {})) {
      const upper = method.toUpperCase();
      if (!allowedMethods.has(upper) || !contractPath.startsWith('/api/v1/')) continue;
      const key = `${upper} ${contractPath}`;
      openApiRoutes.add(key);
      if (!routeKeys.has(key)) fail(`${key}: in ${specPath} but has no route owner`);
    }
  }
}
for (const key of routeKeys) {
  if (!openApiRoutes.has(key) && !uncontractedPublicRoutesBaseline.has(key)) {
    fail(`${key}: registered in route-ownership.v1.json without an OpenAPI contract`);
  }
}
for (const baselineKey of uncontractedPublicRoutesBaseline) {
  if (!routeKeys.has(baselineKey)) fail(`${baselineKey}: stale entry in uncontractedPublicRoutesBaseline; remove it`);
  else if (openApiRoutes.has(baselineKey)) fail(`${baselineKey}: now has an OpenAPI contract; shrink uncontractedPublicRoutesBaseline`);
}

if (dataRegistry.registryVersion !== 1) fail('data registry version must be 1');
if (!Number.isInteger(dataRegistry.registryRevision) || dataRegistry.registryRevision < 1) fail('data registry revision must be positive');

const resources = new Map<string, DataResource>();
for (const resource of dataRegistry.resources ?? []) {
  const key = `${resource.kind}:${resource.name}`;
  if (resources.has(key)) fail(`${key}: duplicate writer`);
  resources.set(key, resource);
  if (typeof resource.writer !== 'string' || resource.writer.length === 0) fail(`${key}: writer is required`);
  if (!Number.isInteger(resource.revision) || resource.revision < 1) fail(`${key}: invalid revision`);
  if (resource.name === 'legacy' || resource.writer === 'legacy-hvac-backend') fail(`${key}: legacy compatibility ownership is forbidden`);
}

for (const access of dataRegistry.databaseAccess ?? []) {
  const schema = resources.get(`schema:${access.schema}`);
  if (!schema) {
    fail(`${access.service}:${access.schema}: schema has no declared writer`);
    continue;
  }
  if (access.service === 'legacy-migration-service' || access.service === 'legacy-hvac-backend') fail(`${access.service}:${access.schema}: legacy compatibility access is forbidden`);
  if (access.mode === 'write' && access.service !== schema.writer) fail(`${access.service}:${access.schema}: forbidden cross-service writer`);
  if (access.mode === 'relay') {
    const validGatewayRelay = access.service === 'audit-ledger-service' && access.schema === 'gateway';
    const validTelemetryRelay = access.service === 'telemetry-worker' && access.schema === 'telemetry_runtime'
      && Array.isArray(access.restrictedTo) && access.restrictedTo.join('|') === 'telemetry_publication_outbox';
    const validNotificationRelay = access.service === 'notification-service' && access.schema === 'alarm_runtime'
      && Array.isArray(access.restrictedTo) && access.restrictedTo.join('|') === 'notification_outbox';
    if (!validGatewayRelay && !validTelemetryRelay && !validNotificationRelay) fail(`${access.service}:${access.schema}: invalid relay access`);
  } else if (access.mode === 'reconciliation') {
    if (access.service !== 'iam-reconciler' || access.schema !== 'iam') fail(`${access.service}:${access.schema}: invalid reconciliation access`);
  } else if (!['write', 'read', 'relay', 'reconciliation', 'grant-state', 'connect-only'].includes(access.mode)) {
    fail(`${access.service}:${access.schema}: invalid current access mode`);
  }
}

for (const identity of dataRegistry.databaseIdentities ?? []) {
  const key = `${identity.schema}:${identity.runtimeRole}`;
  if (identity.runtimeBypassRls !== false) fail(`${key}: runtime identity must not bypass RLS`);
  if (identity.runtimeRole === 's1_migration_operator' || identity.runtimeRole === 's1_legacy_migration_service') fail(`${key}: legacy migration identity is forbidden in the active registry`);
  for (const table of identity.restrictedTo ?? []) {
    if (table === 'organizations' || table === 'equipment' || table === 'organization_memberships') fail(`${key}: legacy Organization/Equipment table scope is forbidden`);
  }
}

const routeText = JSON.stringify(routeRegistry);
const dataText = JSON.stringify(dataRegistry);
if (/organization/i.test(routeText)) fail('active route ownership registry still contains Organization vocabulary');
if (/organization/i.test(dataText)) fail('active data ownership registry still contains Organization vocabulary');

if (targetDomainModel.status !== 'TARGET_DOMAIN_MODEL_FINAL') fail('ownership checker requires the final ADR 0013 target Domain model');
if (targetDomainModel.ownershipRules?.singleAuthoritativeOwner !== true) fail('target Domain model must require one authoritative owner');
if (targetDomainModel.ownershipRules?.crossOwnerDirectDatabaseWritesForbidden !== true) fail('target Domain model must forbid cross-owner direct database writes');
const requiredMechanisms = ['owner-command-port', 'owner-query-port', 'immutable-owner-event', 'rebuildable-projection', 'identity-and-revision-reference'];
const declaredMechanisms = new Set(targetDomainModel.ownershipRules?.allowedCrossDomainMechanisms ?? []);
for (const mechanism of requiredMechanisms) {
  if (!declaredMechanisms.has(mechanism)) fail(`target Domain model is missing cross-domain mechanism ${mechanism}`);
}
const requiredContexts = ['iam', 'registry', 'telemetry-runtime', 'telemetry-query', 'metric', 'command', 'edge-control', 'alarm', 'notification', 'work-order', 'intelligence', 'presentation', 'platform-operations'];
const declaredContexts = new Set((targetDomainModel.boundedContexts ?? []).map((context) => context.id));
for (const context of requiredContexts) {
  if (!declaredContexts.has(context)) fail(`target Domain model is missing bounded context ${context}`);
}
if ((targetDomainModel.canonicalLanguage?.forbiddenCanonicalTerms ?? []).join('|') !== 'Organization|Area|Equipment') {
  fail('target Domain model canonical vocabulary lock is missing');
}

if (errors.length > 0) console.error(errors.map((e) => `- ${e}`).join('\n'));
const boundary = spawnSync(process.execPath, ['--experimental-strip-types', path.join(root, 'scripts/check-ownership-boundaries.ts')], { stdio: 'inherit' });
if (boundary.error) throw boundary.error;
if (errors.length > 0 || boundary.status !== 0) process.exit(boundary.status || 1);

console.log(`Current ownership registries are ADR-0013-consistent: routes=${routeKeys.size}; resources=${resources.size}; singleOwner=true; crossOwnerDirectWrites=forbidden; cohortFallback=disabled`);
