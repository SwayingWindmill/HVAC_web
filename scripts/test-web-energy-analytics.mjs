import assert from 'node:assert/strict';
import test from 'node:test';
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
} from '../apps/hvac-web/src/real/energy-workspace.ts';

const organizationId = '01900000-0000-7000-8000-000000000001';
const siteAId = '01900000-0001-7000-8000-000000000001';
const siteBId = '01900000-0002-7000-8000-000000000002';
const sessionCapability = '[TEST_CSRF_CAPABILITY]';

function query(overrides = {}) {
  return {
    organizationId,
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
    trustedOrganizationId: organizationId,
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

test('browser-supplied Organization cannot override the authenticated Principal scope', async () => {
  let called = false;
  await assert.rejects(
    queryEnergySeries(query({ organizationId: siteBId }), {
      csrfToken: sessionCapability,
      trustedOrganizationId: organizationId,
      fetchImplementation: async () => {
        called = true;
        throw new Error('must not call');
      },
    }),
    /does not match the authenticated Principal/,
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
      trustedOrganizationId: organizationId,
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
