import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const root = resolve(process.cwd());
const directory = await mkdtemp(join(tmpdir(), 's2-release-evidence-test-'));
const reports = [
  'workflow-jobs.json', 'security-negative-report.json', 'postgres-integration-report.json', 'transport-integration-report.json',
  'capacity-report.json', 'reconnect-storm-report.json', 'slow-consumer-report.json', 'revocation-report.json', 'failure-injection-report.json', 'browser-report.json',
  'kind-rollout-report.json', 'rollback-report.json', 'metric-cardinality-report.json', 'log-redaction-report.json',
  'trace-correlation-report.json', 'shadow-comparison-report.json', 'alert-rule-validation-report.json',
  'production-image-report.json', 'sbom-provenance-report.json',
];
const run = (script, args, expectSuccess = true) => {
  const result = spawnSync(process.execPath, [resolve(root, script), ...args], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (expectSuccess && result.status !== 0) throw new Error(`${script} failed: ${result.stderr || result.stdout}`);
  if (!expectSuccess && result.status === 0) throw new Error(`${script} unexpectedly passed`);
  return `${result.stdout}\n${result.stderr}`;
};
const image = (name, formalReleaseEligible) => ({
  name,
  digest: `sha256:${(name === 'telemetry-runtime' ? '1' : '2').repeat(64)}`,
  user: name === 'telemetry-runtime' ? '65532:65532' : 'postgres',
  formalReleaseEligible,
  provenance: formalReleaseEligible ? 'buildkit-mode-max' : 'preflight-build-metadata',
  githubAttestation: formalReleaseEligible ? 'published' : 'not-applicable-preflight',
});
const writeReports = async (formalReleaseEligible) => {
  for (const name of reports) {
    let report = { schemaVersion: 1, status: 'passed' };
    if (name === 'capacity-report.json') report = { ...report, formalReleaseEligible, certificationLevel: formalReleaseEligible ? 'formal' : 'clean-runner-preflight', measurementSource: formalReleaseEligible ? 'approved-wall-clock-attestation' : 'configuration-only-preflight' };
    if (name === 'production-image-report.json') report = { ...report, images: [image('telemetry-runtime', formalReleaseEligible), image('telemetry-runtime-migrator', formalReleaseEligible)] };
    if (name === 'sbom-provenance-report.json') report = { ...report, formalReleaseEligible };
    await writeFile(join(directory, name), `${JSON.stringify(report)}\n`);
  }
};
try {
  const repositoryShaResult = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (repositoryShaResult.status !== 0) throw new Error(`git rev-parse failed: ${repositoryShaResult.stderr}`);
  const repositorySha = repositoryShaResult.stdout.trim();
  const capacityDirectory = join(directory, 'capacity');
  run('scripts/run-s2-capacity-certification.mjs', [`--profile=preflight`, `--output-dir=${capacityDirectory}`]);
  const preflightCapacity = JSON.parse(await readFile(join(capacityDirectory, 'capacity-report.json'), 'utf8'));
  if (preflightCapacity.formalReleaseEligible !== false || preflightCapacity.measured !== false || preflightCapacity.observed !== null) {
    throw new Error('capacity preflight emitted measured or formal evidence');
  }
  const attestation = {
    schemaVersion: 1,
    repositorySha,
    steadyStateSeconds: 3600,
    peakSeconds: 900,
    manualApproval: true,
    approvedBy: 'release-owner',
    environment: { topology: 'measured-capacity-environment' },
    fixture: { name: 's2-release-envelope-v1' },
    load: { connections: 5000, subscriptions: 50000, businessRevisionsPerSecond: 2000, peakMultiplier: 2 },
    observed: { snapshotP99Seconds: 1, publicationP99Seconds: 4, recoveryOrSnapshotFailureFraction: 0.004, maxQueueBytes: 200000, cpuPeak: 0.7, memoryPeak: 0.65, redisMemoryPeak: 0.7, postgresPoolPeak: 0.65, networkPeak: 0.65, minimumHeadroom: 0.3 },
    zeroCounters: { oom: 0, crash: 0, unboundedQueue: 0, securityInvariant: 0, businessRevisionCorruption: 0 },
    reconnect: { clients: 10000, clientsPerSecond: 1000, lostAuthorizedState: 0, mixedRevisionState: 0, snapshotFallbackRequired: true },
    slowConsumer: { fraction: 0.01, disconnectFraction: 0.004, unboundedQueue: 0 },
    revocation: { revocationsPerSecond: 100, postRevocationDeliveries: 0, staleRecoveryCursorsAccepted: 0, browserLastKnownRetained: 0 },
    failureScenarios: ['history-overflow', 'epoch-reset', 'centrifugo-node-loss', 'redis-failover', 'postgres-failover', 'iam-outage', 'upstream-outage'].map((name) => ({ name, passed: true, result: 'bounded-safe-result' })),
  };
  const attestationPath = join(directory, 'full-capacity-attestation.json');
  await writeFile(attestationPath, `${JSON.stringify(attestation)}\n`);
  run('scripts/run-s2-capacity-certification.mjs', [`--profile=full`, `--wall-clock-attestation=${attestationPath}`, `--output-dir=${capacityDirectory}`]);
  const formalCapacity = JSON.parse(await readFile(join(capacityDirectory, 'capacity-report.json'), 'utf8'));
  if (formalCapacity.formalReleaseEligible !== true || formalCapacity.measurementSource !== 'approved-wall-clock-attestation') {
    throw new Error('valid wall-clock attestation did not produce formal capacity evidence');
  }
  await writeFile(attestationPath, `${JSON.stringify({ ...attestation, repositorySha: '0'.repeat(40) })}\n`);
  const shaRejection = run('scripts/run-s2-capacity-certification.mjs', [`--profile=full`, `--wall-clock-attestation=${attestationPath}`, `--output-dir=${capacityDirectory}`], false);
  if (!shaRejection.includes('does not match')) throw new Error('capacity verifier did not reject a mismatched repository SHA');

  const imageInput = join(directory, 'image-input');
  const imageOutput = join(directory, 'image-output');
  await mkdir(imageInput, { recursive: true });
  for (const [name, digit, user] of [['telemetry-runtime', '1', '65532:65532'], ['telemetry-runtime-migrator', '2', 'postgres']]) {
    const sbomFile = `sbom-${name}.cyclonedx.json`;
    const metadataFile = `build-metadata-${name}.json`;
    await writeFile(join(imageInput, sbomFile), '{"bomFormat":"CycloneDX"}\n');
    await writeFile(join(imageInput, metadataFile), '{"buildx":"preflight"}\n');
    await writeFile(join(imageInput, `${name}.json`), `${JSON.stringify({
      schemaVersion: 1,
      status: 'passed',
      name,
      digest: `sha256:${digit.repeat(64)}`,
      user,
      secretFindings: 0,
      highOrCriticalVulnerabilities: 0,
      formalReleaseEligible: false,
      sbom: { format: 'CycloneDX', file: sbomFile, sha256: digit.repeat(64) },
      buildMetadata: { file: metadataFile, sha256: digit.repeat(64) },
      provenance: 'preflight-build-metadata',
      githubAttestation: 'not-applicable-preflight',
    })}\n`);
  }
  run('scripts/merge-s2-image-evidence.mjs', [`--input=${imageInput}`, `--output=${imageOutput}`]);
  const mergedImages = JSON.parse(await readFile(join(imageOutput, 'production-image-report.json'), 'utf8'));
  if (mergedImages.images?.length !== 2) throw new Error('image evidence merger did not preserve both production images');
  const badRuntime = JSON.parse(await readFile(join(imageInput, 'telemetry-runtime.json'), 'utf8'));
  await writeFile(join(imageInput, 'telemetry-runtime.json'), `${JSON.stringify({ ...badRuntime, highOrCriticalVulnerabilities: 1 })}\n`);
  const imageRejection = run('scripts/merge-s2-image-evidence.mjs', [`--input=${imageInput}`, `--output=${imageOutput}`], false);
  if (!imageRejection.includes('image gate failed')) throw new Error('image evidence merger did not reject a high vulnerability finding');

  await writeReports(false);
  await writeFile(join(directory, 'sbom-telemetry-runtime.cyclonedx.json'), '{"bomFormat":"CycloneDX"}\n');
  await writeFile(join(directory, 'build-metadata-telemetry-runtime.json'), '{"buildx":"preflight"}\n');
  run('scripts/build-s2-release-evidence.mjs', [`--directory=${directory}`, '--require-formal=false']);
  run('scripts/verify-s2-release-evidence.mjs', [`--directory=${directory}`]);
  await writeFile(join(directory, 'browser-report.json'), '{"schemaVersion":1,"status":"tampered"}\n');
  const digestRejection = run('scripts/verify-s2-release-evidence.mjs', [`--directory=${directory}`], false);
  if (!digestRejection.includes('digest mismatch')) throw new Error('offline verifier did not reject tampered evidence');
  await writeReports(false);
  run('scripts/build-s2-release-evidence.mjs', [`--directory=${directory}`, '--require-formal=false']);
  const rejection = run('scripts/build-s2-release-evidence.mjs', [`--directory=${directory}`, '--require-formal=true', '--repository-sha=1111111111111111111111111111111111111111'], false);
  if (!rejection.includes('formal release evidence requires')) throw new Error('formal evidence rejection reason drifted');
  await writeReports(true);
  run('scripts/build-s2-release-evidence.mjs', [`--directory=${directory}`, '--require-formal=true', '--repository-sha=1111111111111111111111111111111111111111']);
  run('scripts/verify-s2-release-evidence.mjs', [`--directory=${directory}`]);
  const statement = JSON.parse(await readFile(join(directory, 'release-evidence.intoto.json'), 'utf8'));
  if (statement.predicate?.formalReleaseEligible !== true) throw new Error('formal in-toto predicate was not recorded');
  console.log('S2 release evidence positive, tamper, and formal/preflight tests passed.');
} finally {
  await rm(directory, { recursive: true, force: true });
}
