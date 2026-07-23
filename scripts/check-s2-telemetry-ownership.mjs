import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function text(path) {
  return readFile(resolve(root, path), 'utf8');
}

const contract = JSON.parse(await text('contracts/ownership/s2-telemetry-ownership.v1.json'));
const activeDataOwnership = JSON.parse(await text('contracts/ownership/data-ownership.v1.json'));
const adr = await text('docs/adr/0003-s2-telemetry-runtime-ownership.md');
const context = await text('CONTEXT.md');
const packageJSON = JSON.parse(await text('package.json'));

assert(contract.schemaVersion === 1, 'S2 telemetry ownership schemaVersion must be 1');
assert(contract.decisionRevision === 2, 'S2 telemetry ownership decisionRevision must be 2 for the expand baseline');
assert(contract.activationStatus === 'expand-baseline', 'S2 telemetry ownership must be activated only as an expand baseline');
assert(contract.ownerService === 'telemetry-runtime-service', 'Telemetry Runtime must have one explicit owner');
assert(contract.boundedContext === 'telemetry-runtime', 'Telemetry Runtime bounded context name drifted');

const store = contract.authoritativeStore ?? {};
assert(store.engine === 'postgresql', 'authoritative store must be PostgreSQL');
assert(store.schema === 'telemetry_runtime', 'authoritative schema must be telemetry_runtime');
assert(store.writer === contract.ownerService, 'authoritative schema writer must be the owner service');
assert(store.migrationRole === 's2_telemetry_migrator', 'migration role drifted');
assert(store.migrationServiceRole === 's2_telemetry_migrator_service', 'migration service role drifted');
assert(store.runtimeRole === 's2_telemetry_runtime', 'runtime role drifted');
assert(store.serviceRole === 's2_telemetry_service', 'runtime service role drifted');
assert(store.relayRole === 's2_telemetry_relay' && store.relayServiceRole === 's2_telemetry_relay_service', 'relay roles drifted');
assert(store.gatewayRole === 's2_telemetry_gateway' && store.iamRole === 's2_telemetry_iam', 'connect-only Gateway/IAM roles drifted');
assert(store.runtimeBypassRls === false, 'runtime role must not bypass RLS');

const resourceKeys = new Set();
for (const resource of contract.ownedResources ?? []) {
  const key = `${resource.kind}:${resource.name}`;
  assert(!resourceKeys.has(key), `duplicate owned resource: ${key}`);
  resourceKeys.add(key);
  assert(resource.writer === contract.ownerService, `${key} must be written by Telemetry Runtime`);
}
for (const key of [
  'projection:device-runtime-binding',
  'projection:presence-policy',
  'projection:freshness-policy',
  'projection:latest-accepted-telemetry',
  'projection:device-presence',
  'projection:device-observation-snapshot',
  'projection:ingest-deduplication',
  'projection:ingest-quarantine',
  'outbox:telemetry-publication-outbox',
  'event-family:hvac.telemetry.device-snapshot.v1',
]) {
  assert(resourceKeys.has(key), `required owned resource missing: ${key}`);
}
const outbox = contract.ownedResources.find((resource) => resource.kind === 'outbox');
assert(outbox?.relay === 'outbox-relay', 'publication outbox must use the restricted outbox relay');
const activeResources = new Map((activeDataOwnership.resources ?? []).map((resource) => [`${resource.kind}:${resource.name}`, resource]));
for (const key of resourceKeys) {
  assert(activeResources.get(key)?.writer === contract.ownerService, `active Data Ownership Registry is missing ${key}`);
}
assert(activeResources.get('schema:telemetry_runtime')?.writer === contract.ownerService, 'active telemetry_runtime schema owner drifted');
assert(activeResources.get('transport-store:s2-telemetry-transport-redis')?.authority === false, 'dedicated transport Redis must remain non-authoritative');
assert(activeResources.get('transport-store:s2-telemetry-transport-redis')?.sharedWithLegacy === false, 'transport Redis must remain isolated from Legacy');
assert(activeResources.get('compatibility-boundary:legacy-telemetry-timeseries')?.currentStateAuthority === false, 'Legacy historical compatibility cannot become current authority');

const inputEvents = new Map((contract.inputEvents ?? []).map((event) => [event.name, event]));
assert(inputEvents.get('hvac.registry.external-binding.v1')?.writer === 'platform-core-service', 'Core must own binding change events');
assert(inputEvents.get('hvac.registry.external-binding.v1')?.consumer === contract.ownerService, 'Telemetry Runtime must consume binding changes');
assert(inputEvents.get('hvac.iam.authorization-revoked.v1')?.writer === 'iam-service', 'IAM must own revocation events');
assert(inputEvents.get('hvac.iam.authorization-revoked.v1')?.consumer === contract.ownerService, 'Telemetry Runtime must consume revocation events');

const databaseAccess = contract.databaseAccess ?? [];
assert(databaseAccess.length === 2, 'only owner write and restricted relay database access are allowed');
assert(databaseAccess.some((entry) => entry.service === contract.ownerService && entry.schema === store.schema && entry.mode === 'write'), 'owner write access missing');
const relayAccess = databaseAccess.find((entry) => entry.mode === 'relay');
assert(relayAccess?.service === 'outbox-relay' && relayAccess.schema === store.schema, 'invalid relay identity or schema');
assert(JSON.stringify(relayAccess.restrictedTo) === JSON.stringify(['telemetry_publication_outbox']), 'relay must be restricted to the publication outbox');

const positions = contract.positions ?? {};
assert(positions.businessRevision?.owner === contract.ownerService, 'Business Revision must be owner-authored');
assert(positions.businessRevision?.scope === 'device', 'Business Revision must be Device-scoped');
assert(positions.sourcePosition?.owner === 'source-adapter', 'Source Position ownership drifted');
assert(positions.transportPosition?.owner === 'centrifugo', 'Transport Position ownership drifted');
assert(positions.transportPosition?.representation === 'epoch plus offset', 'Transport Position representation drifted');
assert(positions.recoveryCursor?.owner === contract.ownerService, 'Recovery Cursor must be platform-owner authored');
assert(positions.recoveryCursor?.authority === 'none', 'Recovery Cursor must not become business authority');
assert(positions.recoveryCursor?.reuseRequires?.includes('current authorization'), 'Recovery Cursor reuse must require current authorization');
assert(positions.recoveryCursor?.failureBehavior?.includes('authoritative Device Observation Snapshot'), 'Recovery Cursor failure must return to Snapshot');
for (const noAdvance of ['duplicate source delivery', 'rejected candidate', 'publication retry', 'cache refresh']) {
  assert(positions.businessRevision?.doesNotAdvanceFor?.includes(noAdvance), `Business Revision no-advance rule missing: ${noAdvance}`);
}

const stores = contract.stores ?? {};
assert(stores.businessAuthority === 'postgresql.telemetry_runtime', 'business authority drifted');
assert(stores.optionalReadCache?.authority === 'non-authoritative-replica', 'read cache must remain non-authoritative');
assert(stores.optionalReadCache?.writer === contract.ownerService, 'only owner may populate the optional read cache');
assert(stores.transportHistory?.authority === 'ephemeral-continuity-cache', 'transport history must remain ephemeral');
assert(stores.transportHistory?.engine === 'dedicated-redis', 'transport history must use dedicated Redis');
assert(stores.transportHistory?.writer === 'centrifugo', 'Centrifugo must own transport history writes');
assert(stores.transportHistory?.sharedWithLegacyRedis === false, 'transport Redis must not be shared with Legacy Redis');
assert(stores.thingsBoard?.publicReadThrough === false, 'ThingsBoard public read-through is forbidden');
assert(stores.thingsBoard?.directLatestWrite === false, 'ThingsBoard direct latest writes are forbidden');
assert(stores.legacyRedis?.authority === 'none' && stores.legacyRedis?.s2Read === false && stores.legacyRedis?.s2Write === false, 'Legacy Redis must have no S2 authority or access');

const responsibilities = new Map((contract.serviceResponsibilities ?? []).map((entry) => [entry.service, entry]));
for (const service of [
  'platform-core-service',
  'iam-service',
  'platform-gateway',
  'telemetry-runtime-service',
  'legacy-hvac-backend',
  'thingsboard',
  'centrifugo',
]) {
  assert(responsibilities.has(service), `service responsibility missing: ${service}`);
  assert(responsibilities.get(service).owns?.length > 0, `${service} must declare owned responsibilities`);
  assert(responsibilities.get(service).mustNot?.length > 0, `${service} must declare forbidden responsibilities`);
}
assert(responsibilities.get(contract.ownerService).owns.includes('Business Revision'), 'owner must explicitly own Business Revision');
assert(responsibilities.get('platform-core-service').mustNot.includes('write Presence or latest telemetry'), 'Core must not own runtime truth');
assert(responsibilities.get('platform-gateway').mustNot.includes('cache or merge telemetry business state'), 'Gateway must remain an edge seam');
assert(responsibilities.get('centrifugo').mustNot.includes('own Snapshot'), 'Centrifugo must not own Snapshot');

assert(contract.ingestTransaction?.length === 8, 'ingest transaction steps drifted');
assert(contract.ingestTransaction.at(-1)?.includes('same Business Revision'), 'publication retry must preserve Business Revision');
assert(contract.snapshotReadOrder?.some((step) => step.includes('rather than ThingsBoard read-through')), 'Snapshot failure must not use ThingsBoard read-through');
assert(contract.mappingRules?.publicDeviceIdentity === 'Registry Device UUIDv7', 'public Device identity drifted');
assert(contract.mappingRules?.observationMayCreateBinding === false, 'observations must not create mappings');
assert(contract.mappingRules?.zeroActiveBindings?.includes('MAPPING_NOT_FOUND'), 'missing mapping quarantine rule is absent');
assert(contract.mappingRules?.multipleActiveBindings?.includes('MAPPING_CONFLICT'), 'conflicting mapping quarantine rule is absent');

assert(contract.historicalTimeseries?.insideS2Slice === false, 'historical timeseries must remain outside S2');
assert(contract.historicalTimeseries?.compatibilityOwner === 'legacy-hvac-backend', 'historical compatibility owner drifted');
assert(contract.historicalTimeseries?.requirements?.includes('no write into S2 latest projections'), 'historical response must not write S2 latest');
assert(contract.historicalTimeseries?.requirements?.includes('no fallback from S2 latest or Snapshot routes'), 'S2 current reads must not fall back to historical compatibility');

const requiredForbiddenFlows = [
  'browser-to-thingsboard',
  'public-latest-read-to-thingsboard-read-through',
  'thingsboard-direct-to-latest-projection',
  'legacy-hvac-backend-to-telemetry-runtime-write',
  'legacy-redis-to-s2-current-state',
  'cache-to-subscription-authorization',
  'centrifugo-history-to-snapshot-authority',
  's2-latest-to-thingsboard-reverse-sync',
  'core-and-telemetry-runtime-cross-schema-business-writes',
  'shadow-or-fallback-path-with-side-effects',
];
assert(JSON.stringify(contract.forbiddenFlows) === JSON.stringify(requiredForbiddenFlows), 'forbidden-flow set or order drifted');

for (const phrase of [
  'Create a dedicated Telemetry Runtime bounded context',
  'Authoritative persistence is PostgreSQL Schema `telemetry_runtime`',
  'One business writer, one restricted relay',
  'Three position concepts remain distinct',
  'Source readback is reconciliation, not read-through',
  'Transport persistence uses dedicated Redis',
  'Historical timeseries remains outside S2',
  'Forbidden data flows',
  'Issue #51 defines the public Snapshot',
]) {
  assert(adr.includes(phrase), `ADR is missing required decision phrase: ${phrase}`);
}

for (const term of [
  '## Telemetry Runtime',
  '## Device Observation Snapshot',
  '## Business Revision',
  '## Source Position',
  '## Transport Position',
  '## Recovery Cursor',
  '## Ingest Quarantine',
]) {
  assert(context.includes(term), `CONTEXT.md is missing domain term: ${term}`);
}

assert(packageJSON.scripts?.['s2:ownership:check'] === 'node scripts/check-s2-telemetry-ownership.mjs', 's2:ownership:check is not wired');

console.log('S2 telemetry ownership passed: one runtime owner, PostgreSQL authority, bounded transport and no hidden dual writes.');
