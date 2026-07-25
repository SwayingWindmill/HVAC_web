import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const profile = arg('profile', process.env.S2_CUTOVER_PROFILE ?? 'preflight');
const outputDir = resolve(root, arg('output-dir', 'out/s2-completion-evidence'));
const plan = JSON.parse(await readFile(resolve(root, 'deploy/s2/cutover-plan.v1.json'), 'utf8'));
const gates = JSON.parse(await readFile(resolve(root, 'deploy/s2/release-gates.v1.json'), 'utf8'));
const implementationPlan = JSON.parse(await readFile(resolve(root, 'deploy/s2/implementation-plan.v1.json'), 'utf8'));
const backendRetirement = JSON.parse(await readFile(resolve(root, plan.retirement.backendEvidence), 'utf8'));
const expectedRepositorySha = process.env.GITHUB_SHA ?? execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const parseTime = (value, label) => {
  const timestamp = Date.parse(value);
  assert(Number.isFinite(timestamp), `${label} is not an ISO date-time`);
  return timestamp;
};

assert(['preflight', 'formal'].includes(profile), `unsupported S2 cutover profile: ${profile}`);
assert(plan.schemaVersion === 1 && plan.ticket === 71 && plan.formalPromotionRequired === true, 'S2 cutover plan is invalid');
assert(plan.appliedProductionPhase === 'R0-contract-only', 'repository cutover plan must remain non-promoting before formal certification');
assert(backendRetirement.repository === plan.retirement.backendRepository && backendRetirement.pullRequest === plan.retirement.backendPullRequest && backendRetirement.commitSha === plan.retirement.backendCommitSha && backendRetirement.mergeCommitSha === plan.retirement.backendMergeCommitSha && backendRetirement.pullRequestState === 'MERGED' && backendRetirement.status === 'merged-verified', 'Legacy backend retirement evidence does not match the merged pinned repository change');
assert(plan.routeSurfaces?.length === 4 && new Set(plan.routeSurfaces.map((entry) => `${entry.method} ${entry.path}`)).size === 4, 'S2 cutover route surfaces are incomplete');
const expectedPhases = gates.rolloutPhases.filter((phase) => phase.id !== 'R0-contract-only');
assert(plan.phases?.length === expectedPhases.length, 'S2 cutover phase count drifted from release gates');
for (let index = 0; index < expectedPhases.length; index += 1) {
  const planned = plan.phases[index];
  const gated = expectedPhases[index];
  assert(planned.id === gated.id && planned.trafficPercent === gated.trafficPercent && planned.minimumHoldMinutes === gated.minimumHoldMinutes, `S2 cutover phase ${gated.id} drifted from release gates`);
  if (index > 0) {
    assert(planned.registryRevision > plan.phases[index - 1].registryRevision && planned.routeRevision > plan.phases[index - 1].routeRevision, `S2 cutover phase ${planned.id} revisions are not monotonic`);
  }
}
assert(new Set(plan.requiredEvidence).size === plan.requiredEvidence.length && plan.requiredEvidence.includes('s2-completion-attestation.json') && plan.requiredEvidence.includes('SHA256SUMS'), 'S2 completion evidence list is invalid');

await mkdir(outputDir, { recursive: true });
if (profile === 'preflight') {
  const report = {
    schemaVersion: 1,
    ticket: 71,
    status: 'passed',
    profile,
    certificationLevel: 'configuration-only-preflight',
    formalCompletionEligible: false,
    repositorySha: expectedRepositorySha,
    appliedProductionPhase: plan.appliedProductionPhase,
    statement: 'Configuration-only preflight. No production phase is promoted and no elapsed hold, approval, traffic, or retirement evidence is claimed.',
    checks: [
      'r1-through-r8-plan-matches-release-gates',
      'route-revisions-are-monotonic',
      'formal-profile-requires-approved-attestation',
      'formal-profile-requires-formal-release-evidence',
      'manual-promotion-required',
      'seven-day-zero-legacy-observation-required',
      'historical-timeseries-boundary-retained',
    ],
  };
  await writeFile(resolve(outputDir, 'cutover-preflight-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S2 cutover certification (${profile}) passed: ${outputDir}`);
  process.exit(0);
}

const attestationPath = arg('attestation');
const releaseEvidenceDir = resolve(root, arg('release-evidence-dir'));
assert(attestationPath, 'formal cutover profile requires --attestation=<json>');
assert(arg('release-evidence-dir'), 'formal cutover profile requires --release-evidence-dir=<directory>');
const verify = spawnSync(process.execPath, [resolve(root, 'scripts/verify-s2-release-evidence.mjs'), `--directory=${releaseEvidenceDir}`], { cwd: root, encoding: 'utf8', windowsHide: true });
assert(verify.status === 0, `formal S2 release evidence failed offline verification: ${verify.stdout}\n${verify.stderr}`);
const capacity = JSON.parse(await readFile(resolve(releaseEvidenceDir, 'capacity-report.json'), 'utf8'));
assert(capacity.repositorySha === expectedRepositorySha, 'formal release evidence repository SHA does not match the cutover SHA');
assert(capacity.formalReleaseEligible === true && capacity.certificationLevel === 'formal' && capacity.measurementSource === 'approved-wall-clock-attestation', 'formal cutover requires measured formal capacity evidence');
const releaseChecksumsRaw = await readFile(resolve(releaseEvidenceDir, 'SHA256SUMS'));

const attestationRaw = await readFile(resolve(root, attestationPath));
const attestation = JSON.parse(attestationRaw.toString('utf8'));
assert(attestation.schemaVersion === 1, 'cutover attestation schema version is unsupported');
assert(attestation.repositorySha === expectedRepositorySha, `cutover attestation repository SHA ${attestation.repositorySha} does not match ${expectedRepositorySha}`);
assert(attestation.releaseEvidence?.formalReleaseEligible === true && attestation.releaseEvidence?.certificationLevel === 'formal', 'cutover attestation does not bind formal release evidence');
assert(attestation.releaseEvidence?.artifactSha256 === sha256(releaseChecksumsRaw), 'cutover attestation release evidence digest does not match SHA256SUMS');
assert(String(attestation.releaseEvidence?.workflowRunId ?? '').trim(), 'cutover attestation lacks the formal release workflow run ID');
assert(Array.isArray(attestation.phases) && attestation.phases.length === plan.phases.length, 'cutover attestation must contain exactly R1 through R8');

const now = Date.now();
const approvals = [];
for (let index = 0; index < plan.phases.length; index += 1) {
  const expected = plan.phases[index];
  const observed = attestation.phases[index];
  assert(observed?.id === expected.id, `cutover phase order or id is invalid at ${expected.id}`);
  assert(observed.registryRevision === expected.registryRevision && observed.routeRevision === expected.routeRevision, `cutover phase ${expected.id} route revision is invalid`);
  assert(observed.trafficPercent === expected.trafficPercent, `cutover phase ${expected.id} traffic percent is invalid`);
  const startedAt = parseTime(observed.startedAt, `${expected.id}.startedAt`);
  const endedAt = parseTime(observed.endedAt, `${expected.id}.endedAt`);
  assert(endedAt >= startedAt && endedAt <= now + 300000, `cutover phase ${expected.id} contains an invalid time window`);
  const elapsedMinutes = Math.floor((endedAt - startedAt) / 60000);
  assert(elapsedMinutes >= expected.minimumHoldMinutes && observed.holdMinutes >= expected.minimumHoldMinutes && observed.holdMinutes <= elapsedMinutes + 1, `cutover phase ${expected.id} did not satisfy its real hold time`);
  assert(observed.sampleCounts?.snapshotRequests >= (expected.minimumSnapshotRequests ?? 0), `cutover phase ${expected.id} Snapshot sample minimum was not met`);
  assert(observed.sampleCounts?.subscriptions >= (expected.minimumSubscriptions ?? 0), `cutover phase ${expected.id} subscription sample minimum was not met`);
  assert(observed.sampleCounts?.recoveryAttempts >= (expected.minimumRecoveryAttempts ?? 0), `cutover phase ${expected.id} recovery sample minimum was not met`);
  assert(Number.isFinite(observed.sloBurnRate) && observed.sloBurnRate <= gates.promotionGates.sloBurnRateMaximum, `cutover phase ${expected.id} exceeded the error-budget burn gate`);
  assert(Number.isFinite(observed.capacityHeadroom) && observed.capacityHeadroom >= gates.promotionGates.capacityHeadroomMinimumFraction, `cutover phase ${expected.id} has less than 30% headroom`);
  for (const invariant of gates.securityZeroInvariants) assert(observed.zeroCounters?.[invariant] === 0, `cutover phase ${expected.id} security invariant ${invariant} is non-zero or missing`);
  assert(observed.unclassifiedShadowDifferences === 0 && observed.contractDrift === 0 && observed.ownershipDrift === 0 && observed.requestFallbacks === 0, `cutover phase ${expected.id} has drift, fallback, or unclassified differences`);
  assert(observed.snapshotOwner === expected.owner && observed.liveOwner === expected.liveOwner, `cutover phase ${expected.id} did not switch Snapshot and live ownership together`);
  const approval = observed.approval ?? {};
  const approvedAt = parseTime(approval.approvedAt, `${expected.id}.approval.approvedAt`);
  assert(approval.manual === true && String(approval.primaryOwner ?? '').trim() && String(approval.secondaryOwner ?? '').trim(), `cutover phase ${expected.id} lacks manual owner approval`);
  assert(approval.primaryOwner !== approval.secondaryOwner, `cutover phase ${expected.id} approval owners must be distinct`);
  assert(approvedAt >= endedAt && approvedAt <= now + 300000, `cutover phase ${expected.id} approval was not recorded after the hold window`);
  approvals.push({ phase: expected.id, ...approval });
}

const rollback = attestation.rollbackDrill ?? {};
assert(rollback.passed === true && rollback.decisionMinutes <= plan.rollback.maximumDecisionMinutes && rollback.routeRollbackMinutes <= plan.rollback.maximumRouteRollbackMinutes, 'rollback drill exceeded the five/fifteen-minute objectives');
assert(rollback.disconnectOrExpireLiveSessions === true && rollback.freshSnapshotRequired === true && rollback.databaseDownMigrationPerformed === false, 'rollback drill did not invalidate sessions, require a fresh Snapshot, or preserve expand-only migrations');
assert(Number.isInteger(rollback.fromRouteRevision) && Number.isInteger(rollback.toRouteRevision) && rollback.toRouteRevision < rollback.fromRouteRevision, 'rollback drill route revisions are invalid');

const observation = attestation.legacyObservation ?? {};
const observationStart = parseTime(observation.startedAt, 'legacyObservation.startedAt');
const observationEnd = parseTime(observation.endedAt, 'legacyObservation.endedAt');
const observationMinutes = Math.floor((observationEnd - observationStart) / 60000);
assert(observationEnd <= now + 300000 && observationMinutes >= 10080 && observation.durationMinutes >= 10080 && observation.durationMinutes <= observationMinutes + 1, 'Legacy zero-traffic observation is shorter than seven real days');
for (const key of ['latestRequests', 'batchRequests', 'websocketConnections', 'legacyWritesRequired', 'browserReferences', 'unauthenticatedIngestEffectsOnS2']) {
  assert(observation[key] === 0, `Legacy retirement observation ${key} is non-zero`);
}

const retirement = attestation.retirement ?? {};
assert(retirement.publicRouteOwner === 'telemetry-runtime-service', 'final public current-state owner is not Telemetry Runtime');
assert(retirement.backendRepositorySha === plan.retirement.backendCommitSha, 'formal retirement attestation does not bind the pinned Legacy backend commit');
assert(retirement.currentStateEndpointsRemoved === true && retirement.currentStateNetworkPolicyDenied === true && retirement.socketIoRemoved === true, 'Legacy current-state surfaces are not code-and-network sealed');
assert(retirement.historicalTimeseriesRetained === true && retirement.historicalUsedAsCurrentFallback === false, 'historical timeseries boundary was removed or used as current-state fallback');
const audit = attestation.auditApproval ?? {};
assert(audit.manual === true && String(audit.approvedBy ?? '').trim() && String(audit.statement ?? '').trim(), 'final Audit approval is incomplete');
assert(parseTime(audit.approvedAt, 'auditApproval.approvedAt') >= observationEnd, 'final Audit approval predates the seven-day observation');
for (const risk of implementationPlan.risksToZeroBeforeNextSlice) assert(attestation.risks?.[risk] === 0, `S2 completion risk ${risk} is non-zero or missing`);

const evidence = new Map();
const writeEvidence = async (name, value) => {
  const raw = Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
  await writeFile(resolve(outputDir, name), raw);
  evidence.set(name, sha256(raw));
};
for (let index = 0; index < attestation.phases.length; index += 1) {
  await writeEvidence(`r${index + 1}-phase-report.json`, { schemaVersion: 1, status: 'passed', formal: true, repositorySha: expectedRepositorySha, ...attestation.phases[index] });
}
await writeEvidence('promotion-approvals.json', { schemaVersion: 1, status: 'passed', manualPromotion: true, approvals });
await writeEvidence('rollback-drill-report.json', { schemaVersion: 1, status: 'passed', ...rollback });
await writeEvidence('zero-legacy-traffic-report.json', { schemaVersion: 1, status: 'passed', sevenDayObservation: true, ...observation });
await writeEvidence('legacy-retirement-report.json', { schemaVersion: 1, status: 'passed', codeAndNetworkSealed: true, ...retirement });
await writeEvidence('final-route-ownership-report.json', {
  schemaVersion: 1,
  status: 'passed',
  phase: 'R8-legacy-current-state-retired',
  repositorySha: expectedRepositorySha,
  registryRevision: plan.phases.at(-1).registryRevision,
  routeRevision: plan.phases.at(-1).routeRevision,
  routeSurfaces: plan.routeSurfaces.map((surface) => ({ ...surface, snapshotOwner: 'telemetry-runtime-service', liveOwner: 'telemetry-runtime-service', requestFallback: false })),
  historicalTimeseriesOwner: 'legacy-historical-compatibility',
  historicalCurrentStateFallback: false,
});
const completion = {
  schemaVersion: 1,
  ticket: 71,
  status: 'passed',
  formalCompletionEligible: true,
  certificationLevel: 'formal-production-cutover',
  repositorySha: expectedRepositorySha,
  releaseWorkflowRunId: attestation.releaseEvidence.workflowRunId,
  releaseEvidenceSha256: attestation.releaseEvidence.artifactSha256,
  sourceAttestationSha256: sha256(attestationRaw),
  backendRepositorySha: retirement.backendRepositorySha,
  completedPhase: 'R8-legacy-current-state-retired',
  manualPromotion: true,
  auditApproval: audit,
  allSecurityZeroInvariants: true,
  allCompletionRisksZero: true,
  legacyCurrentStateTrafficRemaining: 0,
  historicalTimeseriesRetained: true,
  completedAt: audit.approvedAt,
};
await writeEvidence('s2-completion-attestation.json', completion);
const statementSubjects = [...evidence].map(([name, digest]) => ({ name, digest: { sha256: digest } }));
const statement = {
  _type: 'https://in-toto.io/Statement/v1',
  subject: statementSubjects,
  predicateType: 'https://hvac.local/attestations/s2-completion/v1',
  predicate: {
    ticket: 71,
    repositorySha: expectedRepositorySha,
    backendRepositorySha: retirement.backendRepositorySha,
    completedPhase: 'R8-legacy-current-state-retired',
    formalReleaseEvidenceVerified: true,
    manualPromotionApprovalsRecorded: true,
    sevenDayZeroLegacyTrafficVerified: true,
    legacyCurrentStateCodeAndNetworkSealed: true,
    historicalTimeseriesRetained: true,
    allSecurityZeroInvariants: true,
    allCompletionRisksZero: true,
    reviewerCanVerifyOffline: true,
  },
};
await writeEvidence('s2-completion.intoto.json', statement);
const checksumLines = [...evidence].sort(([a], [b]) => a.localeCompare(b)).map(([name, digest]) => `${digest}  ${basename(name)}`);
await writeFile(resolve(outputDir, 'SHA256SUMS'), `${checksumLines.join('\n')}\n`);
console.log(`S2 cutover certification (${profile}) passed: ${outputDir}`);
