import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AlarmApiError,
  alarmListResponseSchema,
  alarmSchema,
  validateAlarmListScope,
  validateAlarmScope,
} from '../apps/hvac-web/src/api/alarm-contract.ts';
import { projectRealAlarm } from '../apps/hvac-web/src/real/real-alarms-projection.ts';

const organizationId = '018f3e00-1000-7000-8000-000000000001';
const siteId = '018f3e00-2000-7000-8000-000000000001';
const alarmId = '018f3e00-4000-7000-8000-000000000001';

function publishedTransition() {
  return {
    toStatus: 'OPEN',
    operation: 'PUBLISH',
    reason: 'ALARM_PUBLISHED',
    actorType: 'WORKLOAD',
    occurredAt: '2026-07-31T09:00:00Z',
    version: 1,
  };
}

function lifecycleTransition({
  fromStatus,
  toStatus,
  operation,
  occurredAt,
  version,
  assigneeId,
  suppressedUntil,
}) {
  return {
    fromStatus,
    toStatus,
    operation,
    reason: `ALARM_${operation}`,
    actorType: 'PRINCIPAL',
    actorId: 'principal:operator-1',
    ...(assigneeId ? { assigneeId } : {}),
    ...(suppressedUntil ? { suppressedUntil } : {}),
    policyRevision: 'alarm-policy-9',
    correlationId: `alarm-test-${version}`,
    occurredAt,
    version,
  };
}

function alarm(overrides = {}) {
  return {
    schemaVersion: 1,
    alarmId,
    organizationId,
    siteId,
    sourceType: 'SITE_RULE',
    sourceReference: 'rule:central-plant-temperature-drift:v3',
    title: 'Supply temperature drift',
    summary: 'Alarm Service published a durable operational exception.',
    severity: 'MAJOR',
    status: 'OPEN',
    occurrenceCount: 2,
    firstOccurredAt: '2026-07-31T09:00:00Z',
    lastOccurredAt: '2026-07-31T09:05:00Z',
    evidence: [{ kind: 'telemetry-snapshot', reference: 'snapshot:41', capturedAt: '2026-07-31T09:05:00Z' }],
    transitions: [publishedTransition()],
    version: 1,
    createdAt: '2026-07-31T09:00:00Z',
    updatedAt: '2026-07-31T09:05:00Z',
    ...overrides,
  };
}

test('Alarm projection requires owner-published source, evidence, and convergent lifecycle', () => {
  const parsed = alarmSchema.parse(alarm());
  assert.equal(parsed.sourceType, 'SITE_RULE');
  assert.equal(parsed.evidence.length, 1);
  assert.throws(() => alarmSchema.parse(alarm({ sourceReference: '', transitions: [] })));
});

test('Real Alarm scope validation fails closed for a cross-Site projection', () => {
  const parsed = alarmSchema.parse(alarm());
  assert.throws(
    () => validateAlarmScope(parsed, { trustedOrganizationId: organizationId, trustedSiteId: '018f3e00-2000-7000-8000-000000000002' }),
    (error) => error instanceof AlarmApiError && error.status === 404 && error.code === 'RESOURCE_NOT_FOUND',
  );
});

test('Alarm list preserves empty and valid-zero collection semantics without fabrication', () => {
  const response = alarmListResponseSchema.parse({ schemaVersion: 1, items: [], nextCursor: null, hasMore: false });
  assert.deepEqual(validateAlarmListScope(response, { trustedOrganizationId: organizationId, trustedSiteId: siteId }).items, []);
});

test('Real Alarm projection exposes legal lifecycle operations for every authoritative state', () => {
  const active = projectRealAlarm(alarmSchema.parse(alarm()));
  assert.equal(active.businessState, 'ACTIVE');
  assert.equal(active.occurrenceLabel, '累计 2 次');
  assert.equal(active.canMutate, true);
  assert.equal(active.canAcknowledge, true);
  assert.equal(active.canAssign, true);
  assert.equal(active.canSuppress, true);
  assert.equal(active.canClose, true);
  assert.equal(active.canReopen, false);

  const acknowledged = projectRealAlarm(alarmSchema.parse(alarm({
    status: 'ACKNOWLEDGED',
    transitions: [
      publishedTransition(),
      lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'ACKNOWLEDGED', operation: 'ACKNOWLEDGE', occurredAt: '2026-07-31T09:06:00Z', version: 2 }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  })));
  assert.equal(acknowledged.businessState, 'ACKNOWLEDGED');
  assert.equal(acknowledged.canAcknowledge, false);
  assert.equal(acknowledged.canSuppress, true);

  const suppressedUntil = '2026-07-31T13:07:00Z';
  const suppressed = projectRealAlarm(alarmSchema.parse(alarm({
    status: 'SUPPRESSED',
    suppressedUntil,
    transitions: [
      publishedTransition(),
      lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'SUPPRESSED', operation: 'SUPPRESS', occurredAt: '2026-07-31T09:07:00Z', version: 2, suppressedUntil }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:07:00Z',
  })));
  assert.equal(suppressed.businessState, 'SUPPRESSED');
  assert.equal(suppressed.canSuppress, false);
  assert.equal(suppressed.canUnsuppress, true);

  const closed = projectRealAlarm(alarmSchema.parse(alarm({
    status: 'CLOSED',
    transitions: [
      publishedTransition(),
      lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'CLOSED', operation: 'CLOSE', occurredAt: '2026-07-31T09:08:00Z', version: 2 }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:08:00Z',
  })));
  assert.equal(closed.businessState, 'CLOSED');
  assert.equal(closed.canClose, false);
  assert.equal(closed.canAssign, false);
  assert.equal(closed.canReopen, true);
});

test('Alarm contract requires assignment and suppression facts to converge with the timeline', () => {
  const assigneeId = 'principal:operator-2';
  const assigned = alarm({
    assigneeId,
    transitions: [
      publishedTransition(),
      lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'OPEN', operation: 'ASSIGN', occurredAt: '2026-07-31T09:06:00Z', version: 2, assigneeId }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  });
  assert.equal(alarmSchema.parse(assigned).assigneeId, assigneeId);
  assert.throws(() => alarmSchema.parse({ ...assigned, assigneeId: undefined }));

  const suppressedUntil = '2026-07-31T13:07:00Z';
  const suppressed = alarm({
    status: 'SUPPRESSED',
    suppressedUntil,
    transitions: [
      publishedTransition(),
      lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'SUPPRESSED', operation: 'SUPPRESS', occurredAt: '2026-07-31T09:07:00Z', version: 2, suppressedUntil }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:07:00Z',
  });
  assert.equal(alarmSchema.parse(suppressed).suppressedUntil, suppressedUntil);
  assert.throws(() => alarmSchema.parse({ ...suppressed, status: 'OPEN' }));
});

test('Alarm contract restores the suppression origin after assignment while suppressed', () => {
  const assigneeId = 'principal:operator-2';
  const suppressedUntil = '2026-07-31T13:07:00Z';
  const transitions = [
    publishedTransition(),
    lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'ACKNOWLEDGED', operation: 'ACKNOWLEDGE', occurredAt: '2026-07-31T09:06:00Z', version: 2 }),
    lifecycleTransition({ fromStatus: 'ACKNOWLEDGED', toStatus: 'SUPPRESSED', operation: 'SUPPRESS', occurredAt: '2026-07-31T09:07:00Z', version: 3, suppressedUntil }),
    lifecycleTransition({ fromStatus: 'SUPPRESSED', toStatus: 'SUPPRESSED', operation: 'ASSIGN', occurredAt: '2026-07-31T09:08:00Z', version: 4, assigneeId }),
    lifecycleTransition({ fromStatus: 'SUPPRESSED', toStatus: 'ACKNOWLEDGED', operation: 'UNSUPPRESS', occurredAt: '2026-07-31T09:09:00Z', version: 5 }),
  ];
  const restored = alarmSchema.parse(alarm({
    status: 'ACKNOWLEDGED',
    assigneeId,
    transitions,
    version: 5,
    updatedAt: '2026-07-31T09:09:00Z',
  }));
  assert.equal(restored.status, 'ACKNOWLEDGED');
  assert.equal(restored.assigneeId, assigneeId);
  assert.throws(() => alarmSchema.parse(alarm({
    status: 'OPEN',
    assigneeId,
    transitions: [
      ...transitions.slice(0, -1),
      lifecycleTransition({ fromStatus: 'SUPPRESSED', toStatus: 'OPEN', operation: 'UNSUPPRESS', occurredAt: '2026-07-31T09:09:00Z', version: 5 }),
    ],
    version: 5,
    updatedAt: '2026-07-31T09:09:00Z',
  })));
});

test('Alarm contract rejects unaudited, illegal, and non-convergent lifecycle transitions', () => {
  const unaudited = lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'ACKNOWLEDGED', operation: 'ACKNOWLEDGE', occurredAt: '2026-07-31T09:06:00Z', version: 2 });
  delete unaudited.policyRevision;
  assert.throws(() => alarmSchema.parse(alarm({ status: 'ACKNOWLEDGED', transitions: [publishedTransition(), unaudited], version: 2, updatedAt: '2026-07-31T09:06:00Z' })));

  assert.throws(() => alarmSchema.parse(alarm({
    status: 'CLOSED',
    transitions: [
      publishedTransition(),
      lifecycleTransition({ fromStatus: 'OPEN', toStatus: 'CLOSED', operation: 'ACKNOWLEDGE', occurredAt: '2026-07-31T09:06:00Z', version: 2 }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  })));
});
