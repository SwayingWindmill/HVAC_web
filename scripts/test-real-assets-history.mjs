import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_HISTORY_RANGES,
  buildRealAssetsTrendData,
  createRealAssetsHistoryQuery,
  formatRealAssetsHistoryInstant,
  listRealAssetsTrendDefinitions,
  loadRealAssetsHistory,
  numericHistoryObservations,
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
    keys: ['chiller.power', 'chiller.cop'],
    range: '1h',
    timezone: 'Asia/Tokyo',
    routePolicyRevision: 'registry:9|telemetry:2',
    asOf,
    ...overrides,
  });
}

function observation({
  observationId,
  telemetryKey,
  sampledAt,
  value,
  quality = 'GOOD',
  unit = 'kW',
  pointId = pointA,
  sensorId = sensorA,
  pointRevision = 3,
  acceptance = 'ACCEPTED',
}) {
  return {
    observationId,
    telemetryKey,
    pointId,
    sensorId,
    pointType: 'TELEMETRY',
    pointRevision,
    sampledAt,
    receivedAt: new Date(Date.parse(sampledAt) + 1000).toISOString(),
    acceptance,
    valueType: 'NUMBER',
    value,
    unit,
    quality,
    qualityReasons: quality === 'GOOD' ? [] : ['SOURCE_LAG_EXCEEDED'],
    sourcePosition: {
      partition: `mqtt:gateway:device:${telemetryKey}`,
      offset: 42,
      eventId: observationId,
    },
  };
}

function makeResponse(query, overrides = {}) {
  const observations = [
    observation({ observationId: observationC, telemetryKey: 'chiller.cop', sampledAt: '2026-07-31T03:40:00.000Z', value: 4.2, unit: null, pointId: pointC, sensorId: sensorC }),
    observation({ observationId: observationA, telemetryKey: 'chiller.power', sampledAt: '2026-07-31T03:05:00.000Z', value: 0 }),
    observation({ observationId: observationB, telemetryKey: 'chiller.power', sampledAt: '2026-07-31T03:25:00.000Z', value: 18.5, quality: 'PARTIAL', pointId: pointB, sensorId: sensorB, pointRevision: 4, acceptance: 'OUT_OF_ORDER' }),
  ];
  return {
    schemaVersion: 2,
    tenantId,
    siteId,
    deviceId,
    observations,
    metadata: {
      requestedFrom: query.from,
      requestedTo: query.to,
      projectionWatermark: '2026-07-31T03:50:00.000Z',
      pageSize: query.pageSize,
      returnedObservations: observations.length,
      nextCursor: 'cursor-next',
    },
    ...overrides,
  };
}

test('history ranges and profile selection stay fixed and trendEligible-only', () => {
  assert.deepEqual(Object.keys(REAL_ASSETS_HISTORY_RANGES), ['1h', '6h', '24h']);
  assert.equal(REAL_ASSETS_HISTORY_RANGES['1h'].pageSize, 240);
  const profile = resolveRealAssetsProfile('CHILLER');
  assert.deepEqual(listRealAssetsTrendDefinitions(profile).map((definition) => definition.key), [
    'chiller.power',
    'chiller.cop',
    'chiller.cooling_capacity',
  ]);
  assert.deepEqual(listRealAssetsTrendDefinitions(resolveRealAssetsProfile('UNKNOWN_DEVICE')), []);
});

test('history query and cache key isolate protected scope, session, device, keys, range, RAW aggregation, timezone and policy evidence', () => {
  const query = makeQuery();
  assert.equal(query.from, '2026-07-31T03:00:00.000Z');
  assert.equal(query.to, '2026-07-31T04:00:00.000Z');
  assert.equal(query.pageSize, 240);
  const key = realAssetsHistoryQueryKey(query);
  for (const expected of [7, 'session-test-01', tenantId, siteId, deviceId, '1h', 'RAW', 'Asia/Tokyo', 'real-assets-critical-points:v1', 'registry:9|telemetry:2', 240]) {
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
  const loaded = await loadRealAssetsHistory({ client, query, sessionCapability: 'session-capability-test', signal: controller.signal });
  assert.equal(loaded, response);
  assert.deepEqual(requests[0].request, {
    deviceId,
    keys: ['chiller.power', 'chiller.cop'],
    from: query.from,
    to: query.to,
    pageSize: 240,
  });
  assert.equal(requests[0].capability, 'session-capability-test');
  assert.equal(requests[0].signal, controller.signal);
});

test('history response rejects scope, stable-order, identity and count drift', () => {
  const query = makeQuery();
  const response = makeResponse(query);
  assert.equal(validateRealAssetsHistoryResponse(response, query), response);
  assert.throws(() => validateRealAssetsHistoryResponse({ ...response, siteId: '01900000-0099-7000-8000-000000000099' }, query), /resource scope/);
  const invalidIdentity = {
    ...response,
    observations: [{ ...response.observations[0], pointId: 'not-a-point' }, ...response.observations.slice(1)],
  };
  assert.throws(() => validateRealAssetsHistoryResponse(invalidIdentity, query), /Point identity/);
  const reversedSameKey = {
    ...response,
    observations: [response.observations[0], response.observations[2], response.observations[1]],
  };
  assert.throws(() => validateRealAssetsHistoryResponse(reversedSameKey, query), /stable order/);
  assert.throws(() => validateRealAssetsHistoryResponse({ ...response, metadata: { ...response.metadata, returnedObservations: 4 } }, query), /observation count/);
  assert.throws(() => validateRealAssetsHistoryResponse({ ...response, observations: [{ ...response.observations[0], telemetryKey: 'unrequested.key' }, ...response.observations.slice(1)] }, query), /requested keys/);
});

test('trend model preserves valid zero, degraded quality and inserts null gaps without interpolation', () => {
  const query = makeQuery();
  const points = numericHistoryObservations(makeResponse(query), 'chiller.power');
  const data = buildRealAssetsTrendData(points, '1h', query.pageSize);
  assert.equal(data[0].value, 0);
  assert.equal(data[0].quality, 'GOOD');
  assert.equal(data[0].pointId, pointA);
  assert.equal(data[1].value, null);
  assert.equal(data[1].quality, null);
  assert.equal(data[2].value, 18.5);
  assert.equal(data[2].quality, 'PARTIAL');
  assert.equal(data[2].pointId, pointB);
});

test('trend model breaks the line when Point, Sensor or Point revision identity changes even without a time gap', () => {
  const points = [
    observation({ observationId: observationA, telemetryKey: 'chiller.power', sampledAt: '2026-07-31T03:05:00.000Z', value: 10 }),
    observation({ observationId: observationB, telemetryKey: 'chiller.power', sampledAt: '2026-07-31T03:05:10.000Z', value: 11, pointId: pointB, sensorId: sensorB, pointRevision: 4 }),
  ];
  const data = buildRealAssetsTrendData(points, '1h', 240);
  assert.equal(data.length, 3);
  assert.equal(data[0].pointId, pointA);
  assert.equal(data[1].value, null);
  assert.equal(data[1].pointId, null);
  assert.equal(data[2].pointId, pointB);
  assert.equal(data[2].sensorId, sensorB);
});

test('projection cache identity includes real projection watermark and stable-page cursor, not pseudo dataset revision', () => {
  const query = makeQuery();
  const response = makeResponse(query);
  const key = realAssetsHistoryRevisionKey(query, response);
  assert(key.includes('2026-07-31T03:50:00.000Z'));
  assert(key.includes('cursor-next'));
  assert(!key.includes('history-revision-17'));
  const json = JSON.stringify(response);
  for (const forbidden of ['datasetRevision', 'dataWatermark', 'maxPointsPerKey', 'series']) {
    assert(!json.includes(forbidden), `${forbidden} leaked into v2 response`);
  }
});

test('typed non-numeric observations remain valid but do not enter numeric charts', () => {
  const query = makeQuery();
  const response = makeResponse(query);
  response.observations = [
    {
      ...response.observations[0],
      valueType: 'STRING',
      value: 'AUTO',
      pointType: 'STATE',
    },
    ...response.observations.slice(1),
  ];
  response.metadata.returnedObservations = response.observations.length;
  assert.equal(validateRealAssetsHistoryResponse(response, query), response);
  assert.equal(numericHistoryObservations(response, 'chiller.cop').length, 0);
  const invalidJson = {
    ...response,
    observations: [{ ...response.observations[0], valueType: 'JSON', value: 'AUTO' }, ...response.observations.slice(1)],
  };
  assert.throws(() => validateRealAssetsHistoryResponse(invalidJson, query), /JSON value must be an object or array/);
});

test('history time formatting uses the Site IANA timezone instead of browser local time', () => {
  const formatted = formatRealAssetsHistoryInstant('2026-07-31T04:00:00.000Z', 'Asia/Tokyo');
  assert.match(formatted, /13:00:00/);
});
