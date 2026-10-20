import assert from 'node:assert/strict';
import test from 'node:test';
import { againstYesterday } from '../miniprogram/lib/today.ts';

// Hourly buckets at a UTC+8 Site; today began at 16:00Z and it is now 02:30Z (10:30 at the Site).
const hour = (iso: string, energyKWh: number, periods?: Record<string, [number, number]>) => ({
  periodStart: iso,
  periodEnd: new Date(Date.parse(iso) + 3_600_000).toISOString().replace('.000', ''),
  energyKWh,
  ...(periods ? { cost: { periods: Object.entries(periods).map(([periodCode, [kWh, cny]]) => ({ periodCode, energyKWh: kWh, amountCNY: cny })) } } : {}),
});
const todayStart = '2026-10-19T16:00:00Z';
const now = new Date('2026-10-20T02:30:00Z');
const electricity = [
  hour('2026-10-18T23:00:00Z', 100, { VALLEY: [100, 24] }), // yesterday 07:00
  hour('2026-10-19T00:00:00Z', 120, { PEAK: [120, 124] }), // yesterday 08:00
  hour('2026-10-19T03:00:00Z', 500, { PEAK: [500, 515] }), // yesterday 11:00, after now's time of day
  hour('2026-10-19T23:00:00Z', 110, { VALLEY: [110, 26] }), // today 07:00
  hour('2026-10-20T00:00:00Z', 160, { PEAK: [160, 165] }), // today 08:00
  hour('2026-10-20T02:00:00Z', 50, { PEAK: [50, 52] }), // today 10:00, under way
];
const cooling = [
  hour('2026-10-18T23:00:00Z', 400), hour('2026-10-19T00:00:00Z', 540), hour('2026-10-19T03:00:00Z', 2000),
  hour('2026-10-19T23:00:00Z', 420), hour('2026-10-20T00:00:00Z', 700), hour('2026-10-20T02:00:00Z', 200),
];

test('today so far is compared with yesterday up to the same time of day, hour by hour', () => {
  const result = againstYesterday(electricity, cooling, todayStart, now);
  assert.deepEqual(result.today, { electricityKWh: 320, coolingKWh: 1320, cop: 4.13, costCNY: 243, peakShare: 0.66 });
  // 320 / 220, 1320 / 940, (1320 / 320) / (940 / 220), 243 / 148.
  assert.deepEqual(result.change, { electricity: 0.455, cooling: 0.404, cop: -0.035, cost: 0.642 });
  assert.equal(result.yesterdayPeakShare, 0.55);
});

test('a change needs something to compare with', () => {
  const quiet = againstYesterday(electricity.slice(3), cooling.slice(3), todayStart, now);
  assert.deepEqual(quiet.change, { electricity: null, cooling: null, cop: null, cost: null });
});
