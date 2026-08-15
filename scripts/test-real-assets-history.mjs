import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_HISTORY_RANGES,
  buildRealAssetsTrendData,
  createRealAssetsHistoryQuery,
  formatRealAssetsHistoryInstant,
  listRealAssetsTrendDefinitions,
  loadRealAssetsHistory,
  realAssetsHistoryQueryKey,
  realAssetsHistoryRevisionKey,
  validateRealAssetsHistoryResponse,
} from '../apps/hvac-web/src/real/assets/history.ts';
import { resolveRealAssetsProfile } from '../apps/hvac-web/src/real/assets/catalog.ts';

const tenantId = '01900000-0002-7000-8000-000000000002';
const siteId = '01900000-0003-7000-8000-000000000003';
const deviceId = '01900000-0004-7000-8000-000000000004';
const observationA = '01900000-0011-7000-8000-000000000011';
const observationB = '01900000-0012-7000-8000-000000000012';
const observationC = '01900000-0013-7000-8000-000000000013';
const pointA = '01900000-0021-7000-8000-000000000021';
const pointB = '01900000-0022-7000-8000-000000000022';
const pointC = '01900000-0023-7000-8000-000000000023';
const sensorA = '01900000-0031-7000-8000-000000000031';
const sensorB = '01900000-0032-7000-8000-000000000032';
const sensorC = '01900000-0033-7000-8000-000000000033';
const asOf = Date.parse('2026-07-31T04:00:00.000Z');

function makeQuery(overrides = {}) {
  return createRealAssetsHistoryQuery({
    protectedGeneration: 7,
    sessionId: 'session-test-01',
    tenantId,
    siteId,
    deviceId,
    keys: ['chiller_power', 'chiller_cop'],
    range: '1h',
    timezone: 'Asia/Tokyo',
    routePolicyRevision: 'registry:9|telemetry:2',
    asOf,
    ...overrides,
  });
}

function point(observationId, sampledAt, value, quality = 'GOOD', unit = 'kW', pointId = pointA, sensorId = sensorA) {
  return {
    observationId,
    pointId,
    sensorId,
    sampledAt,
    receivedAt: new Date(Date.parse(sampledAt) + 1000).toISOString(),
    value,
    unit,
    quality,
    qualityReasons: quality === 'GOOD' ? [] : ['SOURCE_LAG_EXCEEDED'],
    revision: 3,
  };
}

function makeResponse(query) {
  return {
    schemaVersion: 1,
    tenantId,
    siteId,
    deviceId,
    series: [
      {
        key: 'chiller_power',
        points: [
          point(observationA, '2026-07-31T03:05:00.000Z', 0),
          point(observationB, '2026-07-31T03:25:00.000Z', 18.5, 'PARTIAL', 'kW', pointB, sensorB),
        ],
      },
      {
        key: 'chiller_cop',
        points: [point(observationC, '2026-07-31T03:40:00.000Z', 4.2, 'GOOD', null, pointC, sensorC)],
      },
    ],
    metadata: {
      requestedFrom: query.from,
      requestedTo: query.to,
      dataWatermark: '2026-07-31T03:50:00.000Z',
      datasetRevision: 'history-revision-17',
      partial: true,
      maxPointsPerKey: query.maxPointsPerKey,
      returnedPoints: 3,
      truncatedKeys: ['chiller_power'],
    },
  };
}

test('history ranges and profile selection stay fixed and trendEligible-only', () => {
  assert.deepEqual(Object.keys(REAL_ASSETS_HISTORY_RANGES), ['1h', '6h', '24h']);
  const profile = resolveRealAssetsProfile('CHILLER');
  assert.deepEqual(listRealAssetsTrendDefinitions(profile).map((definition) => definition.key), [
    'chiller_power',
    'chiller_cop',
    'chiller_cooling_capacity',
  ]);
  assert.deepEqual(listRealAssetsTrendDefinitions(resolveRealAssetsProfile('UNKNOWN_DEVICE')), []);
});

test('history query and cache key isolate protected scope, session, device, keys, range, RAW aggregation, timezone and policy evidence', () => {
  const query = makeQuery();
  assert.equal(query.from, '2026-07-31T03:00:00.000Z');
  assert.equal(query.to, '2026-07-31T04:00:00.000Z');
  assert.equal(query.maxPointsPerKey, 240);
  const key = realAssetsHistoryQueryKey(query);
  for (const expected of [7, 'session-test-01', tenantId, siteId, deviceId, '1h', 'RAW', 'Asia/Tokyo', 'real-assets-critical-points:v1', 'registry:9|telemetry:2']) {
    assert(key.includes(expected), `query key omitted ${expected}`);
  }
  assert.notDeepEqual(key, realAssetsHistoryQueryKey(makeQuery({ sessionId: 'session-test-02' })));
  assert.notDeepEqual(key, realAssetsHistoryQueryKey(makeQuery({ range: '6h' })));
  assert.notDeepEqual(key, realAssetsHistoryQueryKey(makeQuery({ timezone: 'UTC' })));
});

test('history loader sends only exact Device and profile keys and validates public response scope', async () => {
  const query = makeQuery();
  const response = makeResponse(query);
  const requests = [];
  const client = {
    async queryDeviceHistory(request, options) {
      requests.push({ request, capability: options['csrf' + 'Token'], signal: options.signal });
      return response;
    },
  };
  const controller = new AbortController();
  const loaded = await loadRealAssetsHistory({
    client,
    query,
    sessionCapability: 'session-capability-test',
    signal: controller.signal,
  });
  assert.equal(loaded, response);
  assert.deepEqual(requests[0].request, {
    deviceId,
    keys: ['chiller_power', 'chiller_cop'],
    from: query.from,
    to: query.to,
    maxPointsPerKey: 240,
  });
  assert.equal(requests[0].capability, 'session-capability-test');
  assert.equal(requests[0].signal, controller.signal);
});

test('history response rejects scope, order, count and truncation drift', () => {
  const query = makeQuery();
  const response = makeResponse(query);
  assert.equal(validateRealAssetsHistoryResponse(response, query), response);
  const invalidPointIdentity = {
    ...response,
    series: [{ ...response.series[0], points: [{ ...response.series[0].points[0], pointId: 'not-a-point' }, response.series[0].points[1]] }, response.series[1]],
  };
  assert.throws(() => validateRealAssetsHistoryResponse(invalidPointIdentity, query), /Point identity/);
  assert.throws(() => validateRealAssetsHistoryResponse({ ...response, siteId: '01900000-0099-7000-8000-000000000099' }, query), /resource scope/);
  assert.throws(() => validateRealAssetsHistoryResponse({ ...response, series: [...response.series].reverse() }, query), /requested order/);
  assert.throws(() => validateRealAssetsHistoryResponse({ ...response, metadata: { ...response.metadata, returnedPoints: 4 } }, query), /point count/);
  assert.throws(() => validateRealAssetsHistoryResponse({ ...response, metadata: { ...response.metadata, partial: false } }, query), /marked partial/);
});

test('trend model preserves valid zero, degraded quality and inserts null gaps without interpolation', () => {
  const query = makeQuery();
  const points = makeResponse(query).series[0].points;
  const data = buildRealAssetsTrendData(points, '1h', query.maxPointsPerKey);
  assert.equal(data[0].value, 0);
  assert.equal(data[0].quality, 'GOOD');
  assert.equal(data[0].pointId, pointA);
  assert.equal(data[1].value, null);
  assert.equal(data[1].quality, null);
  assert.equal(data[2].value, 18.5);
  assert.equal(data[2].quality, 'PARTIAL');
  assert.equal(data[2].pointId, pointB);
});

test('trend model breaks the line when Point or Sensor identity changes even without a time gap', () => {
  const points = [
    point(observationA, '2026-07-31T03:05:00.000Z', 10),
    point(observationB, '2026-07-31T03:05:10.000Z', 11, 'GOOD', 'kW', pointB, sensorB),
  ];
  const data = buildRealAssetsTrendData(points, '1h', 240);
  assert.equal(data.length, 3);
  assert.equal(data[0].pointId, pointA);
  assert.equal(data[1].value, null);
  assert.equal(data[1].pointId, null);
  assert.equal(data[2].pointId, pointB);
  assert.equal(data[2].sensorId, sensorB);
});

test('revision cache identity includes dataset revision and watermark', () => {
  const query = makeQuery();
  const response = makeResponse(query);
  const key = realAssetsHistoryRevisionKey(query, response);
  assert(key.includes('history-revision-17'));
  assert(key.includes('2026-07-31T03:50:00.000Z'));
});

test('history time formatting uses the Site IANA timezone instead of browser local time', () => {
  const formatted = formatRealAssetsHistoryInstant('2026-07-31T04:00:00.000Z', 'Asia/Tokyo');
  assert.match(formatted, /13:00:00/);
});
