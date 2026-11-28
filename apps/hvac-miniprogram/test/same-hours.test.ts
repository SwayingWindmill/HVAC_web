import assert from 'node:assert/strict';
import test from 'node:test';
import { hourlyElectricity } from '../miniprogram/lib/run.ts';
import {
  compareWithYesterday,
  evaluatePlantCop,
  queryNonEmptyHourly,
  sameHoursWindows,
  siteDayBounds,
  summarize,
} from '../miniprogram/lib/today.ts';

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

test('sameHoursWindows clamps yesterday at todayStart across a 23h spring-forward DST transition and at midnight', () => {
  const springNow = new Date('2026-03-10T03:30:00.000Z');
  const springBounds = siteDayBounds(springNow, 'America/New_York')!;
  assert.deepEqual(sameHoursWindows(springBounds.todayStart, springNow, springBounds.yesterdayStart), {
    today: { from: '2026-03-09T04:00:00.000Z', to: '2026-03-10T03:30:00.000Z' },
    yesterday: { from: '2026-03-08T05:00:00.000Z', to: '2026-03-09T04:00:00.000Z' },
  });

  const midnight = new Date('2026-10-11T16:00:00.000Z');
  assert.deepEqual(sameHoursWindows('2026-10-11T16:00:00.000Z', midnight, '2026-10-10T16:00:00.000Z'), {
    today: { from: '2026-10-11T16:00:00.000Z', to: '2026-10-11T16:00:00.000Z' },
    yesterday: { from: '2026-10-10T16:00:00.000Z', to: '2026-10-10T16:00:00.000Z' },
  });

  const yesterdayPoint = hour('2026-03-08T05:00:00.000Z', 50);
  const todayPoint = hour('2026-03-09T04:00:00.000Z', 80);
  const springChart = hourlyElectricity([todayPoint], [yesterdayPoint], springBounds.todayStart, springNow, springBounds.yesterdayStart);
  assert.equal(springChart.bars[0].mark, 63);
});

test('queryNonEmptyHourly skips zero-width midnight windows and queries positive windows', async () => {
  const posted: { from: string; to: string; energyType: string }[] = [];
  const client = {
    async post<T>(_path: string, body: unknown) {
      const payload = body as { from: string; to: string; energyType: string };
      posted.push(payload);
      return { points: [hour(payload.from, 42)] } as T;
    },
  };
  const site = { id: 'site-1', tenantId: 'tenant-1', timezone: 'Asia/Shanghai' };

  const skipped = await queryNonEmptyHourly(client, site, 'electricity', {
    from: '2026-10-11T16:00:00.000Z',
    to: '2026-10-11T16:00:00.000Z',
  });
  assert.deepEqual(skipped, []);
  assert.equal(posted.length, 0);

  const loaded = await queryNonEmptyHourly(client, site, 'cooling', {
    from: '2026-10-11T16:00:00.000Z',
    to: '2026-10-11T17:00:00.000Z',
  });
  assert.equal(loaded.length, 1);
  assert.equal(posted.length, 1);
  assert.equal(posted[0].energyType, 'cooling');
});

test('evaluatePlantCop and summarize enforce <1 kWh standby and [1, 12] physical COP bounds', () => {
  assert.deepEqual(evaluatePlantCop(0.8, 0.2), { cop: null, unavailableReason: 'standby' });
  assert.deepEqual(evaluatePlantCop(12, 0.5), { cop: null, unavailableReason: 'out-of-range' });
  assert.deepEqual(evaluatePlantCop(4, 5), { cop: null, unavailableReason: 'standby' });
  assert.deepEqual(evaluatePlantCop(65, 5), { cop: null, unavailableReason: 'out-of-range' });
  assert.deepEqual(evaluatePlantCop(450, 100), { cop: 4.5, unavailableReason: null });

  const standbySummary = summarize(
    [hour('2026-10-20T00:00:00Z', 0.4)],
    [hour('2026-10-20T00:00:00Z', 8)],
  );
  assert.equal(standbySummary.cop, null);

  const outlierSummary = summarize(
    [hour('2026-10-20T00:00:00Z', 2)],
    [hour('2026-10-20T00:00:00Z', 30)],
  );
  assert.equal(outlierSummary.cop, null);
});
