import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const directory = await mkdtemp(join(tmpdir(), 's2-capacity-test-'));
const attestationPath = join(directory, 'attestation.json');
const outputDirectory = join(directory, 'evidence');
const repositorySha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const failures = ['history-overflow', 'epoch-reset', 'centrifugo-node-loss', 'redis-failover', 'postgres-failover', 'iam-outage', 'upstream-outage'];
const valid = {
  schemaVersion: 1,
  repositorySha,
  steadyStateSeconds: 3600,
  peakSeconds: 900,
  manualApproval: true,
  approvedBy: 'release-reviewer',
  environment: { topology: 'certification-fixture', region: 'test' },
  fixture: { version: 's2-envelope-v1' },
  load: { connections: 5000, subscriptions: 50000, businessRevisionsPerSecond: 2000, peakMultiplier: 2 },
  observed: {
    snapshotP99Seconds: 0.8,
    publicationP99Seconds: 2.4,
    recoveryOrSnapshotFailureFraction: 0.001,
    maxQueueBytes: 200000,
    cpuPeak: 0.62,
    memoryPeak: 0.64,
    redisMemoryPeak: 0.58,
    postgresPoolPeak: 0.61,
    networkPeak: 0.60,
    minimumHeadroom: 0.36,
  },
  zeroCounters: { oom: 0, crash: 0, unboundedQueue: 0, securityInvariant: 0, businessRevisionCorruption: 0 },
  reconnect: { clients: 10000, clientsPerSecond: 1000, lostAuthorizedState: 0, mixedRevisionState: 0, snapshotFallbackRequired: true },
  slowConsumer: { fraction: 0.01, disconnectFraction: 0.004, unboundedQueue: 0 },
  revocation: { revocationsPerSecond: 100, postRevocationDeliveries: 0, staleRecoveryCursorsAccepted: 0, browserLastKnownRetained: 0 },
  failureScenarios: failures.map((name) => ({ name, passed: true, result: 'bounded-and-fail-closed' })),
};
const run = (attestation, expectSuccess, expectedMarker = '') => {
  return writeFile(attestationPath, `${JSON.stringify(attestation, null, 2)}\n`).then(() => {
    const result = spawnSync(process.execPath, [resolve(root, 'scripts/run-s2-capacity-certification.mjs'), '--profile=full', `--wall-clock-attestation=${attestationPath}`, `--output-dir=${outputDirectory}`], {
      cwd: root,
      encoding: 'utf8',
      windowsHide: true,
    });
    const detail = `${result.stdout}\n${result.stderr}`;
    if (expectSuccess && result.status !== 0) throw new Error(`valid formal capacity attestation failed: ${detail}`);
    if (!expectSuccess && result.status === 0) throw new Error('invalid formal capacity attestation unexpectedly passed');
    if (!expectSuccess && expectedMarker && !detail.includes(expectedMarker)) throw new Error(`formal capacity rejection reason drifted; expected ${expectedMarker}: ${detail}`);
  });
};

try {
  await run(valid, true);
  const capacity = JSON.parse(await readFile(join(outputDirectory, 'capacity-report.json'), 'utf8'));
  if (capacity.formalReleaseEligible !== true || capacity.measurementSource !== 'approved-wall-clock-attestation' || capacity.measured !== true) {
    throw new Error('formal capacity report was not marked as measured and release eligible');
  }
  await run({ ...valid, observed: { ...valid.observed, recoveryOrSnapshotFailureFraction: 0.006 } }, false, 'recovery-or-Snapshot failure fraction');
  await run({ ...valid, observed: { ...valid.observed, memoryPeak: 0.76, minimumHeadroom: 0.36 } }, false, 'computed headroom');
  await run({ ...valid, failureScenarios: [...valid.failureScenarios.slice(0, 6), valid.failureScenarios[0]] }, false, 'failure-scenario evidence is incomplete');
  await run({ ...valid, repositorySha: '0'.repeat(40) }, false, 'does not match');
  console.log('S2 formal capacity attestation positive and negative tests passed.');
} finally {
  await rm(directory, { recursive: true, force: true });
}
