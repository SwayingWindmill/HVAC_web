import assert from 'node:assert/strict';
import test from 'node:test';

import { OwnerReadError } from '../dist/application/index.js';
import {
  createAlarmOwnerReader,
  createDeviceTelemetryOwnerReader,
  createEnergyAnalyticsOwnerReader,
  createGatewayToolAuthorizationReader,
  createRegistryOwnerReader,
  createTelemetryHistoryOwnerReader,
} from '../dist/tools/index.js';

const tenantId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708192';
const siteId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708193';
const assetId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708194';
const deviceId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708195';

const scope = Object.freeze({
  tenantId,
  siteId,
  assetId: null,
  deviceId: null,
});

const context = Object.freeze({
  sessionId: 'session-owner-read-001',
  runId: 'run-owner-read-001',
  scope,
  correlationId: 'request-owner-read-001',
  authorization: {
    decision: 'ALLOW',
    decisionId: 'decision-owner-read-001',
    delegationGrant: 'fallback-delegation-grant-value',
    toolDelegationGrants: {
      'registry.getSite': 'registry-site-grant-value',
      'registry.listSiteAssets': 'registry-asset-grant-value',
      'registry.getSiteAssetModel': 'registry-asset-model-grant-value',
    },
    policyRevision: 'registry-policy-42',
    traceparent: '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01',
  },
});

const energyContext = Object.freeze({
  ...context,
  authorization: {
    decision: 'ALLOW',
    decisionId: 'decision-owner-read-energy',
    delegationGrant: 'fallback-energy-grant-value',
    toolDelegationGrants: {
      'analytics.getEnergySeries': 'energy-delegation-grant-value',
    },
    traceparent: context.authorization.traceparent,
  },
});

const telemetryContext = Object.freeze({
  ...context,
  scope: { ...scope, deviceId },
  authorization: {
    decision: 'ALLOW',
    decisionId: 'decision-owner-read-telemetry',
    delegationGrant: 'fallback-telemetry-grant-value',
    toolDelegationGrants: {
      'telemetry.current.getDeviceObservationSnapshot': 'telemetry-delegation-grant-value',
    },
    policyRevision: 'telemetry-access:1',
    traceparent: context.authorization.traceparent,
  },
});

const siteDto = Object.freeze({
  id: siteId,
  tenantId: tenantId,
  code: 'TOKYO-HQ',
  displayName: 'Tokyo Headquarters',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 17,
  createdAt: '2026-07-01T00:00:00.000Z',
  updatedAt: '2026-07-30T00:00:00.000Z',
  floorAreaM2: 4200,
  climateZone: null,
});

const assetDto = Object.freeze({
  id: assetId,
  tenantId: tenantId,
  siteId,
  code: 'CH-01',
  displayName: 'Chiller 01',
  assetType: 'CHILLER',
  status: 'ACTIVE',
  revision: 29,
  createdAt: '2026-07-01T00:00:00.000Z',
  updatedAt: '2026-07-30T00:00:00.000Z',
});

const energyRequest = Object.freeze({
  requestId: 'read-energy-series-001',
  tool: 'analytics.getEnergySeries',
  input: {
    tenantId,
    siteId,
    energyType: 'electricity',
    granularity: 'hour',
    timezone: 'Asia/Tokyo',
    from: '2026-07-28T15:00:00.000Z',
    to: '2026-07-29T15:00:00.000Z',
    qualityPolicy: 'VALID_AND_SUSPECT',
  },
});

const energyResponse = Object.freeze({
  schemaVersion: 1,
  points: [{
    periodStart: '2026-07-28T15:00:00.000Z',
    periodEnd: '2026-07-28T16:00:00.000Z',
    energyKWh: 128.5,
  }],
  metadata: {
    requestedGranularity: 'hour',
    actualGranularity: 'hour',
    dataWatermark: '2026-07-29T15:00:00.000Z',
    aggregateWatermark: '2026-07-29T15:00:00.000Z',
    datasetRevision: 'energy-interval:v1:1842',
    partial: false,
    qualitySummary: { valid: 24, suspect: 1, invalid: 0 },
  },
});

const telemetryRequest = Object.freeze({
  requestId: 'read-telemetry-current-001',
  tool: 'telemetry.current.getDeviceObservationSnapshot',
  input: {
    siteId,
    deviceId,
    pointKeys: ['condenserPressureKPa', 'leavingWaterTempC'],
  },
});

const telemetryResponse = Object.freeze({
  schemaVersion: 1,
  deviceId,
  tenantId,
  siteId,
  businessRevision: 73,
  evaluatedAt: '2026-07-30T06:55:00.000Z',
  evaluationAvailability: 'AVAILABLE',
  availabilityReasons: [],
  presence: {
    applicability: 'APPLICABLE',
    currentState: 'ONLINE',
    lastSeenAt: '2026-07-30T06:20:00.000Z',
    policyRevision: 3,
    lastKnown: null,
  },
  telemetryReadiness: 'DEGRADED',
  displayState: 'STALE',
  values: [
    {
      key: 'condenserPressureKPa',
      state: 'PRESENT',
      value: 1280,
      valueType: 'NUMBER',
      unit: 'kPa',
      sampledAt: '2026-07-30T06:20:00.000Z',
      receivedAt: '2026-07-30T06:20:01.000Z',
      freshness: 'STALE',
      quality: 'GOOD',
      qualityReasons: [],
      policyRevision: 3,
    },
    {
      key: 'leavingWaterTempC',
      state: 'MISSING',
      freshness: 'MISSING',
      missingReason: 'NEVER_OBSERVED',
      policyRevision: null,
    },
  ],
});

const jsonResponse = (value, init = {}) => new Response(JSON.stringify(value), {
  status: init.status ?? 200,
  headers: { 'Content-Type': 'application/json', ...(init.headers ?? {}) },
});

const expectOwnerError = async (run, code) => {
  await assert.rejects(run, (error) => (
    error instanceof OwnerReadError && error.code === code
  ));
};

test('Registry reader exposes only Site and Site Asset reads with authoritative metadata', async () => {
  const calls = [];
  const reader = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async (url, init) => {
      calls.push({ url: String(url), init });
      if (String(url).endsWith(`/sites/${siteId}`)) return jsonResponse(siteDto);
      if (String(url).includes(`/sites/${siteId}/asset`)) {
        return jsonResponse({ items: [assetDto], nextCursor: null, hasMore: false });
      }
      throw new Error('unexpected Registry route');
    },
  });

  const site = await reader.read({
    request: { requestId: 'read-site-001', tool: 'registry.getSite', input: { siteId } },
    context,
  });
  const asset = await reader.read({
    request: {
      requestId: 'read-site-asset-001',
      tool: 'registry.listSiteAssets',
      input: { siteId },
    },
    context,
  });

  assert.equal(site.owner, 'registry');
  assert.deepEqual(site.scope, scope);
  assert.equal(site.revision, 'registry-site:17');
  assert.equal(site.provenance, 'platform-core-service:registry-site/v1');
  assert.deepEqual(site.payload, { kind: 'SITE', site: siteDto });
  assert.match(asset.revision, /^registry-site-assets:sha256:[0-9a-f]{64}$/);
  assert.deepEqual(asset.payload, {
    kind: 'SITE_ASSETS',
    siteId,
    assets: [assetDto],
  });

  assert.equal(calls.length, 2);
  assert.equal(calls[0].init.headers['X-Delegation-Grant'], 'registry-site-grant-value');
  assert.equal(calls[1].init.headers['X-Delegation-Grant'], 'registry-asset-grant-value');
  for (const call of calls) {
    assert.equal(call.init.method, 'GET');
    assert.equal(call.init.headers['X-Route-Policy-Revision'], 'registry-policy-42');
    assert.equal(call.init.headers['X-Request-ID'].startsWith('read-'), true);
    assert.equal(call.init.headers.traceparent, context.authorization.traceparent);
  }
});

const assetModelDto = Object.freeze({
  schemaVersion: 2,
  tenantId,
  siteId,
  spaces: [],
  assets: [assetDto],
  devices: [{ id: deviceId, tenantId, siteId, code: 'meter-c', displayName: 'C 机组电表', deviceType: 'HVAC_POWER_METER', status: 'ACTIVE', revision: 4, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' }],
  sensors: [],
  telemetryPoints: [{ id: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708201', tenantId, siteId, reportingDeviceId: deviceId, sensorId: null, pointCode: 'power_total', sourceKey: 'hvac_meter.active_power', displayName: '有功功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW', writable: false, sampleIntervalMs: 60000, publishIntervalMs: 60000, staleAfterMs: 300000, counterDecreaseMode: null, counterRolloverModulus: null, sourceMetadata: {}, status: 'ACTIVE', revision: 2, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' }],
  relationships: [],
  counts: { spaces: 0, assets: 1, deviceEndpoints: 1, physicalSensors: 0, points: 1 },
});

test('Registry reader reads the Site asset model from its owner route with its own grant', async () => {
  const calls = [];
  const reader = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async (url, init) => {
      calls.push({ url: String(url), init });
      return jsonResponse(assetModelDto);
    },
  });

  const result = await reader.read({
    request: { requestId: 'read-asset-model-001', tool: 'registry.getSiteAssetModel', input: { siteId } },
    context,
  });

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, `https://platform-core.internal/internal/v1/registry/sites/${siteId}/asset-model`);
  assert.equal(calls[0].init.method, 'GET');
  assert.equal(calls[0].init.headers['X-Delegation-Grant'], 'registry-asset-model-grant-value');
  assert.equal(result.owner, 'registry');
  assert.match(result.revision, /^registry-asset-model:sha256:[0-9a-f]{64}$/);
  assert.equal(result.provenance, 'platform-core-service:registry-asset-model/v2');
  assert.equal(result.payload.kind, 'SITE_ASSET_MODEL');
  assert.deepEqual(result.payload.model.devices.map((device) => device.displayName), ['C 机组电表']);

  const otherSite = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async () => jsonResponse({ ...assetModelDto, siteId: deviceId }),
  });
  await expectOwnerError(() => otherSite.read({
    request: { requestId: 'read-asset-model-002', tool: 'registry.getSiteAssetModel', input: { siteId } },
    context,
  }), 'OWNER_RESPONSE_INVALID');
});

test('Energy reader calls only the fixed Energy Series product contract and preserves readiness metadata', async () => {
  let captured;
  const reader = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async (url, init) => {
      captured = { url: String(url), init };
      return jsonResponse(energyResponse);
    },
  });

  const result = await reader.read({ request: energyRequest, context: energyContext });

  assert.equal(captured.url, 'https://telemetry-query.internal/internal/v1/analytics/energy-series');
  assert.equal(captured.init.method, 'POST');
  assert.deepEqual(JSON.parse(captured.init.body), energyRequest.input);
  assert.equal(captured.init.headers['X-Delegation-Grant'], 'energy-delegation-grant-value');
  assert.equal(captured.init.headers['X-Request-ID'], energyRequest.requestId);
  assert.equal('X-Route-Policy-Revision' in captured.init.headers, false);
  assert.equal(result.owner, 'telemetry-query-service');
  assert.deepEqual(result.scope, scope);
  assert.equal(result.revision, energyResponse.metadata.datasetRevision);
  assert.equal(result.quality, 'UNCERTAIN');
  assert.equal(result.provenance, 'telemetry-query-service:energy-series/v1');
  assert.deepEqual(result.payload, energyResponse);
  assert.equal('measures' in JSON.parse(captured.init.body), false);
  assert.equal('dimensions' in JSON.parse(captured.init.body), false);
  assert.equal('sql' in JSON.parse(captured.init.body), false);
});

const historyRequest = Object.freeze({
  requestId: 'history-request-001',
  tool: 'telemetry.history.aggregateDeviceHistory',
  input: {
    tenantId,
    siteId,
    deviceId,
    keys: ['chw_supply_temp', 'active_energy_total'],
    from: '2026-10-15T16:00:00.000Z',
    to: '2026-10-16T16:00:00.000Z',
    granularity: 'HOUR',
    timezone: 'Asia/Shanghai',
    qualityPolicy: 'VALID_ONLY',
  },
});

const historyContext = Object.freeze({
  ...telemetryContext,
  authorization: {
    ...telemetryContext.authorization,
    toolDelegationGrants: { 'telemetry.history.aggregateDeviceHistory': 'history-delegation-grant-value' },
  },
});

const historyQuality = Object.freeze({ good: 60, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 });
const historyResponse = Object.freeze({
  schemaVersion: 1,
  tenantId,
  siteId,
  deviceId,
  buckets: [
    { telemetryKey: 'chw_supply_temp', pointId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708211', pointRevision: 1, pointType: 'TELEMETRY', unit: '°C', periodStart: '2026-10-16T02:00:00Z', periodEnd: '2026-10-16T03:00:00Z', quality: historyQuality, completeness: 1, gauge: { average: 7.2, minimum: 6.9, maximum: 7.6, first: 7, last: 7.4, sampleCount: 60 } },
    { telemetryKey: 'active_energy_total', pointId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708212', pointRevision: 1, pointType: 'COUNTER', unit: 'kWh', periodStart: '2026-10-16T02:00:00Z', periodEnd: '2026-10-16T03:00:00Z', quality: historyQuality, completeness: 1, counter: { deltaSum: 88.5, deltaCount: 59, resetCount: 0, rolloverCount: 0, excludedTransitionCount: 0 } },
  ],
  metadata: { requestedFrom: '2026-10-15T16:00:00Z', requestedTo: '2026-10-16T16:00:00Z', granularity: 'HOUR', timezone: 'Asia/Shanghai', qualityPolicy: 'VALID_ONLY', projectionWatermark: '2026-10-16T09:30:00Z', returnedBuckets: 2 },
});

test('History reader aggregates through the fixed device-history contract and keeps one result per bucket', async () => {
  let captured;
  const reader = createTelemetryHistoryOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async (url, init) => {
      captured = { url: String(url), init };
      return jsonResponse(historyResponse);
    },
  });

  const result = await reader.read({ request: historyRequest, context: historyContext });

  assert.equal(captured.url, 'https://telemetry-query.internal/internal/v1/telemetry/device-history:aggregate');
  assert.equal(captured.init.method, 'POST');
  assert.deepEqual(JSON.parse(captured.init.body), historyRequest.input);
  assert.equal(captured.init.headers['X-Delegation-Grant'], 'history-delegation-grant-value');
  assert.equal(result.owner, 'telemetry-query-service');
  assert.equal(result.provenance, 'telemetry-query-service:device-history-aggregate/v1');
  assert.equal(result.quality, 'UNCERTAIN');
  assert.deepEqual(result.payload, {
    schemaVersion: 1,
    deviceId,
    projectionWatermark: '2026-10-16T09:30:00Z',
    buckets: [
      { telemetryKey: 'chw_supply_temp', pointType: 'TELEMETRY', unit: '°C', periodStart: '2026-10-16T02:00:00Z', periodEnd: '2026-10-16T03:00:00Z', completeness: 1, gauge: { average: 7.2, minimum: 6.9, maximum: 7.6 } },
      { telemetryKey: 'active_energy_total', pointType: 'COUNTER', unit: 'kWh', periodStart: '2026-10-16T02:00:00Z', periodEnd: '2026-10-16T03:00:00Z', completeness: 1, counter: { deltaSum: 88.5 } },
    ],
  });

  const foreign = createTelemetryHistoryOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => jsonResponse({
      ...historyResponse,
      buckets: [{ ...historyResponse.buckets[0], telemetryKey: 'not_requested' }],
      metadata: { ...historyResponse.metadata, returnedBuckets: 1 },
    }),
  });
  await expectOwnerError(() => foreign.read({ request: historyRequest, context: historyContext }), 'OWNER_RESPONSE_INVALID');
});

test('Energy reader reads cooling energy through the same product contract', async () => {
  let body;
  const reader = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async (_url, init) => {
      body = JSON.parse(init.body);
      return jsonResponse(energyResponse);
    },
  });
  const request = { ...energyRequest, input: { ...energyRequest.input, energyType: 'cooling' } };

  const result = await reader.read({ request, context: energyContext });

  assert.equal(body.energyType, 'cooling');
  assert.deepEqual(result.payload.points, energyResponse.points);
});

test('Energy reader asks for the device breakdown and keeps each device series', async () => {
  let captured;
  const breakdownResponse = {
    ...energyResponse,
    devices: [{ deviceId, points: energyResponse.points }],
  };
  const reader = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async (url, init) => {
      captured = { url: String(url), init };
      return jsonResponse(breakdownResponse);
    },
  });
  const request = { ...energyRequest, input: { ...energyRequest.input, breakdown: 'device' } };

  const result = await reader.read({ request, context: energyContext });

  assert.equal(JSON.parse(captured.init.body).breakdown, 'device');
  assert.deepEqual(result.payload.devices, [{ deviceId, points: energyResponse.points }]);

  const malformed = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => jsonResponse({ ...breakdownResponse, devices: [{ deviceId, points: [{ periodStart: 'x' }] }] }),
  });
  await expectOwnerError(() => malformed.read({ request, context: energyContext }), 'OWNER_RESPONSE_INVALID');
});

test('Device Telemetry reader uses exact snapshot owner path and preserves stale and missing states', async () => {
  let captured;
  const reader = createDeviceTelemetryOwnerReader({
    baseUrl: 'https://telemetry-runtime.internal',
    fetchImplementation: async (url, init) => {
      captured = { url: String(url), init };
      return jsonResponse(telemetryResponse);
    },
  });

  const result = await reader.read({ request: telemetryRequest, context: telemetryContext });

  assert.equal(captured.url, `https://telemetry-runtime.internal/internal/v1/devices/${deviceId}/observation-snapshot?key=condenserPressureKPa&key=leavingWaterTempC`);
  assert.equal(captured.init.method, 'GET');
  assert.equal(captured.init.headers.Authorization, 'Bearer telemetry-delegation-grant-value');
  assert.equal('X-Delegation-Grant' in captured.init.headers, false);
  assert.equal(result.owner, 'telemetry-runtime-service');
  assert.deepEqual(result.scope, { tenantId, siteId, assetId: null, deviceId });
  assert.equal(result.revision, 'telemetry-device:73');
  assert.equal(result.quality, 'STALE');
  assert.equal(result.provenance, 'telemetry-runtime-service:device-observation-snapshot/v1');
  assert.deepEqual(result.payload, telemetryResponse);
  assert.equal(result.payload.values[0].freshness, 'STALE');
  assert.equal(result.payload.values[1].state, 'MISSING');
});

test('independent Registry and Energy READ adapters can execute concurrently', async () => {
  let active = 0;
  let maximumActive = 0;
  const concurrentFetch = async (_url, init) => {
    active += 1;
    maximumActive = Math.max(maximumActive, active);
    await new Promise((resolve) => setTimeout(resolve, 10));
    active -= 1;
    return init.method === 'GET' ? jsonResponse(siteDto) : jsonResponse(energyResponse);
  };
  const registry = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: concurrentFetch,
  });
  const energy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: concurrentFetch,
  });

  await Promise.all([
    registry.read({
      request: { requestId: 'read-site-parallel', tool: 'registry.getSite', input: { siteId } },
      context,
    }),
    energy.read({ request: energyRequest, context: energyContext }),
  ]);
  assert.equal(maximumActive, 2);
});

test('Owner readers fail closed for nondiscoverable, malformed, oversized, contradictory and unavailable responses', async () => {
  const registryForbidden = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async () => new Response('{}', { status: 403 }),
  });
  await expectOwnerError(() => registryForbidden.read({
    request: { requestId: 'read-site-forbidden', tool: 'registry.getSite', input: { siteId } },
    context,
  }), 'OWNER_RESOURCE_NOT_FOUND');

  const malformedEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => jsonResponse({ ...energyResponse, unexpected: true }),
  });
  await expectOwnerError(
    () => malformedEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_INVALID',
  );

  const contradictoryEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => jsonResponse({
      ...energyResponse,
      metadata: { ...energyResponse.metadata, actualGranularity: 'day' },
    }),
  });
  await expectOwnerError(
    () => contradictoryEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_INVALID',
  );

  const oversizedEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    maximumResponseBytes: 32,
    fetchImplementation: async () => jsonResponse(energyResponse),
  });
  await expectOwnerError(
    () => oversizedEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_TOO_LARGE',
  );

  const unavailableEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => new Response('{}', { status: 503 }),
  });
  await expectOwnerError(
    () => unavailableEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_READ_UNAVAILABLE',
  );
});

test('Owner readers reject timeout, malformed JSON, missing authorization and cross-Scope identities', async () => {
  const timeoutEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    requestTimeoutMs: 5,
    fetchImplementation: async () => new Promise(() => {}),
  });
  await expectOwnerError(
    () => timeoutEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_READ_TIMEOUT',
  );

  const invalidJsonEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => new Response('{', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  });
  await expectOwnerError(
    () => invalidJsonEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_RESPONSE_INVALID',
  );

  const registryMissingPolicy = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async () => jsonResponse(siteDto),
  });
  await expectOwnerError(() => registryMissingPolicy.read({
    request: { requestId: 'read-site-no-policy', tool: 'registry.getSite', input: { siteId } },
    context: energyContext,
  }), 'OWNER_REQUEST_INVALID');

  const registryWrongOrganization = createRegistryOwnerReader({
    baseUrl: 'https://platform-core.internal',
    fetchImplementation: async () => jsonResponse({
      ...siteDto,
      tenantId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708199',
    }),
  });
  await expectOwnerError(() => registryWrongOrganization.read({
    request: {
      requestId: 'read-site-wrong-organization',
      tool: 'registry.getSite',
      input: { siteId },
    },
    context,
  }), 'OWNER_RESPONSE_INVALID');

  const invalidRequestEnergy = createEnergyAnalyticsOwnerReader({
    baseUrl: 'https://telemetry-query.internal',
    fetchImplementation: async () => new Response('{}', { status: 422 }),
  });
  await expectOwnerError(
    () => invalidRequestEnergy.read({ request: energyRequest, context: energyContext }),
    'OWNER_REQUEST_INVALID',
  );
  await expectOwnerError(
    () => invalidRequestEnergy.read({
      request: energyRequest,
      context: {
        ...energyContext,
        authorization: {
          ...energyContext.authorization,
          traceparent: '00-' + '0'.repeat(32) + '-' + '0'.repeat(16) + '-01',
        },
      },
    }),
    'OWNER_REQUEST_INVALID',
  );
});

test('Gateway Tool authorization exchanges the service delegation for an exact Owner grant', async () => {
  const calls = [];
  const reader = createGatewayToolAuthorizationReader({
    baseUrl: 'https://platform-gateway.internal',
    fetchImplementation: async (url, init) => {
      calls.push({ url: String(url), init });
      return jsonResponse({
        delegationGrant: 'exact-energy-owner-grant',
        policyRevision: 'analytics-policy-7',
      });
    },
  });
  const authorizationContext = {
    ...energyContext,
    authorization: {
      decision: 'ALLOW',
      decisionId: 'operations-service-delegation',
      delegationGrant: 'operations-service-grant',
      traceparent: context.authorization.traceparent,
    },
  };

  const grant = await reader.authorize({ request: energyRequest, context: authorizationContext });
  assert.deepEqual(grant, {
    delegationGrant: 'exact-energy-owner-grant',
    policyRevision: 'analytics-policy-7',
  });
  assert.equal(calls.length, 1);
  assert.equal(
    calls[0].url,
    'https://platform-gateway.internal/internal/v1/operations/tool-authorization',
  );
  assert.equal(calls[0].init.method, 'POST');
  assert.equal(calls[0].init.headers['X-Delegation-Grant'], 'operations-service-grant');
  assert.equal(calls[0].init.headers['X-Request-ID'], energyRequest.requestId);
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    sessionId: authorizationContext.sessionId,
    runId: authorizationContext.runId,
    request: energyRequest,
  });
});

test('Gateway Tool authorization rejects incomplete context and malformed grant responses', async () => {
  const reader = createGatewayToolAuthorizationReader({
    baseUrl: 'https://platform-gateway.internal',
    fetchImplementation: async () => jsonResponse({
      delegationGrant: 'grant',
      policyRevision: 'policy',
      payload: { forbidden: true },
    }),
  });
  await expectOwnerError(() => reader.authorize({
    request: energyRequest,
    context: {
      ...energyContext,
      authorization: {
        decision: 'ALLOW',
        decisionId: 'missing-service-grant',
      },
    },
  }), 'OWNER_REQUEST_INVALID');
  await expectOwnerError(() => reader.authorize({
    request: energyRequest,
    context: {
      ...energyContext,
      authorization: {
        decision: 'ALLOW',
        decisionId: 'service-grant-present',
        delegationGrant: 'operations-service-grant',
      },
    },
  }), 'OWNER_RESPONSE_INVALID');
});

// #594: the alarm owner takes the Gateway's context in its own header and treats a delegation
// header as a forged identity, so the reader sends only the former.
test('Alarm reader lists a Site’s alarms with the context in the alarm owner’s own header', async () => {
  const calls = [];
  const alarm = {
    tenantId: scope.tenantId, siteId, title: '冷冻水出水温度过高', summary: '高于 12 ℃', condition: 'ACTIVE',
    currentSeverity: 'MAJOR', occurrenceCount: 2, firstOccurredAt: '2026-10-17T12:00:00Z', lastOccurredAt: '2026-10-17T12:30:00Z',
  };
  const reader = createAlarmOwnerReader({
    baseUrl: 'https://alarm-service.internal',
    fetchImplementation: async (url, init) => {
      calls.push({ url: String(url), init });
      return jsonResponse({ schemaVersion: 1, items: [alarm], nextCursor: null, hasMore: false });
    },
  });
  const alarmContext = { ...context, authorization: { ...context.authorization, toolDelegationGrants: { 'alarm.listAlarms': 'alarm-context-value' } } };
  const result = await reader.read({
    request: { requestId: 'read-alarms-001', tool: 'alarm.listAlarms', input: { siteId, condition: 'ACTIVE', limit: 50 } },
    context: alarmContext,
  });

  assert.equal(calls[0].url, `https://alarm-service.internal/internal/v1/sites/${siteId}/alarms?condition=ACTIVE&limit=50`);
  assert.equal(calls[0].init.method, 'GET');
  assert.equal(calls[0].init.headers['X-Alarm-Read-Context'], 'alarm-context-value');
  assert.equal(calls[0].init.headers['X-Delegation-Grant'], undefined);
  assert.equal(result.owner, 'alarm-service');
  assert.deepEqual(result.payload.items.map(({ title }) => title), ['冷冻水出水温度过高']);

  const otherSite = createAlarmOwnerReader({
    baseUrl: 'https://alarm-service.internal',
    fetchImplementation: async () => jsonResponse({ schemaVersion: 1, items: [{ ...alarm, siteId: deviceId }], nextCursor: null, hasMore: false }),
  });
  await expectOwnerError(() => otherSite.read({
    request: { requestId: 'read-alarms-002', tool: 'alarm.listAlarms', input: { siteId, condition: 'ACTIVE', limit: 50 } },
    context: alarmContext,
  }), 'OWNER_RESPONSE_INVALID');
});
