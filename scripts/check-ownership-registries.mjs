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
const allowedOwners = new Set(['platform-gateway', 'legacy-hvac-backend', 'platform-core-service', 'telemetry-runtime-service', 'command-service', 'telemetry-query-service', 'operations-agent-service', 'alarm-service', 'work-order-service']);
const s1MigratedPaths = new Set([
  '/api/v1/organizations',
  '/api/v1/organizations/{organizationId}',
  '/api/v1/organizations/{organizationId}/sites',
  '/api/v1/sites/{siteId}',
  '/api/v1/sites/{siteId}/equipment',
  '/api/v1/equipment/{equipmentId}',
  '/api/v1/sites/{siteId}/devices',
  '/api/v1/devices/{deviceId}',
]);
const s1NativeAdditivePaths = new Set([
  '/api/v1/sites/{siteId}/device-bindings',
]);
const s1RegistryPaths = new Set([...s1MigratedPaths, ...s1NativeAdditivePaths]);
const expectedMigrationPhases = [
  'LEGACY_PRIMARY_GO_SHADOW',
  'GO_CANARY_LEGACY_SHADOW',
  'GO_PRIMARY_LEGACY_READ_FALLBACK',
  'GO_PRIMARY',
];
const s2TelemetryRoutes = new Map([
  ['GET /api/v1/devices/{deviceId}/observation-snapshot', 'getDeviceObservationSnapshot'],
  ['POST /api/v1/telemetry/observation-snapshots:batchGet', 'batchGetDeviceObservationSnapshots'],
  ['POST /api/v1/telemetry/subscriptions:bootstrap', 'bootstrapTelemetrySubscriptions'],
  ['POST /api/v1/telemetry/recovery-cursors:checkpoint', 'checkpointTelemetryRecoveryCursors'],
]);
const expectedS2MigrationPhases = [
  'R0-contract-only',
  'R1-dark-ingest',
  'R2-shadow-compare',
  'R3-internal-canary',
  'R4-external-canary-5',
  'R5-ramp-25',
  'R6-ramp-50',
  'R7-primary-100',
  'R8-legacy-current-state-retired',
];
const s3CommandRoutes = new Set([
  'POST /api/v1/commands',
  'GET /api/v1/commands/{commandId}',
  'POST /api/v1/commands/{commandId}:approve',
]);
const expectedS3MigrationPhases = [
  'S3-R0-contract-only',
  'S3-R1-synthetic-only',
  'S3-R2-internal-low-risk',
  'S3-R3-site-canary',
  'S3-R4-operationally-certified',
];
const s4AlarmReadRoutes = new Set([
  'GET /api/v1/sites/{siteId}/alarms',
  'GET /api/v1/sites/{siteId}/alarms/{alarmId}',
]);
const s4AlarmLifecycleRoutes = new Set([
  'POST /api/v1/sites/{siteId}/alarms/{alarmId}:acknowledge',
  'POST /api/v1/sites/{siteId}/alarms/{alarmId}:assign',
  'POST /api/v1/sites/{siteId}/alarms/{alarmId}:unassign',
  'POST /api/v1/sites/{siteId}/alarms/{alarmId}:suppress',
  'POST /api/v1/sites/{siteId}/alarms/{alarmId}:unsuppress',
  'POST /api/v1/sites/{siteId}/alarms/{alarmId}:close',
  'POST /api/v1/sites/{siteId}/alarms/{alarmId}:reopen',
]);
const expectedS4ReadPhases = ['S4-R0-contract-only', 'S4-R1-internal-read-only', 'S4-R2-site-canary', 'S4-R3-operationally-certified'];
const expectedS4LifecyclePhases = ['S4-R0-contract-only', 'S4-R1-internal-lifecycle', 'S4-R2-site-canary', 'S4-R3-operationally-certified'];
const s5WorkOrderReadRoutes = new Set([
  'GET /api/v1/sites/{siteId}/work-orders',
  'GET /api/v1/sites/{siteId}/work-orders/{workOrderId}',
]);
const s5WorkOrderWriteRoutes = new Set([
  'POST /api/v1/sites/{siteId}/work-orders',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:assign',
]);
const s5WorkOrderLifecycleRoutes = new Set([
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:plan',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:start',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:block',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:resume',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:complete',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:cancel',
  'POST /api/v1/sites/{siteId}/work-orders/{workOrderId}:reopen',
]);
const expectedS5ReadPhases = ['S5-R0-contract-only', 'S5-R1-internal-read-only', 'S5-R2-site-canary', 'S5-R3-operationally-certified'];
const expectedS5WritePhases = ['S5-R0-contract-only', 'S5-R1-internal-read-only', 'S5-R1-internal-create-assign', 'S5-R2-site-canary', 'S5-R3-operationally-certified'];
const expectedS5LifecyclePhases = ['S5-R0-contract-only', 'S5-R1-internal-read-only', 'S5-R1-internal-create-assign', 'S5-R1-internal-lifecycle', 'S5-R2-site-canary', 'S5-R3-operationally-certified'];
const allowedScopes = new Set(['organization', 'principal', 'site', 'device', 'key', 'alarm', 'work-order']);
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
  } else if (rollout.mode === 'disabled') {
    for (const forbidden of ['percentage', 'fallbackOwner', 'cohortSalt']) {
      if (rollout[forbidden] !== undefined) errors.push(`${key}: disabled rollout cannot declare ${forbidden}`);
    }
  } else if (rollout.mode === 'percentage') {
    if (!Number.isInteger(rollout.percentage) || rollout.percentage < 0 || rollout.percentage > 100) errors.push(`${key}: rollout percentage must be 0..100`);
    const noFallbackReviewedCanary = rollout.fallbackOwner === undefined && ((route.method === 'GET' && ((route.owner === 'alarm-service' && route.migrationPhase === 'S4-R1-internal-read-only') || (route.owner === 'work-order-service' && route.migrationPhase === 'S5-R1-internal-read-only'))) || (route.method === 'POST' && route.owner === 'work-order-service' && (route.migrationPhase === 'S5-R1-internal-create-assign' || route.migrationPhase === 'S5-R1-internal-lifecycle')));
    if (!noFallbackReviewedCanary && (!allowedOwners.has(rollout.fallbackOwner) || rollout.fallbackOwner === route.owner)) errors.push(`${key}: invalid fallback owner`);
    if (typeof rollout.cohortSalt !== 'string' || rollout.cohortSalt.length < 8) errors.push(`${key}: cohort salt is required`);
    if (!(route.allowedScopeDimensions ?? []).includes('organization') || !(route.allowedScopeDimensions ?? []).includes('principal')) {
      errors.push(`${key}: percentage rollout requires organization and principal scope dimensions`);
    }
  } else {
    errors.push(`${key}: rollout mode must be all, disabled or percentage`);
  }
  if (s1RegistryPaths.has(route.path)) {
    if (route.method !== 'GET') errors.push(`${key}: S1 Registry route must be read-only`);
    if (route.owner !== 'platform-core-service' || rollout.mode !== 'all' || route.compatibilityMode !== 'native') {
      errors.push(`${key}: final S1 route must be Core primary without cohort routing`);
    }
    if (route.migrationPhase !== 'GO_PRIMARY' || route.readFallbackOwner !== undefined) {
      errors.push(`${key}: active S1 route must finish in GO_PRIMARY without Legacy fallback`);
    }
    if (s1MigratedPaths.has(route.path)) {
      if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedMigrationPhases.join('|')) {
        errors.push(`${key}: S1 migration phases are incomplete or reordered`);
      }
    } else if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== 'GO_PRIMARY') {
      errors.push(`${key}: native additive S1 route must declare GO_PRIMARY only`);
    }
    if (route.shadowSideEffectPolicy !== 'NONE') errors.push(`${key}: S1 shadow must be side-effect free`);
    if (route.readOnlyFallback !== false) errors.push(`${key}: active S1 route must not advertise runtime fallback`);
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    if (!forbiddenResults.includes('AUTHORIZATION_DENIED') || !forbiddenResults.includes('RESOURCE_NOT_FOUND')) {
      errors.push(`${key}: S1 fallback must be forbidden after denial or resource invisibility`);
    }
  }
  if (s2TelemetryRoutes.has(key)) {
    if (route.owner !== 'telemetry-runtime-service' || route.publicIngress !== 'platform-gateway') {
      errors.push(`${key}: S2 route must keep Telemetry Runtime business ownership behind Gateway ingress`);
    }
    if (route.activationStatus !== 'expand-baseline' || rollout.mode !== 'disabled') {
      errors.push(`${key}: S2 baseline must be registered but carry zero production traffic`);
    }
    if (route.migrationPhase !== 'R0-contract-only' || route.readFallbackOwner !== undefined || route.readOnlyFallback !== false) {
      errors.push(`${key}: S2 baseline must remain R0 without request fallback`);
    }
    if (route.cohortGroup !== 's2-current-state-v1') errors.push(`${key}: S2 route must use the shared current-state cohort group`);
    if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedS2MigrationPhases.join('|')) {
      errors.push(`${key}: S2 rollout phases are incomplete or reordered`);
    }
    if (route.shadowSideEffectPolicy !== 'NONE') errors.push(`${key}: S2 shadow policy must be side-effect free`);
    for (const scope of ['organization', 'site', 'device', 'principal', 'key']) {
      if (!(route.allowedScopeDimensions ?? []).includes(scope)) errors.push(`${key}: missing S2 scope ${scope}`);
    }
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND', 'REVISION_GAP', 'RECOVERY_FAILED']) {
      if (!forbiddenResults.includes(result)) errors.push(`${key}: fallback is not forbidden for ${result}`);
    }
  }
  if (s3CommandRoutes.has(key)) {
    if (route.owner !== 'command-service' || route.publicIngress !== 'platform-gateway') {
      errors.push(`${key}: S3 route must keep Command business ownership behind Gateway ingress`);
    }
    if (route.activationStatus !== 'expand-baseline' || rollout.mode !== 'disabled') {
      errors.push(`${key}: S3 baseline must carry zero production control traffic`);
    }
    if (route.migrationPhase !== 'S3-R0-contract-only' || route.readOnlyFallback !== false) {
      errors.push(`${key}: S3 baseline must remain contract-only without fallback`);
    }
    if (route.cohortGroup !== 's3-command-v1') errors.push(`${key}: S3 route must use the command cohort group`);
    if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedS3MigrationPhases.join('|')) {
      errors.push(`${key}: S3 rollout phases are incomplete or reordered`);
    }
    if (route.shadowSideEffectPolicy !== 'SYNTHETIC_ONLY') errors.push(`${key}: S3 baseline must be Synthetic-only`);
    for (const scope of ['organization', 'site', 'device', 'principal']) {
      if (!(route.allowedScopeDimensions ?? []).includes(scope)) errors.push(`${key}: missing S3 scope ${scope}`);
    }
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND', 'CURRENT_STATE_UNSAFE', 'OUTCOME_UNKNOWN']) {
      if (!forbiddenResults.includes(result)) errors.push(`${key}: fallback is not forbidden for ${result}`);
    }
  }
  if (s4AlarmReadRoutes.has(key) || s4AlarmLifecycleRoutes.has(key)) {
    if (route.owner !== 'alarm-service' || route.publicIngress !== 'platform-gateway') errors.push(`${key}: S4 Alarm route must remain behind Gateway ingress`);
    const lifecycle = s4AlarmLifecycleRoutes.has(key);
    if (lifecycle) {
      if (route.activationStatus !== 'expand-baseline' || rollout.mode !== 'disabled' || route.migrationPhase !== 'S4-R0-contract-only') errors.push(`${key}: S4 Alarm lifecycle must remain contract-only at zero traffic`);
    } else {
      if (route.activationStatus !== 'internal-canary' || rollout.mode !== 'percentage' || rollout.percentage !== 1 || rollout.fallbackOwner !== undefined || typeof rollout.cohortSalt !== 'string' || route.migrationPhase !== 'S4-R1-internal-read-only') errors.push(`${key}: S4 Alarm read must use the no-fallback 1% internal canary`);
    }
    if (route.readOnlyFallback !== false) errors.push(`${key}: S4 Alarm request fallback is forbidden`);
    const expectedPhases = lifecycle ? expectedS4LifecyclePhases : expectedS4ReadPhases;
    const expectedCohort = lifecycle ? 's4-alarm-lifecycle-v1' : 's4-alarm-read-v1';
    const expectedShadow = lifecycle ? 'SYNTHETIC_ONLY' : 'NONE';
    if (route.cohortGroup !== expectedCohort) errors.push(`${key}: S4 Alarm cohort group drifted`);
    if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedPhases.join('|')) errors.push(`${key}: S4 Alarm phases are incomplete or reordered`);
    if (route.shadowSideEffectPolicy !== expectedShadow) errors.push(`${key}: S4 Alarm shadow policy drifted`);
    for (const scope of lifecycle ? ['organization', 'site', 'alarm', 'principal', 'key'] : ['organization', 'site', 'principal']) {
      if (!(route.allowedScopeDimensions ?? []).includes(scope)) errors.push(`${key}: missing S4 Alarm scope ${scope}`);
    }
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    for (const result of lifecycle ? ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND', 'VERSION_CONFLICT', 'IDEMPOTENCY_CONFLICT'] : ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND']) {
      if (!forbiddenResults.includes(result)) errors.push(`${key}: S4 Alarm fallback is not forbidden for ${result}`);
    }
  }
  if (s5WorkOrderWriteRoutes.has(key)) {
    if (route.owner !== 'work-order-service' || route.publicIngress !== 'platform-gateway') errors.push(`${key}: S5 Work Order create/assign must remain behind Gateway ingress`);
    if (route.activationStatus !== 'internal-canary' || rollout.mode !== 'percentage' || rollout.percentage !== 1 || rollout.fallbackOwner !== undefined || typeof rollout.cohortSalt !== 'string' || route.migrationPhase !== 'S5-R1-internal-create-assign') errors.push(`${key}: S5 Work Order create/assign must use the no-fallback 1% internal canary`);
    if (route.readOnlyFallback !== false || route.readFallbackOwner !== undefined || rollout.fallbackOwner !== undefined) errors.push(`${key}: S5 Work Order create/assign fallback is forbidden`);
    if (route.cohortGroup !== 's5-work-order-write-v1') errors.push(`${key}: S5 Work Order create/assign cohort group drifted`);
    if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedS5WritePhases.join('|')) errors.push(`${key}: S5 Work Order create/assign phases are incomplete or reordered`);
    if (route.shadowSideEffectPolicy !== 'NONE') errors.push(`${key}: S5 Work Order create/assign shadow must be disabled`);
    for (const scope of ['organization', 'site', 'principal', 'key']) {
      if (!(route.allowedScopeDimensions ?? []).includes(scope)) errors.push(`${key}: missing S5 Work Order create/assign scope ${scope}`);
    }
    if (route.path.includes('{workOrderId}') && !(route.allowedScopeDimensions ?? []).includes('work-order')) errors.push(`${key}: missing S5 Work Order identity scope`);
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND', 'VERSION_CONFLICT', 'IDEMPOTENCY_CONFLICT']) {
      if (!forbiddenResults.includes(result)) errors.push(`${key}: S5 Work Order create/assign fallback is not forbidden for ${result}`);
    }
  }
  if (s5WorkOrderLifecycleRoutes.has(key)) {
    if (route.owner !== 'work-order-service' || route.publicIngress !== 'platform-gateway') errors.push(`${key}: S5 Work Order lifecycle must remain behind Gateway ingress`);
    if (route.activationStatus !== 'internal-canary' || rollout.mode !== 'percentage' || rollout.percentage !== 1 || rollout.fallbackOwner !== undefined || typeof rollout.cohortSalt !== 'string' || route.migrationPhase !== 'S5-R1-internal-lifecycle') errors.push(`${key}: S5 Work Order lifecycle must use the no-fallback 1% internal canary`);
    if (route.readOnlyFallback !== false || route.readFallbackOwner !== undefined || rollout.fallbackOwner !== undefined) errors.push(`${key}: S5 Work Order lifecycle fallback is forbidden`);
    if (route.cohortGroup !== 's5-work-order-lifecycle-v1') errors.push(`${key}: S5 Work Order lifecycle cohort group drifted`);
    if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedS5LifecyclePhases.join('|')) errors.push(`${key}: S5 Work Order lifecycle phases are incomplete or reordered`);
    if (route.shadowSideEffectPolicy !== 'NONE') errors.push(`${key}: S5 Work Order lifecycle shadow must be disabled`);
    for (const scope of ['organization', 'site', 'principal', 'work-order', 'key']) {
      if (!(route.allowedScopeDimensions ?? []).includes(scope)) errors.push(`${key}: missing S5 Work Order lifecycle scope ${scope}`);
    }
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND', 'VERSION_CONFLICT', 'IDEMPOTENCY_CONFLICT']) {
      if (!forbiddenResults.includes(result)) errors.push(`${key}: S5 Work Order lifecycle fallback is not forbidden for ${result}`);
    }
  }
  if (s5WorkOrderReadRoutes.has(key)) {
    if (route.owner !== 'work-order-service' || route.publicIngress !== 'platform-gateway') errors.push(`${key}: S5 Work Order route must remain behind Gateway ingress`);
    if (route.activationStatus !== 'internal-canary' || rollout.mode !== 'percentage' || rollout.percentage !== 1 || rollout.fallbackOwner !== undefined || typeof rollout.cohortSalt !== 'string' || route.migrationPhase !== 'S5-R1-internal-read-only') errors.push(`${key}: S5 Work Order read must use the no-fallback 1% internal canary`);
    if (route.readOnlyFallback !== false || route.readFallbackOwner !== undefined || rollout.fallbackOwner !== undefined) errors.push(`${key}: S5 Work Order fallback is forbidden`);
    if (route.cohortGroup !== 's5-work-order-read-v1') errors.push(`${key}: S5 Work Order cohort group drifted`);
    if (!Array.isArray(route.migrationPhases) || route.migrationPhases.join('|') !== expectedS5ReadPhases.join('|')) errors.push(`${key}: S5 Work Order phases are incomplete or reordered`);
    if (route.shadowSideEffectPolicy !== 'NONE') errors.push(`${key}: S5 Work Order read must be side-effect-free`);
    for (const scope of ['organization', 'site', 'principal']) {
      if (!(route.allowedScopeDimensions ?? []).includes(scope)) errors.push(`${key}: missing S5 Work Order scope ${scope}`);
    }
    if (route.path.includes('{workOrderId}') && !(route.allowedScopeDimensions ?? []).includes('work-order')) errors.push(`${key}: missing S5 Work Order identity scope`);
    const forbiddenResults = route.fallbackForbiddenResults ?? [];
    for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND']) {
      if (!forbiddenResults.includes(result)) errors.push(`${key}: S5 Work Order fallback is not forbidden for ${result}`);
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
for (const key of s2TelemetryRoutes.keys()) {
  if (!routeKeys.has(key)) errors.push(`${key}: required S2 route is missing`);
}
for (const key of s3CommandRoutes) {
  if (!routeKeys.has(key)) errors.push(`${key}: required S3 route is missing`);
}
for (const key of s4AlarmReadRoutes) {
  if (!routeKeys.has(key)) errors.push(`${key}: required S4 read route is missing`);
}
for (const key of s4AlarmLifecycleRoutes) {
  if (!routeKeys.has(key)) errors.push(`${key}: required S4 lifecycle route is missing`);
}
for (const key of s5WorkOrderReadRoutes) {
  if (!routeKeys.has(key)) errors.push(`${key}: required S5 Work Order read route is missing`);
}
for (const key of s5WorkOrderWriteRoutes) {
  if (!routeKeys.has(key)) errors.push(`${key}: required S5 Work Order create/assign route is missing`);
}
for (const key of s5WorkOrderLifecycleRoutes) {
  if (!routeKeys.has(key)) errors.push(`${key}: required S5 Work Order lifecycle route is missing`);
}

const phaseExpectations = [
  { revision: 3, routeRevision: 2, phase: 'LEGACY_PRIMARY_GO_SHADOW', owner: 'legacy-hvac-backend', compatibility: 'legacy-read', rolloutMode: 'percentage', percentage: 100, fallbackOwner: 'platform-core-service', readOnlyFallback: true },
  { revision: 4, routeRevision: 3, phase: 'GO_CANARY_LEGACY_SHADOW', owner: 'platform-core-service', compatibility: 'native', rolloutMode: 'percentage', percentage: 10, fallbackOwner: 'legacy-hvac-backend', readOnlyFallback: true },
  { revision: 5, routeRevision: 4, phase: 'GO_PRIMARY_LEGACY_READ_FALLBACK', owner: 'platform-core-service', compatibility: 'native', rolloutMode: 'all', readFallbackOwner: 'legacy-hvac-backend', readOnlyFallback: true },
  { revision: 7, routeRevision: 5, phase: 'GO_PRIMARY', owner: 'platform-core-service', compatibility: 'native', rolloutMode: 'all', readOnlyFallback: false },
];
for (let index = 0; index < phaseRegistries.length; index += 1) {
  const registry = phaseRegistries[index];
  const expected = phaseExpectations[index];
  if (registry.registryRevision !== expected.revision) errors.push(`S1 phase ${expected.phase}: registry revision mismatch`);
  const phaseRoutes = (registry.routes ?? []).filter((route) => s1RegistryPaths.has(route.path));
  const expectedPaths = index === phaseRegistries.length - 1 ? s1RegistryPaths : s1MigratedPaths;
  if (phaseRoutes.length !== expectedPaths.size) errors.push(`S1 phase ${expected.phase}: route set is incomplete`);
  for (const route of phaseRoutes) {
    const key = `${route.method} ${route.path}`;
    if (s1NativeAdditivePaths.has(route.path)) {
      if (index !== phaseRegistries.length - 1 || route.revision !== 1 || route.migrationPhase !== 'GO_PRIMARY' || route.owner !== 'platform-core-service' || route.compatibilityMode !== 'native') errors.push(`${key}: native additive ownership drifted`);
      if (route.rollout?.mode !== 'all' || route.readOnlyFallback !== false || route.readFallbackOwner !== undefined) errors.push(`${key}: native additive rollout drifted`);
      continue;
    }
    if (route.revision !== expected.routeRevision || route.migrationPhase !== expected.phase || route.owner !== expected.owner || route.compatibilityMode !== expected.compatibility) errors.push(`${key}: phase ${expected.phase} ownership drifted`);
    if (route.rollout?.mode !== expected.rolloutMode || route.rollout?.percentage !== expected.percentage || route.rollout?.fallbackOwner !== expected.fallbackOwner) errors.push(`${key}: phase ${expected.phase} rollout drifted`);
    if ((route.readFallbackOwner ?? undefined) !== expected.readFallbackOwner) errors.push(`${key}: phase ${expected.phase} read fallback drifted`);
    if (route.shadowSideEffectPolicy !== 'NONE' || route.readOnlyFallback !== expected.readOnlyFallback) errors.push(`${key}: phase ${expected.phase} safety policy drifted`);
  }
}
const activeS1Routes = (routeRegistry.routes ?? []).filter((route) => s1RegistryPaths.has(route.path));
const finalS1Routes = (phaseRegistries.at(-1)?.routes ?? []).filter((route) => s1RegistryPaths.has(route.path));
if (JSON.stringify(activeS1Routes) !== JSON.stringify(finalS1Routes)) errors.push('active S1 Route Ownership subset is not the final GO_PRIMARY phase asset');

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
    const validGatewayRelay = access.service === 'outbox-relay' && access.schema === 'gateway';
    const validTelemetryRelay = access.service === 'outbox-relay'
      && access.schema === 'telemetry_runtime'
      && Array.isArray(access.restrictedTo)
      && access.restrictedTo.join('|') === 'telemetry_publication_outbox';
    if (!validGatewayRelay && !validTelemetryRelay) errors.push(`${access.service}:${access.schema}: invalid relay access`);
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
  ['iam:s1_iam_reconciler', { migrationRole: 's1_iam_migrator', restrictedTo: ['principals', 'organization_memberships', 'role_bindings', 'site_bindings', 'explicit_denies', 'reconciliation_state', 'reconciliation_events', 'reconciliation_quarantine'] }],
  ['iam:s2_iam_grant_runtime', { migrationRole: 's1_iam_migrator', accessMode: 'grant-state', restrictedTo: ['telemetry_grant_revocations', 'telemetry_grant_uses', 'telemetry_revocation_facts'] }],
  ['core_registry:s1_core_runtime', { migrationRole: 's1_core_migrator' }],
  ['core_registry:s1_core_service', { migrationRole: 's1_core_migrator', activationRole: 's1_core_runtime' }],
  ['core_registry:s1_migration_operator', { migrationRole: 's1_core_migrator', restrictedTo: ['organizations', 'sites', 'equipment', 'devices', 'legacy_resource_maps', 'migration_provenance', 'migration_quarantine'] }],
  ['core_registry:s1_legacy_migration_service', { migrationRole: 's1_core_migrator', activationRole: 's1_migration_operator', restrictedTo: ['organizations', 'sites', 'equipment', 'devices', 'legacy_resource_maps', 'migration_provenance', 'migration_quarantine'] }],
  ['telemetry_runtime:s2_telemetry_migrator_service', { migrationRole: 's2_telemetry_migrator', activationRole: 's2_telemetry_migrator', accessMode: 'migration' }],
  ['telemetry_runtime:s2_telemetry_runtime', { migrationRole: 's2_telemetry_migrator', accessMode: 'write' }],
  ['telemetry_runtime:s2_telemetry_service', { migrationRole: 's2_telemetry_migrator', activationRole: 's2_telemetry_runtime', accessMode: 'write' }],
  ['telemetry_runtime:s2_telemetry_relay', { migrationRole: 's2_telemetry_migrator', accessMode: 'relay', restrictedTo: ['telemetry_publication_outbox'] }],
  ['telemetry_runtime:s2_telemetry_relay_service', { migrationRole: 's2_telemetry_migrator', activationRole: 's2_telemetry_relay', accessMode: 'relay', restrictedTo: ['telemetry_publication_outbox'] }],
  ['telemetry_runtime:s2_telemetry_gateway', { migrationRole: 's2_telemetry_migrator', accessMode: 'connect-only', restrictedTo: [] }],
  ['telemetry_runtime:s2_telemetry_iam', { migrationRole: 's2_telemetry_migrator', accessMode: 'connect-only', restrictedTo: [] }],
  ['command_runtime:s3_command_runtime', { migrationRole: 's3_command_migrator', accessMode: 'write' }],
  ['command_runtime:s3_command_service', { migrationRole: 's3_command_migrator', activationRole: 's3_command_runtime', accessMode: 'write' }],
  ['alarm_runtime:s4_alarm_runtime', { migrationRole: 's4_alarm_migrator', accessMode: 'write' }],
  ['alarm_runtime:s4_alarm_service', { migrationRole: 's4_alarm_migrator', activationRole: 's4_alarm_runtime', accessMode: 'write' }],
  ['work_order_runtime:s5_work_order_runtime', { migrationRole: 's5_work_order_migrator', accessMode: 'read' }],
  ['work_order_runtime:s5_work_order_service', { migrationRole: 's5_work_order_migrator', activationRole: 's5_work_order_runtime', accessMode: 'read' }],
  ['work_order_runtime:s5_work_order_writer', { migrationRole: 's5_work_order_migrator', accessMode: 'write', restrictedTo: ['work_order_current', 'work_order_source_reference', 'work_order_timeline', 'work_order_idempotency', 'work_order_mutation_audit', 'work_order_completion_evidence'] }],
  ['work_order_runtime:s5_work_order_mutation_service', { migrationRole: 's5_work_order_migrator', activationRole: 's5_work_order_writer', accessMode: 'write', restrictedTo: ['work_order_current', 'work_order_source_reference', 'work_order_timeline', 'work_order_idempotency', 'work_order_mutation_audit', 'work_order_completion_evidence'] }],
]);
for (const identity of dataRegistry.databaseIdentities ?? []) {
  const key = `${identity.schema}:${identity.runtimeRole}`;
  const expected = requiredIdentities.get(key);
  if (!expected) errors.push(`${key}: unexpected database identity`);
  if (identity.migrationRole !== expected?.migrationRole) errors.push(`${key}: migration role mismatch`);
  if ((identity.activationRole ?? null) !== (expected?.activationRole ?? null)) errors.push(`${key}: activation role mismatch`);
  if ((identity.accessMode ?? null) !== (expected?.accessMode ?? null)) errors.push(`${key}: access mode mismatch`);
  if (JSON.stringify(identity.restrictedTo ?? null) !== JSON.stringify(expected?.restrictedTo ?? null)) errors.push(`${key}: restricted table set mismatch`);
  if (identity.runtimeBypassRls !== false) errors.push(`${key}: runtime identity must not bypass RLS`);
  requiredIdentities.delete(key);
}
for (const key of requiredIdentities.keys()) errors.push(`${key}: required database identity is missing`);

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('Route and Data Ownership Registry checks passed.');
