import assert from 'node:assert/strict';
import test from 'node:test';

import { AgentToolError } from '../dist/agent/index.js';
import { createHvacReadTools } from '../dist/tools/index.js';

const tenantId = 'tenant-1';
const siteId = 'site-1';
const deviceId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708195';

const context = Object.freeze({
  tenantId,
  siteId,
  principalId: 'principal-1',
  capabilities: Object.freeze(['site.read', 'asset.list', 'asset-model.read', 'telemetry.snapshot.read', 'telemetry.history.read', 'analytics.energy-series.read']),
  sessionId: 'session-1',
  runId: 'run-1',
  correlationId: 'correlation-1',
});

const authorization = Object.freeze({
  decision: 'ALLOW',
  decisionId: 'decision-1',
  delegationGrant: 'service-delegation',
  policyRevision: 'policy-1',
});

const site = Object.freeze({
  id: siteId,
  tenantId,
  code: 'PHX-1',
  displayName: 'Phoenix Plant',
  timezone: 'America/Phoenix',
  status: 'ACTIVE',
  revision: 7,
  createdAt: '2026-01-01T00:00:00.000Z',
  updatedAt: '2026-09-01T00:00:00.000Z',
});

const assets = Object.freeze([
  Object.freeze({ id: 'asset-1', tenantId, siteId, code: 'CH-1', displayName: 'Chiller 1', assetType: 'CHILLER', status: 'ACTIVE', revision: 2, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' }),
  Object.freeze({ id: 'asset-2', tenantId, siteId, code: 'CH-2', displayName: 'Chiller 2', assetType: 'CHILLER', status: 'ACTIVE', revision: 3, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' }),
  Object.freeze({ id: 'asset-3', tenantId, siteId, code: 'CT-1', displayName: 'Cooling Tower 1', assetType: 'COOLING_TOWER', status: 'ACTIVE', revision: 4, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' }),
]);

const chillerDeviceId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708196';
const meterDeviceId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708197';
const assetModel = Object.freeze({
  schemaVersion: 2,
  tenantId,
  siteId,
  spaces: [],
  assets: [assets[0]],
  devices: [
    { id: chillerDeviceId, tenantId, siteId, code: 'ch-1-controller', displayName: 'C 机组控制器', deviceType: 'CHILLER', status: 'ACTIVE', revision: 1, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' },
    { id: meterDeviceId, tenantId, siteId, code: 'meter-c', displayName: 'C 机组电表', deviceType: 'HVAC_POWER_METER', status: 'ACTIVE', revision: 1, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' },
  ],
  sensors: [],
  telemetryPoints: [
    { id: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708201', tenantId, siteId, reportingDeviceId: meterDeviceId, sensorId: null, pointCode: 'power_total', sourceKey: 'hvac_meter.active_power', displayName: '有功功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW', writable: false, sampleIntervalMs: 60000, publishIntervalMs: 60000, staleAfterMs: 300000, counterDecreaseMode: null, counterRolloverModulus: null, sourceMetadata: {}, status: 'ACTIVE', revision: 1, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' },
    { id: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708202', tenantId, siteId, reportingDeviceId: meterDeviceId, sensorId: null, pointCode: 'active_energy_total', sourceKey: 'hvac_meter.energy', displayName: '累计电量', pointType: 'COUNTER', valueType: 'NUMBER', unit: 'kWh', writable: false, sampleIntervalMs: 60000, publishIntervalMs: 60000, staleAfterMs: 300000, counterDecreaseMode: 'RESET_TO_ZERO', counterRolloverModulus: null, sourceMetadata: {}, status: 'ACTIVE', revision: 1, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' },
    { id: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708203', tenantId, siteId, reportingDeviceId: meterDeviceId, sensorId: null, pointCode: 'spare_input', sourceKey: 'hvac_meter.spare', displayName: '备用', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: null, writable: false, sampleIntervalMs: 60000, publishIntervalMs: 60000, staleAfterMs: 300000, counterDecreaseMode: null, counterRolloverModulus: null, sourceMetadata: {}, status: 'INACTIVE', revision: 1, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' },
  ],
  relationships: [
    { id: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708301', tenantId, siteId, fromType: 'DEVICE', fromId: meterDeviceId, toType: 'ASSET', toId: 'asset-1', role: 'METERS', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00.000Z', validTo: null, revision: 1, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z' },
  ],
  counts: { spaces: 0, assets: 1, deviceEndpoints: 2, physicalSensors: 0, points: 3 },
});

const ownerResult = (request, owner, revision, quality, provenance, payload) => ({
  requestId: request.requestId,
  owner,
  scope: { tenantId, siteId, assetId: null, deviceId: null },
  revision,
  quality,
  provenance,
  payload,
});

const energyPayload = ({ from, to, partial = false, energyKWh = 12, qualitySummary = { valid: 1, suspect: 0, invalid: 0 } }) => ({
  schemaVersion: 1,
  points: energyKWh === null ? [] : [{ periodStart: from, periodEnd: to, energyKWh }],
  metadata: {
    requestedGranularity: 'hour',
    actualGranularity: 'hour',
    datasetRevision: `energy:${from}`,
    partial,
    qualitySummary,
    ...(partial ? {} : { dataWatermark: to, aggregateWatermark: to }),
  },
});

const historyPayload = (query) => ({
  schemaVersion: 1,
  tenantId,
  siteId,
  deviceId: query.deviceId,
  buckets: [
    { telemetryKey: 'chw_supply_temp', pointId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708211', pointRevision: 1, pointType: 'TELEMETRY', unit: '°C', periodStart: '2026-10-16T10:00:00.000Z', periodEnd: '2026-10-16T11:00:00.000Z', quality: { good: 60, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 }, completeness: 1, gauge: { average: 7.2, minimum: 6.9, maximum: 7.6, first: 7, last: 7.4, sampleCount: 60 } },
    { telemetryKey: 'active_energy_total', pointId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708212', pointRevision: 1, pointType: 'COUNTER', unit: 'kWh', periodStart: '2026-10-16T10:00:00.000Z', periodEnd: '2026-10-16T11:00:00.000Z', quality: { good: 60, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 }, completeness: 0.5, counter: { deltaSum: 88.5, deltaCount: 59, resetCount: 0, rolloverCount: 0, excludedTransitionCount: 0 } },
  ],
  metadata: { requestedFrom: query.from, requestedTo: query.to, granularity: query.granularity, timezone: query.timezone, qualityPolicy: query.qualityPolicy, projectionWatermark: query.to, returnedBuckets: 2 },
});

const createHarness = ({ energyFor, registryFailure } = {}) => {
  const authorizationInputs = [];
  const registryInputs = [];
  const telemetryInputs = [];
  const historyInputs = [];
  const energyInputs = [];
  let activeEnergyReads = 0;
  let maxActiveEnergyReads = 0;

  const toolAuthorizationReader = {
    authorize: async (input) => {
      authorizationInputs.push(input);
      return { delegationGrant: `grant:${input.request.tool}`, policyRevision: 'tool-policy-1' };
    },
  };
  const registryReader = {
    read: async (input) => {
      registryInputs.push(input);
      if (registryFailure) throw registryFailure;
      if (input.request.tool === 'registry.getSite') {
        return ownerResult(input.request, 'registry', 'registry-site:7', 'GOOD', 'registry:site/v1', { kind: 'SITE', site });
      }
      if (input.request.tool === 'registry.getSiteAssetModel') {
        return ownerResult(input.request, 'registry', 'registry-asset-model:11', 'GOOD', 'registry:asset-model/v2', { kind: 'SITE_ASSET_MODEL', model: assetModel });
      }
      return ownerResult(input.request, 'registry', 'registry-assets:9', 'GOOD', 'registry:assets/v1', { kind: 'SITE_ASSETS', siteId, assets });
    },
  };
  const deviceTelemetryReader = {
    read: async (input) => {
      telemetryInputs.push(input);
      return {
        requestId: input.request.requestId,
        owner: 'telemetry-runtime-service',
        scope: { tenantId, siteId, assetId: null, deviceId: input.request.input.deviceId },
        revision: 'telemetry-device:73',
        quality: 'STALE',
        provenance: 'telemetry-runtime-service:device-observation-snapshot/v1',
        payload: {
          schemaVersion: 1,
          deviceId: input.request.input.deviceId,
          tenantId,
          siteId,
          businessRevision: 73,
          evaluatedAt: '2026-07-30T06:55:00.000Z',
          evaluationAvailability: 'AVAILABLE',
          availabilityReasons: [],
          presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-07-30T06:20:00.000Z', policyRevision: 3, lastKnown: null },
          telemetryReadiness: 'DEGRADED',
          displayState: 'STALE',
          values: [
            { key: input.request.input.pointKeys[0], state: 'PRESENT', value: 1280, valueType: 'NUMBER', unit: 'kPa', sampledAt: '2026-07-30T06:20:00.000Z', receivedAt: '2026-07-30T06:20:01.000Z', freshness: 'STALE', quality: 'GOOD', qualityReasons: [], policyRevision: 3 },
            ...(input.request.input.pointKeys.length > 1
              ? [{ key: input.request.input.pointKeys[1], state: 'MISSING', freshness: 'MISSING', missingReason: 'NEVER_OBSERVED', policyRevision: null }]
              : []),
          ],
        },
      };
    },
  };
  const telemetryHistoryReader = {
    read: async (input) => {
      historyInputs.push(input);
      return ownerResult(input.request, 'telemetry-query-service', 'history:2026-10-16T16:00:00.000Z', 'UNCERTAIN', 'telemetry-query-service:device-history-aggregate/v1', historyPayload(input.request.input));
    },
  };
  const energyAnalyticsReader = {
    read: async (input) => {
      energyInputs.push(input);
      activeEnergyReads += 1;
      maxActiveEnergyReads = Math.max(maxActiveEnergyReads, activeEnergyReads);
      try {
        if (energyFor) return await energyFor(input);
        const payload = energyPayload({ from: input.request.input.from, to: input.request.input.to });
        return ownerResult(input.request, 'telemetry-query-service', payload.metadata.datasetRevision, 'GOOD', 'telemetry-query-service:energy-series/v1', payload);
      } finally {
        activeEnergyReads -= 1;
      }
    },
  };

  const tools = createHvacReadTools({
    capabilities: context.capabilities,
    authorization,
    toolAuthorizationReader,
    registryReader,
    deviceTelemetryReader,
    telemetryHistoryReader,
    energyAnalyticsReader,
    limits: { maxAssets: 2, maxEnergyPoints: 48, maxEnergyRangeMs: 31 * 24 * 60 * 60 * 1000, maxResultBytes: 32_768, timeoutMs: 2_000 },
  });

  return { tools, authorizationInputs, registryInputs, telemetryInputs, historyInputs, energyInputs, maxActiveEnergyReads: () => maxActiveEnergyReads };
};

const byName = (tools, name) => {
  const tool = tools.find((candidate) => candidate.definition.name === name);
  assert.ok(tool, `missing Tool ${name}`);
  return tool;
};

const execute = (tool, args, toolContext = context) => tool.execute({
  context: toolContext,
  arguments: args,
  signal: new AbortController().signal,
});

test('HVAC catalog contains the capability-filtered READ Tools', () => {
  const full = createHarness().tools.map((tool) => tool.definition.name);
  assert.deepEqual(full, ['site_get_context', 'assets_list', 'equipment_list', 'telemetry_get_current', 'telemetry_query_history', 'energy_query_series', 'energy_compare_periods', 'energy_by_equipment']);

  const limited = createHvacReadTools({
    capabilities: ['site.read'],
    authorization,
    toolAuthorizationReader: { authorize: async () => ({ delegationGrant: 'grant' }) },
    registryReader: { read: async () => { throw new Error('not called'); } },
    deviceTelemetryReader: { read: async () => { throw new Error('not called'); } },
    telemetryHistoryReader: { read: async () => { throw new Error('not called'); } },
    energyAnalyticsReader: { read: async () => { throw new Error('not called'); } },
  });
  assert.deepEqual(limited.map((tool) => tool.definition.name), ['site_get_context']);
  assert.equal(limited.every((tool) => tool.definition.executionMode === 'parallel' && tool.definition.replayPolicy === 'safe'), true);
});

test('model arguments cannot select trusted identity or infrastructure and direct invocation still rechecks capability', async () => {
  const harness = createHarness();
  const siteTool = byName(harness.tools, 'site_get_context');
  const forbiddenFields = [
    'tenantId',
    'siteId',
    'principalId',
    'capabilityGrant',
    'credential',
    'url',
    'sql',
    'ownerServiceAddress',
  ];
  for (const tool of harness.tools) {
    const schema = JSON.stringify(tool.definition.inputSchema);
    for (const field of forbiddenFields) assert.equal(schema.includes(`"${field}"`), false);
  }

  await assert.rejects(
    () => execute(siteTool, { siteId: 'other-site' }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID',
  );
  await assert.rejects(
    () => execute(byName(harness.tools, 'energy_query_series'), {
      from: '2026-08-01T00:00:00.000Z',
      to: '2026-08-01T01:00:00.000Z',
      tenantId: 'other-tenant',
    }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID',
  );
  await assert.rejects(
    () => execute(byName(harness.tools, 'telemetry_get_current'), {
      deviceId,
      pointKeys: ['condenserPressureKPa'],
      siteId: 'other-site',
    }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID',
  );
  assert.equal(harness.authorizationInputs.length, 0);
  assert.equal(harness.registryInputs.length, 0);
  assert.equal(harness.telemetryInputs.length, 0);

  await assert.rejects(
    () => execute(siteTool, {}, { ...context, capabilities: [] }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_UNAUTHORIZED',
  );
  await assert.rejects(
    () => execute(
      byName(harness.tools, 'telemetry_get_current'),
      { deviceId, pointKeys: ['condenserPressureKPa'] },
      { ...context, capabilities: ['site.read'] },
    ),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_UNAUTHORIZED',
  );
  assert.equal(harness.authorizationInputs.length, 0);
});

test('site and asset Tools reauthorize then call Registry with trusted scope and bounded output', async () => {
  const harness = createHarness();
  const siteResult = await execute(byName(harness.tools, 'site_get_context'), {});
  const assetsResult = await execute(byName(harness.tools, 'assets_list'), {});

  assert.equal(harness.authorizationInputs.length, 2);
  assert.equal(harness.registryInputs.length, 2);
  assert.equal(harness.registryInputs[0].request.input.siteId, siteId);
  assert.equal(harness.registryInputs[0].context.scope.tenantId, tenantId);
  assert.equal(harness.registryInputs[0].context.authorization.delegationGrant, 'grant:registry.getSite');
  assert.deepEqual(siteResult.source, { owner: 'registry', revision: 'registry-site:7', quality: 'GOOD', completeness: 'COMPLETE', provenance: 'registry:site/v1' });

  assert.equal(assetsResult.totalCount, 3);
  assert.equal(assetsResult.returnedCount, 2);
  assert.equal(assetsResult.completeness, 'PARTIAL');
  assert.deepEqual(assetsResult.assets.map(({ id }) => id), ['asset-1', 'asset-2']);
});

test('telemetry_get_current injects trusted Site scope and preserves stale and missing telemetry semantics', async () => {
  const harness = createHarness();
  const result = await execute(byName(harness.tools, 'telemetry_get_current'), {
    deviceId,
    pointKeys: ['condenserPressureKPa', 'leavingWaterTempC'],
  });

  assert.equal(harness.authorizationInputs.length, 1);
  assert.equal(harness.authorizationInputs[0].request.tool, 'telemetry.current.getDeviceObservationSnapshot');
  assert.equal(harness.authorizationInputs[0].request.input.siteId, siteId);
  assert.equal(harness.authorizationInputs[0].request.input.deviceId, deviceId);
  assert.deepEqual(harness.authorizationInputs[0].request.input.pointKeys, ['condenserPressureKPa', 'leavingWaterTempC']);
  assert.deepEqual(harness.authorizationInputs[0].context.scope, { tenantId, siteId, assetId: null, deviceId });

  assert.equal(harness.telemetryInputs.length, 1);
  assert.equal(harness.telemetryInputs[0].context.authorization.delegationGrant, 'grant:telemetry.current.getDeviceObservationSnapshot');
  assert.equal(result.deviceId, deviceId);
  assert.equal(result.displayState, 'STALE');
  assert.equal(result.telemetryReadiness, 'DEGRADED');
  assert.equal(result.completeness, 'PARTIAL');
  assert.equal(result.quality, 'STALE');
  assert.equal(result.values[0].state, 'PRESENT');
  assert.equal(result.values[0].freshness, 'STALE');
  assert.equal(result.values[1].state, 'MISSING');
  assert.equal(result.values[1].missingReason, 'NEVER_OBSERVED');
  assert.deepEqual(result.source, {
    owner: 'telemetry-runtime-service',
    revision: 'telemetry-device:73',
    quality: 'STALE',
    completeness: 'PARTIAL',
    provenance: 'telemetry-runtime-service:device-observation-snapshot/v1',
    businessRevision: 73,
    evaluatedAt: '2026-07-30T06:55:00.000Z',
  });
});

test('energy_query_series injects authoritative timezone and keeps partial data distinct from zero', async () => {
  const from = '2026-08-01T00:00:00.000Z';
  const to = '2026-08-01T01:00:00.000Z';
  const harness = createHarness({
    energyFor: async (input) => {
      const payload = energyPayload({ from, to, partial: true, energyKWh: null, qualitySummary: { valid: 0, suspect: 1, invalid: 0 } });
      return ownerResult(input.request, 'telemetry-query-service', 'energy-partial-1', 'UNCERTAIN', 'telemetry-query-service:energy-series/v1', payload);
    },
  });

  const result = await execute(byName(harness.tools, 'energy_query_series'), { from, to, granularity: 'hour', qualityPolicy: 'VALID_ONLY' });
  assert.equal(harness.energyInputs[0].request.input.tenantId, tenantId);
  assert.equal(harness.energyInputs[0].request.input.siteId, siteId);
  assert.equal(harness.energyInputs[0].request.input.timezone, 'America/Phoenix');
  assert.equal(result.totalKWh, null);
  assert.equal(result.completeness, 'PARTIAL');
  assert.equal(result.quality, 'UNCERTAIN');
  assert.deepEqual(result.qualitySummary, { valid: 0, suspect: 1, invalid: 0 });
  assert.equal(result.source.revision, 'energy-partial-1');

  const zeroHarness = createHarness({
    energyFor: async (input) => {
      const payload = energyPayload({ from, to, energyKWh: 0 });
      return ownerResult(input.request, 'telemetry-query-service', 'energy-zero-1', 'GOOD', 'telemetry-query-service:energy-series/v1', payload);
    },
  });
  const zero = await execute(byName(zeroHarness.tools, 'energy_query_series'), { from, to });
  assert.equal(zero.totalKWh, 0);
  assert.equal(zero.completeness, 'COMPLETE');
});

test('energy_compare_periods executes independent Energy reads in parallel and returns bounded summaries', async () => {
  const baselineFrom = '2026-08-01T00:00:00.000Z';
  const baselineTo = '2026-08-01T01:00:00.000Z';
  const currentFrom = '2026-08-02T00:00:00.000Z';
  const currentTo = '2026-08-02T01:00:00.000Z';
  let release;
  let waiting = 0;
  const gate = new Promise((resolve) => { release = resolve; });
  const harness = createHarness({
    energyFor: async (input) => {
      waiting += 1;
      if (waiting === 2) release();
      await gate;
      const isBaseline = input.request.input.from === baselineFrom;
      const payload = energyPayload({
        from: input.request.input.from,
        to: input.request.input.to,
        energyKWh: isBaseline ? 10 : 15,
      });
      return ownerResult(input.request, 'telemetry-query-service', isBaseline ? 'baseline-r1' : 'current-r1', 'GOOD', 'telemetry-query-service:energy-series/v1', payload);
    },
  });

  const result = await execute(byName(harness.tools, 'energy_compare_periods'), {
    baselineFrom,
    baselineTo,
    currentFrom,
    currentTo,
  });

  assert.equal(harness.maxActiveEnergyReads(), 2);
  assert.equal(result.baseline.totalKWh, 10);
  assert.equal(result.current.totalKWh, 15);
  assert.deepEqual(result.comparison, { status: 'COMPARABLE', absoluteChangeKWh: 5, percentChange: 50 });
  assert.equal('points' in result.baseline, false);
  assert.equal('points' in result.current, false);
});

test('Owner failures are stable Agent Tool failures rather than successful strings or empty data', async () => {
  const { OwnerReadError } = await import('../dist/application/index.js');
  const harness = createHarness({ registryFailure: new OwnerReadError('OWNER_READ_UNAVAILABLE', 'secret upstream detail') });

  await assert.rejects(
    () => execute(byName(harness.tools, 'site_get_context'), {}),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_OWNER_UNAVAILABLE' && !error.message.includes('secret upstream detail'),
  );
});

test('equipment_list names each device, its machine and its active points so other Tools can be called with real keys', async () => {
  const harness = createHarness();
  const result = await execute(byName(harness.tools, 'equipment_list'), {});

  const lastRegistry = harness.registryInputs.at(-1);
  assert.equal(lastRegistry.request.tool, 'registry.getSiteAssetModel');
  assert.deepEqual(lastRegistry.request.input, { siteId });
  assert.equal(harness.authorizationInputs.at(-1).request.tool, 'registry.getSiteAssetModel');

  assert.deepEqual(result.devices, [
    { deviceId: chillerDeviceId, name: 'C 机组控制器', type: 'CHILLER', machine: null, points: [] },
    {
      deviceId: meterDeviceId,
      name: 'C 机组电表',
      type: 'HVAC_POWER_METER',
      machine: { name: 'Chiller 1', type: 'CHILLER' },
      points: [
        { key: 'power_total', name: '有功功率', unit: 'kW', kind: 'TELEMETRY' },
        { key: 'active_energy_total', name: '累计电量', unit: 'kWh', kind: 'COUNTER' },
      ],
    },
  ]);
  assert.equal(result.completeness, 'COMPLETE');
  assert.equal(result.source.revision, 'registry-asset-model:11');
});

test('energy_by_equipment splits the Site total by sub-metered device, named from the asset model', async () => {
  const from = '2026-10-15T16:00:00.000Z';
  const to = '2026-10-16T16:00:00.000Z';
  const harness = createHarness({
    energyFor: async (input) => {
      const payload = {
        ...energyPayload({ from, to, energyKWh: 1634 }),
        devices: [{
          deviceId: meterDeviceId,
          points: [
            { periodStart: '2026-10-16T10:00:00.000Z', periodEnd: '2026-10-16T11:00:00.000Z', energyKWh: 300 },
            { periodStart: '2026-10-16T11:00:00.000Z', periodEnd: '2026-10-16T12:00:00.000Z', energyKWh: 579 },
          ],
        }],
      };
      return ownerResult(input.request, 'telemetry-query-service', 'energy-breakdown-1', 'GOOD', 'telemetry-query-service:energy-series/v1', payload);
    },
  });

  const result = await execute(byName(harness.tools, 'energy_by_equipment'), { from, to, granularity: 'hour' });

  const energyRequest = harness.energyInputs[0].request;
  assert.equal(energyRequest.input.breakdown, 'device');
  assert.equal(energyRequest.input.siteId, siteId);
  assert.equal(energyRequest.input.timezone, 'America/Phoenix');
  assert.equal(result.siteTotalKWh, 1634);
  assert.deepEqual(result.devices.map(({ points: _points, ...device }) => device), [
    { deviceId: meterDeviceId, name: 'C 机组电表', machine: { name: 'Chiller 1', type: 'CHILLER' }, totalKWh: 879, shareOfSite: 0.538 },
  ]);
  // Points read in the Site's own calendar (America/Phoenix is UTC−7), so hours are not mistaken for UTC.
  assert.deepEqual(result.devices[0].points[0], { siteTime: '2026-10-16 03:00', energyKWh: 300 });
  assert.equal(result.completeness, 'COMPLETE');
  assert.equal(result.source.revision, 'energy-breakdown-1');
});

test('energy tools read cooling energy when asked and say which energy they read', async () => {
  const harness = createHarness({
    energyFor: async (input) => {
      const payload = energyPayload({ from: input.request.input.from, to: input.request.input.to, energyKWh: 7217 });
      return ownerResult(input.request, 'telemetry-query-service', 'cooling-1', 'GOOD', 'telemetry-query-service:energy-series/v1', payload);
    },
  });
  const from = '2026-10-15T16:00:00.000Z';
  const to = '2026-10-16T16:00:00.000Z';

  const series = await execute(byName(harness.tools, 'energy_query_series'), { from, to, energyType: 'cooling' });
  const comparison = await execute(byName(harness.tools, 'energy_compare_periods'), {
    baselineFrom: '2026-10-08T16:00:00.000Z',
    baselineTo: '2026-10-09T16:00:00.000Z',
    currentFrom: from,
    currentTo: to,
    energyType: 'cooling',
  });
  const electricity = await execute(byName(harness.tools, 'energy_query_series'), { from, to });

  assert.deepEqual(harness.energyInputs.map(({ request }) => request.input.energyType), ['cooling', 'cooling', 'cooling', 'electricity']);
  assert.equal(series.query.energyType, 'cooling');
  assert.equal(comparison.current.query.energyType, 'cooling');
  assert.equal(electricity.query.energyType, 'electricity');
});

test('telemetry_query_history aggregates one device readings over time in the Site calendar', async () => {
  const harness = createHarness();
  const from = '2026-10-15T16:00:00.000Z';
  const to = '2026-10-16T16:00:00.000Z';

  const result = await execute(byName(harness.tools, 'telemetry_query_history'), {
    deviceId: chillerDeviceId,
    pointKeys: ['chw_supply_temp', 'active_energy_total'],
    from,
    to,
  });

  assert.deepEqual(harness.historyInputs[0].request.input, {
    tenantId,
    siteId,
    deviceId: chillerDeviceId,
    keys: ['chw_supply_temp', 'active_energy_total'],
    from,
    to,
    granularity: 'HOUR',
    timezone: 'America/Phoenix',
    qualityPolicy: 'VALID_ONLY',
  });
  assert.deepEqual(result.series, [
    { key: 'chw_supply_temp', unit: '°C', kind: 'reading', points: [{ siteTime: '2026-10-16 03:00', average: 7.2, minimum: 6.9, maximum: 7.6, coverage: 1 }] },
    { key: 'active_energy_total', unit: 'kWh', kind: 'counter', points: [{ siteTime: '2026-10-16 03:00', increase: 88.5, coverage: 0.5 }] },
  ]);
  assert.equal(result.completeness, 'PARTIAL');
  assert.equal(result.source.revision, 'history:2026-10-16T16:00:00.000Z');
});

test('telemetry_query_history refuses a range that would return more buckets than one result holds, before reading', async () => {
  const harness = createHarness();
  await assert.rejects(
    execute(byName(harness.tools, 'telemetry_query_history'), {
      deviceId: chillerDeviceId,
      pointKeys: ['chw_supply_temp'],
      from: '2026-10-14T16:00:00.000Z',
      to: '2026-10-16T16:00:00.000Z',
      granularity: 'quarter_hour',
    }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID' && /粒度|granularity/u.test(error.message),
  );
  assert.equal(harness.historyInputs.length, 0);
});

test('energy_query_series points carry Site-local times', async () => {
  const harness = createHarness();
  const result = await execute(byName(harness.tools, 'energy_query_series'), { from: '2026-10-16T01:00:00.000Z', to: '2026-10-16T02:00:00.000Z' });
  assert.deepEqual(result.points, [{ siteTime: '2026-10-15 18:00', energyKWh: 12 }]);
});
