import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());

function assert(condition, message) {
  if (!condition) throw new Error(`Invalid S2 implementation plan: ${message}`);
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

const [plan, releaseGates, ownership, publicContract, docs, packageJSON] = await Promise.all([
  readJSON('deploy/s2/implementation-plan.v1.json'),
  readJSON('deploy/s2/release-gates.v1.json'),
  readJSON('contracts/ownership/s2-telemetry-ownership.v1.json'),
  readJSON('contracts/http/s2-telemetry-public.openapi.json'),
  readText('docs/operations/s2-implementation-plan.md'),
  readJSON('package.json'),
]);

assert(plan.schemaVersion === 1, 'schemaVersion must remain 1');
assert(plan.activationStatus === 'planned', 'implementation plan must remain planned');
assert(plan.name === 's2-telemetry-implementation-plan-v1', 'plan name drifted');
assert(plan.mapIssue === 46, 'source map must remain #46');
assert(plan.planningIssue === 53, 'planning issue must remain #53');
assert(exact(plan.decisionIssues, [47, 48, 49, 50, 51, 52]), 'decision issue trace drifted');
assert(plan.ticketCount === 12, 'ticket count must remain 12');
assert(plan.dependencyEdgeCount === 25, 'dependency edge count must remain 25');
assert(exact(plan.initialFrontier, [60]), 'initial frontier must contain only #60');
assert(releaseGates.activationStatus === 'planned', 'release gates must remain planned');
assert(ownership.activationStatus === 'planned', 'ownership contract must remain planned');
assert(publicContract['x-activation-status'] === 'planned', 'public telemetry contract must remain planned');
assert(ownership.ownerService === 'telemetry-runtime-service', 'Telemetry Runtime owner drifted');
assert(releaseGates.authority?.businessOwner === ownership.ownerService, 'implementation owner differs from release/ownership authority');
assert(releaseGates.authority?.legacyFallback === false && releaseGates.authority?.mockFallback === false, 'fallback authority drifted');

const principles = [
  'one-ticket-one-short-lived-branch-one-merge',
  'every-ticket-leaves-runnable-machine-readable-evidence',
  'no-long-lived-integration-branch',
  'no-cross-service-big-bang-merge',
  'expand-contract-before-cutover',
  'telemetry-runtime-is-the-only-s2-business-writer',
  'no-request-level-legacy-thingsboard-or-mock-fallback',
  'snapshot-authority-survives-transport-reset-and-rollback',
  'new-adr-required-before-changing-accepted-semantics-or-release-envelope',
];
assert(exact(plan.deliveryPrinciples, principles), 'delivery principles drifted');

const expectedTickets = [
  {
    issue: 60,
    key: 'T01',
    title: 'S2 Ticket 01: 激活 Telemetry contract、ownership 与 PostgreSQL baseline',
    owner: 'Platform architecture / contracts / data foundations',
    stage: 'expand-baseline',
    blockedBy: [],
  },
  {
    issue: 61,
    key: 'T02',
    title: 'S2 Ticket 02: IAM 精确 Device/key 授权与撤权',
    owner: 'IAM service',
    stage: 'authorization',
    blockedBy: [60],
  },
  {
    issue: 62,
    key: 'T03',
    title: 'S2 Ticket 03: Telemetry Runtime 权威 Snapshot 纵向切片',
    owner: 'Telemetry Runtime service',
    stage: 'authoritative-owner',
    blockedBy: [60, 61],
  },
  {
    issue: 63,
    key: 'T04',
    title: 'S2 Ticket 04: ThingsBoard ingest、reconciliation、quarantine 与 outbox',
    owner: 'Telemetry integrations + Telemetry Runtime',
    stage: 'trusted-ingest',
    blockedBy: [62],
  },
  {
    issue: 64,
    key: 'T05',
    title: 'S2 Ticket 05: Gateway Snapshot 与 batch 公共路由',
    owner: 'Platform Gateway + contract generation',
    stage: 'public-snapshot',
    blockedBy: [61, 62],
  },
  {
    issue: 65,
    key: 'T06',
    title: 'S2 Ticket 06: Centrifugo 实时发布、订阅与恢复后端',
    owner: 'Telemetry Runtime + realtime platform',
    stage: 'realtime-backend',
    blockedBy: [61, 63, 64],
  },
  {
    issue: 66,
    key: 'T07',
    title: 'S2 Ticket 07: TelemetryLiveClient 与浏览器恢复适配器',
    owner: 'HVAC Web platform client',
    stage: 'browser-live-adapter',
    blockedBy: [64, 65],
  },
  {
    issue: 67,
    key: 'T08',
    title: 'S2 Ticket 08: Legacy shadow comparison 与 cohort routing',
    owner: 'Platform Gateway + migration/compatibility',
    stage: 'migration-control-plane',
    blockedBy: [63, 64, 65],
  },
  {
    issue: 68,
    key: 'T09',
    title: 'S2 Ticket 09: HVAC Web real Presence 与 latest telemetry',
    owner: 'HVAC Web feature team',
    stage: 'real-product-experience',
    blockedBy: [64, 66, 67],
  },
  {
    issue: 69,
    key: 'T10',
    title: 'S2 Ticket 10: 安全负向、可观测性与脱敏门禁',
    owner: 'Platform security + observability',
    stage: 'cross-chain-guardrails',
    blockedBy: [63, 64, 65, 66, 67, 68],
  },
  {
    issue: 70,
    key: 'T11',
    title: 'S2 Ticket 11: 容量、故障注入与 release evidence',
    owner: 'Release engineering + SRE',
    stage: 'release-certification',
    blockedBy: [69],
  },
  {
    issue: 71,
    key: 'T12',
    title: 'S2 Ticket 12: Canary、100% cutover 与 Legacy current-state retirement',
    owner: 'S2 release owner + Gateway/HVAC Web/Legacy owners',
    stage: 'production-cutover',
    blockedBy: [70],
  },
];

assert(Array.isArray(plan.tickets) && plan.tickets.length === expectedTickets.length, 'ticket array length drifted');
const issueSet = new Set();
const keySet = new Set();
let edgeCount = 0;
for (let index = 0; index < expectedTickets.length; index += 1) {
  const actual = plan.tickets[index];
  const expected = expectedTickets[index];
  assert(actual.sequence === index + 1, `ticket ${expected.key} sequence drifted`);
  assert(actual.issue === expected.issue, `ticket ${expected.key} issue number drifted`);
  assert(actual.key === expected.key, `issue #${expected.issue} key drifted`);
  assert(actual.title === expected.title, `issue #${expected.issue} title drifted`);
  assert(actual.owner === expected.owner, `issue #${expected.issue} owner drifted`);
  assert(actual.stage === expected.stage, `issue #${expected.issue} stage drifted`);
  assert(exact(actual.blockedBy, expected.blockedBy), `issue #${expected.issue} blockers drifted`);
  assert(!issueSet.has(actual.issue), `duplicate issue #${actual.issue}`);
  assert(!keySet.has(actual.key), `duplicate key ${actual.key}`);
  issueSet.add(actual.issue);
  keySet.add(actual.key);
  edgeCount += actual.blockedBy.length;
  assert(Array.isArray(actual.verticalEvidence) && actual.verticalEvidence.length >= 4, `issue #${actual.issue} lacks complete runnable evidence`);
  assert(Array.isArray(actual.decisionLocks) && actual.decisionLocks.length >= 3, `issue #${actual.issue} lacks decision locks`);
  for (const blocker of actual.blockedBy) {
    assert(issueSet.has(blocker), `issue #${actual.issue} blocker #${blocker} is not earlier in the topological order`);
  }
}
assert(edgeCount === plan.dependencyEdgeCount, 'computed dependency edge count differs from plan');
assert(plan.tickets.slice(0, -1).every((ticket) => ticket.productionTrafficAllowed === false), 'production traffic was enabled before the cutover ticket');
assert(plan.tickets.at(-1)?.productionTrafficAllowed === true, 'cutover ticket must be the only ticket allowed to move production traffic');

const computedFrontier = plan.tickets
  .filter((ticket) => ticket.blockedBy.length === 0)
  .map((ticket) => ticket.issue);
assert(exact(computedFrontier, plan.initialFrontier), 'computed initial frontier differs from declared frontier');

assert(plan.parallelGroups?.length === 2, 'parallel group count drifted');
assert(plan.parallelGroups[0]?.id === 'owner-output-parallelism', 'first parallel group drifted');
assert(exact(plan.parallelGroups[0]?.after, [62]), 'first parallel group prerequisite drifted');
assert(exact(plan.parallelGroups[0]?.tickets, [63, 64]), 'first parallel group tickets drifted');
assert(plan.parallelGroups[1]?.id === 'live-client-and-migration-parallelism', 'second parallel group drifted');
assert(exact(plan.parallelGroups[1]?.after, [65]), 'second parallel group prerequisite drifted');
assert(exact(plan.parallelGroups[1]?.tickets, [66, 67]), 'second parallel group tickets drifted');

const mapCloseConditions = [
  'decision-issues-47-through-53-are-closed',
  'implementation-issues-60-through-71-are-published-as-sub-issues-of-53',
  'all-native-blocking-edges-match-this-plan',
  'issue-60-is-the-only-initial-frontier',
  'implementation-plan-static-gate-passes',
  'map-not-yet-specified-is-empty',
  'planned-contracts-ownership-and-release-gates-remain-unactivated-until-ticket-60',
];
assert(exact(plan.mapCloseConditions, mapCloseConditions), 'map close conditions drifted');

const nextSliceConditions = [
  'issues-60-through-71-are-closed-with-accepted-evidence',
  'ticket-71-completion-attestation-is-passed',
  'active-contract-and-ownership-state-match-the-accepted-s2-decisions',
  'all-s2-security-zero-invariants-equal-zero',
  'release-envelope-v1-is-certified-or-a-new-reviewed-version-replaces-it',
  'legacy-current-state-latest-batch-and-websocket-have-zero-production-traffic-and-are-retired',
  'historical-timeseries-remains-an-explicit-separate-compatibility-boundary',
  'no-known-limitation-defers-tenant-security-business-correctness-audit-or-rollback',
];
assert(exact(plan.nextSliceEntryConditions, nextSliceConditions), 'next-slice entry conditions drifted');

const risks = [
  'ambiguous-snapshot-or-business-revision-owner',
  'non-owner-s2-business-write',
  'unauthenticated-source-ingest-affecting-s2',
  'registry-lifecycle-used-as-presence',
  'thingsboard-legacy-cache-or-redis-used-as-current-authority',
  'transport-position-or-cursor-used-as-business-authority',
  'subscription-or-cursor-use-without-current-authorization',
  'post-revocation-publication',
  'cross-organization-site-device-or-key-disclosure',
  'request-level-legacy-thingsboard-or-mock-fallback',
  'undetected-business-revision-gap',
  'raw-token-cursor-channel-or-telemetry-value-leak',
  'unclassified-shadow-difference',
  'capacity-or-rollback-evidence-missing',
  'legacy-current-state-production-traffic-remaining',
];
assert(exact(plan.risksToZeroBeforeNextSlice, risks), 'risk-zero set drifted');

for (const heading of [
  '## Delivery rules',
  '## Dependency graph',
  '## Ordered implementation tickets',
  '## Ticket execution protocol',
  '## ADR and contract policy',
  '## Evidence ownership by ticket',
  '## S2 specification map close conditions',
  '## Conditions before entering the next slice',
  '## Risks that must be zero',
  '## Planning verification',
]) {
  assert(docs.includes(heading), `handoff document is missing ${heading}`);
}
for (const ticket of expectedTickets) {
  assert(docs.includes(ticket.title), `handoff document is missing ticket title: ${ticket.title}`);
  assert(docs.includes(`/issues/${ticket.issue})`), `handoff document is missing issue link #${ticket.issue}`);
}
assert(docs.includes('The only initial frontier is #60.'), 'handoff document does not name the only initial frontier');
assert(docs.includes('all 25 native blocked-by edges'), 'handoff document does not lock the native edge count');
assert(docs.includes('Closing the map means the route is clear and implementation can start. It does not mean S2 is production-ready.'), 'map-close meaning is missing');

assert(packageJSON.scripts?.['s2:implementation-plan:check'] === 'node scripts/check-s2-implementation-plan.mjs', 's2:implementation-plan:check is not wired');

console.log('S2 implementation plan passed: 12 tracer bullets, 25 native dependencies, one initial frontier, bounded parallelism and risk-zero completion gates.');
