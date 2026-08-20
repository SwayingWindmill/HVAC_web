import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, extname, join, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-shadow-routing/shadow-routing.json');
const readJSON = async (path) => JSON.parse(await readFile(resolve(root, path), 'utf8'));
const text = async (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const registry = await readJSON('contracts/ownership/route-ownership.v1.json');
const plan = await readJSON('deploy/acceptance/s2-shadow-routing-revisions.v1.json');
const policy = await readJSON('deploy/acceptance/s2-shadow-comparator-policy.v1.json');
const fixture = await readJSON('deploy/s2/fixtures/shadow-comparison-pass.json');
const comparator = await text('services/telemetry-shadow-comparator/internal/comparison/comparison.go');
const comparatorCLI = await text('services/telemetry-shadow-comparator/cmd/telemetry-shadow-comparator/main.go');

const routeKeys = new Set([
  'GET /api/v1/devices/{deviceId}/observation-snapshot',
  'POST /api/v1/telemetry/observation-snapshots:batchGet',
  'POST /api/v1/telemetry/subscriptions:bootstrap',
  'POST /api/v1/telemetry/recovery-cursors:checkpoint',
]);
const active = (registry.routes ?? []).filter((route) => routeKeys.has(`${route.method} ${route.path}`));
assert(active.length === routeKeys.size, 'active S2 current-state route group is incomplete');
for (const route of active) {
  assert(route.owner === 'telemetry-runtime-service', `${route.method} ${route.path}: authoritative owner drifted`);
  assert(route.rollout?.mode === 'all', `${route.method} ${route.path}: route is not fully cut over`);
  assert(route.compatibilityMode === 'native', `${route.method} ${route.path}: compatibility mode is not native`);
  assert(route.readOnlyFallback !== true && route.readFallbackOwner === undefined && route.fallbackOwner === undefined, `${route.method} ${route.path}: fallback was reintroduced`);
}

assert(plan.schemaVersion === 1 && plan.ticket === 67, 'historical shadow routing plan identity drifted');
assert(plan.productionTrafficPercent === 0, 'historical shadow plan must not itself be an active traffic authority');
assert(plan.invariants?.crossWriteCount === 0 && plan.invariants?.sharedCacheCount === 0 && plan.invariants?.requestFallback === false, 'historical rollout invariants drifted');

assert(policy.schemaVersion === 1 && policy.executionMode === 'offline-batch' && policy.servingPath === false, 'comparator execution boundary drifted');
const forbiddenPolicyValues = [
  policy.identity?.serviceAccountToken, policy.identity?.workloadCertificate, policy.identity?.tokenMintPermission,
  policy.network?.dns, policy.dataAccess?.databaseCredentials, policy.dataAccess?.databaseWritePermission,
  policy.dataAccess?.legacyWritePermission, policy.dataAccess?.telemetryRuntimeWritePermission, policy.dataAccess?.sharedCacheAccess,
  policy.transportPermissions?.publish, policy.transportPermissions?.subscribe, policy.transportPermissions?.centrifugoApi,
  policy.transportPermissions?.redis, policy.transportPermissions?.thingsBoard, policy.mutationPermissions?.authorization,
  policy.mutationPermissions?.mappingRepair, policy.mutationPermissions?.routeOwnership, policy.mutationPermissions?.currentStateFeedback,
];
assert(forbiddenPolicyValues.every((value) => value !== true), 'comparator acquired a forbidden capability');
assert(policy.network?.ingress === 'deny-all' && policy.network?.egress === 'deny-all', 'comparator network policy is not deny-all');

for (const marker of [
  'MinimumValueAgreementRate       = 0.999', 'MinimumTimestampAgreementRate   = 0.995',
  'MappingMismatches', 'MissingDevices', 'ExtraDevices', 'UnmatchedAcceptedValues', 'AcceptedValueAgreementRate',
  'TimestampAgreementRate', 'UnclassifiedDifferenceCount', 'LEGACY_ACTIVE_COARSE_VS_S2_STALE',
  'UNCLASSIFIED_ACTIVE_PRESENCE_CONFLICT', 'SideEffects: SideEffectEvidence{}',
]) assert(comparator.includes(marker), `comparator is missing ${marker}`);

const comparatorSources = `${comparator}\n${comparatorCLI}`;
for (const forbidden of [
  '"database/sql"', '"net/http"', 'github.com/jackc/', 'centrifuge', 'redis', 'thingsboard',
  'telemetryauth', 'identitycontext', 'jwt', 'oauth', 'oidc', 'kafka', 'nats', 'amqp',
]) assert(!comparatorSources.toLowerCase().includes(forbidden.toLowerCase()), `comparator source includes forbidden capability marker: ${forbidden}`);
for (const marker of ['maximumInputBytes = 64 << 20', 'DisallowUnknownFields', 'writeAtomic', '0o600']) {
  assert(comparatorCLI.includes(marker), `offline comparator CLI is missing ${marker}`);
}
assert(fixture.schemaVersion === 1 && fixture.mappings?.length > 0 && fixture.legacyDevices?.length > 0 && fixture.s2Snapshots?.length > 0, 'comparison fixture is incomplete');

async function sourceFiles(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) result.push(...await sourceFiles(path));
    else if (extname(path) === '.go') result.push(path);
  }
  return result;
}
const files = await sourceFiles(resolve(root, 'services/telemetry-shadow-comparator'));
assert(files.length >= 3, 'historical comparator implementation/test surface is incomplete');

await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify({
  schemaVersion: 2,
  ticket: 295,
  status: 'retired-from-production',
  authoritativeOwner: 'telemetry-runtime-service',
  productionRouteSurfaces: active.length,
  productionFallbacks: 0,
  historicalComparatorSideEffectFree: true,
  historicalEvidenceRoot: 'deploy/acceptance',
  generatedAt: new Date().toISOString(),
}, null, 2)}\n`);
console.log(`S2 shadow routing retirement check passed: ${output}`);
