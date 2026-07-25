import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';

const root = resolve(process.cwd());
const directory = await mkdtemp(join(tmpdir(), 's2-cutover-test-'));
const releaseDir = join(directory, 'release');
const outputDir = join(directory, 'completion');
const attestationPath = join(directory, 'attestation.json');
const repositorySha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const gates = JSON.parse(await readFile(resolve(root, 'deploy/s2/release-gates.v1.json'), 'utf8'));
const plan = JSON.parse(await readFile(resolve(root, 'deploy/s2/cutover-plan.v1.json'), 'utf8'));
const implementationPlan = JSON.parse(await readFile(resolve(root, 'deploy/s2/implementation-plan.v1.json'), 'utf8'));
const sha256 = (value) => createHash('sha256').update(value).digest('hex');

const createFormalReleaseEvidence = async () => {
  await mkdir(releaseDir, { recursive: true });
  const evidence = new Map();
  for (const path of gates.requiredEvidence) {
    const name = basename(path);
    if (name === 'SHA256SUMS' || name === 'release-evidence.intoto.json') continue;
    const value = name === 'capacity-report.json'
      ? { schemaVersion: 1, status: 'passed', certificationLevel: 'formal', formalReleaseEligible: true, measurementSource: 'approved-wall-clock-attestation', repositorySha }
      : { schemaVersion: 1, status: 'passed', repositorySha, testFixture: true };
    const raw = Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
    await writeFile(join(releaseDir, name), raw);
    evidence.set(name, sha256(raw));
  }
  const statement = {
    _type: 'https://in-toto.io/Statement/v1',
    subject: [...evidence].map(([name, digest]) => ({ name, digest: { sha256: digest } })),
    predicateType: 'https://hvac.local/attestations/s2-release/v1',
    predicate: { reviewerCanVerifyOffline: true, allSecurityZeroInvariants: true, legacyRequestFallback: false },
  };
  const statementRaw = Buffer.from(`${JSON.stringify(statement, null, 2)}\n`);
  await writeFile(join(releaseDir, 'release-evidence.intoto.json'), statementRaw);
  evidence.set('release-evidence.intoto.json', sha256(statementRaw));
  const checksums = `${[...evidence].sort(([a], [b]) => a.localeCompare(b)).map(([name, digest]) => `${digest}  ${name}`).join('\n')}\n`;
  await writeFile(join(releaseDir, 'SHA256SUMS'), checksums);
  return sha256(Buffer.from(checksums));
};

const buildAttestation = (artifactSha256) => {
  const zeroCounters = Object.fromEntries(gates.securityZeroInvariants.map((name) => [name, 0]));
  let cursor = Date.now() - (plan.phases.reduce((total, phase) => total + phase.minimumHoldMinutes + 2, 0) + 60) * 60000;
  const phases = plan.phases.map((phase) => {
    const startedAt = new Date(cursor).toISOString();
    cursor += phase.minimumHoldMinutes * 60000;
    const endedAt = new Date(cursor).toISOString();
    cursor += 60000;
    const approvedAt = new Date(cursor).toISOString();
    cursor += 60000;
    return {
      id: phase.id,
      registryRevision: phase.registryRevision,
      routeRevision: phase.routeRevision,
      trafficPercent: phase.trafficPercent,
      startedAt,
      endedAt,
      holdMinutes: phase.minimumHoldMinutes,
      sampleCounts: {
        snapshotRequests: phase.minimumSnapshotRequests ?? 0,
        subscriptions: phase.minimumSubscriptions ?? 0,
        recoveryAttempts: phase.minimumRecoveryAttempts ?? 0,
      },
      sloBurnRate: 0.5,
      capacityHeadroom: 0.35,
      zeroCounters,
      unclassifiedShadowDifferences: 0,
      contractDrift: 0,
      ownershipDrift: 0,
      snapshotOwner: phase.owner,
      liveOwner: phase.liveOwner,
      requestFallbacks: 0,
      approval: { manual: true, primaryOwner: 'release-owner', secondaryOwner: 'security-owner', approvedAt },
    };
  });
  const r8 = phases.at(-1);
  const auditApprovedAt = new Date(Date.parse(r8.endedAt) + 120000).toISOString();
  return {
    schemaVersion: 1,
    repositorySha,
    releaseEvidence: { workflowRunId: 'test-formal-release-run', artifactSha256, formalReleaseEligible: true, certificationLevel: 'formal' },
    phases,
    rollbackDrill: {
      passed: true,
      decisionMinutes: 4,
      routeRollbackMinutes: 12,
      disconnectOrExpireLiveSessions: true,
      freshSnapshotRequired: true,
      databaseDownMigrationPerformed: false,
      fromRouteRevision: 9,
      toRouteRevision: 8,
    },
    legacyObservation: {
      startedAt: r8.startedAt,
      endedAt: r8.endedAt,
      durationMinutes: 10080,
      latestRequests: 0,
      batchRequests: 0,
      websocketConnections: 0,
      legacyWritesRequired: 0,
      browserReferences: 0,
      unauthenticatedIngestEffectsOnS2: 0,
    },
    retirement: {
      publicRouteOwner: 'telemetry-runtime-service',
      backendRepositorySha: plan.retirement.backendCommitSha,
      currentStateEndpointsRemoved: true,
      currentStateNetworkPolicyDenied: true,
      socketIoRemoved: true,
      historicalTimeseriesRetained: true,
      historicalUsedAsCurrentFallback: false,
    },
    auditApproval: { manual: true, approvedBy: 'audit-owner', approvedAt: auditApprovedAt, statement: 'S2 completion risks and retirement evidence approved.' },
    risks: Object.fromEntries(implementationPlan.risksToZeroBeforeNextSlice.map((name) => [name, 0])),
  };
};

const run = async (attestation, expectSuccess, marker = '') => {
  await writeFile(attestationPath, `${JSON.stringify(attestation, null, 2)}\n`);
  const result = spawnSync(process.execPath, [
    resolve(root, 'scripts/run-s2-cutover-certification.mjs'),
    '--profile=formal',
    `--attestation=${attestationPath}`,
    `--release-evidence-dir=${releaseDir}`,
    `--output-dir=${outputDir}`,
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  const detail = `${result.stdout}\n${result.stderr}`;
  if (expectSuccess && result.status !== 0) throw new Error(`valid S2 cutover attestation failed: ${detail}`);
  if (!expectSuccess && result.status === 0) throw new Error('invalid S2 cutover attestation unexpectedly passed');
  if (!expectSuccess && marker && !detail.includes(marker)) throw new Error(`S2 cutover rejection reason drifted; expected ${marker}: ${detail}`);
};

try {
  const artifactSha256 = await createFormalReleaseEvidence();
  const valid = buildAttestation(artifactSha256);
  await run(valid, true);
  const verify = spawnSync(process.execPath, [resolve(root, 'scripts/verify-s2-completion-evidence.mjs'), `--directory=${outputDir}`], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (verify.status !== 0) throw new Error(`valid S2 completion evidence failed offline verification: ${verify.stdout}\n${verify.stderr}`);
  await run({ ...valid, legacyObservation: { ...valid.legacyObservation, durationMinutes: 10079 } }, false, 'shorter than seven real days');
  const nonZero = structuredClone(valid);
  nonZero.phases[3].zeroCounters.cross_organization_successes = 1;
  await run(nonZero, false, 'security invariant cross_organization_successes');
  const ownerDrift = structuredClone(valid);
  ownerDrift.phases[4].liveOwner = 'legacy-hvac-backend';
  await run(ownerDrift, false, 'did not switch Snapshot and live ownership together');
  await run({ ...valid, retirement: { ...valid.retirement, backendRepositorySha: '0'.repeat(40) } }, false, 'pinned Legacy backend commit');
  await run({ ...valid, releaseEvidence: { ...valid.releaseEvidence, artifactSha256: '0'.repeat(64) } }, false, 'release evidence digest');
  await writeFile(join(outputDir, 'r3-phase-report.json'), '{"tampered":true}\n');
  const tamper = spawnSync(process.execPath, [resolve(root, 'scripts/verify-s2-completion-evidence.mjs'), `--directory=${outputDir}`], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (tamper.status === 0 || !`${tamper.stdout}\n${tamper.stderr}`.includes('digest mismatch')) throw new Error('tampered S2 completion evidence was not rejected');
  console.log('S2 cutover positive, negative, and tamper tests passed.');
} finally {
  await rm(directory, { recursive: true, force: true });
}
