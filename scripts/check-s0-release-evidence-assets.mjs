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
const s2ReleaseGates = JSON.parse(await readFile(resolve(root, 'deploy/s2/release-gates.v1.json'), 'utf8'));

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

for (const heading of ['## Reuse-first selection', '## Evidence model', '## Staging rolling update and rollback proof', '## Zero invariants', '## Reusable S0 surface for S1–S7', '## Legacy status', '## Known limitations', '## Approval and S0 completion']) {
  assert(operations.includes(heading), `release evidence runbook is missing ${heading}`);
}
for (const upstream of ['in-toto/attestation v1.2.0', 'slsa-framework/slsa-verifier v2.7.1', 'oras-project/oras v1.3.3', 'kubernetes-sigs/kind v0.32.0', 'helm/kind-action v1.14.0']) {
  assert(operations.includes(upstream), `release evidence runbook is missing upstream decision: ${upstream}`);
}
assert(operations.includes('NestJS remains Legacy Frozen and private'), 'release evidence runbook must preserve the Legacy Frozen decision');
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
assert(schema.properties?.predicate?.properties?.images?.minItems === 7, 'bundle schema must require seven images');

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
  'iam-onboarding-reconciliation',
  'iam-reconciliation-quarantine',
  's1-legacy-resource-map',
  's1-migration-provenance',
  's1-migration-quarantine',
  ...acceptedS2OwnershipNames,
]);
const leakedOwnership = ownership.resources.filter((resource) => !allowedOwnershipNames.has(resource.name));
assert(leakedOwnership.length === 0, `ownership registry contains resources outside the accepted S1/S2 baselines: ${JSON.stringify(leakedOwnership)}`);

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
