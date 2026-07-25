import { access, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const sources = await Promise.all([
  read('deploy/s2/cutover-plan.v1.json'),
  read('deploy/s2/full-cutover-attestation.schema.json'),
  read('deploy/s2/legacy-current-state-retirement.v1.json'),
  read('scripts/run-s2-cutover-certification.mjs'),
  read('scripts/verify-s2-completion-evidence.mjs'),
  read('scripts/test-s2-cutover-certification.mjs'),
  read('.github/workflows/s2-telemetry-cutover.yml'),
  read('docs/operations/s2-cutover-and-legacy-retirement.md'),
  read('deploy/s2/legacy-current-state-retirement.yaml'),
  read('package.json'),
  read('package-lock.json'),
  read('libs/ownershipregistry/s2_rollout_test.go'),
  read('apps/hvac-web/src/api/rest.ts'),
  read('apps/hvac-web/src/api/index.ts'),
  read('apps/hvac-web/src/layout/Header.tsx'),
]);
const [planRaw, schema, backendRaw, runner, verifier, test, workflow, runbook, networkPolicy, packageRaw, lockRaw, rolloutTest, legacyRest, apiIndex, headerSource] = sources;
const plan = JSON.parse(planRaw);
const backend = JSON.parse(backendRaw);
const packageJSON = JSON.parse(packageRaw);

const expectedPhases = [
  ['R1-dark-ingest', 0, 1440],
  ['R2-shadow-compare', 0, 1440],
  ['R3-internal-canary', 1, 120],
  ['R4-external-canary-5', 5, 240],
  ['R5-ramp-25', 25, 480],
  ['R6-ramp-50', 50, 720],
  ['R7-primary-100', 100, 1440],
  ['R8-legacy-current-state-retired', 100, 10080],
];
assert(plan.schemaVersion === 1 && plan.ticket === 71 && plan.appliedProductionPhase === 'R0-contract-only' && plan.formalPromotionRequired === true, 'cutover plan must remain fail-closed at R0');
assert(plan.phases?.length === expectedPhases.length, 'cutover plan must contain R1 through R8');
for (let index = 0; index < expectedPhases.length; index += 1) {
  const [id, trafficPercent, minimumHoldMinutes] = expectedPhases[index];
  const phase = plan.phases[index];
  assert(phase.id === id && phase.trafficPercent === trafficPercent && phase.minimumHoldMinutes === minimumHoldMinutes, `cutover phase ${id} drifted`);
  assert(phase.owner === phase.liveOwner, `cutover phase ${id} Snapshot/live ownership drifted`);
  if (index > 0) assert(phase.registryRevision > plan.phases[index - 1].registryRevision && phase.routeRevision > plan.phases[index - 1].routeRevision, `cutover phase ${id} revisions are not monotonic`);
}
assert(plan.routeSurfaces?.length === 4 && plan.rollback?.maximumDecisionMinutes === 5 && plan.rollback?.maximumRouteRollbackMinutes === 15, 'cutover route group or rollback objectives are incomplete');
assert(plan.retirement?.historicalCurrentStateFallbackForbidden === true && plan.retirement?.codeAndNetworkSealRequired === true, 'Legacy retirement boundary is incomplete');
assert(backend.schemaVersion === 1 && backend.ticket === 71 && backend.repository === plan.retirement.backendRepository && backend.pullRequest === plan.retirement.backendPullRequest && backend.commitSha === plan.retirement.backendCommitSha && backend.mergeCommitSha === plan.retirement.backendMergeCommitSha && backend.pullRequestState === 'MERGED' && backend.status === 'merged-verified', 'pinned Legacy backend retirement evidence is invalid');
assert(/^[0-9a-f]{40}$/.test(backend.commitSha) && backend.removedSurfaces?.length >= 6 && backend.removedCode?.length >= 6 && backend.retainedHistoricalSurfaces?.length === 2, 'Legacy backend retirement inventory is incomplete');
assert(backend.verification?.backendTestSuites === 27 && backend.verification?.backendTests === 148 && backend.verification?.buildPassed === true && backend.verification?.targetedTelemetryLintPassed === true, 'Legacy backend retirement verification is incomplete');
for (const name of ['r1-phase-report.json', 'r8-phase-report.json', 'promotion-approvals.json', 'zero-legacy-traffic-report.json', 'legacy-retirement-report.json', 's2-completion-attestation.json', 's2-completion.intoto.json', 'SHA256SUMS']) assert(plan.requiredEvidence?.includes(name), `cutover evidence ${name} is missing`);

for (const marker of ['repositorySha', 'releaseEvidence', 'phases', 'rollbackDrill', 'legacyObservation', 'retirement', 'backendRepositorySha', backend.commitSha, 'auditApproval', 'risks', 'minimum": 10080', 'formalReleaseEligible', 'historicalUsedAsCurrentFallback']) assert(schema.includes(marker), `full cutover attestation schema is missing ${marker}`);
for (const marker of ['configuration-only-preflight', 'formalCompletionEligible: false', 'verify-s2-release-evidence.mjs', "certificationLevel === 'formal'", 'approved-wall-clock-attestation', 'Legacy backend retirement evidence does not match', 'pinned Legacy backend commit', 'did not satisfy its real hold time', 'Snapshot and live ownership together', 'shorter than seven real days', 'allCompletionRisksZero', 's2-completion.intoto.json', 'SHA256SUMS']) assert(runner.includes(marker), `cutover runner is missing ${marker}`);
for (const marker of ['S2 completion digest mismatch', 'formal-production-cutover', 'R8-legacy-current-state-retired', 'sevenDayZeroLegacyTrafficVerified', 'reviewerCanVerifyOffline']) assert(verifier.includes(marker), `completion verifier is missing ${marker}`);
for (const marker of ['10079', 'cross_organization_successes', 'did not switch Snapshot and live ownership together', 'pinned Legacy backend commit', "'0'.repeat(64)", 'tampered']) assert(test.includes(marker), `cutover negative/tamper test is missing ${marker}`);

for (const marker of ['workflow_dispatch:', 'options: [preflight, formal]', 'environment: s2-production', 'release_run_id', 'cutover_attestation_json', 'actions/download-artifact@v6', 'run-id: ${{ inputs.release_run_id }}', 's2-telemetry-release-evidence', '--profile=formal', 's2-completion-evidence']) assert(workflow.includes(marker), `cutover workflow is missing ${marker}`);
assert(!workflow.includes('kubectl apply'), 'cutover workflow must verify rather than auto-promote production');
for (const marker of ['does not promote production traffic', 'seven real days', 'distinct primary and secondary owner approvals', 'five-minute decision', 'fifteen-minute route-revision', 'Historical data may never satisfy a current Snapshot', 'not production completion evidence', 'SwayingWindmill/hvac-backend', '#31', backend.commitSha]) assert(runbook.toLowerCase().includes(marker.toLowerCase()), `cutover runbook is missing ${marker}`);
for (const marker of ['kind: NetworkPolicy', 'name: deny-legacy-current-state', 'hvac.surface: legacy-current-state', 'ingress: []', 'egress: []', 'historicalCurrentStateFallback: "false"']) assert(networkPolicy.includes(marker), `Legacy current-state network seal is missing ${marker}`);

try {
  await access(resolve(root, 'apps/hvac-web/src/api/telemetry.ts'));
  throw new Error('Legacy browser Socket.IO telemetry client still exists');
} catch (error) {
  if (error?.message === 'Legacy browser Socket.IO telemetry client still exists') throw error;
}
assert(!packageJSON.dependencies?.['socket.io-client'] && !lockRaw.includes('node_modules/socket.io-client'), 'root browser Socket.IO dependency remains');
for (const forbidden of ["from './telemetry'", '/telemetry/devices/${deviceId}/latest', '/telemetry/latest/batch', 'telemetry.subscribe', 'telemetry.unsubscribe']) assert(!legacyRest.includes(forbidden), `browser Legacy current-state path remains: ${forbidden}`);
assert(legacyRest.includes('currentStateRetired') && legacyRest.includes('compatibilityRuntime.live.open') && legacyRest.includes('/telemetry/devices/${deviceId}/timeseries'), 'browser compatibility hooks are not S2-backed, fail-closed, or historical-only');
assert(!apiIndex.includes("from './telemetry'") && !headerSource.includes('telemetry.getStatus') && headerSource.includes('must not claim a global Socket.IO connection'), 'browser barrel or header still depends on the Legacy realtime client');
assert(rolloutTest.includes('TestS2FullPromotionSequenceAndR8RetirementAccepted'), 'ownership registry lacks full R1-R8 promotion coverage');
for (const script of ['s2:cutover:check', 's2:cutover:preflight', 's2:completion:verify', 'test:s2-cutover', 's2:ticket-12']) assert(packageJSON.scripts?.[script], `package script ${script} is missing`);
assert(!packageJSON.scripts?.['test:s2-legacy-retirement'] && !packageJSON.scripts?.['build:s2-legacy-retirement'], 'root clean runner must not depend on the ignored nested backend repository');
console.log('S2 Ticket 12 cutover, retirement, and completion assets passed.');
