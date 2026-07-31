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
    transitions: [{ toStatus: 'OPEN', reason: 'ALARM_PUBLISHED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:00Z', version: 1 }],
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

test('Real Alarm projection distinguishes active, acknowledged, suppressed, and closed states', () => {
  const active = projectRealAlarm(alarmSchema.parse(alarm()));
  assert.equal(active.businessState, 'ACTIVE');
  assert.equal(active.occurrenceLabel, '累计 2 次');
  assert.equal(active.canMutate, false);

  const acknowledged = projectRealAlarm(alarmSchema.parse(alarm({
    status: 'ACKNOWLEDGED',
    transitions: [
      { toStatus: 'OPEN', reason: 'ALARM_PUBLISHED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:00Z', version: 1 },
      { fromStatus: 'OPEN', toStatus: 'ACKNOWLEDGED', reason: 'ALARM_ACKNOWLEDGED', actorType: 'PRINCIPAL', occurredAt: '2026-07-31T09:06:00Z', version: 2 },
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  })));
  assert.equal(acknowledged.businessState, 'ACKNOWLEDGED');

  const suppressed = projectRealAlarm(alarmSchema.parse(alarm({
    status: 'SUPPRESSED',
    transitions: [
      { toStatus: 'OPEN', reason: 'ALARM_PUBLISHED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:00Z', version: 1 },
      { fromStatus: 'OPEN', toStatus: 'SUPPRESSED', reason: 'ALARM_SUPPRESSED', actorType: 'PRINCIPAL', occurredAt: '2026-07-31T09:07:00Z', version: 2 },
    ],
    version: 2,
    updatedAt: '2026-07-31T09:07:00Z',
  })));
  assert.equal(suppressed.businessState, 'SUPPRESSED');

  const closed = projectRealAlarm(alarmSchema.parse(alarm({
    status: 'CLOSED',
    transitions: [
      { toStatus: 'OPEN', reason: 'ALARM_PUBLISHED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:00Z', version: 1 },
      { fromStatus: 'OPEN', toStatus: 'CLOSED', reason: 'ALARM_CLOSED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:08:00Z', version: 2 },
    ],
    version: 2,
    updatedAt: '2026-07-31T09:08:00Z',
  })));
  assert.equal(closed.businessState, 'CLOSED');
});
