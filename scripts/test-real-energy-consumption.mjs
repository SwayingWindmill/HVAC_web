import assert from 'node:assert/strict';
import test from 'node:test';
import { periodWindow, summarizeEnergy } from '../apps/hvac-web/src/features/energy-analysis/consumption/model.ts';

const now = new Date('2026-10-10T06:30:00Z'); // 14:30 Saturday in Asia/Shanghai

test('periods start at Site-local boundaries, not UTC or browser midnight', () => {
  const window = (period) => {
    const { granularity, from, to } = periodWindow(period, now, 'Asia/Shanghai');
    return [granularity, from.toISOString(), to.toISOString()];
  };
  assert.deepEqual(window('today'), ['hour', '2026-10-09T16:00:00.000Z', '2026-10-10T06:30:00.000Z']);
  assert.deepEqual(window('7d'), ['day', '2026-10-03T16:00:00.000Z', '2026-10-10T06:30:00.000Z']);
  assert.deepEqual(window('month'), ['day', '2026-09-30T16:00:00.000Z', '2026-10-10T06:30:00.000Z']);
  assert.deepEqual(window('year'), ['month', '2025-12-31T16:00:00.000Z', '2026-10-10T06:30:00.000Z']);
});

const series = (points, metadata = {}) => ({
  schemaVersion: 1,
  points: points.map(([periodStart, energyKWh]) => ({ periodStart, periodEnd: periodStart, energyKWh })),
  metadata: {
    requestedGranularity: 'day', actualGranularity: 'day', datasetRevision: 'r', partial: false,
    qualitySummary: { valid: 1, suspect: 0, invalid: 0 }, ...metadata,
  },
});

test('electricity and cooling join per Site-local bucket into plant efficiency', () => {
  const summary = summarizeEnergy(
    series([['2026-10-07T16:00:00Z', 2000], ['2026-10-08T16:00:00Z', 2500]], { dataWatermark: '2026-10-10T06:29:00Z', partial: true }),
    series([['2026-10-07T16:00:00Z', 7000], ['2026-10-08T16:00:00Z', 8000]], { dataWatermark: '2026-10-10T06:28:00Z', qualitySummary: { valid: 1, suspect: 2, invalid: 1 } }),
    'day',
    'Asia/Shanghai',
  );
  assert.deepEqual(summary.rows.map((row) => [row.label, row.electricityKWh, row.coolingKWh, row.cop]), [
    ['10/8 周四', 2000, 7000, 3.5],
    ['10/9 周五', 2500, 8000, 3.2],
  ]);
  assert.equal(summary.electricityKWh, 4500);
  assert.equal(summary.coolingKWh, 15000);
  assert.equal(summary.cop, 15000 / 4500);
  assert.equal(summary.peak?.label, '10/9 周五');
  assert.equal(summary.dataWatermark, '2026-10-10T06:28:00Z');
  assert.equal(summary.granularity, 'day');
  assert.equal(summary.excludedIntervals, 3);
});

test('a bucket without cooling has no efficiency and stays out of the period efficiency', () => {
  const summary = summarizeEnergy(
    series([['2026-10-10T02:00:00Z', 180], ['2026-10-10T03:00:00Z', 250]]),
    series([['2026-10-10T03:00:00Z', 875]]),
    'hour',
    'Asia/Shanghai',
  );
  assert.deepEqual(summary.rows.map((row) => [row.label, row.coolingKWh, row.cop]), [['10:00', null, null], ['11:00', 875, 3.5]]);
  assert.equal(summary.electricityKWh, 430);
  assert.equal(summary.cop, 3.5);
  assert.equal(summarizeEnergy(series([['2026-10-10T02:00:00Z', 180]]), series([]), 'hour', 'Asia/Shanghai').cop, null);
});

test('the previous period covers the same span, ending where the current one starts', async () => {
  const { previousWindow } = await import('../apps/hvac-web/src/features/energy-analysis/consumption/model.ts');
  const window = (period) => {
    const { from, to } = previousWindow(period, now, 'Asia/Shanghai');
    return [from.toISOString(), to.toISOString()];
  };
  assert.deepEqual(window('today'), ['2026-10-08T16:00:00.000Z', '2026-10-09T06:30:00.000Z']);
  assert.deepEqual(window('7d'), ['2026-09-26T16:00:00.000Z', '2026-10-03T06:30:00.000Z']);
  assert.deepEqual(window('month'), ['2026-08-31T16:00:00.000Z', '2026-09-10T06:30:00.000Z']);
  assert.deepEqual(window('year'), ['2024-12-31T16:00:00.000Z', '2025-10-10T06:30:00.000Z']);
  // On 31 March the same span of February would run into March: it stops at February's end.
  const { to } = previousWindow('month', new Date('2026-03-31T04:00:00Z'), 'Asia/Shanghai');
  assert.equal(to.toISOString(), '2026-02-28T16:00:00.000Z');
});

test('buckets line up by their place in the period, and the bucket still running stays off the chart', async () => {
  const { comparePeriods, completeOnly, relativeChange } = await import('../apps/hvac-web/src/features/energy-analysis/consumption/model.ts');
  const today = summarizeEnergy(series([['2026-10-10T02:00:00Z', 200], ['2026-10-10T03:00:00Z', 250]]), series([['2026-10-10T02:00:00Z', 700]]), 'hour', 'Asia/Shanghai');
  const yesterday = summarizeEnergy(series([['2026-10-09T02:00:00Z', 180], ['2026-10-09T05:00:00Z', 999]]), series([]), 'hour', 'Asia/Shanghai');
  const hours = (at) => comparePeriods(today, yesterday, {
    from: new Date('2026-10-09T16:00:00Z'), previousFrom: new Date('2026-10-08T16:00:00Z'), now: new Date(at), timeZone: 'Asia/Shanghai',
  });
  // At 11:20 the 11:00 hour is still running: listed, but not drawn.
  const running = hours('2026-10-10T03:20:00Z');
  assert.deepEqual(running.map((row) => [row.label, row.electricityKWh, row.previousElectricityKWh, row.complete]), [
    ['10:00', 200, 180, true],
    ['11:00', 250, null, false],
    ['13:00', null, 999, false],
  ]);
  assert.deepEqual(completeOnly(running).map((row) => row.electricityKWh), [200, null, null]);
  assert.deepEqual(completeOnly(hours('2026-10-10T04:00:00Z')).map((row) => row.electricityKWh), [200, 250, null]);

  const days = (points) => summarizeEnergy(series(points), series([]), 'day', 'Asia/Shanghai');
  const month = comparePeriods(days([['2026-09-30T16:00:00Z', 50], ['2026-10-02T16:00:00Z', 70]]), days([['2026-08-31T16:00:00Z', 40], ['2026-09-01T16:00:00Z', 45]]), {
    from: new Date('2026-09-30T16:00:00Z'), previousFrom: new Date('2026-08-31T16:00:00Z'), now, timeZone: 'Asia/Shanghai',
  });
  assert.deepEqual(month.map((row) => [row.label, row.electricityKWh, row.previousElectricityKWh]), [
    ['10/1 周四', 50, 40],
    ['10/2 周五', null, 45],
    ['10/3 周六', 70, null],
  ]);

  assert.equal(relativeChange(450, 400), 0.125);
  assert.equal(relativeChange(450, null), null);
  assert.equal(relativeChange(450, 0), null);
});

test('hourly energy becomes a Site-local day by hour grid with unread hours left empty', async () => {
  const { hourlyHeatmap, siteDays } = await import('../apps/hvac-web/src/features/energy-analysis/consumption/model.ts');
  const days = siteDays(now, 'Asia/Shanghai', 2);
  assert.deepEqual(days, ['2026-10-09', '2026-10-10']);
  const grid = hourlyHeatmap(series([['2026-10-09T16:00:00Z', 12], ['2026-10-10T05:00:00Z', 30]]), 'Asia/Shanghai', days);
  assert.deepEqual(grid.map((day) => day.label), ['10/9 周五', '10/10 周六']);
  assert.equal(grid[1].values[0], 12);
  assert.equal(grid[1].values[13], 30);
  assert.equal(grid[1].values[14], null);
  assert.equal(grid[0].values.every((value) => value === null), true);
});
