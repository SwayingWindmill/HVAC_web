import { existsSync, statSync } from 'node:fs';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const outputRoot = resolve(root, 'out/s0-release-evidence');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function sourcePath(reference) {
  return reference.split('#')[0];
}

function assertPath(reference, label) {
  const path = resolve(root, sourcePath(reference));
  assert(existsSync(path), `${label} does not exist: ${reference}`);
  const stat = statSync(path);
  assert(stat.isFile() || stat.isDirectory(), `${label} is not a file or directory: ${reference}`);
}

const matrixPath = resolve(root, 'deploy/s0/release-evidence/acceptance-matrix.json');
const schemaPath = resolve(root, 'deploy/s0/release-evidence/bundle.schema.json');
const matrix = JSON.parse(await readFile(matrixPath, 'utf8'));
const schema = JSON.parse(await readFile(schemaPath, 'utf8'));
const ticket = await readFile(resolve(root, '.scratch/go-data-ai-platform-s0/issues/08-s0-release-evidence.md'), 'utf8');
const tracker = await readFile(resolve(root, '.scratch/go-data-ai-platform-s0/README.md'), 'utf8');
const operations = await readFile(resolve(root, 'docs/operations/s0-release-evidence.md'), 'utf8');
const packageJSON = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
const workflow = await readFile(resolve(root, '.github/workflows/s0-supply-chain.yml'), 'utf8');
const openapiText = await readFile(resolve(root, 'contracts/http/platform-gateway.openapi.yaml'), 'utf8');
const ownership = JSON.parse(await readFile(resolve(root, 'contracts/ownership/data-ownership.v1.json'), 'utf8'));
const routeOwnership = JSON.parse(await readFile(resolve(root, 'contracts/ownership/route-ownership.v1.json'), 'utf8'));
const s2Ownership = JSON.parse(await readFile(resolve(root, 'contracts/ownership/s2-telemetry-ownership.v1.json'), 'utf8'));
const s3Ownership = JSON.parse(await readFile(resolve(root, 'contracts/ownership/s3-command-ownership.v1.json'), 'utf8'));
const s2ReleaseGates = JSON.parse(await readFile(resolve(root, 'deploy/s2/release-gates.v1.json'), 'utf8'));
const s4AlarmPromotion = JSON.parse(await readFile(resolve(root, 'deploy/s4/alarm-read-promotion-envelope.v1.json'), 'utf8'));

assert(matrix.schemaVersion === 1, 'acceptance matrix schemaVersion must be 1');
assert(matrix.ticket === '08-s0-release-evidence', 'acceptance matrix ticket identifier is invalid');
assert(Array.isArray(matrix.criteria) && matrix.criteria.length === 14, 'Ticket 08 must map exactly fourteen acceptance criteria');
const ids = new Set();
for (const criterion of matrix.criteria) {
  assert(typeof criterion.id === 'string' && criterion.id.length > 0, 'criterion id is required');
  assert(!ids.has(criterion.id), `duplicate criterion id: ${criterion.id}`);
  ids.add(criterion.id);
  assert(typeof criterion.requirement === 'string' && criterion.requirement.length > 20, `${criterion.id} requirement is incomplete`);
  for (const field of ['decisions', 'implementation', 'tests', 'runtimeEvidence']) {
    assert(Array.isArray(criterion[field]) && criterion[field].length > 0, `${criterion.id}.${field} must be non-empty`);
  }
  for (const reference of [...criterion.decisions, ...criterion.implementation, ...criterion.tests]) {
    assertPath(reference, `${criterion.id} reference`);
  }
  for (const reference of criterion.runtimeEvidence) {
    assert(reference.startsWith('out/s0-'), `${criterion.id} runtime evidence must be generated under out/s0-*`);
  }
}

const ticketStatus = ticket.match(/^\*\*Status:\*\*\s+([^\r\n]+)/m)?.[1]?.trim();
const uncheckedItems = ticket.match(/^- \[ \]/gm) ?? [];
const checkedItems = ticket.match(/^- \[x\]/gmi) ?? [];
if (ticketStatus === 'completed') {
  assert(checkedItems.length === 14 && uncheckedItems.length === 0, `completed Ticket 08 must have fourteen checked acceptance items; checked=${checkedItems.length}, unchecked=${uncheckedItems.length}`);
} else {
  assert(ticketStatus === 'ready-for-agent', `Ticket 08 status must be ready-for-agent or completed; found ${String(ticketStatus)}`);
  assert(uncheckedItems.length === 14 && checkedItems.length === 0, `active Ticket 08 must have fourteen unchecked acceptance items; checked=${checkedItems.length}, unchecked=${uncheckedItems.length}`);
}
assert(tracker.includes('Ticket 01 is the only initial frontier item'), 'S0 tracker no longer identifies Ticket 01 as the initial frontier');
for (const edge of ['01 Contract-first Gateway bootstrap', '02 Authenticated principal loop', '03 Durable Session', '04 Route/Data Ownership', '05 End-to-end observability', '06 Reproducible delivery and supply chain', '07 Security, tenant and failure gates', '08 S0 Release Evidence Bundle']) {
  assert(tracker.includes(edge), `S0 tracker is missing dependency node: ${edge}`);
}

for (const heading of ['## Reuse-first selection', '## Evidence model', '## Staging rolling update and rollback proof', '## Zero invariants', '## Reusable S0 surface for S1–S7', '## Legacy retirement status', '## Known limitations', '## Approval and S0 completion']) {
  assert(operations.includes(heading), `release evidence runbook is missing ${heading}`);
}
for (const upstream of ['in-toto/attestation v1.2.0', 'slsa-framework/slsa-verifier v2.7.1', 'oras-project/oras v1.3.3', 'kubernetes-sigs/kind v0.32.0', 'helm/kind-action v1.14.0']) {
  assert(operations.includes(upstream), `release evidence runbook is missing upstream decision: ${upstream}`);
}
assert(operations.includes('Legacy is retained only as historical migration evidence'), 'release evidence runbook must record Legacy retirement');
assert(operations.includes('not an active route owner, fallback, staging workload, local topology dependency or release image'), 'release evidence runbook must keep Legacy out of active runtime and release paths');
assert(operations.includes('S0 is complete. S1 is ready to enter implementation specification.'), 'release evidence runbook is missing the final declaration text');

for (const script of ['release:evidence-assets', 'audit:s0-kind-rollout', 'release:evidence:images', 'release:evidence:build', 'test:release-evidence']) {
  assert(typeof packageJSON.scripts?.[script] === 'string', `package.json is missing ${script}`);
}

for (const marker of [
  'release-evidence:',
  'helm/kind-action@ef37e7f390d99f746eb8b610417061a60e82a6cc',
  'release-image-${{ matrix.name }}',
  's0-release-evidence-bundle',
  'npm run audit:s0-kind-rollout',
  'npm run release:evidence:images',
  'npm run release:evidence:build',
]) {
  assert(workflow.includes(marker), `s0-supply-chain workflow is missing release evidence marker: ${marker}`);
}

assert(schema.properties?._type?.const === 'https://in-toto.io/Statement/v1', 'bundle schema must use in-toto Statement v1');
assert(schema.properties?.predicateType?.const === 'https://hvac.local/attestations/s0-release-evidence/v1', 'bundle predicate type is invalid');
assert(schema.properties?.predicate?.properties?.images?.minItems === 6 && schema.properties?.predicate?.properties?.images?.maxItems === 6, 'bundle schema must require exactly six active release images');

// S0 evidence remains valid when later phase contracts are added. S2 resources
// are accepted only while they match the reviewed expand baseline and carry no
// production traffic.
const forbiddenPostS1PublicPaths = [
  '/api/v1/telemetry',
  '/api/v1/commands',
  '/api/v1/schedules',
  '/api/v1/ai',
  '/api/v1/recommendations',
];
const leakedPaths = forbiddenPostS1PublicPaths.filter((path) => openapiText.includes(`"${path}`));
assert(leakedPaths.length === 0, `public contract contains post-S1 deferred paths: ${leakedPaths.join(', ')}`);
assert(s2Ownership.activationStatus === 'expand-baseline', 'S2 ownership must remain an expand baseline');
assert(s2ReleaseGates.activationStatus === 'expand-baseline', 'S2 release gates must remain an expand baseline');
const s2Routes = routeOwnership.routes.filter((route) => route.owner === s2Ownership.ownerService);
assert(s2Routes.length === 4, `S2 expand baseline must register exactly four routes; found ${s2Routes.length}`);
for (const route of s2Routes) {
  assert(route.publicIngress === 'platform-gateway', `${route.method} ${route.path} bypasses Gateway ingress`);
  assert(route.activationStatus === 'expand-baseline', `${route.method} ${route.path} activation drifted`);
  assert(route.rollout?.mode === 'disabled' && route.migrationPhase === 'R0-contract-only', `${route.method} ${route.path} carries production traffic`);
  assert(route.readOnlyFallback === false && route.readFallbackOwner === undefined, `${route.method} ${route.path} gained request fallback`);
}
const acceptedS2OwnershipNames = new Set([
  s2Ownership.authoritativeStore.schema,
  ...(s2Ownership.ownedResources ?? []).map((resource) => resource.name),
  's2-telemetry-transport-redis',
  'legacy-telemetry-timeseries',
]);
assert(s3Ownership.activationStatus === 'expand-baseline', 'S3 ownership must remain an expand baseline');
assert(s3Ownership.productionTrafficPercent === 0, 'S3 ownership must remain at zero production traffic');
const acceptedS3OwnershipNames = new Set([
  s3Ownership.authoritativeStore.schema,
  'hvac.command.lifecycle.v1',
  ...(s3Ownership.ownedResources ?? []).map((name) => name === 'capability-profile' ? 'command-capability-profile' : name),
]);
const registeredS3Resources = ownership.resources.filter((resource) => acceptedS3OwnershipNames.has(resource.name));
assert(registeredS3Resources.length > 0, 'S3 ownership resources are missing from the data registry');
assert(registeredS3Resources.every((resource) => resource.writer === s3Ownership.businessOwner), `S3 ownership resources have an unexpected writer: ${JSON.stringify(registeredS3Resources.filter((resource) => resource.writer !== s3Ownership.businessOwner))}`);
const uncontractedS3Resources = ownership.resources.filter((resource) => resource.writer === s3Ownership.businessOwner && !acceptedS3OwnershipNames.has(resource.name));
assert(uncontractedS3Resources.length === 0, `data registry contains S3 resources outside the S3 ownership contract: ${JSON.stringify(uncontractedS3Resources)}`);

const acceptedS4OwnershipNames = new Set([
  'alarm_runtime',
  'hvac.alarm.lifecycle.v1',
  'alarm-current',
  'alarm-transition',
  'alarm-evidence',
  'alarm-occurrence-deduplication',
  'alarm-idempotency',
]);
const registeredS4Resources = ownership.resources.filter((resource) => acceptedS4OwnershipNames.has(resource.name));
assert(registeredS4Resources.length === acceptedS4OwnershipNames.size, `S4 Alarm ownership resources are incomplete: ${JSON.stringify(registeredS4Resources)}`);
assert(registeredS4Resources.every((resource) => resource.writer === 'alarm-service'), `S4 Alarm ownership resources have an unexpected writer: ${JSON.stringify(registeredS4Resources.filter((resource) => resource.writer !== 'alarm-service'))}`);
const uncontractedS4Resources = ownership.resources.filter((resource) => resource.writer === 'alarm-service' && !acceptedS4OwnershipNames.has(resource.name));
assert(uncontractedS4Resources.length === 0, `data registry contains S4 resources outside the S4 Alarm ownership contract: ${JSON.stringify(uncontractedS4Resources)}`);
const acceptedS5OwnershipNames = new Set([
  'work_order_runtime',
  'hvac.work-order.lifecycle.v1',
  'work-order-current',
  'work-order-source-reference',
  'work-order-timeline',
  'work-order-task',
  'work-order-note',
  'work-order-attachment-metadata',
  'work-order-completion-evidence',
]);
const registeredS5Resources = ownership.resources.filter((resource) => acceptedS5OwnershipNames.has(resource.name));
assert(registeredS5Resources.length === acceptedS5OwnershipNames.size, `S5 Work Order ownership resources are incomplete: ${JSON.stringify(registeredS5Resources)}`);
assert(registeredS5Resources.every((resource) => resource.writer === 'work-order-service'), `S5 Work Order ownership resources have an unexpected writer: ${JSON.stringify(registeredS5Resources.filter((resource) => resource.writer !== 'work-order-service'))}`);
const uncontractedS5Resources = ownership.resources.filter((resource) => resource.writer === 'work-order-service' && !acceptedS5OwnershipNames.has(resource.name));
assert(uncontractedS5Resources.length === 0, `data registry contains S5 resources outside the S5 Work Order ownership contract: ${JSON.stringify(uncontractedS5Resources)}`);

const acceptedS4IAMOwnership = [
  { kind: 'projection', name: 'iam-alarm-permission', writer: 'iam-service', revision: 1 },
  { kind: 'projection', name: 'iam-alarm-authorization-decision', writer: 'iam-service', revision: 1 },
];
for (const expected of acceptedS4IAMOwnership) {
  const registered = ownership.resources.find((resource) => resource.name === expected.name);
  assert(registered, `S4 IAM Alarm ownership resource is missing: ${expected.name}`);
  for (const [field, value] of Object.entries(expected)) {
    assert(registered[field] === value, `S4 IAM Alarm ownership ${expected.name}.${field} drifted: ${JSON.stringify(registered)}`);
  }
}
assert(s4AlarmPromotion.schemaVersion === 1 && s4AlarmPromotion.issue === 187, 'S4 Alarm promotion evidence contract is invalid');
assert(s4AlarmPromotion.formalPromotionRequired === true && s4AlarmPromotion.repositoryMutationByCertification === false, 'S4 Alarm promotion evidence can bypass formal review or mutate routing');
assert(s4AlarmPromotion.routeGroup?.source?.phase === 'S4-R1-internal-read-only' && s4AlarmPromotion.routeGroup?.source?.trafficPercent === 1, 'S4 Alarm promotion source phase drifted');
assert(s4AlarmPromotion.routeGroup?.target?.phase === 'S4-R2-site-canary' && s4AlarmPromotion.routeGroup?.target?.trafficPercent === 5, 'S4 Alarm promotion target phase drifted');
assert(s4AlarmPromotion.routeGroup?.rollback?.phase === 'S4-R1-internal-read-only' && s4AlarmPromotion.routeGroup?.rollback?.trafficPercent === 1, 'S4 Alarm promotion rollback phase drifted');
assert(s4AlarmPromotion.requiredEvidence?.includes('s4-alarm-read-promotion.intoto.json') && s4AlarmPromotion.requiredEvidence?.includes('SHA256SUMS'), 'S4 Alarm promotion offline evidence is incomplete');

const acceptedHistoryAnalyticsOwnership = [
  { kind: 'schema', name: 'telemetry_history', writer: 'telemetry-history-projector', revision: 1, database: 'clickhouse' },
  { kind: 'schema', name: 'analytics', writer: 'analytics-read-model-projector', revision: 1, database: 'clickhouse' },
  { kind: 'projection', name: 'telemetry-history-observation', writer: 'telemetry-history-projector', revision: 1, sourceOwner: 'telemetry-runtime-service' },
  { kind: 'projection', name: 'analytics-energy-interval-fact', writer: 'analytics-read-model-projector', revision: 1, sourceOwner: 'telemetry-history-projector' },
  { kind: 'query-contract', name: 'analytics-energy-series-v1', writer: 'telemetry-query-service', revision: 1, publicIngress: 'platform-gateway' },
  { kind: 'query-contract', name: 'telemetry-device-history-v1', writer: 'telemetry-query-service', revision: 1, publicIngress: 'platform-gateway', source: 'telemetry_history.observations' },
  { kind: 'semantic-model', name: 'cube-energy-usage-v1', writer: 'telemetry-query-service', revision: 1, runtime: 'cube-core', source: 'analytics.energy_interval_facts' },
];
for (const expected of acceptedHistoryAnalyticsOwnership) {
  const registered = ownership.resources.find((resource) => resource.name === expected.name);
  assert(registered, `history/analytics ownership resource is missing: ${expected.name}`);
  for (const [field, value] of Object.entries(expected)) {
    assert(registered[field] === value, `history/analytics ownership ${expected.name}.${field} drifted: ${JSON.stringify(registered)}`);
  }
}

const allowedOwnershipNames = new Set([
  'gateway',
  'audit_ledger',
  'legacy',
  'iam',
  'core_registry',
  'hvac.security.session.v1',
  'hvac.routing.decision.v1',
  'audit-session-record',
  'gateway-route-audit',
  'iam-registry-read-decision',
  'iam-registry-grant-revocation',
  'iam-telemetry-authorization-decision',
  'iam-telemetry-grant-use',
  'iam-telemetry-revocation-fact',
  ...acceptedS4IAMOwnership.map((resource) => resource.name),
  'iam-onboarding-reconciliation',
  'iam-reconciliation-quarantine',
  'presence-signal',
  'observation-coverage',
  'source-observation-evidence',
  's1-legacy-resource-map',
  's1-migration-provenance',
  's1-migration-quarantine',
  ...acceptedS2OwnershipNames,
  ...acceptedS3OwnershipNames,
  ...acceptedS4OwnershipNames,
  ...acceptedS5OwnershipNames,
  ...acceptedHistoryAnalyticsOwnership.map((resource) => resource.name),
]);
const leakedOwnership = ownership.resources.filter((resource) => !allowedOwnershipNames.has(resource.name));
assert(leakedOwnership.length === 0, `ownership registry contains resources outside the accepted S1/S2/S3/S4/S5/history/analytics baselines: ${JSON.stringify(leakedOwnership)}`);

await mkdir(outputRoot, { recursive: true });
const scopeAudit = {
  schemaVersion: 1,
  ticket: '08-s0-release-evidence',
  status: 'passed',
  checkedAt: new Date().toISOString(),
  forbiddenPublicPaths: forbiddenPostS1PublicPaths,
  leakedPublicPaths: [],
  allowedOwnershipResources: [...allowedOwnershipNames].sort(),
  acceptedS2ExpandBaselineResources: [...acceptedS2OwnershipNames].sort(),
  acceptedS3ExpandBaselineResources: [...acceptedS3OwnershipNames].sort(),
  acceptedS4AlarmBaselineResources: [...acceptedS4OwnershipNames].sort(),
  acceptedS5WorkOrderBaselineResources: [...acceptedS5OwnershipNames].sort(),
  acceptedS4IAMAlarmResources: acceptedS4IAMOwnership.map((resource) => resource.name).sort(),
  acceptedHistoryAnalyticsResources: acceptedHistoryAnalyticsOwnership.map((resource) => resource.name).sort(),
  leakedOwnershipResources: [],
};
await writeFile(resolve(outputRoot, 'scope-audit-report.json'), `${JSON.stringify(scopeAudit, null, 2)}\n`);
const acceptanceResults = {
  schemaVersion: 1,
  ticket: '08-s0-release-evidence',
  status: 'source-assets-present',
  checkedAt: new Date().toISOString(),
  criteria: matrix.criteria.map((criterion) => ({
    id: criterion.id,
    status: 'source-assets-present',
    runtimeEvidence: criterion.runtimeEvidence,
  })),
};
await writeFile(resolve(outputRoot, 'acceptance-results.json'), `${JSON.stringify(acceptanceResults, null, 2)}\n`);
console.log(`S0 Ticket 08 release evidence assets passed: ${matrix.criteria.length} acceptance criteria mapped.`);
