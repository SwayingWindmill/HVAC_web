import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { hourlyCop, hourlyElectricity } from '../miniprogram/lib/run.ts';
import { compareWithYesterday, deriveHourlyTodayStart, sameHoursWindows, siteDayBounds, splitHourlyWindow, summarize, type HourPoint } from '../miniprogram/lib/today.ts';

// The web overview's 今日用能 (apps/hvac-web/src/features/energy-analysis/consumption/model.ts):
// today runs from the Site's midnight to now, yesterday from the midnight before to the same
// time of day, both cut by the Energy owner at those instants.

test('today and yesterday up to the same time of day are two windows of equal length', () => {
  assert.deepEqual(sameHoursWindows('2026-10-19T16:00:00Z', new Date('2026-10-20T06:05:00Z')), {
    today: { from: '2026-10-19T16:00:00.000Z', to: '2026-10-20T06:05:00.000Z' },
    yesterday: { from: '2026-10-18T16:00:00.000Z', to: '2026-10-19T06:05:00.000Z' },
  });
});

const hour = (start: string, energyKWh: number, cost?: { amountCNY: number; unpricedKWh: number }) => ({
  periodStart: start, periodEnd: start, energyKWh, ...(cost ? { cost: { ...cost, periods: [] } } : {}),
});

test('a summary adds the hours up, counts efficiency only where both meters read, and cost only where it was priced', () => {
  const electricity = [
    hour('2026-10-20T00:00:00Z', 100, { amountCNY: 103, unpricedKWh: 0 }),
    hour('2026-10-20T01:00:00Z', 120, { amountCNY: 124, unpricedKWh: 0 }),
    hour('2026-10-20T02:00:00Z', 80),
  ];
  const cooling = [hour('2026-10-20T00:00:00Z', 450), hour('2026-10-20T02:00:00Z', 360)];
  assert.deepEqual(summarize(electricity, cooling), {
    electricityKWh: 300,
    coolingKWh: 810,
    // (450 + 360) / (100 + 80): the 01:00 hour has no cooling reading.
    cop: 4.5,
    costCNY: 227,
  });
});

test('no readings is no number, not zero', () => {
  assert.deepEqual(summarize([], []), { electricityKWh: null, coolingKWh: null, cop: null, costCNY: null });
  assert.equal(summarize([hour('2026-10-20T00:00:00Z', 100)], []).costCNY, null);
});

test('each change is against yesterday up to the same time, and needs something to compare with', () => {
  const today = { electricityKWh: 1691, coolingKWh: 7657.3, cop: 4.53, costCNY: 1423 };
  const yesterday = { electricityKWh: 1463, coolingKWh: 6733, cop: 4.6, costCNY: 1095 };
  const change = compareWithYesterday(today, yesterday);
  assert.equal(change.electricity?.toFixed(3), '0.156');
  assert.equal(change.cost?.toFixed(3), '0.300');
  assert.deepEqual(compareWithYesterday(today, { electricityKWh: null, coolingKWh: 0, cop: null, costCNY: null }),
    { electricity: null, cooling: null, cop: null, cost: null });
});

test('splitHourlyWindow slices 72h hourly series identically to sameHoursWindows across recorded fixtures', () => {
  const recorded = JSON.parse(readFileSync(new URL('../../hvac-web/e2e/fixtures/energy-series.json', import.meta.url), 'utf8')) as {
    electricity: { points: HourPoint[] };
    cooling: { points: HourPoint[] };
  };
  const DAY_MS = 86_400_000;
  const recordedDayStart = Date.parse('2026-10-09T16:00:00.000Z');
  const shiftPoints = (points: readonly HourPoint[], dayShift: number, factor: number): HourPoint[] =>
    points.map((point) => ({
      ...point,
      periodStart: new Date(Date.parse(point.periodStart) + dayShift).toISOString(),
      periodEnd: new Date(Date.parse(point.periodEnd) + dayShift).toISOString(),
      energyKWh: point.energyKWh * factor,
      ...(point.cost ? { cost: { ...point.cost, amountCNY: point.cost.amountCNY * factor, unpricedKWh: point.cost.unpricedKWh * factor } } : {}),
    }));

  const now = new Date('2026-10-12T05:30:00.000Z');
  const elec72h = [
    ...shiftPoints(recorded.electricity.points, DAY_MS * 0, 0.8),
    ...shiftPoints(recorded.electricity.points, DAY_MS * 1, 0.9),
    ...shiftPoints(recorded.electricity.points, DAY_MS * 2, 1.0),
  ];
  const cool72h = [
    ...shiftPoints(recorded.cooling.points, DAY_MS * 0, 0.8),
    ...shiftPoints(recorded.cooling.points, DAY_MS * 1, 0.9),
    ...shiftPoints(recorded.cooling.points, DAY_MS * 2, 1.0),
  ];

  const start = deriveHourlyTodayStart(elec72h, now, 'Asia/Shanghai')!;
  assert.equal(start, new Date(recordedDayStart + DAY_MS * 2).toISOString());
  const windows = sameHoursWindows(start, now);
  const filterWindow = (points: readonly HourPoint[], window: { from: string; to: string }) => {
    const from = Date.parse(window.from);
    const to = Date.parse(window.to);
    return points.filter((point) => {
      const at = Date.parse(point.periodStart);
      return at >= from && at < to;
    });
  };
  const legacyTodayElec = filterWindow(elec72h, windows.today);
  const legacyTodayCool = filterWindow(cool72h, windows.today);
  const legacyYesterdayElec = filterWindow(elec72h, windows.yesterday);
  const legacyYesterdayCool = filterWindow(cool72h, windows.yesterday);

  const bounds = siteDayBounds(now, 'Asia/Shanghai')!;
  const slicedElec = splitHourlyWindow(elec72h, start, now, bounds.yesterdayStart);
  const slicedCool = splitHourlyWindow(cool72h, start, now, bounds.yesterdayStart);

  assert.deepEqual(slicedElec.today, legacyTodayElec);
  assert.deepEqual(slicedElec.yesterday, legacyYesterdayElec);
  assert.deepEqual(slicedCool.today, legacyTodayCool);
  assert.deepEqual(slicedCool.yesterday, legacyYesterdayCool);

  const todaySummary = summarize(slicedElec.today, slicedCool.today);
  const yesterdaySummary = summarize(slicedElec.yesterday, slicedCool.yesterday);
  assert.deepEqual(todaySummary, summarize(legacyTodayElec, legacyTodayCool));
  assert.deepEqual(yesterdaySummary, summarize(legacyYesterdayElec, legacyYesterdayCool));
  assert.deepEqual(compareWithYesterday(todaySummary, yesterdaySummary), compareWithYesterday(summarize(legacyTodayElec, legacyTodayCool), summarize(legacyYesterdayElec, legacyYesterdayCool)));
  assert.deepEqual(hourlyElectricity(slicedElec.today, slicedElec.yesterday, start, now, bounds.yesterdayStart), hourlyElectricity(legacyTodayElec, legacyYesterdayElec, start, now));
  assert.deepEqual(hourlyCop(slicedElec.today, slicedCool.today, start, now), hourlyCop(legacyTodayElec, legacyTodayCool, start, now));

  const midnight = new Date('2026-10-11T16:00:00.000Z');
  assert.deepEqual(splitHourlyWindow(elec72h, '2026-10-11T16:00:00.000Z', midnight, '2026-10-10T16:00:00.000Z'), { today: [], yesterday: [] });
});

test('splitHourlyWindow clamps yesterday at todayStart across a 23h spring-forward DST transition', () => {
  const springNow = new Date('2026-03-10T03:30:00.000Z');
  const springBounds = siteDayBounds(springNow, 'America/New_York')!;
  const springPoints = [
    hour('2026-03-08T05:00:00.000Z', 50),
    hour('2026-03-09T04:00:00.000Z', 80),
  ];
  const springSplit = splitHourlyWindow(springPoints, springBounds.todayStart, springNow, springBounds.yesterdayStart);
  assert.deepEqual(springSplit.yesterday.map((p) => p.energyKWh), [50]);
  assert.deepEqual(springSplit.today.map((p) => p.energyKWh), [80]);
  const springChart = hourlyElectricity(springSplit.today, springSplit.yesterday, springBounds.todayStart, springNow, springBounds.yesterdayStart);
  assert.equal(springChart.bars[0].mark, 63);
});
