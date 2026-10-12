import assert from 'node:assert/strict';
import test from 'node:test';
import { alarmSummary, findingSummary, recentMonths, reportWindow, workOrderSummary } from '../apps/hvac-web/src/features/reports/report-model.ts';

const zone = 'Asia/Shanghai';

test('a past month runs midnight to midnight on the Site clock, compared with the whole month before', () => {
  const window = reportWindow('2026-09', new Date('2026-10-12T04:00:00Z'), zone);
  assert.equal(window.from.toISOString(), '2026-08-31T16:00:00.000Z');
  assert.equal(window.to.toISOString(), '2026-09-30T16:00:00.000Z');
  assert.equal(window.complete, true);
  assert.equal(window.previousFrom.toISOString(), '2026-07-31T16:00:00.000Z');
  assert.equal(window.previousTo.toISOString(), '2026-08-31T16:00:00.000Z');
  assert.equal(window.days, 30);
});

test('the current month runs to now, compared with the same span of the month before', () => {
  const now = new Date('2026-10-12T04:00:00Z');
  const window = reportWindow('2026-10', now, zone);
  assert.equal(window.from.toISOString(), '2026-09-30T16:00:00.000Z');
  assert.equal(window.to.toISOString(), now.toISOString());
  assert.equal(window.complete, false);
  assert.equal(window.previousFrom.toISOString(), '2026-08-31T16:00:00.000Z');
  assert.equal(window.previousTo.getTime(), window.previousFrom.getTime() + (now.getTime() - window.from.getTime()));
});

test('the month picker offers the current month and the eleven before it, newest first', () => {
  const months = recentMonths(new Date('2026-01-05T04:00:00Z'), zone);
  assert.equal(months.length, 12);
  assert.deepEqual(months.slice(0, 3), ['2026-01', '2025-12', '2025-11']);
});

const from = new Date('2026-08-31T16:00:00Z');
const to = new Date('2026-09-30T16:00:00Z');

test('alarms raised in the month are counted by severity, with how long cleared ones lasted', () => {
  const alarm = (firstOccurredAt, currentSeverity, clearedAt) => ({ firstOccurredAt, currentSeverity, peakSeverity: currentSeverity, clearedAt, condition: clearedAt ? 'CLEARED' : 'ACTIVE' });
  const summary = alarmSummary([
    alarm('2026-09-02T00:00:00Z', 'CRITICAL', '2026-09-02T01:00:00Z'),
    alarm('2026-09-10T00:00:00Z', 'MINOR', '2026-09-10T00:30:00Z'),
    alarm('2026-09-20T00:00:00Z', 'MINOR'),
    alarm('2026-08-20T00:00:00Z', 'MAJOR', '2026-09-03T00:00:00Z'),
  ], from, to);
  assert.equal(summary.raised, 3);
  assert.equal(summary.cleared, 2);
  assert.equal(summary.meanMinutesToClear, 45);
  assert.deepEqual(summary.bySeverity, { CRITICAL: 1, MAJOR: 0, MINOR: 2, WARNING: 0, INFO: 0 });
});

test('work orders opened in the month, and those completed in it whenever they were opened', () => {
  const order = (createdAt, completedAt, status = completedAt ? 'COMPLETED' : 'OPEN') => ({
    createdAt, status, timeline: completedAt ? [{ operation: 'COMPLETE', occurredAt: completedAt }] : [],
  });
  const summary = workOrderSummary([
    order('2026-09-05T00:00:00Z', '2026-09-06T00:00:00Z'),
    order('2026-09-15T00:00:00Z'),
    order('2026-08-25T00:00:00Z', '2026-09-02T00:00:00Z'),
    order('2026-08-10T00:00:00Z', '2026-08-12T00:00:00Z'),
  ], from, to);
  assert.deepEqual(summary, { opened: 2, completed: 2, stillOpen: 1 });
});

test('findings in the month are grouped by what they found, most frequent first', () => {
  const finding = (evaluationTo, findingType) => ({ evaluationTo, findingType });
  const summary = findingSummary([
    finding('2026-09-03T00:00:00Z', 'CHILLED_WATER_LOW_DELTA_T'),
    finding('2026-09-04T00:00:00Z', 'CHILLED_WATER_LOW_DELTA_T'),
    finding('2026-09-05T00:00:00Z', 'OTHER'),
    finding('2026-10-01T00:00:00Z', 'OTHER'),
  ], from, to);
  assert.equal(summary.count, 3);
  assert.deepEqual(summary.byType, [{ findingType: 'CHILLED_WATER_LOW_DELTA_T', count: 2 }, { findingType: 'OTHER', count: 1 }]);
});
