import { createHash } from 'node:crypto';
import { readdir, readFile } from 'node:fs/promises';
import { extname, join, relative, resolve } from 'node:path';

const root = resolve(process.cwd());

function assert(condition, message) {
  if (!condition) throw new Error(`Invalid S2 Ticket 01 baseline: ${message}`);
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

function routeKey(route) {
  return `${route.method} ${route.path}`;
}

async function listSourceFiles(path) {
  const directory = resolve(root, path);
  const entries = await readdir(directory, { withFileTypes: true });
  const output = [];
  for (const entry of entries) {
    const absolute = join(directory, entry.name);
    if (entry.isDirectory()) output.push(...await listSourceFiles(relative(root, absolute)));
    else output.push(relative(root, absolute).replaceAll('\\', '/'));
  }
  return output;
}

const [
  openAPIText,
  publicationText,
  compatibility,
  toolingLock,
  ownership,
  releaseGates,
  implementationPlan,
  routeRegistry,
  dataRegistry,
  ownershipLock,
  bootstrapSQL,
  baselineSQL,
  runtimeSnapshotSQL,
  fixtureSQL,
  goGenerated,
  gatewayGoGenerated,
  tsGenerated,
  goWork,
  goMod,
  packageJSON,
  workflow,
] = await Promise.all([
  readText('contracts/http/s2-telemetry-public.openapi.json'),
  readText('contracts/events/s2-device-observation-publication.v1.schema.json'),
  readJSON('contracts/telemetry/s2-baseline-compatibility.v1.json'),
  readJSON('contracts/http/s2-tooling.lock.json'),
  readJSON('contracts/ownership/s2-telemetry-ownership.v1.json'),
  readJSON('deploy/s2/release-gates.v1.json'),
  readJSON('deploy/s2/implementation-plan.v1.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readText('infra/s2-telemetry/postgres/init/000-bootstrap-identities.sql'),
  readText('infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql'),
  readText('infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql'),
  readText('infra/s2-telemetry/postgres/init/004-s2-telemetry-fixtures.sql'),
  readText('services/telemetry-runtime-service/pkg/telemetryapi/api.gen.go'),
  readText('services/platform-gateway/pkg/s2telemetryapi/api.gen.go'),
  readText('apps/hvac-web/src/api/generated/s2Telemetry.gen.ts'),
  readText('go.work'),
  readText('services/telemetry-runtime-service/go.mod'),
  readJSON('package.json'),
  readText('.github/workflows/s2-telemetry-baseline.yml'),
]);
const openAPI = JSON.parse(openAPIText);
const publication = JSON.parse(publicationText);
const openAPIDigest = createHash('sha256').update(openAPIText).digest('hex');
const publicationDigest = createHash('sha256').update(publicationText).digest('hex');

assert(compatibility.schemaVersion === 1, 'compatibility lock schema version drifted');
assert(compatibility.predecessorContractVersion === '1.0.0-planned', 'predecessor contract version drifted');
assert(compatibility.currentContractVersion === '1.0.0', 'current contract version drifted');
assert(compatibility.activationStatus === 'expand-baseline', 'compatibility lock must remain expand-baseline');
assert(openAPI.info?.version === compatibility.currentContractVersion, 'OpenAPI version differs from compatibility lock');
assert(openAPI['x-activation-status'] === 'expand-baseline', 'OpenAPI must remain an expand baseline');
assert(publication['x-activation-status'] === 'expand-baseline', 'publication contract must remain an expand baseline');
assert(ownership.activationStatus === 'expand-baseline', 'ownership contract must remain an expand baseline');
assert(releaseGates.activationStatus === 'expand-baseline', 'release gates must remain an expand baseline');
assert(implementationPlan.activationStatus === 'active', 'implementation plan must be active');

const operations = new Map();
for (const [path, item] of Object.entries(openAPI.paths ?? {})) {
  for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
    const value = item?.[method];
    if (value?.operationId) operations.set(value.operationId, { method: method.toUpperCase(), path });
  }
}
assert(operations.size === 5, 'public surface must contain exactly five operations');
for (const expected of compatibility.operations) {
  const actual = operations.get(expected.operationId);
  assert(actual?.method === expected.method && actual?.path === expected.path, `${expected.operationId} method/path drifted`);
}
for (const [schemaName, properties] of Object.entries(compatibility.requiredSchemaProperties ?? {})) {
  const schema = openAPI.components?.schemas?.[schemaName];
  assert(schema?.type === 'object' && schema.additionalProperties === false, `${schemaName} must remain closed`);
  for (const property of properties) {
    assert(schema.required?.includes(property) && schema.properties?.[property], `${schemaName}.${property} is no longer required`);
  }
}
assert(publication.title === compatibility.publication.title, 'publication title drifted');
assert(publication.properties?.kind?.const === compatibility.publication.kind, 'publication kind drifted');
for (const property of compatibility.publication.requiredProperties) {
  assert(publication.required?.includes(property) && publication.properties?.[property], `publication.${property} is missing`);
}
for (const property of compatibility.publication.forbiddenProperties) {
  assert(!publication.properties?.[property], `publication must not expose ${property}`);
}

assert(toolingLock.generatorVersion === '1.0.0', 'generator version drifted');
assert(toolingLock.generator === 'scripts/generate-s2-telemetry-contracts.mjs', 'generator path drifted');
assert(exact(toolingLock.outputs, [
  'services/telemetry-runtime-service/pkg/telemetryapi/api.gen.go',
  'services/platform-gateway/pkg/s2telemetryapi/api.gen.go',
  'apps/hvac-web/src/api/generated/s2Telemetry.gen.ts',
]), 'generated output set drifted');
for (const generated of [goGenerated, gatewayGoGenerated, tsGenerated]) {
  assert(generated.includes('Code generated by scripts/generate-s2-telemetry-contracts.mjs; DO NOT EDIT.'), 'generated banner is missing');
  assert(generated.includes(openAPIDigest), 'generated output has stale OpenAPI digest');
  assert(generated.includes(publicationDigest), 'generated output has stale publication digest');
  for (const expected of compatibility.operations) {
    assert(generated.includes(expected.operationId), `generated operation is missing: ${expected.operationId}`);
  }
  for (const typeName of ['DeviceObservationSnapshot', 'DeviceHistoryRequest', 'DeviceHistoryResponse', 'ProblemDetails', 'RecoveryCursorCheckpoint', 'DeviceObservationPublication']) {
    assert(generated.includes(typeName), `generated type is missing: ${typeName}`);
  }
}
const goContractFiles = await listSourceFiles('services/telemetry-runtime-service/pkg/telemetryapi');
assert(exact(goContractFiles, ['services/telemetry-runtime-service/pkg/telemetryapi/api.gen.go']), 'Telemetry Runtime contract package contains a handwritten DTO file');
const webSourceFiles = await listSourceFiles('apps/hvac-web/src');
for (const path of webSourceFiles.filter((value) => ['.ts', '.tsx'].includes(extname(value)) && !value.includes('/api/generated/'))) {
  const source = await readText(path);
  assert(!/\b(?:interface|type)\s+(?:DeviceObservationSnapshot|DeviceHistoryRequest|DeviceHistoryResponse|RecoveryCursorCheckpoint|DeviceObservationPublication)\b/.test(source), `handwritten S2 DTO duplicates generated contract: ${path}`);
}
assert(goWork.includes('./services/telemetry-runtime-service'), 'Telemetry Runtime module is missing from go.work');
assert(goMod.includes('module github.com/quanlaihe/hvac-web/services/telemetry-runtime-service'), 'Telemetry Runtime module path drifted');
assert(goMod.includes('github.com/jackc/pgx/v5') && goMod.includes('github.com/quanlaihe/hvac-web/libs/telemetryauth'), 'Telemetry Runtime implementation dependencies drifted');
assert(goMod.includes('replace github.com/quanlaihe/hvac-web/libs/telemetryauth => ../../libs/telemetryauth'), 'Telemetry Runtime telemetryauth replacement is missing');

assert(compatibility.postgres?.schema === 'telemetry_runtime', 'PostgreSQL schema lock drifted');
assert(compatibility.postgres?.expandOnly === true, 'PostgreSQL baseline must remain expand-only');
const expandSQL = `${baselineSQL}\n${runtimeSnapshotSQL}`;
for (const forbidden of compatibility.postgres.forbiddenTokens ?? []) {
  assert(!expandSQL.toUpperCase().includes(forbidden), `expand migration contains forbidden token ${forbidden}`);
}
const expectedTables = Object.keys(compatibility.postgres.tables ?? {});
assert(expectedTables.length === 13, 'PostgreSQL table set must remain 13');
for (const [table, columns] of Object.entries(compatibility.postgres.tables ?? {})) {
  const pattern = new RegExp(`CREATE TABLE IF NOT EXISTS telemetry_runtime\\.${table} \\(([\\s\\S]*?)\\n\\);`, 'm');
  const tableBlock = baselineSQL.match(pattern)?.[1];
  assert(tableBlock, `table DDL is missing: ${table}`);
  for (const column of columns) {
    assert(new RegExp(`^\\s{2}${column}\\s`, 'm').test(tableBlock), `${table}.${column} is missing`);
  }
  assert(baselineSQL.includes(`ALTER TABLE telemetry_runtime.${table} ENABLE ROW LEVEL SECURITY;`), `${table} does not enable RLS`);
  assert(baselineSQL.includes(`ALTER TABLE telemetry_runtime.${table} FORCE ROW LEVEL SECURITY;`), `${table} does not force RLS`);
  assert(baselineSQL.includes(`CREATE POLICY ${table}_migrator_all ON telemetry_runtime.${table}`), `${table} migrator policy is missing`);
  assert(baselineSQL.includes(`CREATE POLICY ${table}_runtime_all ON telemetry_runtime.${table}`), `${table} runtime policy is missing`);
}
assert(baselineSQL.includes('registry_device_bindings_active_external_key_uidx'), 'active ExternalBinding uniqueness index is missing');
assert(baselineSQL.includes('telemetry_publication_outbox_pending_idx'), 'outbox pending index is missing');
for (const marker of [
  'ADD COLUMN IF NOT EXISTS accepted_signal_types',
  'presence_policies_accepted_signal_types_check',
  'CREATE TABLE IF NOT EXISTS telemetry_runtime.presence_signals',
  'CREATE TABLE IF NOT EXISTS telemetry_runtime.observation_coverage',
  'ADD COLUMN IF NOT EXISTS state_sha256',
  'ALTER TABLE telemetry_runtime.presence_signals FORCE ROW LEVEL SECURITY',
  'ALTER TABLE telemetry_runtime.observation_coverage FORCE ROW LEVEL SECURITY',
]) {
  assert(runtimeSnapshotSQL.includes(marker), `runtime Snapshot expansion is missing ${marker}`);
}
assert(baselineSQL.includes('GRANT UPDATE (delivery_state, available_at, attempts, last_error_code, published_at)'), 'relay delivery-column grant drifted');
assert(!/GRANT\s+INSERT[\s\S]*?TO\s+s2_telemetry_relay\s*;/i.test(baselineSQL), 'relay must not receive INSERT');

for (const role of [
  's2_telemetry_migrator',
  's2_telemetry_migrator_service',
  's2_telemetry_runtime',
  's2_telemetry_service',
  's2_telemetry_relay',
  's2_telemetry_relay_service',
  's2_telemetry_gateway',
  's2_telemetry_iam',
]) {
  assert(bootstrapSQL.includes(role), `database role is missing: ${role}`);
}
for (const role of ['s2_telemetry_migrator', 's2_telemetry_runtime', 's2_telemetry_relay']) {
  assert(new RegExp(`CREATE ROLE ${role} NOLOGIN[^;]*NOINHERIT NOBYPASSRLS;`).test(bootstrapSQL), `${role} must be NOLOGIN/NOINHERIT/NOBYPASSRLS`);
}
for (const role of ['s2_telemetry_migrator_service', 's2_telemetry_service', 's2_telemetry_gateway', 's2_telemetry_iam', 's2_telemetry_relay_service']) {
  assert(new RegExp(`CREATE ROLE ${role} LOGIN[^;]*NOINHERIT NOBYPASSRLS;`).test(bootstrapSQL), `${role} must be LOGIN/NOINHERIT/NOBYPASSRLS`);
}
assert(!bootstrapSQL.includes('GRANT s2_telemetry_runtime TO s2_telemetry_gateway'), 'Gateway must not inherit runtime writes');
assert(!bootstrapSQL.includes('GRANT s2_telemetry_runtime TO s2_telemetry_iam'), 'IAM must not inherit runtime writes');

for (const marker of [
  '018f2e00-0000-7000-8000-000000000001',
  '018f2e00-0000-7000-8000-000000000002',
  '018f2e00-1000-7000-8000-000000000002',
  '018f2e00-1000-7000-8000-000000000003',
  "'zone.temperature'",
  "'zone.humidity'",
  "'duct.pressure'",
  'NEVER_OBSERVED',
  'ONLY_REJECTED_CANDIDATES',
  'MAPPING_CONFLICT',
]) {
  assert(fixtureSQL.includes(marker), `deterministic fixture marker is missing: ${marker}`);
}

assert(
  routeRegistry.registryRevision >= 7 && ownershipLock.routeRegistryRevision === routeRegistry.registryRevision,
  'route ownership revision must remain monotonic and locked',
);
assert(dataRegistry.registryRevision >= 6 && ownershipLock.dataRegistryRevision === dataRegistry.registryRevision, 'data ownership revision must remain monotonic and locked');
const activeRoutes = new Map((routeRegistry.routes ?? []).map((route) => [routeKey(route), route]));
for (const expected of compatibility.operations) {
  const route = activeRoutes.get(`${expected.method} ${expected.path}`);
  assert(route?.publicIngress === 'platform-gateway', `${expected.operationId} public Gateway seam drifted`);
  assert(route?.readOnlyFallback === false && route.readFallbackOwner === undefined, `${expected.operationId} must not have request fallback`);
  if (expected.operationId === 'queryDeviceHistory') {
    assert(route?.owner === 'telemetry-query-service', 'queryDeviceHistory business owner drifted');
    assert(route?.activationStatus === 'primary' && route?.rollout?.mode === 'all', 'queryDeviceHistory must use the active Query Service product boundary');
  } else {
    assert(route?.owner === 'telemetry-runtime-service', `${expected.operationId} business owner drifted`);
    assert(route?.activationStatus === 'expand-baseline', `${expected.operationId} activation status drifted`);
    assert(route?.rollout?.mode === 'disabled' && route.migrationPhase === 'R0-contract-only', `${expected.operationId} must carry zero production traffic`);
  }
}
const resources = new Map((dataRegistry.resources ?? []).map((resource) => [`${resource.kind}:${resource.name}`, resource]));
assert(resources.get('schema:telemetry_runtime')?.writer === 'telemetry-runtime-service', 'active telemetry_runtime writer drifted');
assert(resources.get('transport-store:s2-telemetry-transport-redis')?.authority === false, 'transport Redis became authoritative');
assert(resources.get('transport-store:s2-telemetry-transport-redis')?.sharedWithLegacy === false, 'transport Redis shares Legacy state');
assert(resources.get('compatibility-boundary:legacy-telemetry-timeseries')?.currentStateAuthority === false, 'Legacy historical boundary became current authority');
const telemetryAccess = (dataRegistry.databaseAccess ?? []).filter((entry) => entry.schema === 'telemetry_runtime');
assert(telemetryAccess.length === 2, 'telemetry_runtime must have exactly owner write and restricted relay access');
assert(telemetryAccess.some((entry) => entry.service === 'telemetry-runtime-service' && entry.mode === 'write'), 'owner database write access is missing');
assert(telemetryAccess.some((entry) => entry.service === 'outbox-relay' && entry.mode === 'relay' && exact(entry.restrictedTo, ['telemetry_publication_outbox'])), 'restricted relay access is missing');

const expectedScripts = {
  's2:contracts:generate': 'node scripts/generate-s2-telemetry-contracts.mjs',
  's2:contracts:check': 'node scripts/generate-s2-telemetry-contracts.mjs --check',
  's2:baseline:check': 'node scripts/check-s2-telemetry-baseline.mjs',
  's2:postgres': 'node scripts/run-s2-telemetry-postgres-tests.mjs',
};
for (const [name, command] of Object.entries(expectedScripts)) {
  assert(packageJSON.scripts?.[name] === command, `${name} is not wired`);
}
assert(typeof packageJSON.scripts?.['s2:telemetry-baseline'] === 'string', 's2:telemetry-baseline clean baseline command is missing');
for (const command of [
  'npm run s2:contracts:check',
  'npm run ownership:check',
  'npm run s2:baseline:check',
  'npm run s2:postgres',
  'npm run release:evidence-assets',
  'npm run s1:registry:check',
  'npm run test:ownership',
  'npm run test:registry-routing',
  'npm run lint',
  'npm run build',
]) {
  assert(packageJSON.scripts['s2:telemetry-baseline'].includes(command), `s2:telemetry-baseline omits ${command}`);
}
assert(workflow.includes('npm run s2:telemetry-baseline'), 'clean CI workflow does not run the Ticket 01 command');
assert(workflow.includes('actions/setup-go@v5') && workflow.includes('actions/setup-node@v4'), 'clean CI workflow must provision Go and Node');
assert(workflow.includes('runs-on: ubuntu-24.04'), 'clean CI workflow must use the pinned clean hosted runner');

console.log('S2 Ticket 01 baseline passed: generated contracts, expand-only PostgreSQL, isolated identities, active ownership and zero-traffic routes.');
