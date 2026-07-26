import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { basename, resolve } from 'node:path';
import { mkdir, readFile, writeFile } from 'node:fs/promises';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const profile = arg('profile', process.env.S3_COMMAND_CERTIFICATION_PROFILE ?? 'preflight');
const outputDir = resolve(root, arg('output-dir', 'out/s3-ticket-09'));
const expectedRepositorySha = process.env.GITHUB_SHA ?? execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const readJSON = async (path) => JSON.parse(await readFile(resolve(root, path), 'utf8'));
const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const parseTime = (value, label) => {
  const timestamp = Date.parse(value);
  assert(Number.isFinite(timestamp), `${label} is not an ISO date-time`);
  return timestamp;
};

assert(['preflight', 'formal'].includes(profile), `unsupported S3 certification profile: ${profile}`);
const [envelope, plan, routes, providerContract] = await Promise.all([
  readJSON('deploy/s3/certification-envelope.v1.json'),
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/thingsboard/s3-set-temperature-setpoint.local.v1.json'),
]);

assert(envelope.schemaVersion === 1 && envelope.ticket === 'S3-09', 'S3 certification envelope is invalid');
assert(plan.productionTrafficPercent === 0, 'repository S3 plan must remain at zero production traffic before formal promotion');
assert(JSON.stringify(plan.currentFrontier) === JSON.stringify(['S3-09']), 'S3-09 must remain the active frontier until formal certification is accepted');
assert(!plan.completedTickets?.includes('S3-09'), 'S3-09 cannot be marked complete by configuration-only certification');
assert(plan.firstTracerBullet?.publicRoutesEnabled === false, 'Command public routes must remain disabled during S3-09 certification');
assert(plan.firstTracerBullet?.productionProviderEnabled === false, 'production provider must remain disabled during S3-09 certification');
const commandRoutes = (routes.routes ?? []).filter((route) => route.owner === 'command-service');
assert(commandRoutes.length === 3, 'exactly three public Command routes must be registered');
assert(commandRoutes.every((route) => route.rollout?.mode === 'disabled' && route.shadowSideEffectPolicy === 'SYNTHETIC_ONLY'), 'public Command routes must stay disabled and Synthetic-only');
assert(providerContract.productionEligible === false && providerContract.verificationStatus === 'LOCAL_VERIFIED', 'repository ThingsBoard mapping must remain local-only before formal certification');
assert(new Set(envelope.requiredCrashPoints).size === envelope.requiredCrashPoints.length, 'required crash-point names must be unique');
assert(new Set(envelope.zeroCounters).size === envelope.zeroCounters.length, 'zero-counter names must be unique');
assert(new Set(envelope.requiredEvidence).size === envelope.requiredEvidence.length, 'required evidence names must be unique');

await mkdir(outputDir, { recursive: true });
if (profile === 'preflight') {
  const report = {
    schemaVersion: 1,
    ticket: 'S3-09',
    status: 'passed',
    profile,
    certificationLevel: 'configuration-and-deterministic-test-preflight',
    formalCertificationEligible: false,
    repositorySha: expectedRepositorySha,
    productionTrafficPercent: 0,
    statement: 'Preflight only. It validates the certification contract and deterministic safety-test inventory; it does not claim target-environment capacity, a VERIFIED production mapping, manual approval, elapsed canary hold time, or a real Device side effect.',
    capacityEnvelope: envelope.capacity,
    recoveryObjectives: envelope.recoveryObjectives,
    requiredCrashPoints: envelope.requiredCrashPoints,
    zeroCounters: envelope.zeroCounters,
    internalCanary: envelope.internalCanary,
    checks: [
      'public-command-routes-remain-disabled',
      'repository-provider-remains-local-verified-and-production-ineligible',
      'formal-profile-requires-repository-sha-bound-attestation',
      'formal-profile-requires-100-command-per-second-steady-load',
      'formal-profile-requires-1000-command-per-second-one-minute-burst',
      'formal-profile-requires-complete-crash-point-matrix',
      'formal-profile-requires-all-zero-safety-counters',
      'formal-profile-requires-one-device-low-risk-approved-canary',
      'formal-profile-requires-four-hour-canary-hold',
      'formal-profile-requires-future-command-only-rollback-drill'
    ]
  };
  await writeFile(resolve(outputDir, 'certification-preflight-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S3 Command certification (${profile}) passed: ${outputDir}`);
  process.exit(0);
}

const attestationPath = arg('attestation');
assert(attestationPath, 'formal S3 certification requires --attestation=<json>');
const attestationRaw = await readFile(resolve(root, attestationPath));
const attestation = JSON.parse(attestationRaw.toString('utf8'));
assert(attestation.schemaVersion === 1, 'formal attestation schema version is unsupported');
assert(attestation.repositorySha === expectedRepositorySha, `formal attestation repository SHA ${attestation.repositorySha} does not match ${expectedRepositorySha}`);
assert(String(attestation.workflowRunId ?? '').trim(), 'formal attestation lacks workflowRunId');
assert(attestation.environment && Object.keys(attestation.environment).length > 0, 'formal attestation lacks target environment evidence');

const load = attestation.load ?? {};
assert(load.steadyCommandsPerSecond >= envelope.capacity.steadyState.commandsPerSecond, 'steady Command throughput is below 100 commands/s');
assert(load.steadyDurationSeconds >= envelope.capacity.steadyState.minimumDurationSeconds, 'steady Command run is shorter than the certification minimum');
assert(load.burstCommandsPerSecond >= envelope.capacity.burst.commandsPerSecond, 'burst Command throughput is below 1000 commands/s');
assert(load.burstDurationSeconds >= envelope.capacity.burst.durationSeconds, 'burst Command run is shorter than 60 seconds');

const observed = attestation.observed ?? {};
const slo = envelope.capacity.latencySlo;
for (const key of [
  'acceptedToSubmittedP95Milliseconds', 'acceptedToSubmittedP99Milliseconds',
  'governanceP95Milliseconds', 'governanceP99Milliseconds',
  'queuedReadyToSendStartedP95Milliseconds', 'queuedReadyToSendStartedP99Milliseconds',
  'statusPropagationP95Milliseconds', 'statusPropagationP99Milliseconds',
  'cpuPeakFraction', 'memoryPeakFraction', 'postgresPoolPeakFraction', 'networkPeakFraction', 'minimumHeadroomFraction'
]) assert(Number.isFinite(observed[key]), `formal attestation is missing observed.${key}`);
assert(observed.acceptedToSubmittedP95Milliseconds <= slo.acceptedToSubmittedP95MillisecondsMaximum, 'accepted-to-SUBMITTED p95 exceeds the SLO');
assert(observed.acceptedToSubmittedP99Milliseconds <= slo.acceptedToSubmittedP99MillisecondsMaximum, 'accepted-to-SUBMITTED p99 exceeds the SLO');
assert(observed.governanceP95Milliseconds <= slo.governanceP95MillisecondsMaximum, 'governance p95 exceeds the SLO');
assert(observed.governanceP99Milliseconds <= slo.governanceP99MillisecondsMaximum, 'governance p99 exceeds the SLO');
assert(observed.queuedReadyToSendStartedP95Milliseconds <= slo.queuedReadyToSendStartedP95MillisecondsMaximum, 'QUEUED_READY-to-SEND_STARTED p95 exceeds the SLO');
assert(observed.queuedReadyToSendStartedP99Milliseconds <= slo.queuedReadyToSendStartedP99MillisecondsMaximum, 'QUEUED_READY-to-SEND_STARTED p99 exceeds the SLO');
assert(observed.statusPropagationP95Milliseconds <= slo.statusPropagationP95MillisecondsMaximum, 'status propagation p95 exceeds the SLO');
assert(observed.statusPropagationP99Milliseconds <= slo.statusPropagationP99MillisecondsMaximum, 'status propagation p99 exceeds the SLO');
const computedHeadroom = 1 - Math.max(observed.cpuPeakFraction, observed.memoryPeakFraction, observed.postgresPoolPeakFraction, observed.networkPeakFraction);
assert(computedHeadroom >= envelope.capacity.minimumHeadroomFraction, 'resource peaks leave less than 30% computed headroom');
assert(observed.minimumHeadroomFraction >= envelope.capacity.minimumHeadroomFraction && observed.minimumHeadroomFraction <= computedHeadroom + 0.001, 'reported minimum headroom is below 30% or inconsistent with resource peaks');

for (const key of envelope.zeroCounters) assert(attestation.zeroCounters?.[key] === 0, `zero-tolerance counter ${key} is non-zero or missing`);
const crashPoints = attestation.crashPoints ?? [];
const crashPointNames = new Set(crashPoints.map((entry) => entry.name));
assert(crashPoints.length === envelope.requiredCrashPoints.length && crashPointNames.size === envelope.requiredCrashPoints.length, 'formal crash-point matrix has duplicates or an unexpected count');
for (const name of envelope.requiredCrashPoints) {
  const entry = crashPoints.find((candidate) => candidate.name === name);
  assert(entry?.passed === true && String(entry.evidence ?? '').trim(), `crash point ${name} lacks passing evidence`);
}
const recovery = new Map([
  ['dispatcher-crash-after-claim-before-connector-result', envelope.recoveryObjectives.singlePodSecondsMaximum],
  ['consumer-rebalance', envelope.recoveryObjectives.consumerRebalanceSecondsMaximum],
  ['connector-crash-after-request-commit', envelope.recoveryObjectives.connectorInstanceSecondsMaximum],
  ['single-availability-zone-loss', envelope.recoveryObjectives.singleAvailabilityZoneSecondsMaximum],
]);
for (const [name, maximum] of recovery) {
  const entry = crashPoints.find((candidate) => candidate.name === name);
  assert(Number.isFinite(entry?.recoverySeconds) && entry.recoverySeconds <= maximum, `${name} exceeds its recovery objective`);
}

const canaryPolicy = envelope.internalCanary;
const canary = attestation.canary ?? {};
assert(canary.phase === canaryPolicy.phase, 'canary phase is not S3-R2-internal-low-risk');
assert(canary.organizationCount === canaryPolicy.organizationCount && canary.siteCount === canaryPolicy.siteCount && canary.deviceCount === canaryPolicy.deviceCount, 'canary cohort is not exactly one Organization, one Site and one Device');
assert(String(canary.organizationId ?? '').trim() && String(canary.siteId ?? '').trim() && String(canary.deviceId ?? '').trim(), 'canary cohort identifiers are incomplete');
assert(canary.capability === canaryPolicy.capability && canary.capabilityRevision === canaryPolicy.capabilityRevision, 'canary capability or revision is invalid');
assert(canary.risk === canaryPolicy.risk && canary.maximumObservedSetpointDeltaC <= canaryPolicy.maximumSetpointDeltaC, 'canary exceeded the LOW-risk setpoint delta');
assert(canary.mappingStatus === canaryPolicy.mappingStatusRequired && canary.providerContract === canaryPolicy.providerContract, 'canary did not use the required VERIFIED ThingsBoard provider contract');
assert(canary.s2CurrentStateProductionCertified === true, 'canary S2 current-state cohort is not production-certified');
assert(canary.publicRoutesEnabled === false && canary.productionTrafficPercent === 0, 'canary enabled public or production Command traffic');
assert(canary.operatorConfirmedEachCommand === true, 'canary lacks operator confirmation between commands');
assert(canary.commandCount >= canaryPolicy.minimumCommands && canary.commandCount <= canaryPolicy.maximumCommands, 'canary command count is outside the approved bounded cohort');
assert(canary.verifiedCommandCount === canary.commandCount, 'not every canary Command reached reported-state VERIFIED success');
assert(canary.outcomeUnknownCount === 0 && canary.automaticReissuesAfterRequestCommitted === 0, 'canary produced uncertainty or a forbidden automatic reissue');
assert(/^(secret|workload):\/\//.test(String(canary.credentialReference ?? '')), 'canary credential evidence must be an opaque secret:// or workload:// reference');
const certificationNow = Date.now();
const canaryStartedAt = parseTime(canary.startedAt, 'canary.startedAt');
const canaryEndedAt = parseTime(canary.endedAt, 'canary.endedAt');
const elapsedCanaryMinutes = Math.floor((canaryEndedAt - canaryStartedAt) / 60000);
assert(canaryEndedAt >= canaryStartedAt && canaryEndedAt <= certificationNow + 300000 && elapsedCanaryMinutes >= canaryPolicy.minimumHoldMinutes, 'canary time window is in the future, reversed, or shorter than four hours');
assert(canary.holdMinutes >= canaryPolicy.minimumHoldMinutes && canary.holdMinutes <= elapsedCanaryMinutes + 1, 'canary holdMinutes is below the policy or inconsistent with timestamps');

const approval = attestation.approval ?? {};
const approvedAt = parseTime(approval.approvedAt, 'approval.approvedAt');
assert(approval.manual === true && String(approval.primaryOwner ?? '').trim() && String(approval.secondaryOwner ?? '').trim() && String(approval.statement ?? '').trim(), 'manual canary approval is incomplete');
assert(approval.primaryOwner !== approval.secondaryOwner, 'canary approval owners must be distinct');
assert(approvedAt >= canaryEndedAt && approvedAt <= certificationNow + 300000, 'formal approval predates the canary hold completion or is in the future');

const rollback = attestation.rollback ?? {};
assert(rollback.passed === true, 'future-command rollback drill did not pass');
assert(rollback.futureCommandsOnly === true && rollback.acceptedCommandsRemainWithOriginalOwner === true, 'rollback violated accepted-command ownership');
assert(rollback.connectorCredentialDisabledMinutes <= canaryPolicy.rollback.disableConnectorCredentialMaximumMinutes, 'connector credential disable exceeded the five-minute objective');

const evidence = new Map();
const writeEvidence = async (name, value) => {
  const raw = Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
  await writeFile(resolve(outputDir, name), raw);
  evidence.set(name, sha256(raw));
};
await writeEvidence('capacity-report.json', { schemaVersion: 1, status: 'passed', formal: true, repositorySha: expectedRepositorySha, environment: attestation.environment, load, observed });
await writeEvidence('failure-injection-report.json', { schemaVersion: 1, status: 'passed', formal: true, scenarios: crashPoints });
await writeEvidence('command-fence-report.json', {
  schemaVersion: 1,
  status: 'passed',
  oldFenceExecutions: attestation.zeroCounters.oldFenceExecutions,
  duplicateDeviceSideEffects: attestation.zeroCounters.duplicateDeviceSideEffects,
  blindRetriesAfterRequestCommitted: attestation.zeroCounters.blindRetriesAfterRequestCommitted,
  relevantCrashPoints: crashPoints.filter((entry) => ['pre-send-rejection-safe-retry', 'request-committed-timeout-no-reissue', 'late-old-fence-result', 'dispatcher-crash-after-claim-before-connector-result'].includes(entry.name))
});
await writeEvidence('internal-canary-report.json', { schemaVersion: 1, status: 'passed', formal: true, ...canary });
await writeEvidence('promotion-approvals.json', { schemaVersion: 1, status: 'passed', manualPromotion: true, approval });
await writeEvidence('security-zero-report.json', { schemaVersion: 1, status: 'passed', zeroCounters: attestation.zeroCounters });
await writeEvidence('rollback-report.json', { schemaVersion: 1, status: 'passed', ...rollback });
const certification = {
  schemaVersion: 1,
  ticket: 'S3-09',
  status: 'passed',
  formalCertificationEligible: true,
  certificationLevel: 'target-environment-capacity-crash-point-and-approved-internal-device-canary',
  repositorySha: expectedRepositorySha,
  workflowRunId: attestation.workflowRunId,
  sourceAttestationSha256: sha256(attestationRaw),
  completedPhase: canary.phase,
  productionTrafficPercent: 0,
  publicRoutesEnabled: false,
  allZeroCountersPassed: true,
  allCrashPointsPassed: true,
  canaryCommandCount: canary.commandCount,
  completedAt: approval.approvedAt
};
await writeEvidence('s3-certification-attestation.json', certification);
const statement = {
  _type: 'https://in-toto.io/Statement/v1',
  subject: [...evidence].map(([name, digest]) => ({ name, digest: { sha256: digest } })),
  predicateType: 'https://hvac.local/attestations/s3-command-certification/v1',
  predicate: {
    ticket: 'S3-09',
    repositorySha: expectedRepositorySha,
    capacityEnvelopePassed: true,
    crashPointMatrixPassed: true,
    commandFenceZeroInvariantsPassed: true,
    approvedInternalLowRiskCanaryPassed: true,
    publicProductionRoutesRemainDisabled: true,
    reviewerCanVerifyOffline: true
  }
};
await writeEvidence('s3-certification.intoto.json', statement);
const checksumLines = [...evidence].sort(([a], [b]) => a.localeCompare(b)).map(([name, digest]) => `${digest}  ${basename(name)}`);
await writeFile(resolve(outputDir, 'SHA256SUMS'), `${checksumLines.join('\n')}\n`);
console.log(`S3 Command certification (${profile}) passed: ${outputDir}`);
