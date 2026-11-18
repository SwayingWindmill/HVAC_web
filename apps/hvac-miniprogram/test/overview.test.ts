import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { createGateway, type Transport } from '../miniprogram/lib/gateway.ts';
import { buildPlant } from '../miniprogram/lib/plant.ts';
import { loadOverview, presentOverview } from '../miniprogram/lib/overview.ts';
import { hourlyCop, hourlyElectricity } from '../miniprogram/lib/run.ts';
import { monthToDate, savingsCard } from '../miniprogram/lib/savings.ts';
import { compareWithYesterday, sameHoursWindows, summarize, type HourPoint } from '../miniprogram/lib/today.ts';
import { assetModel, recordedAt, snapshots } from './recorded.ts';

const today = { electricityKWh: 1691, coolingKWh: 7657.32, cop: 4.528, costCNY: 1423.48 };
const change = { electricity: 0.156, cooling: 0.137, cop: -0.016, cost: 0.3 };
const recommendation = {
  createdAt: '2026-10-20T00:00:00Z',
  baseline: { supplyTempC: 7 }, candidate: { supplyTempC: 8 },
  expectedImpact: { energySavingKWhPerDay: 180.4, costSavingPerDay: 210.2 },
  risk: { level: 'LOW' as const, comfortMarginC: 1.2 },
};

test('the overview leads with savings and what more could be saved, then today as the web shows it, then what needs handling', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: { cost: 8640, kWh: 7350, rate: 0.142 }, why: null, measures: [
      { id: 'm1', name: '冷冻水供水温度调高 1 °C', status: 'IMPLEMENTED', baseline: { trusted: true }, totals: { days: 20, avoidedKWh: 5020, avoidedCost: 5910 } },
    ] } },
    recommendation: { status: 'ok', latest: recommendation },
    energy: { status: 'ok', today, change },
    alarms: { status: 'ok', active: 5, complete: true, waiting: 3 },
  });
  assert.deepEqual(view.savings, {
    amount: '¥8,640', detail: '少用电 7,350 kWh · 比不做措施少 14.2%',
    measures: [{ id: 'm1', name: '冷冻水供水温度调高 1 °C', amount: '¥5,910', detail: '本月统计 20 天 · 少用电 5,020 kWh' }],
    hint: '',
  });
  assert.deepEqual(view.recommendation, {
    title: '冷冻水供水 7.0 → 8.0 °C，预计每天再省 ¥210', detail: '少用电 180 kWh/天', risk: { text: '低风险', tone: 'good' },
  });
  // The web shows electricity and cooling to one decimal, efficiency to two, cost in whole yuan.
  assert.deepEqual(view.energy.metrics.map((each) => [each.label, each.value, each.unit, each.badge?.text, each.badge?.tone]), [
    ['空调用电', '1,691.0', 'kWh', '▲ 15.6%', 'bad'],
    ['电费（估算）', '¥1,423', '', '▲ 30.0%', 'bad'],
    ['供冷量', '7,657.3', 'kWh', '▲ 13.7%', 'neutral'],
    ['综合能效', '4.53', '', '▼ 1.6%', 'bad'],
  ]);
  assert.deepEqual(view.energy.metrics.map((each) => each.icon), ['zap', 'receipt', 'snowflake', 'gauge']);
  // The same count as the red dot on 通知: the alarms happening that no one has acknowledged.
  assert.deepEqual(view.alarms, { count: '5', waiting: '3 条没人确认', happening: true });
});

// Savings are what the customer opens the mini program for: they stay the first card even before
// there are any, saying why, with what more could be saved right under them.
test('savings not there yet keep the top card with a dash and why, and the recommendation says it has none', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'ok', today, change },
    alarms: { status: 'ok', active: 0, complete: true, waiting: 0 },
  });
  assert.deepEqual(view.savings, {
    amount: '—', detail: '还没有在统计的节能措施', measures: [], hint: '在电脑网页「能源分析 → 节能项目」登记措施后，这里显示省了多少。',
  });
  assert.deepEqual(view.recommendation, { title: '还没有优化建议', detail: '在电脑网页「能源分析 → 优化建议」生成后，这里显示还能省多少。', risk: null });
  assert.deepEqual(view.alarms, { count: '0', waiting: '', happening: false });

  // A measure in its first day needs nothing done on the web: only wait.
  const waiting = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'FIRST_DAY_PENDING', measures: [] } },
    recommendation: { status: 'ok', latest: recommendation },
    energy: { status: 'ok', today, change },
    alarms: { status: 'ok', active: 2, complete: true, waiting: 0 },
  });
  assert.deepEqual(waiting.savings, { amount: '—', detail: '措施实施满 24 小时后，第二天开始算', measures: [], hint: '' });
  assert.equal(waiting.recommendation.title, '冷冻水供水 7.0 → 8.0 °C，预计每天再省 ¥210');
  assert.deepEqual(waiting.alarms, { count: '2', waiting: '都有人确认了', happening: true });

  const partial = presentOverview({
    savings: { status: 'ok', card: { saved: { cost: 8640, kWh: 7350, rate: null }, why: null, measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'ok', today, change },
    alarms: { status: 'ok', active: 3, complete: false, waiting: 0 },
  });
  assert.equal(partial.savings.amount, '¥8,640');
  // Only the first page of alarms was read: the rest may be waiting.
  assert.deepEqual(partial.alarms, { count: '3+', waiting: '', happening: true });

  const unreadable = presentOverview({ savings: { status: 'error' }, recommendation: { status: 'error' }, energy: { status: 'error' }, alarms: { status: 'error' } });
  assert.deepEqual(unreadable.savings, { amount: '—', detail: '暂时读不到，下拉刷新再试', measures: [], hint: '' });
  assert.deepEqual(unreadable.recommendation, { title: '暂时读不到，下拉刷新再试', detail: '', risk: null });
  // Unreadable alarms do not show the red icon of alarms happening.
  assert.deepEqual(unreadable.alarms, { count: '—', waiting: '', happening: false });
});

test('missing data says so: no tariff hides the cost, an unreadable section says it could not be read', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'ok', today: { electricityKWh: null, coolingKWh: 12, cop: null, costCNY: null }, change: { electricity: null, cooling: null, cop: null, cost: null } },
    alarms: { status: 'ok', active: 100, complete: false, waiting: 40 },
  });
  assert.deepEqual(view.energy.metrics.map((each) => [each.label, each.value]), [['空调用电', '—'], ['供冷量', '12.0'], ['综合能效', '—']]);
  // Only the first page of alarms was read.
  assert.deepEqual(view.alarms, { count: '100+', waiting: '至少 40 条没人确认', happening: true });
  assert.equal(presentOverview({ savings: { status: 'error' }, recommendation: { status: 'error' }, energy: { status: 'error' }, alarms: { status: 'error' } }).energy.state, 'error');
});

test('a Site with no electricity readings says so, instead of reporting a failure', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'empty' },
    alarms: { status: 'ok', active: 0, complete: true, waiting: 0 },
  });
  assert.deepEqual(view.energy, { state: 'empty', metrics: [], note: '这个站点今天和昨天都没有用电读数。' });
});

test('loadOverview fetches all four energy queries and savings/measures concurrently in Stage 1, triggers savings/summary without waiting for cooling/plant, and matches exact calculations', async () => {
  const recorded = JSON.parse(readFileSync(new URL('../../hvac-web/e2e/fixtures/energy-series.json', import.meta.url), 'utf8')) as {
    electricity: { points: HourPoint[] };
    cooling: { points: HourPoint[] };
  };
  const DAY_MS = 86_400_000;
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
    ...shiftPoints(recorded.electricity.points, 0, 0.8),
    ...shiftPoints(recorded.electricity.points, DAY_MS, 0.9),
    ...shiftPoints(recorded.electricity.points, DAY_MS * 2, 1.0),
  ];
  const cool72h = [
    ...shiftPoints(recorded.cooling.points, 0, 0.8),
    ...shiftPoints(recorded.cooling.points, DAY_MS, 0.9),
    ...shiftPoints(recorded.cooling.points, DAY_MS * 2, 1.0),
  ];
  const inRange = (points: readonly HourPoint[], fromIso: string, toIso: string) => {
    const from = Date.parse(fromIso);
    const to = Date.parse(toIso);
    return points.filter((p) => Date.parse(p.periodStart) >= from && Date.parse(p.periodStart) < to);
  };

  const site = { id: 'site-1', tenantId: 'tenant-1', displayName: '松雅轩', timezone: 'Asia/Shanghai' };
  const calls: string[] = [];
  let releaseSlowStage1!: () => void;
  const slowStage1 = new Promise<void>((resolve) => { releaseSlowStage1 = resolve; });
  let summaryRequestedWhileSlowStage1Pending = false;

  let markSummaryRequested!: () => void;
  const summaryRequested = new Promise<void>((resolve) => { markSummaryRequested = resolve; });

  const gateway = fakeTestGateway(async (request) => {
    const path = new URL(request.url).pathname + new URL(request.url).search;
    if (request.method === 'GET') {
      calls.push(`GET ${path}`);
      if (path.endsWith('/savings/measures')) {
        await slowStage1;
        return { statusCode: 200, data: { items: [{ id: 'm1', name: '供水温度优化', status: 'IMPLEMENTED', baseline: { trusted: true }, totals: { days: 10, avoidedKWh: 1200, avoidedCost: 1400 } }] } };
      }
      if (path.includes('/savings/summary?')) {
        summaryRequestedWhileSlowStage1Pending = true;
        markSummaryRequested();
        return { statusCode: 200, data: { totals: { days: 10, avoidedKWh: 1200, avoidedCost: 1400, plantKWh: 8800 } } };
      }
      if (path.endsWith('/optimization/recommendations/latest')) {
        return { statusCode: 200, data: { createdAt: '2026-10-12T00:00:00Z', baseline: { supplyTempC: 7 }, candidate: { supplyTempC: 8 }, expectedImpact: { energySavingKWhPerDay: 180, costSavingPerDay: 210 }, risk: { level: 'LOW', comfortMarginC: 1.2 } } };
      }
      if (path.startsWith('/api/v1/alarms')) {
        return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
      }
      throw new Error(`unexpected GET ${path}`);
    }
    const body = request.data as { energyType: string; granularity: string; from: string; to: string };
    calls.push(`POST ${path} ${body.energyType}:${body.granularity}`);
    assert.equal(body.granularity, 'hour');
    if (body.energyType === 'electricity') return { statusCode: 200, data: { points: inRange(elec72h, body.from, body.to) } };
    if (body.energyType === 'cooling') {
      await slowStage1;
      return { statusCode: 200, data: { points: inRange(cool72h, body.from, body.to) } };
    }
    throw new Error(`unexpected POST ${path}`);
  });

  const loading = loadOverview({
    gateway,
    loadPlant: async () => {
      calls.push('PLANT');
      await slowStage1;
      return buildPlant(assetModel, snapshots, recordedAt);
    },
  }, site, now);

  await summaryRequested;
  assert.equal(summaryRequestedWhileSlowStage1Pending, true);
  releaseSlowStage1();

  const result = await loading;
  assert.equal(calls.filter((entry) => entry.includes('/energy-series')).length, 4);
  assert.ok(!calls.some((entry) => entry.includes(':day')));
  assert.ok(calls.includes('GET /api/v1/sites/site-1/savings/summary?from=2026-10-01&to=2026-10-12'));

  const start = '2026-10-11T16:00:00.000Z';
  const windows = sameHoursWindows(start, now);
  const expectedToday = summarize(inRange(elec72h, windows.today.from, windows.today.to), inRange(cool72h, windows.today.from, windows.today.to));
  const expectedYesterday = summarize(inRange(elec72h, windows.yesterday.from, windows.yesterday.to), inRange(cool72h, windows.yesterday.from, windows.yesterday.to));

  assert.deepEqual(result.inputs.energy, {
    status: 'ok',
    today: expectedToday,
    change: compareWithYesterday(expectedToday, expectedYesterday),
  });
  assert.deepEqual(result.charts, {
    electricity: hourlyElectricity(inRange(elec72h, windows.today.from, windows.today.to), inRange(elec72h, windows.yesterday.from, windows.yesterday.to), start, now),
    cop: hourlyCop(inRange(elec72h, windows.today.from, windows.today.to), inRange(cool72h, windows.today.from, windows.today.to), start, now),
  });
  assert.deepEqual(result.inputs.savings, {
    status: 'ok',
    card: savingsCard(
      { days: 10, avoidedKWh: 1200, avoidedCost: 1400, plantKWh: 8800 },
      [{ id: 'm1', name: '供水温度优化', status: 'IMPLEMENTED', baseline: { trusted: true }, totals: { days: 10, avoidedKWh: 1200, avoidedCost: 1400 } }],
    ),
  });
  assert.deepEqual(monthToDate(start), { from: '2026-10-01', to: '2026-10-12' });
});

function fakeTestGateway(transport: Transport) {
  return createGateway({
    origin: 'https://hvac.example.test',
    transport,
    chunked: () => { throw new Error('not used'); },
    session: { get: () => 'session-1', set: () => {}, clear: () => {} },
    onSignedOut: () => {},
  });
}

async function withBrokenIntlTimeZone<T>(run: () => Promise<T>): Promise<T> {
  const original = Intl.DateTimeFormat;
  Intl.DateTimeFormat = new Proxy(original, {
    construct(target, args: [Intl.LocalesArgument?, Intl.DateTimeFormatOptions?]) {
      return new target('en-US', { ...args[1], timeZone: 'UTC' });
    },
    apply(target, _thisArg, args: [Intl.LocalesArgument?, Intl.DateTimeFormatOptions?]) {
      return target('en-US', { ...args[1], timeZone: 'UTC' });
    },
  });
  try {
    return await run();
  } finally {
    Intl.DateTimeFormat = original;
  }
}

test('loadOverview skips savings/summary on empty sites', async () => {
  const site = { id: 'site-empty', tenantId: 'tenant-1', displayName: '空站点', timezone: 'Asia/Shanghai' };
  const calls: string[] = [];
  const gateway = fakeTestGateway(async (request) => {
    const path = new URL(request.url).pathname + new URL(request.url).search;
    if (request.method === 'GET') {
      calls.push(`GET ${path}`);
      if (path.endsWith('/savings/measures')) return { statusCode: 200, data: { items: [] } };
      if (path.endsWith('/optimization/recommendations/latest')) return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
      if (path.startsWith('/api/v1/alarms')) return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
      throw new Error(`unexpected GET ${path}`);
    }
    calls.push(`POST ${path}`);
    return { statusCode: 200, data: { points: [] } };
  });
  const empty = await loadOverview({ gateway, loadPlant: async () => buildPlant({ ...assetModel, devices: [] }, snapshots, recordedAt) }, site, new Date('2026-10-20T06:00:00.000Z'));
  assert.deepEqual(empty.inputs.energy, { status: 'empty' });
  assert.equal(empty.charts, null);
  assert.ok(!calls.some((entry) => entry.includes('/savings/summary')));
  assert.deepEqual(empty.inputs.savings, { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } });
  assert.deepEqual(empty.inputs.recommendation, { status: 'ok', latest: null });
});

test('loadOverview falls back to recentDays when Intl ignores timeZone on a DST zone', async () => {
  const nySite = { id: 'site-ny', tenantId: 'tenant-1', displayName: 'NY', timezone: 'America/New_York' };
  const nyCalls: string[] = [];
  await withBrokenIntlTimeZone(async () => {
    const fallbackResult = await loadOverview({
      gateway: fakeTestGateway(async (request) => {
        const path = new URL(request.url).pathname + new URL(request.url).search;
        if (request.method === 'GET') {
          nyCalls.push(`GET ${path}`);
          if (path.endsWith('/savings/measures')) return { statusCode: 200, data: { items: [] } };
          if (path.includes('/savings/summary?')) return { statusCode: 200, data: { totals: { days: 0, avoidedKWh: 0, avoidedCost: 0, plantKWh: 0 } } };
          if (path.endsWith('/optimization/recommendations/latest')) return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
          return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
        }
        const body = request.data as { energyType: string; granularity: string; from: string; to: string };
        nyCalls.push(`POST ${body.energyType}:${body.granularity}`);
        if (body.granularity === 'day') {
          return { statusCode: 200, data: { points: [{ periodStart: '2026-11-02T05:00:00.000Z', periodEnd: '2026-11-03T05:00:00.000Z', energyKWh: 200 }] } };
        }
        if (body.from.startsWith('2026-11-01')) return { statusCode: 200, data: { points: [] } };
        if (body.energyType === 'electricity') {
          return { statusCode: 200, data: { points: [{ periodStart: '2026-11-02T10:00:00.000Z', periodEnd: '2026-11-02T11:00:00.000Z', energyKWh: 100 }] } };
        }
        return { statusCode: 200, data: { points: [{ periodStart: '2026-11-02T10:00:00.000Z', periodEnd: '2026-11-02T11:00:00.000Z', energyKWh: 400 }] } };
      }),
      loadPlant: async () => buildPlant({ ...assetModel, devices: [] }, snapshots, recordedAt),
    }, nySite, new Date('2026-11-02T12:00:00.000Z'));

    assert.ok(nyCalls.includes('POST electricity:day'));
    assert.equal(fallbackResult.inputs.energy.status, 'ok');
    if (fallbackResult.inputs.energy.status === 'ok') {
      assert.equal(fallbackResult.inputs.energy.today.electricityKWh, 100);
      assert.equal(fallbackResult.inputs.energy.today.coolingKWh, 400);
      assert.equal(fallbackResult.inputs.energy.today.cop, 4);
    }
  });
});

test('loadOverview cuts yesterday at exact same time of day (05:30Z) for half-hour parity with web', async () => {
  // Raw 10-minute intervals truncated by Cube SQL rule: period_end >= from AND period_end <= to - 1ms.
  interface RawInterval {
    readonly periodStart: string;
    readonly periodEnd: string;
    readonly electricityKWh: number;
    readonly coolingKWh: number;
  }
  const HOUR_MS = 3_600_000;
  const TEN_MIN_MS = 600_000;
  const intervals: RawInterval[] = [];
  for (const dayStartIso of ['2026-10-10T16:00:00.000Z', '2026-10-11T16:00:00.000Z']) {
    const dayStartMs = Date.parse(dayStartIso);
    const isYesterday = dayStartIso.startsWith('2026-10-10');
    for (let hourIndex = 12; hourIndex < 15; hourIndex += 1) {
      const hourStartMs = dayStartMs + hourIndex * HOUR_MS;
      for (let slot = 0; slot < 6; slot += 1) {
        const startMs = hourStartMs + slot * TEN_MIN_MS;
        const endMs = startMs + 5 * 60_000; // end at :05, :15, :25, :35, :45, :55
        intervals.push({
          periodStart: new Date(startMs).toISOString(),
          periodEnd: new Date(endMs).toISOString(),
          electricityKWh: isYesterday ? 10 : 12,
          coolingKWh: isYesterday ? 45 : 54,
        });
      }
    }
  }

  const cubeHourlyQuery = (energyType: 'electricity' | 'cooling', fromIso: string, toIso: string): HourPoint[] => {
    const fromMs = Date.parse(fromIso);
    const toMs = Date.parse(toIso);
    const buckets = new Map<number, number>();
    for (const row of intervals) {
      const endMs = Date.parse(row.periodEnd);
      if (endMs >= fromMs && endMs <= toMs - 1) {
        const hourMs = Math.floor(Date.parse(row.periodStart) / HOUR_MS) * HOUR_MS;
        const value = energyType === 'electricity' ? row.electricityKWh : row.coolingKWh;
        buckets.set(hourMs, (buckets.get(hourMs) ?? 0) + value);
      }
    }
    return [...buckets.entries()]
      .sort(([a], [b]) => a - b)
      .map(([hourMs, energyKWh]) => ({
        periodStart: new Date(hourMs).toISOString(),
        periodEnd: new Date(hourMs + HOUR_MS).toISOString(),
        energyKWh,
      }));
  };

  const now = new Date('2026-10-12T05:30:00.000Z');
  const site = { id: 'site-partial', tenantId: 'tenant-1', displayName: '松雅轩', timezone: 'Asia/Shanghai' };
  const energyBodies: { energyType: string; from: string; to: string }[] = [];

  const result = await loadOverview({
    gateway: fakeTestGateway(async (request) => {
      const path = new URL(request.url).pathname + new URL(request.url).search;
      if (request.method === 'GET') {
        if (path.endsWith('/savings/measures')) return { statusCode: 200, data: { items: [] } };
        if (path.includes('/savings/summary?')) return { statusCode: 200, data: { totals: { days: 0, avoidedKWh: 0, avoidedCost: 0, plantKWh: 0 } } };
        if (path.endsWith('/optimization/recommendations/latest')) return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
        return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
      }
      const body = request.data as { energyType: 'electricity' | 'cooling'; from: string; to: string };
      energyBodies.push(body);
      return { statusCode: 200, data: { points: cubeHourlyQuery(body.energyType, body.from, body.to) } };
    }),
    loadPlant: async () => buildPlant({ ...assetModel, devices: [] }, snapshots, recordedAt),
  }, site, now);

  assert.deepEqual(
    energyBodies.filter((entry) => entry.from === '2026-10-10T16:00:00.000Z' && entry.to !== now.toISOString()),
    [
      { energyType: 'electricity', from: '2026-10-10T16:00:00.000Z', to: '2026-10-11T05:30:00.000Z', granularity: 'hour', qualityPolicy: 'VALID_ONLY', siteId: 'site-partial', tenantId: 'tenant-1', timezone: 'Asia/Shanghai' },
      { energyType: 'cooling', from: '2026-10-10T16:00:00.000Z', to: '2026-10-11T05:30:00.000Z', granularity: 'hour', qualityPolicy: 'VALID_ONLY', siteId: 'site-partial', tenantId: 'tenant-1', timezone: 'Asia/Shanghai' },
    ],
  );

  // Web useTodayAgainstYesterday totals over identical windows:
  // Today: full hour 04:00Z (72 kWh) + half of 05:00Z (36 kWh) = 108 kWh elec, 486 kWh cool.
  // Yesterday up to 05:30Z: 60 + 30 (half hour, not full 60!) = 90 kWh elec, 405 kWh cool.
  const webToday = summarize(
    cubeHourlyQuery('electricity', '2026-10-11T16:00:00.000Z', '2026-10-12T05:30:00.000Z'),
    cubeHourlyQuery('cooling', '2026-10-11T16:00:00.000Z', '2026-10-12T05:30:00.000Z'),
  );
  const webYesterday = summarize(
    cubeHourlyQuery('electricity', '2026-10-10T16:00:00.000Z', '2026-10-11T05:30:00.000Z'),
    cubeHourlyQuery('cooling', '2026-10-10T16:00:00.000Z', '2026-10-11T05:30:00.000Z'),
  );
  assert.equal(webYesterday.electricityKWh, 90);
  assert.equal(webToday.electricityKWh, 108);
  assert.deepEqual(result.inputs.energy, {
    status: 'ok',
    today: webToday,
    change: compareWithYesterday(webToday, webYesterday),
  });
});

test('loadOverview treats 00:02 local time with yesterday readings and empty 00:00-00:02 as non-empty and requests savings/summary', async () => {
  const site = { id: 'site-early', tenantId: 'tenant-1', displayName: '松雅轩', timezone: 'Asia/Shanghai' };
  const now = new Date('2026-10-19T16:02:00.000Z');
  const calls: string[] = [];

  const result = await loadOverview({
    gateway: fakeTestGateway(async (request) => {
      const path = new URL(request.url).pathname + new URL(request.url).search;
      if (request.method === 'GET') {
        calls.push(`GET ${path}`);
        if (path.endsWith('/savings/measures')) {
          return { statusCode: 200, data: { items: [{ id: 'm1', name: '措施', status: 'IMPLEMENTED', baseline: { trusted: true }, totals: { days: 5, avoidedKWh: 500, avoidedCost: 600 } }] } };
        }
        if (path.includes('/savings/summary?')) {
          return { statusCode: 200, data: { totals: { days: 5, avoidedKWh: 500, avoidedCost: 600, plantKWh: 4500 } } };
        }
        if (path.endsWith('/optimization/recommendations/latest')) return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
        return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
      }
      const body = request.data as { energyType: string; from: string; to: string };
      calls.push(`POST ${body.energyType} ${body.from}..${body.to}`);
      if (body.energyType === 'electricity' && body.to === now.toISOString()) {
        return {
          statusCode: 200,
          data: { points: [{ periodStart: '2026-10-19T02:00:00.000Z', periodEnd: '2026-10-19T03:00:00.000Z', energyKWh: 120 }] },
        };
      }
      return { statusCode: 200, data: { points: [] } };
    }),
    loadPlant: async () => buildPlant({ ...assetModel, devices: [] }, snapshots, recordedAt),
  }, site, now);

  assert.equal(result.inputs.energy.status, 'ok');
  assert.ok(calls.includes('GET /api/v1/sites/site-early/savings/summary?from=2026-10-01&to=2026-10-20'));
  assert.equal(result.inputs.savings.status, 'ok');
});

test('loadOverview never masks upstream failures as FIRST_DAY_PENDING when an implemented trusted measure exists', async () => {
  const measure = { id: 'm1', name: '供水优化', status: 'IMPLEMENTED', baseline: { trusted: true }, totals: { days: 10, avoidedKWh: 1200, avoidedCost: 1400 } };
  const shanghaiSite = { id: 'site-sh', tenantId: 'tenant-1', displayName: '松雅轩', timezone: 'Asia/Shanghai' };
  const now = new Date('2026-10-20T06:00:00.000Z');
  const plantEmpty = async () => buildPlant({ ...assetModel, devices: [] }, snapshots, recordedAt);

  // 1. Fixed-offset path: Cube energy-series 503 while mv-service succeeds -> real savings shown.
  const cubeDownMvUp = await loadOverview({
    gateway: fakeTestGateway(async (request) => {
      const path = new URL(request.url).pathname + new URL(request.url).search;
      if (request.method === 'POST') return { statusCode: 503, data: { code: 'UPSTREAM_UNAVAILABLE' } };
      if (path.endsWith('/savings/measures')) return { statusCode: 200, data: { items: [measure] } };
      if (path.includes('/savings/summary?')) return { statusCode: 200, data: { totals: { days: 10, avoidedKWh: 1200, avoidedCost: 1400, plantKWh: 8800 } } };
      if (path.endsWith('/optimization/recommendations/latest')) return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
      return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
    }),
    loadPlant: plantEmpty,
  }, shanghaiSite, now);
  assert.equal(cubeDownMvUp.inputs.energy.status, 'error');
  assert.equal(cubeDownMvUp.inputs.savings.status, 'ok');
  if (cubeDownMvUp.inputs.savings.status === 'ok') {
    assert.deepEqual(cubeDownMvUp.inputs.savings.card.saved, { cost: 1400, kWh: 1200, rate: 0.12 });
  }

  // 2. Fixed-offset path: Cube energy-series 503 AND savings/summary 503 -> savings status is error, never FIRST_DAY_PENDING.
  const bothDown = await loadOverview({
    gateway: fakeTestGateway(async (request) => {
      const path = new URL(request.url).pathname + new URL(request.url).search;
      if (request.method === 'POST' || path.includes('/savings/summary?')) {
        return { statusCode: 503, data: { code: 'UPSTREAM_UNAVAILABLE' } };
      }
      if (path.endsWith('/savings/measures')) return { statusCode: 200, data: { items: [measure] } };
      if (path.endsWith('/optimization/recommendations/latest')) return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
      return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
    }),
    loadPlant: plantEmpty,
  }, shanghaiSite, now);
  assert.equal(bothDown.inputs.energy.status, 'error');
  assert.equal(bothDown.inputs.savings.status, 'error');
  assert.equal(presentOverview(bothDown.inputs).savings.detail, '暂时读不到，下拉刷新再试');

  // 3. Fallback DST path under broken Intl: fetchRecentDays 503 -> savings status is error, never FIRST_DAY_PENDING.
  const nySite = { id: 'site-ny', tenantId: 'tenant-1', displayName: 'NY', timezone: 'America/New_York' };
  await withBrokenIntlTimeZone(async () => {
    const nyFailed = await loadOverview({
      gateway: fakeTestGateway(async (request) => {
        const path = new URL(request.url).pathname + new URL(request.url).search;
        if (request.method === 'POST') return { statusCode: 503, data: { code: 'UPSTREAM_UNAVAILABLE' } };
        if (path.endsWith('/savings/measures')) return { statusCode: 200, data: { items: [measure] } };
        if (path.includes('/savings/summary?')) return { statusCode: 200, data: { totals: { days: 10, avoidedKWh: 1200, avoidedCost: 1400, plantKWh: 8800 } } };
        if (path.endsWith('/optimization/recommendations/latest')) return { statusCode: 404, data: { code: 'RESOURCE_NOT_FOUND' } };
        return { statusCode: 200, data: { data: [], meta: { nextCursor: null, hasMore: false } } };
      }),
      loadPlant: plantEmpty,
    }, nySite, new Date('2026-11-02T12:00:00.000Z'));
    assert.equal(nyFailed.inputs.energy.status, 'error');
    assert.equal(nyFailed.inputs.savings.status, 'error');
    assert.equal(presentOverview(nyFailed.inputs).savings.detail, '暂时读不到，下拉刷新再试');
  });
});
