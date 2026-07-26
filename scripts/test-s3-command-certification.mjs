import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const script = resolve(root, 'scripts/run-s3-command-certification.mjs');
const verifier = resolve(root, 'scripts/verify-s3-command-certification.mjs');
const envelope = JSON.parse(await readFile(resolve(root, 'deploy/s3/certification-envelope.v1.json'), 'utf8'));
const repositorySha = process.env.GITHUB_SHA ?? execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const temporaryRoot = await mkdtemp(resolve(tmpdir(), 's3-command-certification-'));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const run = (argumentsList) => spawnSync(process.execPath, [script, ...argumentsList], {
  cwd: root,
  encoding: 'utf8',
  windowsHide: true,
  env: { ...process.env, GITHUB_SHA: repositorySha },
});
const verify = (directory) => spawnSync(process.execPath, [verifier, `--directory=${directory}`], {
  cwd: root,
  encoding: 'utf8',
  windowsHide: true,
});

const preflightOutput = resolve(temporaryRoot, 'preflight');
const preflight = run(['--profile=preflight', `--output-dir=${preflightOutput}`]);
assert(preflight.status === 0, `S3 preflight validator failed:\n${preflight.stdout}\n${preflight.stderr}`);
const preflightReport = JSON.parse(await readFile(resolve(preflightOutput, 'certification-preflight-report.json'), 'utf8'));
assert(preflightReport.status === 'passed' && preflightReport.formalCertificationEligible === false, 'preflight report incorrectly claims formal eligibility');
assert(preflightReport.productionTrafficPercent === 0, 'preflight report enabled production traffic');

const missingAttestation = run(['--profile=formal', `--output-dir=${resolve(temporaryRoot, 'missing')}`]);
assert(missingAttestation.status !== 0 && `${missingAttestation.stdout}\n${missingAttestation.stderr}`.includes('requires --attestation'), 'formal profile accepted a missing attestation');

const zeroCounters = Object.fromEntries(envelope.zeroCounters.map((name) => [name, 0]));
const recoverySeconds = {
  'dispatcher-crash-after-claim-before-connector-result': 20,
  'consumer-rebalance': 45,
  'connector-crash-after-request-commit': 90,
  'single-availability-zone-loss': 240,
};
const crashPoints = envelope.requiredCrashPoints.map((name) => ({
  name,
  passed: true,
  evidence: `validator-fixture://${name}`,
  ...(recoverySeconds[name] === undefined ? {} : { recoverySeconds: recoverySeconds[name] }),
}));
const fixtureNow = Date.now();
const canaryStartedAt = new Date(fixtureNow - 5 * 60 * 60 * 1000).toISOString();
const canaryEndedAt = new Date(fixtureNow - 60 * 60 * 1000).toISOString();
const approvedAt = new Date(fixtureNow - 30 * 60 * 1000).toISOString();
const attestation = {
  schemaVersion: 1,
  repositorySha,
  workflowRunId: 'validator-fixture-run',
  environment: {
    kind: 'validator-fixture-only',
    targetEquivalent: true,
    note: 'Synthetic validator input; not release evidence.',
  },
  load: {
    steadyCommandsPerSecond: envelope.capacity.steadyState.commandsPerSecond,
    steadyDurationSeconds: envelope.capacity.steadyState.minimumDurationSeconds,
    burstCommandsPerSecond: envelope.capacity.burst.commandsPerSecond,
    burstDurationSeconds: envelope.capacity.burst.durationSeconds,
  },
  observed: {
    acceptedToSubmittedP95Milliseconds: 200,
    acceptedToSubmittedP99Milliseconds: 800,
    governanceP95Milliseconds: 350,
    governanceP99Milliseconds: 1500,
    queuedReadyToSendStartedP95Milliseconds: 700,
    queuedReadyToSendStartedP99Milliseconds: 2200,
    statusPropagationP95Milliseconds: 700,
    statusPropagationP99Milliseconds: 2200,
    cpuPeakFraction: 0.5,
    memoryPeakFraction: 0.45,
    postgresPoolPeakFraction: 0.4,
    networkPeakFraction: 0.35,
    minimumHeadroomFraction: 0.5,
  },
  zeroCounters,
  crashPoints,
  canary: {
    phase: envelope.internalCanary.phase,
    organizationCount: envelope.internalCanary.organizationCount,
    siteCount: envelope.internalCanary.siteCount,
    deviceCount: envelope.internalCanary.deviceCount,
    organizationId: 'org-validator-fixture',
    siteId: 'site-validator-fixture',
    deviceId: 'device-validator-fixture',
    capability: envelope.internalCanary.capability,
    capabilityRevision: envelope.internalCanary.capabilityRevision,
    risk: envelope.internalCanary.risk,
    mappingStatus: envelope.internalCanary.mappingStatusRequired,
    providerContract: envelope.internalCanary.providerContract,
    maximumObservedSetpointDeltaC: 1,
    s2CurrentStateProductionCertified: true,
    publicRoutesEnabled: false,
    productionTrafficPercent: 0,
    startedAt: canaryStartedAt,
    endedAt: canaryEndedAt,
    holdMinutes: envelope.internalCanary.minimumHoldMinutes,
    commandCount: envelope.internalCanary.minimumCommands,
    verifiedCommandCount: envelope.internalCanary.minimumCommands,
    outcomeUnknownCount: 0,
    automaticReissuesAfterRequestCommitted: 0,
    operatorConfirmedEachCommand: true,
    credentialReference: 'workload://validator-fixture/not-a-secret',
  },
  rollback: {
    passed: true,
    futureCommandsOnly: true,
    acceptedCommandsRemainWithOriginalOwner: true,
    connectorCredentialDisabledMinutes: 1,
  },
  approval: {
    manual: true,
    approvedAt,
    primaryOwner: 'validator-primary-owner',
    secondaryOwner: 'validator-secondary-owner',
    statement: 'Validator fixture only; not a real promotion approval.',
  },
};

const attestationPath = resolve(temporaryRoot, 'valid-attestation.json');
await writeFile(attestationPath, `${JSON.stringify(attestation, null, 2)}\n`);
const formalOutput = resolve(temporaryRoot, 'formal');
const formal = run(['--profile=formal', `--attestation=${attestationPath}`, `--output-dir=${formalOutput}`]);
assert(formal.status === 0, `valid formal attestation was rejected:\n${formal.stdout}\n${formal.stderr}`);
for (const name of envelope.requiredEvidence) {
  await readFile(resolve(formalOutput, name));
}
const formalReport = JSON.parse(await readFile(resolve(formalOutput, 's3-certification-attestation.json'), 'utf8'));
assert(formalReport.formalCertificationEligible === true && formalReport.productionTrafficPercent === 0 && formalReport.publicRoutesEnabled === false, 'formal validator report violated the disabled-public-route boundary');
const verified = verify(formalOutput);
assert(verified.status === 0, `offline verification rejected a valid evidence bundle:\n${verified.stdout}\n${verified.stderr}`);

const invalidAttestation = structuredClone(attestation);
invalidAttestation.zeroCounters.blindRetriesAfterRequestCommitted = 1;
const invalidPath = resolve(temporaryRoot, 'invalid-attestation.json');
await writeFile(invalidPath, `${JSON.stringify(invalidAttestation, null, 2)}\n`);
const invalid = run(['--profile=formal', `--attestation=${invalidPath}`, `--output-dir=${resolve(temporaryRoot, 'invalid')}`]);
assert(invalid.status !== 0 && `${invalid.stdout}\n${invalid.stderr}`.includes('blindRetriesAfterRequestCommitted'), 'formal validator accepted a non-zero blind retry counter');

const futureAttestation = structuredClone(attestation);
futureAttestation.canary.startedAt = new Date(fixtureNow + 60 * 60 * 1000).toISOString();
futureAttestation.canary.endedAt = new Date(fixtureNow + 5 * 60 * 60 * 1000).toISOString();
futureAttestation.approval.approvedAt = new Date(fixtureNow + 6 * 60 * 60 * 1000).toISOString();
const futurePath = resolve(temporaryRoot, 'future-attestation.json');
await writeFile(futurePath, `${JSON.stringify(futureAttestation, null, 2)}\n`);
const future = run(['--profile=formal', `--attestation=${futurePath}`, `--output-dir=${resolve(temporaryRoot, 'future')}`]);
assert(future.status !== 0 && `${future.stdout}\n${future.stderr}`.includes('time window is in the future'), 'formal validator accepted a future-dated canary hold');

const capacityPath = resolve(formalOutput, 'capacity-report.json');
await writeFile(capacityPath, `${await readFile(capacityPath, 'utf8')} `);
const tampered = verify(formalOutput);
assert(tampered.status !== 0 && `${tampered.stdout}\n${tampered.stderr}`.includes('digest mismatch'), 'offline verifier accepted a tampered evidence file');

console.log('S3 Command certification validator tests passed: preflight is non-formal, formal evidence verifies offline, and zero-invariant, future-time or tamper violations fail closed.');
