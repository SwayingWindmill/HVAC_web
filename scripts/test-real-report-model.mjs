import assert from 'node:assert/strict';
import test from 'node:test';
import { alarmSummary, findingSummary, recentMonths, reportWindow, snapshotDays, snapshotPeak, workOrderSummary, yearMonths, yearRows, yearTotals } from '../apps/hvac-web/src/features/reports/report-model.ts';

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

const snapshot = (month, electricityKWh, coolingKWh, energyCostCNY, days = []) => ({
  id: `snapshot-${month}`, month, version: 2, generatedAt: '2026-10-01T03:00:05Z',
  report: { schemaVersion: 1, month, timezone: zone, daysInMonth: 30, daysWithData: days.length, electricityKWh, coolingKWh, cop: coolingKWh / electricityKWh, energyCostCNY, energyCostStatus: energyCostCNY === null ? 'NO_TARIFF' : 'PRICED', tariffPeriods: [], days, findings: { count: 0, byType: [] }, savings: { days: 0, adjustedBaselineKWh: 0, avoidedKWh: 0, avoidedCost: 0, unpricedHours: 0 } },
});

test('a year runs January to the current month, or all twelve once it is over', () => {
  assert.deepEqual(yearMonths(2026, new Date('2026-03-15T04:00:00Z'), zone), ['2026-01', '2026-02', '2026-03']);
  assert.equal(yearMonths(2025, new Date('2026-03-15T04:00:00Z'), zone).length, 12);
});

test("a year's months come from their snapshots, else from the live series, else have no data", () => {
  const rows = yearRows(['2026-08', '2026-09', '2026-10'], [snapshot('2026-09', 100, 400, 80)],
    new Map([['2026-08', { electricityKWh: 90, coolingKWh: 360, energyCost: null }], ['2026-09', { electricityKWh: 1, coolingKWh: 1, energyCost: 1 }]]));
  assert.deepEqual(rows.map((row) => [row.month, row.source, row.electricityKWh]), [['2026-08', 'live', 90], ['2026-09', 'snapshot', 100], ['2026-10', 'none', null]]);
  assert.equal(rows[1].version, 2);
  assert.equal(rows[0].cop, 4);
  assert.equal(rows[2].daysInMonth, 31);
});

test("a year's cost is only shown when every month with energy has one", () => {
  const priced = yearTotals(yearRows(['2026-09'], [snapshot('2026-09', 100, 400, 80)], new Map()));
  assert.equal(priced.energyCost, 80);
  assert.equal(priced.cop, 4);
  const partial = yearTotals(yearRows(['2026-08', '2026-09'], [snapshot('2026-09', 100, 400, 80)], new Map([['2026-08', { electricityKWh: 90, coolingKWh: 360, energyCost: null }]])));
  assert.equal(partial.electricityKWh, 190);
  assert.equal(partial.energyCost, null);
});

test("a snapshot's days fill the month's axis, with gaps for days without metering", () => {
  const stored = snapshot('2026-09', 30, 120, 25, [
    { date: '2026-09-02', electricityKWh: 20, coolingKWh: 80, cop: 4, energyCostCNY: 16 },
    { date: '2026-09-05', electricityKWh: 10, coolingKWh: 40, cop: 4, energyCostCNY: 9 },
  ]);
  const days = snapshotDays(stored.report);
  assert.equal(days.length, 30);
  assert.deepEqual(days[1], { label: '9/2', electricityKWh: 20, cop: 4 });
  assert.deepEqual(days[0], { label: '9/1', electricityKWh: null, cop: null });
  assert.deepEqual(snapshotPeak(stored.report), { label: '9/2 周三', electricityKWh: 20 });
});
