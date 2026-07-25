import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const profile = arg('profile', process.env.S2_CAPACITY_PROFILE ?? 'preflight');
const outputDir = resolve(root, arg('output-dir', 'out/s2-release-evidence'));
const envelope = JSON.parse(await readFile(resolve(root, 'deploy/s2/release-envelope.v1.json'), 'utf8'));
const expectedRepositorySha = process.env.GITHUB_SHA ?? execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const assert = (condition, message) => { if (!condition) throw new Error(message); };
if (!['preflight', 'full'].includes(profile)) throw new Error(`unsupported S2 capacity profile: ${profile}`);

const requiredFailures = ['history-overflow', 'epoch-reset', 'centrifugo-node-loss', 'redis-failover', 'postgres-failover', 'iam-outage', 'upstream-outage'];
const preflightStatement = 'Configuration-only clean-runner preflight. It does not execute or claim the frozen 60-minute steady-state or 15-minute 2x peak envelope.';
let capacity;
let reconnect;
let slowConsumer;
let revocation;
let failures;

if (profile === 'preflight') {
  capacity = {
    schemaVersion: 1,
    status: 'passed',
    profile,
    certificationLevel: 'clean-runner-preflight',
    formalReleaseEligible: false,
    measurementSource: 'configuration-only-preflight',
    repositorySha: expectedRepositorySha,
    statement: preflightStatement,
    measured: false,
    envelope: {
      steadyState: envelope.steadyState,
      peak: envelope.peak,
      slo: envelope.slo,
      transportBounds: envelope.transportBounds,
    },
    checks: [
      'release-envelope-schema-loaded',
      'formal-profile-requires-approved-wall-clock-attestation',
      'repository-sha-binding-required',
      'zero-tolerance-counter-contract-loaded',
      'resource-and-headroom-thresholds-loaded',
      'failure-scenario-set-loaded',
    ],
    observed: null,
    zeroCounters: null,
    wallClockAttestation: null,
  };
  reconnect = {
    schemaVersion: 1,
    status: 'passed',
    profile,
    formalReleaseEligible: false,
    measured: false,
    statement: preflightStatement,
    required: { clients: envelope.failureScenarios.reconnectClients, clientsPerSecond: envelope.failureScenarios.reconnectClientsPerSecond, snapshotFallbackRequired: true },
  };
  slowConsumer = {
    schemaVersion: 1,
    status: 'passed',
    profile,
    formalReleaseEligible: false,
    measured: false,
    statement: preflightStatement,
    required: { fraction: envelope.failureScenarios.slowConsumerFraction, queueBytesMaximum: envelope.transportBounds.clientQueueBytesMaximum, disconnectFractionMaximum: envelope.slo.slowConsumerDisconnectFractionMaximum, unboundedQueueMaximum: 0 },
  };
  revocation = {
    schemaVersion: 1,
    status: 'passed',
    profile,
    formalReleaseEligible: false,
    measured: false,
    statement: preflightStatement,
    required: { revocationsPerSecond: envelope.failureScenarios.revocationsPerSecond, postRevocationDeliveriesMaximum: 0, staleRecoveryCursorsAcceptedMaximum: 0, browserLastKnownRetainedMaximum: 0 },
  };
  failures = {
    schemaVersion: 1,
    status: 'passed',
    profile,
    formalReleaseEligible: false,
    measured: false,
    statement: preflightStatement,
    requiredScenarios: requiredFailures,
  };
} else {
  const attestationPath = arg('wall-clock-attestation', '');
  assert(attestationPath, 'full capacity profile requires --wall-clock-attestation=<json>');
  const attestationRaw = await readFile(resolve(root, attestationPath));
  const attestation = JSON.parse(attestationRaw.toString('utf8'));
  assert(attestation.schemaVersion === 1, 'wall-clock attestation schema version is unsupported');
  assert(attestation.repositorySha === expectedRepositorySha, `wall-clock attestation repository SHA ${attestation.repositorySha} does not match ${expectedRepositorySha}`);
  assert(attestation.steadyStateSeconds >= envelope.steadyState.durationSeconds, 'wall-clock attestation is shorter than the 60-minute steady-state requirement');
  assert(attestation.peakSeconds >= envelope.peak.durationSeconds, 'wall-clock attestation is shorter than the 15-minute peak requirement');
  assert(attestation.manualApproval === true && String(attestation.approvedBy ?? '').trim(), 'wall-clock attestation lacks manual approval');
  assert(attestation.environment && Object.keys(attestation.environment).length > 0, 'wall-clock attestation lacks environment evidence');
  assert(attestation.fixture && Object.keys(attestation.fixture).length > 0, 'wall-clock attestation lacks fixture evidence');
  assert(attestation.load?.connections >= envelope.steadyState.connections, 'wall-clock attestation did not exercise 5k connections');
  assert(attestation.load?.subscriptions >= envelope.steadyState.subscriptions, 'wall-clock attestation did not exercise 50k subscriptions');
  assert(attestation.load?.businessRevisionsPerSecond >= envelope.steadyState.businessRevisionsPerSecond, 'wall-clock attestation did not exercise 2k revisions/s');
  assert(attestation.load?.peakMultiplier >= envelope.peak.multiplier, 'wall-clock attestation did not exercise the 2x peak');

  const observed = attestation.observed ?? {};
  for (const key of ['snapshotP99Seconds', 'publicationP99Seconds', 'recoveryOrSnapshotFailureFraction', 'maxQueueBytes', 'cpuPeak', 'memoryPeak', 'redisMemoryPeak', 'postgresPoolPeak', 'networkPeak', 'minimumHeadroom']) {
    assert(Number.isFinite(observed[key]), `wall-clock attestation is missing observed.${key}`);
  }
  assert(observed.snapshotP99Seconds <= envelope.slo.snapshotP99SecondsMaximum, 'Snapshot p99 exceeds the release SLO');
  assert(observed.publicationP99Seconds <= envelope.slo.publicationP99SecondsMaximum, 'publication p99 exceeds the release SLO');
  assert(observed.recoveryOrSnapshotFailureFraction <= envelope.slo.recoveryOrSnapshotFailureFractionMaximum, 'recovery-or-Snapshot failure fraction exceeds the release SLO');
  assert(observed.maxQueueBytes <= envelope.transportBounds.clientQueueBytesMaximum, 'client queue bound was exceeded');
  assert(observed.cpuPeak <= envelope.slo.cpuUtilizationFractionMaximum, 'CPU utilization exceeds the release SLO');
  assert(observed.redisMemoryPeak <= envelope.slo.redisMemoryUtilizationFractionMaximum, 'Redis memory utilization exceeds the release SLO');
  const computedHeadroom = 1 - Math.max(observed.cpuPeak, observed.memoryPeak, observed.redisMemoryPeak, observed.postgresPoolPeak, observed.networkPeak);
  assert(computedHeadroom >= envelope.steadyState.minimumHeadroomFraction, 'resource peaks leave less than 30% computed headroom');
  assert(observed.minimumHeadroom >= envelope.steadyState.minimumHeadroomFraction && observed.minimumHeadroom <= computedHeadroom + 0.001, 'minimum measured headroom is below 30% or inconsistent with resource peaks');

  const zeroCounters = attestation.zeroCounters ?? {};
  for (const key of ['oom', 'crash', 'unboundedQueue', 'securityInvariant', 'businessRevisionCorruption']) {
    assert(zeroCounters[key] === 0, `zero-tolerance counter ${key} is non-zero`);
  }
  assert(attestation.reconnect?.clients >= envelope.failureScenarios.reconnectClients && attestation.reconnect?.clientsPerSecond >= envelope.failureScenarios.reconnectClientsPerSecond, 'reconnect storm target was not exercised');
  assert(attestation.reconnect?.lostAuthorizedState === 0 && attestation.reconnect?.mixedRevisionState === 0 && attestation.reconnect?.snapshotFallbackRequired === true, 'reconnect evidence violates the recovery contract');
  assert(attestation.slowConsumer?.fraction >= envelope.failureScenarios.slowConsumerFraction, 'slow-consumer fraction was not exercised');
  assert(attestation.slowConsumer?.disconnectFraction <= envelope.slo.slowConsumerDisconnectFractionMaximum && attestation.slowConsumer?.unboundedQueue === 0, 'slow-consumer evidence violates bounded degradation');
  assert(attestation.revocation?.revocationsPerSecond >= envelope.failureScenarios.revocationsPerSecond, 'revocation rate target was not exercised');
  assert(attestation.revocation?.postRevocationDeliveries === 0 && attestation.revocation?.staleRecoveryCursorsAccepted === 0 && attestation.revocation?.browserLastKnownRetained === 0, 'revocation evidence violates a zero invariant');
  const attestedFailures = attestation.failureScenarios ?? [];
  const attestedNames = new Set(attestedFailures.map((entry) => entry.name));
  assert(attestedFailures.length === requiredFailures.length
    && attestedFailures.every((entry) => requiredFailures.includes(entry.name) && entry.passed === true && String(entry.result ?? '').trim())
    && requiredFailures.every((name) => attestedNames.has(name))
    && attestedNames.size === requiredFailures.length, 'wall-clock failure-scenario evidence is incomplete');

  const wallClockAttestation = { ...attestation, sha256: createHash('sha256').update(attestationRaw).digest('hex') };
  capacity = {
    schemaVersion: 1,
    status: 'passed',
    profile,
    certificationLevel: 'formal',
    formalReleaseEligible: true,
    measurementSource: 'approved-wall-clock-attestation',
    repositorySha: expectedRepositorySha,
    measured: true,
    envelope: { steadyState: envelope.steadyState, peak: envelope.peak },
    environment: attestation.environment,
    fixture: attestation.fixture,
    load: attestation.load,
    observed,
    zeroCounters,
    wallClockAttestation,
  };
  reconnect = { schemaVersion: 1, status: 'passed', profile, formalReleaseEligible: true, measured: true, ...attestation.reconnect };
  slowConsumer = { schemaVersion: 1, status: 'passed', profile, formalReleaseEligible: true, measured: true, queueBytesMaximum: envelope.transportBounds.clientQueueBytesMaximum, ...attestation.slowConsumer };
  revocation = { schemaVersion: 1, status: 'passed', profile, formalReleaseEligible: true, measured: true, ...attestation.revocation };
  failures = { schemaVersion: 1, status: 'passed', profile, formalReleaseEligible: true, measured: true, scenarios: attestation.failureScenarios, securityInvariantViolations: 0, businessCorruptions: 0 };
}

await mkdir(outputDir, { recursive: true });
for (const [name, report] of Object.entries({
  'capacity-report.json': capacity,
  'reconnect-storm-report.json': reconnect,
  'slow-consumer-report.json': slowConsumer,
  'revocation-report.json': revocation,
  'failure-injection-report.json': failures,
})) {
  await writeFile(resolve(outputDir, name), `${JSON.stringify(report, null, 2)}\n`);
}
console.log(`S2 capacity and failure certification (${profile}) passed: ${outputDir}`);
