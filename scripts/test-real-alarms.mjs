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

const tenantId = '018f3e00-1000-7000-8000-000000000001';
const siteId = '018f3e00-2000-7000-8000-000000000001';
const alarmId = '018f3e00-4000-7000-8000-000000000001';
const incidentCorrelationId = '018f3e00-4000-7000-8000-000000000002';

function timelineEntry({ operation = 'PUBLISH', condition = 'ACTIVE', occurredAt = '2026-07-31T09:00:00Z', version = 1, reason = `ALARM_${operation}`, assigneeId, suppression, currentSeverity = 'MAJOR' } = {}) {
  return {
    operation,
    condition,
    reason,
    actorType: operation === 'PUBLISH' ? 'WORKLOAD' : 'PRINCIPAL',
    actorId: operation === 'PUBLISH' ? 'alarm-evaluator' : 'principal:operator-1',
    ...(assigneeId ? { assigneeId } : {}),
    ...(suppression ? { suppression } : {}),
    currentSeverity,
    policyRevision: 'alarm-policy-9',
    correlationId: `alarm-test-${version}`,
    occurredAt,
    version,
  };
}

function alarm(overrides = {}) {
  return {
    schemaVersion: 2,
    alarmId,
    tenantId,
    siteId,
    alarmType: 'SUPPLY_TEMPERATURE_DRIFT',
    fingerprint: 'a'.repeat(64),
    incidentCorrelationId,
    sourceType: 'SITE_RULE',
    sourceReference: 'rule:central-plant-temperature-drift:v3',
    ruleRevision: 'alarm-policy-9',
    title: 'Supply temperature drift',
    summary: 'Alarm Service published a durable operational exception.',
    condition: 'ACTIVE',
    currentSeverity: 'MAJOR',
    peakSeverity: 'MAJOR',
    occurrenceCount: 2,
    firstOccurredAt: '2026-07-31T09:00:00Z',
    lastOccurredAt: '2026-07-31T09:05:00Z',
    evidence: [{ kind: 'telemetry-snapshot', reference: 'snapshot:41', capturedAt: '2026-07-31T09:05:00Z' }],
    links: [],
    timeline: [timelineEntry()],
    version: 1,
    createdAt: '2026-07-31T09:00:00Z',
    updatedAt: '2026-07-31T09:05:00Z',
    ...overrides,
  };
}

function acknowledgement(at = '2026-07-31T09:06:00Z') {
  return { acknowledgedAt: at, acknowledgedBy: 'principal:operator-1', comment: 'known' };
}

function suppression(startsAt = '2026-07-31T09:07:00Z') {
  return {
    startsAt,
    expiresAt: '2026-07-31T13:07:00Z',
    reason: 'maintenance window',
    actorId: 'principal:operator-1',
    policyRevision: 'alarm-policy-9',
  };
}

test('Alarm projection requires the S13 orthogonal aggregate facts', () => {
  const parsed = alarmSchema.parse(alarm());
  assert.equal(parsed.condition, 'ACTIVE');
  assert.equal(parsed.currentSeverity, 'MAJOR');
  assert.equal(parsed.peakSeverity, 'MAJOR');
  assert.equal(parsed.evidence.length, 1);
  assert.throws(() => alarmSchema.parse(alarm({ sourceReference: '', timeline: [] })));
  assert.throws(() => alarmSchema.parse(alarm({ peakSeverity: 'WARNING' })));
});

test('Real Alarm scope validation fails closed for a cross-Site projection', () => {
  const parsed = alarmSchema.parse(alarm());
  assert.throws(
    () => validateAlarmScope(parsed, { trustedTenantId: tenantId, trustedSiteId: '018f3e00-2000-7000-8000-000000000002' }),
    (error) => error instanceof AlarmApiError && error.status === 404 && error.code === 'RESOURCE_NOT_FOUND',
  );
});

test('Alarm list preserves empty collection semantics without fabrication', () => {
  const response = alarmListResponseSchema.parse({ schemaVersion: 2, items: [], nextCursor: null, hasMore: false });
  assert.deepEqual(validateAlarmListScope(response, { trustedTenantId: tenantId, trustedSiteId: siteId }).items, []);
});

test('ACK and suppression are orthogonal to the ACTIVE physical condition', () => {
  const active = projectRealAlarm(alarmSchema.parse(alarm()));
  assert.equal(active.businessState, 'ACTIVE');
  assert.equal(active.canAcknowledge, true);
  assert.equal(active.canSuppress, true);

  const acknowledged = projectRealAlarm(alarmSchema.parse(alarm({
    acknowledgement: acknowledgement(),
    timeline: [
      timelineEntry(),
      timelineEntry({ operation: 'ACKNOWLEDGE', occurredAt: '2026-07-31T09:06:00Z', version: 2, reason: 'known' }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  })));
  assert.equal(acknowledged.businessState, 'ACKNOWLEDGED');
  assert.equal(acknowledged.canAcknowledge, false);
  assert.equal(acknowledged.canSuppress, true);

  const activeSuppression = suppression();
  const suppressed = projectRealAlarm(alarmSchema.parse(alarm({
    suppression: activeSuppression,
    timeline: [
      timelineEntry(),
      timelineEntry({ operation: 'SUPPRESS', occurredAt: '2026-07-31T09:07:00Z', version: 2, suppression: activeSuppression, reason: activeSuppression.reason }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:07:00Z',
  })));
  assert.equal(suppressed.businessState, 'SUPPRESSED');
  assert.equal(suppressed.canSuppress, false);
  assert.equal(suppressed.canUnsuppress, true);
});

test('recovery is CLEARED and has no operator reopen path', () => {
  const clearedAt = '2026-07-31T09:08:00Z';
  const cleared = projectRealAlarm(alarmSchema.parse(alarm({
    condition: 'CLEARED',
    clearedAt,
    timeline: [
      timelineEntry(),
      timelineEntry({ operation: 'CLEAR', condition: 'CLEARED', occurredAt: clearedAt, version: 2, reason: 'clear predicate matched' }),
    ],
    version: 2,
    updatedAt: clearedAt,
  })));
  assert.equal(cleared.businessState, 'CLEARED');
  assert.equal(cleared.canAssign, false);
  assert.equal(cleared.canSuppress, false);
  assert.equal('canReopen' in cleared, false);
  assert.throws(() => alarmSchema.parse(alarm({
    timeline: [timelineEntry(), timelineEntry({ operation: 'REOPEN', occurredAt: clearedAt, version: 2 })],
    version: 2,
    updatedAt: clearedAt,
  })));
});

test('current severity may recover while peak severity preserves the incident maximum', () => {
  const parsed = alarmSchema.parse(alarm({
    currentSeverity: 'MINOR',
    peakSeverity: 'CRITICAL',
    timeline: [
      timelineEntry({ currentSeverity: 'CRITICAL' }),
      timelineEntry({ operation: 'PUBLISH', occurredAt: '2026-07-31T09:06:00Z', version: 2, currentSeverity: 'MINOR' }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  }));
  assert.equal(parsed.currentSeverity, 'MINOR');
  assert.equal(parsed.peakSeverity, 'CRITICAL');
});

test('Work Order completion is not an Alarm condition transition', () => {
  assert.throws(() => alarmSchema.parse(alarm({
    condition: 'CLEARED',
    clearedAt: '2026-07-31T09:06:00Z',
    timeline: [
      timelineEntry(),
      timelineEntry({ operation: 'WORK_ORDER_COMPLETE', condition: 'CLEARED', occurredAt: '2026-07-31T09:06:00Z', version: 2 }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  })));
});

test('Alarm contract rejects timeline tampering and non-convergent condition/severity', () => {
  assert.throws(() => alarmSchema.parse(alarm({
    timeline: [timelineEntry({ version: 2 })],
  })));
  assert.throws(() => alarmSchema.parse(alarm({
    condition: 'CLEARED',
    clearedAt: '2026-07-31T09:06:00Z',
    timeline: [timelineEntry()],
    updatedAt: '2026-07-31T09:06:00Z',
  })));
  assert.throws(() => alarmSchema.parse(alarm({
    currentSeverity: 'MINOR',
    timeline: [timelineEntry({ currentSeverity: 'MAJOR' })],
  })));
});
