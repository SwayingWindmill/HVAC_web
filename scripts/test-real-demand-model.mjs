import assert from 'node:assert/strict';
import test from 'node:test';
import { demandChartRows, demandSummary, demandWindows, quarterHours } from '../apps/hvac-web/src/features/demand/demand-model.ts';

const zone = 'Asia/Shanghai';
const bucket = (start, average, minutes = 15) => ({
  telemetryKey: 'active_power', pointId: 'p', pointRevision: 1, pointType: 'TELEMETRY', unit: 'kW',
  periodStart: start, periodEnd: new Date(Date.parse(start) + minutes * 60_000).toISOString(),
  quality: {}, completeness: 1, gauge: { average, minimum: average, maximum: average, first: average, last: average, sampleCount: 90 },
});

test('the month and yesterday are read in chunks the history owner accepts', () => {
  // 22:00 on 11 October, Site time.
  const windows = demandWindows(new Date('2026-10-11T14:00:00Z'), zone);
  assert.deepEqual(windows.map((window) => [window.from.toISOString(), window.to.toISOString()]), [
    ['2026-09-30T16:00:00.000Z', '2026-10-10T16:00:00.000Z'],
    ['2026-10-10T16:00:00.000Z', '2026-10-11T14:00:00.000Z'],
  ]);
  // On the first of the month yesterday still has to be read for the comparison.
  assert.equal(demandWindows(new Date('2026-10-01T02:00:00Z'), zone)[0].from.toISOString(), '2026-09-29T16:00:00.000Z');
});

test('the running quarter-hour is shown but never counted as a peak', () => {
  const now = new Date('2026-10-11T14:05:00Z');
  const hours = quarterHours([
    bucket('2026-10-11T13:45:00Z', 200),
    { ...bucket('2026-10-11T14:00:00Z', 900), periodEnd: now.toISOString() },
  ], now);
  assert.deepEqual(hours.map((hour) => hour.complete), [true, false]);
  const summary = demandSummary(hours, now, zone);
  assert.equal(summary.current.demandKW, 900);
  assert.deepEqual(summary.today, { at: '2026-10-11T13:45:00.000Z', demandKW: 200 });
});

test('peaks are read per Site day, and the load factor is today\'s average over its peak', () => {
  const now = new Date('2026-10-11T14:00:00Z');
  const hours = quarterHours([
    bucket('2026-10-09T20:00:00Z', 150), // 10 Oct, Site time
    bucket('2026-10-10T03:00:00Z', 260),
    bucket('2026-10-10T16:00:00Z', 100), // 11 Oct
    bucket('2026-10-11T04:00:00Z', 300),
  ], now);
  const summary = demandSummary(hours, now, zone);
  assert.deepEqual(summary.yesterday, { at: '2026-10-10T03:00:00.000Z', demandKW: 260 });
  assert.deepEqual(summary.month, { at: '2026-10-11T04:00:00.000Z', demandKW: 300 });
  assert.equal(summary.todayLoadFactor, 200 / 300);
  assert.deepEqual(summary.dailyPeaks.map((day) => [day.day, day.peak?.demandKW]), [['2026-10-10', 260], ['2026-10-11', 300]]);

  const rows = demandChartRows(hours, now, zone);
  assert.equal(rows.length, 96);
  assert.deepEqual(rows[0], { minute: 0, today: 100, yesterday: null });
  assert.deepEqual(rows[48], { minute: 720, today: 300, yesterday: null });
  assert.equal(rows[16].yesterday, 150);
});
