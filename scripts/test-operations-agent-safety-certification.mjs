import assert from 'node:assert/strict';
import test from 'node:test';

import {
  OPERATIONS_AGENT_SAFETY_GATES,
  OPERATIONS_AGENT_SAFETY_INVARIANTS,
  OPERATIONS_AGENT_SAFETY_SCENARIOS,
  buildOperationsAgentSafetyCertificationReport,
  validateOperationsAgentSafetyCertificationReport,
} from './operations-agent-safety-certification.v1.mjs';

const browserAssertions = OPERATIONS_AGENT_SAFETY_SCENARIOS
  .flatMap(({ requirements }) => requirements)
  .filter(({ sourceArtifact }) => sourceArtifact?.endsWith('browser-evidence.json'))
  .map(({ marker }) => marker);

const passingFixture = () => {
  const gateResults = Object.fromEntries(OPERATIONS_AGENT_SAFETY_GATES.map((id) => {
    const markers = OPERATIONS_AGENT_SAFETY_SCENARIOS
      .flatMap(({ requirements }) => requirements)
      .filter(({ gate, sourceArtifact }) => gate === id && sourceArtifact === null)
      .map(({ marker }) => marker);
    return [id, {
      passed: true,
      command: `fixture:${id}`,
      durationMs: 1,
      exitCode: 0,
      signal: null,
      stdout: markers.join('\n'),
      stderr: '',
      stdoutPath: `logs/${id}.stdout.log`,
      stderrPath: `logs/${id}.stderr.log`,
      stdoutSha256: 'a'.repeat(64),
      stderrSha256: 'b'.repeat(64),
    }];
  }));
  const browserEvidence = {
    schemaVersion: 1,
    passed: true,
    assertions: browserAssertions,
    safety: {
      productionTrafficPercent: 0,
      duplicateDurableRecords: false,
    },
  };
  return { gateResults, browserEvidence };
};

test('Map 5.5 scenario contract covers every required safety invariant and seam', () => {
  assert.deepEqual(
    OPERATIONS_AGENT_SAFETY_SCENARIOS.map(({ id }) => id),
    [
      'authorization-negative-complete-boundary',
      'retry-exactly-once-durable-outcomes',
      'restart-authoritative-state-recovery',
      'concurrency-single-writer-authority',
      'stream-recovery-authoritative-rebuild',
    ],
  );
  for (const scenario of OPERATIONS_AGENT_SAFETY_SCENARIOS) {
    assert(scenario.requirements.length > 0);
    assert.deepEqual(Object.keys(scenario.invariants), OPERATIONS_AGENT_SAFETY_INVARIANTS);
    for (const invariant of Object.values(scenario.invariants)) {
      assert(['PROVEN', 'NOT_APPLICABLE'].includes(invariant.status));
      assert(Array.isArray(invariant.evidence));
      if (invariant.status === 'PROVEN') assert(invariant.evidence.length > 0);
    }
    for (const requirement of scenario.requirements) {
      assert(OPERATIONS_AGENT_SAFETY_GATES.includes(requirement.gate));
      assert.equal(typeof requirement.marker, 'string');
      assert(requirement.marker.length > 0);
    }
  }
  for (const invariantName of OPERATIONS_AGENT_SAFETY_INVARIANTS) {
    assert(OPERATIONS_AGENT_SAFETY_SCENARIOS.some(({ invariants }) => invariants[invariantName].status === 'PROVEN'));
  }
});

test('Map 5.5 report is machine-readable and passes only with every gate and evidence marker', () => {
  const fixture = passingFixture();
  const report = buildOperationsAgentSafetyCertificationReport({
    repositorySha: '1'.repeat(40),
    startedAt: '2026-08-02T00:00:00.000Z',
    completedAt: '2026-08-02T00:01:00.000Z',
    ...fixture,
  });
  assert.equal(report.passed, true);
  assert.equal(report.formalProductionClaim, false);
  assert.equal(report.productionTrafficPercent, 0);
  assert(report.scenarios.every(({ passed }) => passed));
  assert.deepEqual(validateOperationsAgentSafetyCertificationReport(report), { valid: true, failures: [] });
});

test('Map 5.5 report fails closed on a missing marker, failed gate, or production claim', () => {
  const markerFixture = passingFixture();
  markerFixture.gateResults['agent-service'].stdout = markerFixture.gateResults['agent-service'].stdout
    .replace('simultaneous Operator Input acceptance commits one authoritative outcome', 'missing-concurrency-evidence');
  const missingMarker = buildOperationsAgentSafetyCertificationReport({
    repositorySha: '2'.repeat(40),
    startedAt: '2026-08-02T00:00:00.000Z',
    completedAt: '2026-08-02T00:01:00.000Z',
    ...markerFixture,
  });
  assert.equal(missingMarker.passed, false);
  assert(missingMarker.scenarios.find(({ id }) => id === 'concurrency-single-writer-authority').evidence.some(({ present }) => !present));

  const gateFixture = passingFixture();
  gateFixture.gateResults['operations-postgres'].passed = false;
  const failedGate = buildOperationsAgentSafetyCertificationReport({
    repositorySha: '3'.repeat(40),
    startedAt: '2026-08-02T00:00:00.000Z',
    completedAt: '2026-08-02T00:01:00.000Z',
    ...gateFixture,
  });
  assert.equal(failedGate.passed, false);

  const validFixture = passingFixture();
  const invalidClaim = buildOperationsAgentSafetyCertificationReport({
    repositorySha: '4'.repeat(40),
    startedAt: '2026-08-02T00:00:00.000Z',
    completedAt: '2026-08-02T00:01:00.000Z',
    ...validFixture,
  });
  invalidClaim.formalProductionClaim = true;
  const validation = validateOperationsAgentSafetyCertificationReport(invalidClaim);
  assert.equal(validation.valid, false);
  assert(validation.failures.some((failure) => failure.includes('production traffic')));
});
