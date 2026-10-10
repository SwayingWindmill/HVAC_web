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
  assert.equal(summary.partial, true);
  assert.equal(summary.excludedIntervals, 3);
});

test('a bucket without cooling has no efficiency rather than zero', () => {
  const summary = summarizeEnergy(series([['2026-10-10T02:00:00Z', 180]]), series([]), 'hour', 'Asia/Shanghai');
  assert.deepEqual(summary.rows.map((row) => [row.label, row.coolingKWh, row.cop]), [['10:00', null, null]]);
  assert.equal(summary.coolingKWh, null);
  assert.equal(summary.cop, null);
  assert.equal(summary.dataWatermark, null);
});
