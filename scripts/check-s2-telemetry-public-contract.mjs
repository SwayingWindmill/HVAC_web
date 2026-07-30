import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function readText(path) {
  return readFile(resolve(root, path), 'utf8');
}

async function readJSON(path) {
  return JSON.parse(await readText(path));
}

function exact(actual, expected) {
  return Array.isArray(actual)
    && actual.length === expected.length
    && actual.every((value, index) => value === expected[index]);
}

function exactSet(actual, expected) {
  return Array.isArray(actual)
    && actual.length === expected.length
    && expected.every((value) => actual.includes(value));
}

function exactKeys(value, expected) {
  return value !== null
    && typeof value === 'object'
    && exactSet(Object.keys(value), expected);
}

function operation(spec, operationId) {
  for (const [path, item] of Object.entries(spec.paths ?? {})) {
    for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
      if (item?.[method]?.operationId === operationId) return { path, method, operation: item[method] };
    }
  }
  return null;
}

const [spec, publication, ownership, activeSpecText, adr, context, packageJSON] = await Promise.all([
  readJSON('contracts/http/s2-telemetry-public.openapi.json'),
  readJSON('contracts/events/s2-device-observation-publication.v1.schema.json'),
  readJSON('contracts/ownership/s2-telemetry-ownership.v1.json'),
  readText('contracts/http/platform-gateway.openapi.yaml'),
  readText('docs/adr/0004-s2-public-api-subscription-recovery-contract.md'),
  readText('CONTEXT.md'),
  readJSON('package.json'),
]);

assert(spec.openapi === '3.1.0', 'telemetry contract must use OpenAPI 3.1.0');
assert(spec.info?.version === '1.0.0', 'expand-baseline telemetry contract version drifted');
assert(spec['x-activation-status'] === 'expand-baseline', 'telemetry contract must be activated only as an expand baseline');
assert(spec['x-public-owner'] === 'platform-gateway', 'Gateway must remain the public owner');
assert(spec['x-upstream-owner'] === 'telemetry-runtime-service', 'Telemetry Runtime must remain the upstream business owner');
assert(publication['x-activation-status'] === 'expand-baseline', 'publication contract must be activated only as an expand baseline');
assert(ownership.activationStatus === 'expand-baseline', 'S2 ownership contract must be an expand baseline');
assert(ownership.ownerService === spec['x-upstream-owner'], 'public contract owner must match the ownership decision');

assert(JSON.stringify(spec.security) === JSON.stringify([{ BffSession: [] }, { WorkloadMTLS: [] }]), 'security alternatives must be BffSession and WorkloadMTLS');
assert(spec.components?.securitySchemes?.BffSession?.name === '__Host-hvac_session', 'BFF Session cookie name drifted');
assert(spec.components?.securitySchemes?.BffSession?.type === 'apiKey', 'BFF Session scheme drifted');
assert(spec.components?.securitySchemes?.WorkloadMTLS?.type === 'mutualTLS', 'workload security must be mTLS');
assert(spec.components?.parameters?.ConditionalCsrfToken?.required === false, 'conditional CSRF header must remain optional in OpenAPI and required by BFF policy');
assert(spec.components?.parameters?.ConditionalCsrfToken?.description?.includes('BffSession'), 'conditional CSRF semantics are missing');

const expectedOperations = {
  getDeviceObservationSnapshot: ['get', '/api/v1/devices/{deviceId}/observation-snapshot'],
  batchGetDeviceObservationSnapshots: ['post', '/api/v1/telemetry/observation-snapshots:batchGet'],
  bootstrapTelemetrySubscriptions: ['post', '/api/v1/telemetry/subscriptions:bootstrap'],
  checkpointTelemetryRecoveryCursors: ['post', '/api/v1/telemetry/recovery-cursors:checkpoint'],
  queryDeviceHistory: ['post', '/api/v1/telemetry/device-history'],
};
const operations = {};
for (const [operationId, [method, path]] of Object.entries(expectedOperations)) {
  const value = operation(spec, operationId);
  assert(value?.method === method && value?.path === path, `${operationId} method/path drifted`);
  operations[operationId] = value.operation;
}
assert(Object.values(spec.paths ?? {}).reduce((count, item) => count + ['get', 'post', 'put', 'patch', 'delete'].filter((method) => item?.[method]).length, 0) === 5, 'S2 public surface must remain exactly five operations');
assert(!Object.keys(spec.paths ?? {}).some((path) => path.endsWith('/presence') || path.endsWith('/telemetry/latest')), 'Presence/latest must not split into separate public resources');

const invariants = spec['x-contract-invariants'] ?? [];
for (const invariant of [
  'single-device-snapshot-is-coherent-at-one-business-revision',
  'snapshot-values-preserve-requested-key-order',
  'batch-items-preserve-request-order',
  'batch-partial-results-use-item-problems',
  'subscription-bootstrap-is-all-or-nothing',
  'authorization-is-re-evaluated-for-every-read-subscribe-and-cursor-use',
  'stale-missing-and-upstream-unavailable-are-explicit-data-states',
  'publication-revisions-are-contiguous-per-device',
  'recovery-failure-loads-an-authoritative-snapshot',
  'real-mode-never-falls-back-to-mock-or-thingsboard-read-through',
  'device-history-public-requests-never-accept-organization-or-site-claims',
  'device-history-authorization-binds-exact-device-keys-range-and-point-limit',
  'device-history-is-numeric-accepted-observations-only',
]) {
  assert(invariants.includes(invariant), `public contract invariant missing: ${invariant}`);
}

const limits = spec['x-limits'] ?? {};
assert(limits.maxKeysPerDevice === 64, 'keys-per-Device limit drifted');
assert(limits.maxBatchDevices === 100, 'batch Device limit drifted');
assert(limits.maxBatchKeySelections === 2048, 'batch total-key limit drifted');
assert(limits.maxSubscriptions === 100, 'subscription count limit drifted');
assert(limits.maxSubscriptionKeySelections === 2048, 'subscription total-key limit drifted');
assert(limits.maxCursorCheckpoints === 100, 'cursor checkpoint limit drifted');
assert(limits.maxHistoryKeys === 8, 'history key limit drifted');
assert(limits.maxHistoryPointsPerKey === 500, 'history per-key point limit drifted');
assert(limits.maxHistoryRangeHours === 24, 'history range limit drifted');
assert(limits.maxHistoryResponsePoints === 4000, 'history total response limit drifted');
assert(spec.components?.parameters?.TelemetryKeys?.required === false, 'single Snapshot keys must remain optional for Presence-only reads');
assert(spec.components?.parameters?.TelemetryKeys?.schema?.maxItems === limits.maxKeysPerDevice, 'single Snapshot key limit disagrees with root limits');
assert(spec.components?.parameters?.TelemetryKeys?.schema?.uniqueItems === true, 'single Snapshot keys must be unique');
assert(!JSON.stringify(operations.batchGetDeviceObservationSnapshots.parameters ?? []).includes('Cursor'), 'explicit batch must not paginate');

const schemas = spec.components?.schemas ?? {};
assert(schemas.BatchGetObservationSnapshotsRequest?.properties?.requests?.maxItems === limits.maxBatchDevices, 'batch schema Device limit drifted');
assert(schemas.ObservationSnapshotTarget?.properties?.keys?.maxItems === limits.maxKeysPerDevice, 'batch item key limit drifted');
assert(schemas.SubscriptionBootstrapRequest?.properties?.subscriptions?.maxItems === limits.maxSubscriptions, 'bootstrap subscription limit drifted');
assert(schemas.SubscriptionTargetRequest?.properties?.keys?.maxItems === limits.maxKeysPerDevice, 'subscription key limit drifted');
assert(schemas.RecoveryCursorCheckpointRequest?.properties?.checkpoints?.maxItems === limits.maxCursorCheckpoints, 'checkpoint count limit drifted');
assert(schemas.DeviceHistoryRequest?.properties?.keys?.maxItems === limits.maxHistoryKeys, 'history request key limit drifted');
assert(schemas.DeviceHistoryRequest?.properties?.keys?.uniqueItems === true, 'history request keys must be unique');
assert(schemas.DeviceHistoryRequest?.properties?.maxPointsPerKey?.maximum === limits.maxHistoryPointsPerKey, 'history per-key point limit disagrees with root limits');
assert(schemas.DeviceHistoryResponse?.properties?.series?.maxItems === limits.maxHistoryKeys, 'history response series limit drifted');
assert(schemas.DeviceHistorySeries?.properties?.points?.maxItems === limits.maxHistoryPointsPerKey, 'history series point limit drifted');
assert(schemas.DeviceHistoryMetadata?.properties?.returnedPoints?.maximum === limits.maxHistoryResponsePoints, 'history total response limit drifted');

const requestSchemaProperties = {
  ObservationSnapshotTarget: ['requestId', 'deviceId', 'keys'],
  BatchGetObservationSnapshotsRequest: ['requests'],
  SubscriptionTargetRequest: ['clientSubscriptionId', 'deviceId', 'keys', 'recoveryCursor'],
  SubscriptionBootstrapRequest: ['subscriptions'],
  RecoveryCursorCheckpoint: ['subscriptionId', 'businessRevision', 'transportPosition'],
  RecoveryCursorCheckpointRequest: ['checkpoints'],
  DeviceHistoryRequest: ['deviceId', 'keys', 'from', 'to', 'maxPointsPerKey'],
};
for (const [name, allowedProperties] of Object.entries(requestSchemaProperties)) {
  const schema = schemas[name];
  assert(schema?.type === 'object' && schema.additionalProperties === false, `${name} must remain a closed request object`);
  assert(exactKeys(schema.properties, allowedProperties), `${name} request fields drifted`);
}
for (const field of spec['x-client-forbidden-fields'] ?? []) {
  for (const [name, allowedProperties] of Object.entries(requestSchemaProperties)) {
    assert(!allowedProperties.includes(field), `${name} illegally accepts forbidden client field ${field}`);
  }
}
assert(!Object.keys(schemas.SubscriptionTargetRequest.properties).includes('transportPosition'), 'bootstrap targets must not accept raw transport position');
assert(!Object.keys(schemas.SubscriptionTargetRequest.properties).includes('channel'), 'bootstrap targets must not choose channels');
assert(!Object.keys(schemas.ObservationSnapshotTarget.properties).includes('siteId'), 'batch targets must not choose Site scope');

assert(exactKeys(schemas.DeviceObservationSnapshot?.properties, [
  'schemaVersion',
  'deviceId',
  'owningOrganizationId',
  'siteId',
  'businessRevision',
  'evaluatedAt',
  'evaluationAvailability',
  'availabilityReasons',
  'presence',
  'telemetryReadiness',
  'displayState',
  'values',
]), 'DeviceObservationSnapshot fields drifted');
assert(exactSet(schemas.DeviceObservationSnapshot.required, Object.keys(schemas.DeviceObservationSnapshot.properties)), 'all Snapshot fields must be explicit');
assert(schemas.DeviceObservationSnapshot.properties.schemaVersion.const === 1, 'Snapshot schemaVersion drifted');
assert(schemas.BusinessRevision?.minimum === 1, 'Business Revision type drifted');
assert(schemas.PolicyRevision?.minimum === 1, 'Policy Revision type drifted');
assert(schemas.DeviceObservationSnapshot.properties.businessRevision.$ref?.endsWith('/BusinessRevision'), 'Snapshot Business Revision missing');
assert(schemas.DeviceObservationSnapshot.properties.evaluatedAt.$ref?.endsWith('/Instant'), 'Snapshot evaluatedAt missing');
assert(schemas.PresenceSnapshot?.properties?.policyRevision?.oneOf?.[0]?.$ref?.endsWith('/PolicyRevision'), 'Presence Policy Revision must not reuse Business Revision');
assert(schemas.TelemetryPresentState?.properties?.policyRevision?.$ref?.endsWith('/PolicyRevision'), 'Freshness Policy Revision must not reuse Business Revision');
assert(schemas.TelemetryMissingState?.properties?.policyRevision?.oneOf?.[0]?.$ref?.endsWith('/PolicyRevision'), 'missing-key Policy Revision must not reuse Business Revision');
assert(schemas.DeviceObservationSnapshot.properties.values.maxItems === limits.maxKeysPerDevice, 'Snapshot value limit drifted');

assert(exact(schemas.EvaluationAvailability.enum, ['AVAILABLE', 'UNAVAILABLE']), 'Evaluation Availability enum drifted');
assert(exact(schemas.PresenceApplicability.enum, ['APPLICABLE', 'NOT_APPLICABLE']), 'Presence Applicability enum drifted');
assert(exact(schemas.DevicePresenceState.enum, ['ONLINE', 'OFFLINE', 'UNKNOWN']), 'Device Presence enum drifted');
assert(exact(schemas.TelemetryFreshness.enum, ['FRESH', 'STALE', 'MISSING']), 'Telemetry Freshness enum drifted');
assert(exact(schemas.TelemetryQuality.enum, ['GOOD', 'SUSPECT']), 'Telemetry Quality enum drifted');
assert(exact(schemas.DeviceHistoryQuality.enum, ['GOOD', 'SUSPECT']), 'Device History Quality enum drifted');
assert(exact(schemas.TelemetryReadiness.enum, ['CURRENT', 'DEGRADED', 'INCOMPLETE', 'NOT_APPLICABLE']), 'Telemetry Readiness enum drifted');
assert(exact(schemas.DeviceDisplayState.enum, ['ONLINE', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE', null]), 'Device Display State enum drifted');

assert(schemas.TelemetryPresentState?.properties?.state?.const === 'PRESENT', 'present key state discriminator drifted');
assert(exactSet(schemas.TelemetryPresentState?.properties?.freshness?.enum, ['FRESH', 'STALE']), 'present key may only be fresh or stale');
assert(schemas.TelemetryPresentState?.properties?.sampledAt?.$ref?.endsWith('/Instant'), 'present key sampledAt missing');
assert(schemas.TelemetryPresentState?.properties?.receivedAt?.$ref?.endsWith('/Instant'), 'present key receivedAt missing');
assert(schemas.TelemetryMissingState?.properties?.state?.const === 'MISSING', 'missing key state discriminator drifted');
assert(schemas.TelemetryMissingState?.properties?.freshness?.const === 'MISSING', 'missing key freshness drifted');
assert(exact(schemas.TelemetryMissingState?.properties?.missingReason?.enum, ['NEVER_OBSERVED', 'ONLY_REJECTED_CANDIDATES', 'POLICY_NOT_CONFIGURED']), 'missing reason enum drifted');
assert(!Object.keys(schemas.TelemetryMissingState?.properties ?? {}).includes('value'), 'missing key state must not contain a value');

assert(exactKeys(schemas.BatchObservationSuccess?.properties, ['requestId', 'deviceId', 'status', 'snapshot']), 'batch success fields drifted');
assert(exactKeys(schemas.BatchObservationFailure?.properties, ['requestId', 'deviceId', 'status', 'problem']), 'batch failure fields drifted');
assert(schemas.BatchObservationSuccess.properties.status.const === 'OK', 'batch success discriminator drifted');
assert(schemas.BatchObservationFailure.properties.status.const === 'ERROR', 'batch failure discriminator drifted');
assert(schemas.BatchGetObservationSnapshotsResponse?.properties?.items?.items?.$ref?.endsWith('/BatchObservationResult'), 'batch response must use typed item results');

assert(schemas.SubscriptionBootstrapResponse?.properties?.transportProtocol?.const === 'CENTRIFUGO_JSON_V1', 'transport protocol lock drifted');
assert(schemas.SubscriptionBootstrapResponse?.properties?.endpoint?.pattern === '^wss://', 'bootstrap endpoint must be wss');
assert(schemas.SubscriptionDescriptor?.properties?.channel, 'server-selected channel is missing from response');
assert(exact(schemas.SubscriptionDescriptor?.properties?.recoveryMode?.enum, ['SNAPSHOT_THEN_LIVE', 'ATTEMPT_RECOVERY']), 'recovery modes drifted');
assert(schemas.RecoveryCursorCheckpoint?.properties?.transportPosition?.$ref?.endsWith('/TransportPosition'), 'checkpoint must pair transport position');
assert(schemas.RecoveryCursorCheckpointResult?.properties?.recoveryCursor?.$ref?.endsWith('/OpaqueRecoveryCursor'), 'checkpoint must return an opaque cursor');

const stableProblemCodes = [
  'RESOURCE_NOT_FOUND',
  'TELEMETRY_REQUEST_INVALID',
  'TELEMETRY_KEY_INVALID',
  'RECOVERY_CURSOR_INVALID',
  'TELEMETRY_BATCH_LIMIT_EXCEEDED',
  'SUBSCRIPTION_LIMIT_EXCEEDED',
  'TELEMETRY_UNAVAILABLE',
  'TELEMETRY_TIMEOUT',
  'SUBSCRIPTION_UNAVAILABLE',
  'TELEMETRY_AUTHORIZATION_UNAVAILABLE',
];
assert(exact(schemas.ProblemDetails?.properties?.code?.['x-stable-codes'], stableProblemCodes), 'stable telemetry Problem codes drifted');
assert(spec.components?.responses?.Problem?.content?.['application/problem+json']?.schema?.$ref === '#/components/schemas/ProblemDetails', 'problems must use application/problem+json');
assert(spec.components?.headers?.NoStore?.schema?.const === 'private, no-store', 'sensitive Snapshot/bootstrap responses must be no-store');

assert(publication.$schema === 'https://json-schema.org/draft/2020-12/schema', 'publication must use JSON Schema 2020-12');
assert(publication.additionalProperties === false, 'publication must be a closed object');
assert(publication.properties?.schemaVersion?.const === 1, 'publication schemaVersion drifted');
assert(publication.properties?.kind?.const === 'DEVICE_OBSERVATION_DELTA', 'publication kind drifted');
assert(exactKeys(publication.properties, [
  'schemaVersion',
  'kind',
  'eventId',
  'subscriptionId',
  'deviceId',
  'previousRevision',
  'revision',
  'evaluatedAt',
  'publishedAt',
  'evaluationAvailability',
  'availabilityReasons',
  'presence',
  'telemetryReadiness',
  'displayState',
  'telemetryChanges',
]), 'publication fields drifted');
assert(exactSet(publication.required, Object.keys(publication.properties)), 'all publication fields must be explicit');
assert(!Object.keys(publication.properties).includes('recoveryCursor'), 'business publication must not carry Recovery Cursor');
assert(!Object.keys(publication.properties).includes('transportPosition'), 'business publication must not carry Transport Position');
assert(publication.properties.telemetryChanges.maxItems === limits.maxKeysPerDevice, 'publication key-change limit drifted');
assert(publication.$defs?.PolicyRevision?.minimum === 1, 'publication Policy Revision type drifted');
assert(publication.$defs?.PresenceSnapshot?.properties?.policyRevision?.oneOf?.[0]?.$ref?.endsWith('/PolicyRevision'), 'publication Presence Policy Revision must remain distinct');
assert(publication.$defs?.TelemetryPresentState?.properties?.policyRevision?.$ref?.endsWith('/PolicyRevision'), 'publication Freshness Policy Revision must remain distinct');
for (const invariant of [
  'revision-equals-previousRevision-plus-one',
  'one-publication-per-active-subscription-per-device-revision',
  'telemetryChanges-contains-only-authorized-selected-keys',
  'empty-telemetryChanges-still-advances-revision',
  'publication-retry-reuses-eventId-and-revision',
  'transport-position-and-recovery-cursor-are-not-business-publication-fields',
]) {
  assert(publication['x-invariants']?.includes(invariant), `publication invariant missing: ${invariant}`);
}
for (const [openapiName, eventName = openapiName] of [
  ['EvaluationAvailability'],
  ['PresenceApplicability'],
  ['DevicePresenceState'],
  ['TelemetryQuality'],
  ['TelemetryReadiness'],
  ['DeviceDisplayState'],
]) {
  assert(exact(publication.$defs?.[eventName]?.enum, schemas[openapiName]?.enum), `${openapiName} differs between HTTP and publication contracts`);
}
assert(exact(publication.$defs?.QualityReasonCode?.['x-known-codes'], schemas.QualityReasonCode?.['x-known-codes']), 'quality reason codes differ between contracts');
assert(exact(publication.$defs?.AvailabilityReasonCode?.['x-known-codes'], schemas.AvailabilityReasonCode?.['x-known-codes']), 'availability reason codes differ between contracts');

assert(exact(spec['x-legacy-retirement']?.retire, [
  'GET /api/v1/telemetry/devices/{deviceId}/latest',
  'POST /api/v1/telemetry/latest/batch',
  'Socket.IO /ws/telemetry',
]), 'Legacy current-state retirement set drifted');
assert(exact(spec['x-legacy-retirement']?.retainSeparately, [
  'GET /api/v1/telemetry/devices/{deviceId}/timeseries',
  'GET /api/v1/devices/{deviceId}/telemetry',
]), 'historical compatibility set drifted');

for (const path of Object.values(expectedOperations).map(([, path]) => path)) {
  assert(!activeSpecText.includes(`\"${path}\"`), `planned S2 route was prematurely added to active Gateway OpenAPI: ${path}`);
}

for (const phrase of [
  'Publish one coherent Snapshot model, not separate Presence and latest resources',
  'Bounded batch read has item results, not pagination',
  'Subscription bootstrap is all-or-nothing',
  'Recovery Cursor checkpoint is an adapter operation',
  'Publication is a contiguous subscription-scoped delta',
  'Initial subscribe and Snapshot algorithm',
  'Reconnect and recovery algorithm',
  'Stable Problem Details',
  'Client generation and module seam',
  'Legacy compatibility and retirement',
]) {
  assert(adr.includes(phrase), `ADR is missing required decision phrase: ${phrase}`);
}
for (const term of ['## Telemetry Key Selection', '## Subscription Bootstrap', '## Observation Delta']) {
  assert(context.includes(term), `CONTEXT.md is missing domain term: ${term}`);
}
assert(packageJSON.scripts?.['s2:public-contract:check'] === 'node scripts/check-s2-telemetry-public-contract.mjs', 's2:public-contract:check is not wired');

console.log('S2 public telemetry contract passed: coherent Snapshot, bounded batches, exact subscription scope and Snapshot-authoritative recovery.');
