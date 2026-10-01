import assert from 'node:assert/strict';
import test from 'node:test';
import { createOverviewExample } from '../apps/hvac-web/src/features/overview/api/overview-example.ts';
import { buildOverviewCsv } from '../apps/hvac-web/src/features/overview/api/overview-export.ts';
import { createConsumptionExample } from '../apps/hvac-web/src/features/energy-analysis/consumption/api/consumption-example.ts';
import { MOCK_OPPORTUNITIES } from '../apps/hvac-web/src/features/opportunities/api/opportunity-service.ts';
import { MOCK_PROJECTS } from '../apps/hvac-web/src/features/projects/api/project-service.ts';
import { MOCK_MV_PROJECTS } from '../apps/hvac-web/src/features/verification/api/verification-service.ts';

test('saving workflow fixtures link existing owners and measure after commissioning in the planned boundary and period', () => {
  for (const project of MOCK_PROJECTS) {
    assert.ok(MOCK_OPPORTUNITIES.find(item => item.code === project.sourceOpportunityCode), project.title);
  }
  for (const record of MOCK_MV_PROJECTS) {
    const project = MOCK_PROJECTS.find(item => item.code === record.sourceProjectCode);
    assert.ok(project, record.projectName);
    assert.equal(record.measurementBoundary, project.mvPlan.measurementBoundary);
    assert.equal(record.reportingPeriod, project.mvPlan.reportingPeriod);
    assert.equal(record.ipmvpOption, project.mvPlan.ipmvpOption);
    const firstMonth = record.monthlyRecords[0].month + '-01';
    assert.ok(project.milestones.slice(0, -1).every(item => item.status === 'DONE' && item.targetDate < firstMonth), project.title + ' must be commissioned before measurement');
    assert.equal(project.stage === 'COMPLETED', record.status === 'CERTIFIED');
    if (record.status === 'CERTIFIED') {
      const reportEnd = record.reportingPeriod.match(/\d{4}-\d{2}-\d{2}/g)[1];
      assert.ok(record.lastAuditDate >= reportEnd, 'review cannot certify future measurement data');
    }
    assert.equal(record.verifiedSavingsKWh, record.monthlyRecords.reduce((sum, month) => sum + month.adjustedBaselineKWh - month.rawActualKWh, 0));
  }
});

test('consumption periods and scopes change facts while energy and costs reconcile across daily, tariff and subsystem views', () => {
  const month = createConsumptionExample('current-month', 'site:site-01');
  const lastMonth = createConsumptionExample('last-month', 'site:site-01');
  const otherSite = createConsumptionExample('current-month', 'site:site-02');
  assert.notEqual(month.summary.totalEnergyKWh, lastMonth.summary.totalEnergyKWh);
  assert.notEqual(month.summary.totalEnergyKWh, otherSite.summary.totalEnergyKWh);
  assert.equal(lastMonth.dailyRecords.length, 31);
  assert.equal(month.summary.totalEnergyKWh, month.dailyRecords.reduce((sum, item) => sum + item.totalKWh, 0));
  assert.equal(month.summary.totalEnergyKWh, month.touBreakdown.reduce((sum, item) => sum + item.energyKWh, 0));
  assert.ok(Math.abs(month.summary.totalCostCNY - month.touBreakdown.reduce((sum, item) => sum + item.costCNY, 0)) < 0.01);
  assert.ok(Math.abs(month.summary.totalCostCNY - month.subsystemCosts.reduce((sum, item) => sum + item.costCNY, 0)) < 0.01);
});

test('overview example periods change facts and keep aggregate energy consistent with the plotted buckets', () => {
  const month = createOverviewExample('site:site-01', '示例站点', 'month');
  const day = createOverviewExample('site:site-01', '示例站点', 'today');
  const otherSite = createOverviewExample('site:site-02', '另一站点', 'month');
  assert.equal(month.actualKWh, month.trendSeries.reduce((sum, point) => sum + point.actual, 0));
  assert.equal(month.baselineKWh, month.trendSeries.reduce((sum, point) => sum + point.baseline, 0));
  assert.equal(month.savingsKWh, month.baselineKWh - month.actualKWh);
  assert.equal(month.savingsRate, month.savingsKWh / month.baselineKWh * 100);
  assert.notEqual(month.actualKWh, day.actualKWh);
  assert.notEqual(month.actualKWh, otherSite.actualKWh);
  assert.equal(month.mode, 'example');
});

test('overview export identifies example/unverified results, preserves missing facts and neutralizes spreadsheet formulas', () => {
  const sample = createOverviewExample('site:site-01', '=HYPERLINK("https://example.invalid")', 'month');
  const csv = buildOverviewCsv({ ...sample, savingsCny: null });
  assert.match(csv, /示例数据 · 非核证报告/);
  assert.match(csv, /待验证/);
  assert.match(csv, /"估算节约费用\(元\)","未提供"/);
  assert.match(csv, /"'=HYPERLINK\(""https:\/\/example\.invalid""\)"/);
});
import {
  EnergyAnalyticsInvalidResponseError,
  EnergyAnalyticsRequestError,
  buildEnergyTrendData,
  classifyEnergyAnalyticsFailure,
  energySeriesQueryKey,
  energyTotal,
  queryEnergySeries,
} from '../apps/hvac-web/src/api/energy-analytics.ts';
import {
  buildEnergyWorkspaceWindow,
  compareEnergyTotals,
  currentEnergyWorkspaceState,
  drillDownEnergyWorkspaceState,
  energyWorkspaceSearch,
  parseEnergyWorkspaceSearch,
  shiftEnergyWorkspaceState,
} from '../apps/hvac-web/src/features/energy/workspace.ts';
import {
  buildCumulativeEnergy,
  buildEnergyCsv,
  buildMonthCalendar,
  buildWeekSlots,
  buildYearSlots,
  summarizeEnergyPoints,
} from '../apps/hvac-web/src/features/energy/presentation.ts';

const tenantId = '01900000-0000-7000-8000-000000000001';
const siteAId = '01900000-0001-7000-8000-000000000001';
const siteBId = '01900000-0002-7000-8000-000000000002';
const sessionCapability = '[TEST_CSRF_CAPABILITY]';

function query(overrides = {}) {
  return {
    tenantId,
    siteId: siteAId,
    energyType: 'electricity',
    granularity: 'hour',
    timezone: 'Asia/Tokyo',
    from: '2026-07-29T00:00:00.000Z',
    to: '2026-07-30T00:00:00.000Z',
    qualityPolicy: 'VALID_ONLY',
    ...overrides,
  };
}

function responseBody(overrides = {}) {
  return {
    schemaVersion: 1,
    points: [{
      periodStart: '2026-07-29T00:00:00.000Z',
      periodEnd: '2026-07-29T01:00:00.000Z',
      energyKWh: 12.5,
    }],
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      dataWatermark: '2026-07-30T00:00:00.000Z',
      aggregateWatermark: '2026-07-30T00:00:00.000Z',
      datasetRevision: 'energy-revision-7',
      partial: false,
      qualitySummary: { valid: 1, suspect: 0, invalid: 0 },
    },
    ...overrides,
  };
}

test('energy request uses same-origin BFF session, CSRF capability, and public Gateway path', async () => {
  const controller = new AbortController();
  let observed;
  const result = await queryEnergySeries(query(), {
    csrfToken: sessionCapability,
    trustedTenantId: tenantId,
    signal: controller.signal,
    fetchImplementation: async (url, init) => {
      observed = { url, init };
      return new Response(JSON.stringify(responseBody()), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    },
  });

  assert.equal(result.metadata.datasetRevision, 'energy-revision-7');
  assert.equal(observed.url, '/api/v1/analytics/energy-series');
  assert.equal(observed.init.method, 'POST');
  assert.equal(observed.init.credentials, 'same-origin');
  assert.equal(observed.init.signal, controller.signal);
  assert.equal(observed.init.headers['X-CSRF-Token'], sessionCapability);
  assert.deepEqual(JSON.parse(observed.init.body), query());
});

test('browser-supplied Tenant cannot override the trusted Site context', async () => {
  let called = false;
  await assert.rejects(
    queryEnergySeries(query({ tenantId: siteBId }), {
      csrfToken: sessionCapability,
      trustedTenantId: tenantId,
      fetchImplementation: async () => {
        called = true;
        throw new Error('must not call');
      },
    }),
    /does not match the trusted Site context/,
  );
  assert.equal(called, false);
});

test('query keys isolate Site, quality policy, timezone, and time range', () => {
  const base = energySeriesQueryKey(query());
  assert.notDeepEqual(base, energySeriesQueryKey(query({ siteId: siteBId })));
  assert.notDeepEqual(base, energySeriesQueryKey(query({ qualityPolicy: 'VALID_AND_SUSPECT' })));
  assert.notDeepEqual(base, energySeriesQueryKey(query({ timezone: 'Asia/Shanghai' })));
  assert.notDeepEqual(base, energySeriesQueryKey(query({ from: '2026-07-28T00:00:00.000Z' })));
});

test('trend preserves explicit zero measurements and inserts null for missing buckets', () => {
  const points = [
    { periodStart: '2026-07-29T00:00:00.000Z', periodEnd: '2026-07-29T01:00:00.000Z', energyKWh: 0 },
    { periodStart: '2026-07-29T03:00:00.000Z', periodEnd: '2026-07-29T04:00:00.000Z', energyKWh: 8 },
  ];
  const data = buildEnergyTrendData(points, 'hour');
  assert.deepEqual(data, [
    [Date.parse('2026-07-29T00:00:00.000Z'), 0],
    [Date.parse('2026-07-29T01:00:00.000Z'), null],
    [Date.parse('2026-07-29T03:00:00.000Z'), 8],
  ]);
  assert.equal(energyTotal([]), null);
  assert.equal(energyTotal(points.slice(0, 1)), 0);
});

test('invalid success envelopes fail closed', async () => {
  await assert.rejects(
    queryEnergySeries(query(), {
      csrfToken: sessionCapability,
      trustedTenantId: tenantId,
      fetchImplementation: async () => new Response(JSON.stringify({ schemaVersion: 1, points: [] }), { status: 200 }),
    }),
    EnergyAnalyticsInvalidResponseError,
  );
});

test('public error statuses map to product-safe UI states', () => {
  const problem = (status, retryable = false) => new EnergyAnalyticsRequestError({
    type: 'about:blank',
    title: 'request failed',
    status,
    code: `STATUS_${status}`,
    detail: 'public detail',
    retryable,
    traceId: 'trace-fixture',
  });

  assert.equal(classifyEnergyAnalyticsFailure(problem(401)).kind, 'unauthorized');
  assert.equal(classifyEnergyAnalyticsFailure(problem(403)).kind, 'forbidden');
  assert.equal(classifyEnergyAnalyticsFailure(problem(422)).kind, 'invalid-query');
  for (const status of [502, 503, 504]) {
    const failure = classifyEnergyAnalyticsFailure(problem(status, true));
    assert.equal(failure.kind, 'upstream');
    assert.equal(failure.retryable, true);
    assert.equal(failure.detail.includes('ClickHouse'), false);
    assert.equal(failure.detail.includes('Cube'), false);
  }
});

test('aborted stale requests have a distinct non-retryable state', () => {
  const failure = classifyEnergyAnalyticsFailure(new DOMException('aborted', 'AbortError'));
  assert.equal(failure.kind, 'aborted');
  assert.equal(failure.retryable, false);
});

test('Energy workspace search state is canonical, reproducible, and fail-closed', () => {
  const parsed = parseEnergyWorkspaceSearch(
    '?period=week&anchor=2026-07-31&quality=VALID_AND_SUSPECT',
    'Asia/Tokyo',
  );
  assert.deepEqual(parsed, {
    period: 'week',
    anchor: '2026-07-27',
    qualityPolicy: 'VALID_AND_SUSPECT',
  });
  assert.equal(
    energyWorkspaceSearch(parsed),
    '?period=week&anchor=2026-07-27&quality=VALID_AND_SUSPECT',
  );

  assert.deepEqual(
    parseEnergyWorkspaceSearch('?period=quarter&anchor=not-a-date&quality=ALL', 'Asia/Tokyo', new Date('2026-07-31T07:00:00.000Z')),
    { period: 'month', anchor: '2026-07-01', qualityPolicy: 'VALID_ONLY' },
  );
});

test('calendar windows use Site timezone and preserve DST-length days', () => {
  const tokyoMonth = buildEnergyWorkspaceWindow({
    period: 'month',
    anchor: '2026-07-15',
    qualityPolicy: 'VALID_ONLY',
  }, 'Asia/Tokyo');
  assert.equal(tokyoMonth.state.anchor, '2026-07-01');
  assert.equal(tokyoMonth.from, '2026-06-30T15:00:00.000Z');
  assert.equal(tokyoMonth.to, '2026-07-31T15:00:00.000Z');
  assert.equal(tokyoMonth.previousFrom, '2026-05-31T15:00:00.000Z');
  assert.equal(tokyoMonth.granularity, 'day');

  const newYorkDstDay = buildEnergyWorkspaceWindow({
    period: 'day',
    anchor: '2026-03-08',
    qualityPolicy: 'VALID_ONLY',
  }, 'America/New_York');
  assert.equal(newYorkDstDay.from, '2026-03-08T05:00:00.000Z');
  assert.equal(newYorkDstDay.to, '2026-03-09T04:00:00.000Z');
  assert.equal(Date.parse(newYorkDstDay.to) - Date.parse(newYorkDstDay.from), 23 * 60 * 60 * 1000);
});

test('period navigation and drill-down retain quality policy', () => {
  const month = { period: 'month', anchor: '2026-07-01', qualityPolicy: 'VALID_AND_SUSPECT' };
  assert.deepEqual(
    shiftEnergyWorkspaceState(month, -1, 'Asia/Tokyo'),
    { period: 'month', anchor: '2026-06-01', qualityPolicy: 'VALID_AND_SUSPECT' },
  );
  assert.deepEqual(
    shiftEnergyWorkspaceState(month, 1, 'Asia/Tokyo'),
    { period: 'month', anchor: '2026-08-01', qualityPolicy: 'VALID_AND_SUSPECT' },
  );
  assert.deepEqual(
    drillDownEnergyWorkspaceState(month, '2026-07-14T15:00:00.000Z', 'Asia/Tokyo'),
    { period: 'day', anchor: '2026-07-15', qualityPolicy: 'VALID_AND_SUSPECT' },
  );
  assert.equal(
    drillDownEnergyWorkspaceState({ ...month, period: 'day', anchor: '2026-07-15' }, '2026-07-14T15:00:00.000Z', 'Asia/Tokyo'),
    null,
  );
  assert.deepEqual(
    currentEnergyWorkspaceState('year', 'VALID_ONLY', 'Asia/Tokyo', new Date('2026-07-31T07:00:00.000Z')),
    { period: 'year', anchor: '2026-01-01', qualityPolicy: 'VALID_ONLY' },
  );
});

test('period comparison keeps missing and zero baselines explicit', () => {
  assert.deepEqual(compareEnergyTotals(null, 10), { kind: 'unavailable' });
  assert.deepEqual(compareEnergyTotals(10, null), { kind: 'unavailable' });
  assert.deepEqual(compareEnergyTotals(10, 0), { kind: 'baseline-zero', differenceKWh: 10 });
  assert.deepEqual(compareEnergyTotals(75, 100), {
    kind: 'percentage',
    differenceKWh: -25,
    percentage: -25,
  });
});

test('real Energy presentation derives summary and cumulative values only from returned buckets', () => {
  const points = [
    { periodStart: '2026-07-01T02:00:00.000Z', periodEnd: '2026-07-01T03:00:00.000Z', energyKWh: 30 },
    { periodStart: '2026-07-01T00:00:00.000Z', periodEnd: '2026-07-01T01:00:00.000Z', energyKWh: 10 },
    { periodStart: '2026-07-01T01:00:00.000Z', periodEnd: '2026-07-01T02:00:00.000Z', energyKWh: 0 },
  ];
  const summary = summarizeEnergyPoints(points);
  assert.equal(summary.total, 40);
  assert.equal(summary.average, 40 / 3);
  assert.equal(summary.peak.energyKWh, 30);
  assert.equal(summary.valley.energyKWh, 0);
  assert.deepEqual(buildCumulativeEnergy(points), [
    [Date.parse('2026-07-01T00:00:00.000Z'), 10],
    [Date.parse('2026-07-01T01:00:00.000Z'), 10],
    [Date.parse('2026-07-01T02:00:00.000Z'), 40],
  ]);
});

test('real Energy week, month, and year views keep unavailable calendar slots empty', () => {
  const dayPoints = [
    { periodStart: '2026-07-26T15:00:00.000Z', periodEnd: '2026-07-27T15:00:00.000Z', energyKWh: 100 },
    { periodStart: '2026-07-28T15:00:00.000Z', periodEnd: '2026-07-29T15:00:00.000Z', energyKWh: 140 },
  ];
  const previousDayPoints = [
    { periodStart: '2026-07-19T15:00:00.000Z', periodEnd: '2026-07-20T15:00:00.000Z', energyKWh: 90 },
  ];
  const week = buildWeekSlots('2026-07-27', dayPoints, previousDayPoints, 'Asia/Tokyo');
  assert.equal(week.length, 7);
  assert.equal(week[0].point.energyKWh, 100);
  assert.equal(week[1].point, null);
  assert.equal(week[2].point.energyKWh, 140);
  assert.equal(week[0].previousPoint.energyKWh, 90);

  const calendar = buildMonthCalendar('2026-07-01', dayPoints, previousDayPoints, 'Asia/Tokyo');
  assert.equal(calendar.length, 42);
  assert.equal(calendar[2].date, '2026-07-01');
  assert.equal(calendar[2].point, null);
  const july27 = calendar.find((cell) => cell.date === '2026-07-27');
  assert.equal(july27.point.energyKWh, 100);

  const months = buildYearSlots('2026-01-01', [
    { periodStart: '2026-06-30T15:00:00.000Z', periodEnd: '2026-07-31T15:00:00.000Z', energyKWh: 3100 },
  ], [], 'Asia/Tokyo');
  assert.equal(months.length, 12);
  assert.equal(months[6].point.energyKWh, 3100);
  assert.equal(months[0].point, null);
});

test('real Energy CSV export contains only current and comparison API buckets', () => {
  const current = [{ periodStart: '2026-07-01T00:00:00.000Z', periodEnd: '2026-07-01T01:00:00.000Z', energyKWh: 12.5 }];
  const previous = [{ periodStart: '2026-06-30T00:00:00.000Z', periodEnd: '2026-06-30T01:00:00.000Z', energyKWh: 10 }];
  const csv = buildEnergyCsv('当前周期', current, '比较基期', previous);
  assert.equal(csv.split('\n').length, 3);
  assert.match(csv, /当前周期,2026-07-01T00:00:00.000Z,2026-07-01T01:00:00.000Z,12.5/);
  assert.match(csv, /比较基期,2026-06-30T00:00:00.000Z,2026-06-30T01:00:00.000Z,10/);
  assert.equal(csv.includes('tariff'), false);
  assert.equal(csv.includes('carbon'), false);
});
