import assert from 'node:assert/strict';
import test from 'node:test';

import {
  OperationsInvestigation,
  OperationsInvestigationError,
  createIdempotencyKey,
  createStepIdentity,
} from '../dist/domain/index.js';

const createInvestigation = () => OperationsInvestigation.create({
  id: 'investigation-001',
  scope: {
    organizationId: 'organization-001',
    siteId: 'site-001',
    equipmentId: null,
    deviceId: null,
  },
  createdAt: 1_000,
});

const startInvestigation = () => createInvestigation().startRun({
  runId: 'run-001',
  runtimeRevision: 'runtime-r1',
  leaseId: 'lease-001',
  leaseAcquiredAt: 1_100,
  leaseExpiresAt: 2_100,
  expectedRevision: 0,
});

const assertDomainError = (run, expectedCode) => {
  assert.throws(run, (error) => (
    error instanceof OperationsInvestigationError && error.code === expectedCode
  ));
};

test('an Investigation starts, pauses, and resumes the same Agent Run with a new lease', () => {
  const created = createInvestigation();
  const started = created.startRun({
    runId: 'run-001',
    runtimeRevision: 'runtime-r1',
    leaseId: 'lease-001',
    leaseAcquiredAt: 1_100,
    leaseExpiresAt: 2_100,
    expectedRevision: 0,
  });
  const paused = started.pauseRun({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_200,
    expectedRevision: 1,
  });
  assertDomainError(() => paused.resumeRun({
    runId: 'run-001',
    leaseId: 'lease-001',
    leaseAcquiredAt: 1_300,
    leaseExpiresAt: 2_300,
    expectedRevision: 2,
  }), 'LEASE_ID_REUSED');

  const resumed = paused.resumeRun({
    runId: 'run-001',
    leaseId: 'lease-002',
    leaseAcquiredAt: 1_300,
    leaseExpiresAt: 2_300,
    expectedRevision: 2,
  });

  assert.deepEqual(created.view(), {
    id: 'investigation-001',
    scope: {
      organizationId: 'organization-001',
      siteId: 'site-001',
      equipmentId: null,
      deviceId: null,
    },
    status: 'CREATED',
    revision: 0,
    activeRunId: null,
    runs: [],
    committedEffects: [],
    evidenceIds: [],
    analysisReferenceIds: [],
    findingIds: [],
    toolReceiptIds: [],
    proposedActionIds: [],
    activeOperatorInputRequest: null,
    operatorInputAcceptances: [],
    acceptedOperatorInputIds: [],
  });
  assert.equal(started.view().status, 'RUNNING');
  assert.equal(paused.view().status, 'PAUSED');
  assert.equal(paused.view().runs[0].lease, null);
  assert.equal(resumed.view().status, 'RUNNING');
  assert.equal(resumed.view().revision, 3);
  assert.equal(resumed.view().activeRunId, 'run-001');
  assert.equal(resumed.view().runs.length, 1);
  assert.equal(resumed.view().runs[0].status, 'ACTIVE');
  assert.equal(resumed.view().runs[0].runtimeRevision, 'runtime-r1');
  assert.equal(resumed.view().runs[0].lease.id, 'lease-002');
  assert.deepEqual(
    resumed.view().runs[0].leaseHistory.map(({ id }) => id),
    ['lease-001', 'lease-002'],
  );

  assertDomainError(() => resumed.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_400,
    expectedRevision: 3,
    stepId: createStepIdentity('step-after-resume'),
    idempotencyKey: createIdempotencyKey('effect-after-resume'),
    kind: 'EVIDENCE',
    recordId: 'evidence-after-resume',
  }), 'LEASE_MISMATCH');
});

test('stale revisions, mismatched leases, and expired leases are rejected independently', () => {
  const started = startInvestigation();

  assertDomainError(() => started.startRun({
    runId: 'run-002',
    runtimeRevision: 'runtime-r2',
    leaseId: 'lease-002',
    leaseAcquiredAt: 1_150,
    leaseExpiresAt: 2_150,
    expectedRevision: 1,
  }), 'INVESTIGATION_STATE_INVALID');

  assertDomainError(() => started.pauseRun({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_200,
    expectedRevision: 0,
  }), 'REVISION_STALE');

  assertDomainError(() => started.pauseRun({
    runId: 'run-001',
    leaseId: 'lease-stale',
    at: 1_200,
    expectedRevision: 1,
  }), 'LEASE_MISMATCH');

  assertDomainError(() => started.pauseRun({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 2_100,
    expectedRevision: 1,
  }), 'LEASE_EXPIRED');
});

test('idempotency deduplicates one effect while Step Identity can own multiple effects', () => {
  const started = startInvestigation();
  const stepId = createStepIdentity('step-energy-baseline');
  const evidenceKey = createIdempotencyKey('effect-evidence-001');
  const first = started.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_200,
    expectedRevision: 1,
    stepId,
    idempotencyKey: evidenceKey,
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });
  const duplicate = first.investigation.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_250,
    expectedRevision: 1,
    stepId,
    idempotencyKey: evidenceKey,
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });
  const second = duplicate.investigation.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_300,
    expectedRevision: 2,
    stepId,
    idempotencyKey: createIdempotencyKey('effect-finding-001'),
    kind: 'FINDING',
    recordId: 'finding-001',
  });

  assert.equal(first.outcome, 'COMMITTED');
  assert.equal(first.investigation.view().revision, 2);
  assert.equal(duplicate.outcome, 'DUPLICATE');
  assert.equal(duplicate.investigation.view().revision, 2);
  assert.equal(second.outcome, 'COMMITTED');
  assert.equal(second.investigation.view().revision, 3);
  assert.deepEqual(second.investigation.view().evidenceIds, ['evidence-001']);
  assert.deepEqual(second.investigation.view().findingIds, ['finding-001']);
  assert.equal(second.investigation.view().committedEffects.length, 2);

  assertDomainError(() => second.investigation.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_350,
    expectedRevision: 3,
    stepId: createStepIdentity('step-different'),
    idempotencyKey: evidenceKey,
    kind: 'EVIDENCE',
    recordId: 'evidence-002',
  }), 'IDEMPOTENCY_KEY_REUSED');

  assertDomainError(() => second.investigation.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_350,
    expectedRevision: 3,
    stepId,
    idempotencyKey: createIdempotencyKey('effect-evidence-duplicate-record'),
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  }), 'EFFECT_RECORD_ALREADY_COMMITTED');

  assertDomainError(() => second.investigation.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_350,
    expectedRevision: 3,
    stepId,
    idempotencyKey: createIdempotencyKey('effect-cross-kind-duplicate-record'),
    kind: 'PROPOSED_ACTION',
    recordId: 'evidence-001',
  }), 'EFFECT_RECORD_ALREADY_COMMITTED');
});

test('cancellation releases the lease, preserves committed records, and blocks future effects', () => {
  const stepId = createStepIdentity('step-energy-baseline');
  const evidenceKey = createIdempotencyKey('effect-evidence-001');
  const evidence = startInvestigation().commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_200,
    expectedRevision: 1,
    stepId,
    idempotencyKey: evidenceKey,
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });
  const finding = evidence.investigation.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_250,
    expectedRevision: 2,
    stepId,
    idempotencyKey: createIdempotencyKey('effect-finding-001'),
    kind: 'FINDING',
    recordId: 'finding-001',
  });
  const proposal = finding.investigation.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_300,
    expectedRevision: 3,
    stepId,
    idempotencyKey: createIdempotencyKey('effect-proposal-001'),
    kind: 'PROPOSED_ACTION',
    recordId: 'proposal-001',
  });
  const cancelled = proposal.investigation.cancel({
    at: 1_350,
    expectedRevision: 4,
  });
  const cancelledView = cancelled.view();

  assert.equal(cancelledView.status, 'CANCELLED');
  assert.equal(cancelledView.activeRunId, null);
  assert.equal(cancelledView.runs[0].status, 'CANCELLED');
  assert.equal(cancelledView.runs[0].lease, null);
  assert.deepEqual(cancelledView.evidenceIds, ['evidence-001']);
  assert.deepEqual(cancelledView.findingIds, ['finding-001']);
  assert.deepEqual(cancelledView.proposedActionIds, ['proposal-001']);
  assert.equal(cancelledView.committedEffects.length, 3);

  assertDomainError(() => cancelled.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_400,
    expectedRevision: 5,
    stepId: createStepIdentity('step-after-cancel'),
    idempotencyKey: createIdempotencyKey('effect-after-cancel'),
    kind: 'FINDING',
    recordId: 'finding-after-cancel',
  }), 'INVESTIGATION_STATE_INVALID');

  const duplicate = cancelled.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_450,
    expectedRevision: 2,
    stepId,
    idempotencyKey: evidenceKey,
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });
  assert.equal(duplicate.outcome, 'DUPLICATE');
  assert.equal(duplicate.investigation.view().revision, 5);
});

test('terminal Agent Runs are immutable and reopening a completed Investigation creates a new Run', () => {
  const committed = startInvestigation().commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_200,
    expectedRevision: 1,
    stepId: createStepIdentity('step-energy-baseline'),
    idempotencyKey: createIdempotencyKey('effect-evidence-001'),
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  }).investigation;
  const completed = committed.completeRun({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_300,
    expectedRevision: 2,
  });

  assert.equal(completed.view().status, 'COMPLETED');
  assert.equal(completed.view().activeRunId, null);
  assert.equal(completed.view().runs[0].status, 'COMPLETED');
  assert.equal(completed.view().runs[0].lease, null);

  assertDomainError(() => completed.pauseRun({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_400,
    expectedRevision: 3,
  }), 'INVESTIGATION_STATE_INVALID');

  assertDomainError(() => completed.reopenCompleted({
    runId: 'run-001',
    runtimeRevision: 'runtime-r2',
    leaseId: 'lease-002',
    leaseAcquiredAt: 1_500,
    leaseExpiresAt: 2_500,
    expectedRevision: 3,
  }), 'RUN_ID_REUSED');

  const reopened = completed.reopenCompleted({
    runId: 'run-002',
    runtimeRevision: 'runtime-r2',
    leaseId: 'lease-002',
    leaseAcquiredAt: 1_500,
    leaseExpiresAt: 2_500,
    expectedRevision: 3,
  });
  const reopenedView = reopened.view();

  assert.equal(reopenedView.status, 'RUNNING');
  assert.equal(reopenedView.activeRunId, 'run-002');
  assert.equal(reopenedView.runs.length, 2);
  assert.equal(reopenedView.runs[0].status, 'COMPLETED');
  assert.equal(reopenedView.runs[0].runtimeRevision, 'runtime-r1');
  assert.equal(reopenedView.runs[1].status, 'ACTIVE');
  assert.equal(reopenedView.runs[1].runtimeRevision, 'runtime-r2');
  assert.deepEqual(reopenedView.evidenceIds, ['evidence-001']);

  assertDomainError(() => reopened.commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_550,
    expectedRevision: 4,
    stepId: createStepIdentity('step-old-run'),
    idempotencyKey: createIdempotencyKey('effect-old-run'),
    kind: 'FINDING',
    recordId: 'finding-old-run',
  }), 'RUN_NOT_ACTIVE');

  assertDomainError(() => reopened.resumeRun({
    runId: 'run-001',
    leaseId: 'lease-stale',
    leaseAcquiredAt: 1_600,
    leaseExpiresAt: 2_600,
    expectedRevision: 4,
  }), 'INVESTIGATION_STATE_INVALID');
});

test('framework-independent snapshots round-trip and reject corrupted Lease or effect indexes', () => {
  const committed = startInvestigation().commitEffect({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_200,
    expectedRevision: 1,
    stepId: createStepIdentity('step-energy-baseline'),
    idempotencyKey: createIdempotencyKey('effect-evidence-001'),
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  }).investigation;
  const snapshot = committed.snapshot();

  assert.deepEqual(OperationsInvestigation.restore(snapshot).snapshot(), snapshot);
  assertDomainError(() => OperationsInvestigation.restore({
    ...snapshot,
    runs: [{
      ...snapshot.runs[0],
      lease: {
        ...snapshot.runs[0].lease,
        id: 'lease-not-in-history',
      },
    }],
  }), 'LEASE_MISMATCH');
  assertDomainError(() => OperationsInvestigation.restore({
    ...snapshot,
    evidenceIds: [],
  }), 'INVESTIGATION_STATE_INVALID');
  assertDomainError(() => OperationsInvestigation.restore({
    ...snapshot,
    scope: { ...snapshot.scope, siteId: '' },
  }), 'IDENTITY_INVALID');
  assertDomainError(() => OperationsInvestigation.restore({
    ...snapshot,
    committedEffects: [{ ...snapshot.committedEffects[0], kind: 'UNKNOWN' }],
  }), 'INVESTIGATION_STATE_INVALID');
  assertDomainError(() => OperationsInvestigation.restore({
    ...snapshot,
    runs: [
      snapshot.runs[0],
      {
        ...snapshot.runs[0],
        id: 'run-002',
        lease: {
          id: 'lease-002',
          runId: 'run-002',
          acquiredAt: 1_300,
          expiresAt: 2_300,
        },
        leaseHistory: [{
          id: 'lease-002',
          runId: 'run-002',
          acquiredAt: 1_300,
          expiresAt: 2_300,
        }],
      },
    ],
  }), 'INVESTIGATION_STATE_INVALID');
  assertDomainError(() => OperationsInvestigation.restore({
    ...snapshot,
    committedEffects: [{ ...snapshot.committedEffects[0], committedAt: 2_100 }],
  }), 'LEASE_MISMATCH');
});

test('failed and cancelled Investigations cannot be reopened or resumed', () => {
  const failed = startInvestigation().failRun({
    runId: 'run-001',
    leaseId: 'lease-001',
    at: 1_200,
    expectedRevision: 1,
  });

  assert.equal(failed.view().status, 'FAILED');
  assert.equal(failed.view().runs[0].status, 'FAILED');
  assertDomainError(() => failed.reopenCompleted({
    runId: 'run-002',
    runtimeRevision: 'runtime-r2',
    leaseId: 'lease-002',
    leaseAcquiredAt: 1_300,
    leaseExpiresAt: 2_300,
    expectedRevision: 2,
  }), 'INVESTIGATION_STATE_INVALID');

  const cancelled = createInvestigation().cancel({ at: 1_100, expectedRevision: 0 });
  assert.equal(cancelled.view().status, 'CANCELLED');
  assertDomainError(() => cancelled.startRun({
    runId: 'run-001',
    runtimeRevision: 'runtime-r1',
    leaseId: 'lease-001',
    leaseAcquiredAt: 1_200,
    leaseExpiresAt: 2_200,
    expectedRevision: 1,
  }), 'INVESTIGATION_STATE_INVALID');
});
