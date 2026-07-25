import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, extname, join, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-ticket-08/shadow-routing.json');
const readJSON = async (path) => JSON.parse(await readFile(resolve(root, path), 'utf8'));
const text = async (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const registry = await readJSON('contracts/ownership/route-ownership.v1.json');
const plan = await readJSON('deploy/s2/shadow-routing-revisions.v1.json');
const policy = await readJSON('deploy/s2/shadow-comparator-policy.v1.json');
const fixture = await readJSON('deploy/s2/fixtures/shadow-comparison-pass.json');
const ownership = await text('libs/ownershipregistry/registry.go');
const transition = await text('libs/ownershipregistry/s2_transition.go');
const comparator = await text('services/telemetry-shadow-comparator/internal/comparison/comparison.go');
const comparatorCLI = await text('services/telemetry-shadow-comparator/cmd/telemetry-shadow-comparator/main.go');
const harness = await text('services/telemetry-shadow-comparator/cmd/s2-ticket-08-harness/main.go');

const routeKeys = new Set([
  'GET /api/v1/devices/{deviceId}/observation-snapshot',
  'POST /api/v1/telemetry/observation-snapshots:batchGet',
  'POST /api/v1/telemetry/subscriptions:bootstrap',
  'POST /api/v1/telemetry/recovery-cursors:checkpoint',
]);
const active = (registry.routes ?? []).filter((route) => routeKeys.has(`${route.method} ${route.path}`));
assert(active.length === 4, 'active S2 current-state route group is incomplete');
for (const route of active) {
  assert(route.owner === 'telemetry-runtime-service', `${route.method} ${route.path}: R0 owner drifted`);
  assert(route.activationStatus === 'expand-baseline' && route.rollout?.mode === 'disabled', `${route.method} ${route.path}: production traffic is not zero`);
  assert(route.migrationPhase === 'R0-contract-only' && route.revision === 1, `${route.method} ${route.path}: active phase/revision drifted`);
  assert(route.cohortGroup === 's2-current-state-v1', `${route.method} ${route.path}: cohort group drifted`);
  assert(route.readOnlyFallback === false && route.readFallbackOwner === undefined, `${route.method} ${route.path}: request fallback was introduced`);
}

assert(plan.schemaVersion === 1 && plan.ticket === 67, 'shadow routing plan identity drifted');
assert(plan.appliedProductionPhase === 'R0-contract-only' && plan.productionTrafficPercent === 0, 'Ticket 08 activated production canary traffic');
assert(plan.cohortGroup === 's2-current-state-v1' && plan.cohortSalt === 's2-current-state-rollout-v1', 'cohort group/salt drifted');
assert(plan.routeSurfaces?.length === 4, 'single/batch/live route surface set is incomplete');
const revisionById = new Map((plan.revisions ?? []).map((value) => [value.id, value]));
for (const expected of [
  ['R0-contract-only', 7, 1, 'telemetry-runtime-service', 'disabled'],
  ['R1-dark-ingest', 8, 2, 'legacy-hvac-backend', 'all'],
  ['R2-shadow-compare', 9, 3, 'legacy-hvac-backend', 'all'],
  ['R3-internal-canary', 10, 4, 'telemetry-runtime-service', 'percentage'],
]) {
  const [id, registryRevision, routeRevision, owner, mode] = expected;
  const value = revisionById.get(id);
  assert(value?.registryRevision === registryRevision && value?.routeRevision === routeRevision && value?.owner === owner && value?.rollout?.mode === mode, `${id}: rollout revision drifted`);
}
const rollback = revisionById.get('R3-to-R2-rollback');
assert(rollback?.fromPhase === 'R3-internal-canary' && rollback?.toPhase === 'R2-shadow-compare' && rollback?.registryRevision === 11 && rollback?.routeRevision === 5, 'rollback revision is incomplete');
assert(rollback?.sessionInvalidation?.disconnectOrExpire === true && rollback?.sessionInvalidation?.freshSnapshotRequired === true && rollback?.sessionInvalidation?.databaseAction === 'EXPAND_ONLY_NO_DOWN_MIGRATION', 'rollback session/database semantics drifted');
assert(plan.invariants?.crossWriteCount === 0 && plan.invariants?.sharedCacheCount === 0 && plan.invariants?.requestFallback === false && plan.invariants?.sameOwnerForSingleBatchLive === true && plan.invariants?.productionCanaryActivationInTicket08 === false, 'rollout invariants drifted');

assert(policy.schemaVersion === 1 && policy.executionMode === 'offline-batch' && policy.servingPath === false, 'comparator execution boundary drifted');
const forbiddenPolicyValues = [
  policy.identity?.serviceAccountToken, policy.identity?.workloadCertificate, policy.identity?.tokenMintPermission,
  policy.network?.dns, policy.dataAccess?.databaseCredentials, policy.dataAccess?.databaseWritePermission,
  policy.dataAccess?.legacyWritePermission, policy.dataAccess?.telemetryRuntimeWritePermission, policy.dataAccess?.sharedCacheAccess,
  policy.transportPermissions?.publish, policy.transportPermissions?.subscribe, policy.transportPermissions?.centrifugoApi,
  policy.transportPermissions?.redis, policy.transportPermissions?.thingsBoard, policy.mutationPermissions?.authorization,
  policy.mutationPermissions?.mappingRepair, policy.mutationPermissions?.routeOwnership, policy.mutationPermissions?.currentStateFeedback,
];
assert(forbiddenPolicyValues.every((value) => value === false), 'comparator acquired a forbidden capability');
assert(policy.network?.ingress === 'deny-all' && policy.network?.egress === 'deny-all', 'comparator network policy is not deny-all');
assert(policy.failureSemantics?.comparisonFailureAffectsLegacyServing === false && policy.failureSemantics?.comparisonFailureAffectsS2Serving === false && policy.failureSemantics?.comparisonFailureBlocksPromotion === true, 'comparator failure isolation drifted');

for (const marker of [
  'MinimumValueAgreementRate       = 0.999', 'MinimumTimestampAgreementRate   = 0.995',
  'MappingMismatches', 'MissingDevices', 'ExtraDevices', 'UnmatchedAcceptedValues', 'AcceptedValueAgreementRate',
  'TimestampAgreementRate', 'UnclassifiedDifferenceCount', 'LEGACY_ACTIVE_COARSE_VS_S2_STALE',
  'UNCLASSIFIED_ACTIVE_PRESENCE_CONFLICT', 'SideEffects: SideEffectEvidence{}',
]) assert(comparator.includes(marker), `comparator is missing ${marker}`);
for (const marker of [
  'CohortGroup', 's2PhaseRank', 'cohortMaterial = fmt.Sprintf', 'validateCohortGroup',
]) assert(ownership.includes(marker), `ownership registry is missing ${marker}`);
for (const marker of [
  'ReloadS2', 'S2 route transition requires a live-session invalidator',
  'DisconnectOrExpire:       true', 'FreshSnapshotRequired:    true', 'EXPAND_ONLY_NO_DOWN_MIGRATION',
]) assert(transition.includes(marker), `S2 transition controller is missing ${marker}`);

const comparatorSources = `${comparator}\n${comparatorCLI}`;
for (const forbidden of [
  '"database/sql"', '"net/http"', 'github.com/jackc/', 'centrifuge', 'redis', 'thingsboard',
  'telemetryauth', 'identitycontext', 'jwt', 'oauth', 'oidc', 'kafka', 'nats', 'amqp',
]) assert(!comparatorSources.toLowerCase().includes(forbidden.toLowerCase()), `comparator source includes forbidden capability marker: ${forbidden}`);
for (const marker of ['maximumInputBytes = 64 << 20', 'DisallowUnknownFields', 'writeAtomic', '0o600']) {
  assert(comparatorCLI.includes(marker), `offline comparator CLI is missing ${marker}`);
}
for (const marker of ['20_000', 'ReloadS2', 'RouteRevisionBoundCohort', 'ShadowPromotionEligible']) {
  assert(harness.includes(marker), `Ticket 08 harness is missing ${marker}`);
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
assert(files.length >= 4, 'comparator implementation/test surface is incomplete');

await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify({
  schemaVersion: 1,
  ticket: 67,
  status: 'passed',
  activeProductionPhase: 'R0-contract-only',
  activeProductionTrafficPercent: 0,
  routeSurfaces: active.length,
  cohortGroup: 's2-current-state-v1',
  valueAgreementThreshold: 0.999,
  timestampAgreementThreshold: 0.995,
  comparatorSideEffectFree: true,
  rollbackRequiresSessionInvalidation: true,
  harnessEvidence: 'out/s2-ticket-08/shadow-routing-harness.json',
  comparisonEvidence: 'out/s2-ticket-08/shadow-comparison.json',
  generatedAt: new Date().toISOString(),
}, null, 2)}\n`);
console.log(`S2 Ticket 08 shadow routing passed: ${output}`);
