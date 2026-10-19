import assert from 'node:assert/strict';
import test from 'node:test';

import { AgentToolError } from '../dist/agent/index.js';
import { createHvacReadTools, createSiteDescriber } from '../dist/tools/index.js';

const tenantId = 'tenant-1';
const siteId = 'site-1';
const deviceId = '0198a36e-4c9d-7b5a-8f2d-4c5e6f708195';

const context = Object.freeze({
  tenantId,
  siteId,
  principalId: 'principal-1',
  capabilities: Object.freeze(['site.read', 'asset.list', 'asset-model.read', 'telemetry.snapshot.read', 'telemetry.history.read', 'analytics.energy-series.read', 'alarm.read', 'fdd.read', 'work-order.read']),
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
    { telemetryKey: 'chw_supply_temp', pointId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708211', pointRevision: 1, pointType: 'TELEMETRY', unit: 'Cel', periodStart: '2026-10-16T10:00:00.000Z', periodEnd: '2026-10-16T11:00:00.000Z', quality: { good: 60, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 }, completeness: 1, gauge: { average: 7.2, minimum: 6.9, maximum: 7.6, first: 7, last: 7.4, sampleCount: 60 } },
    { telemetryKey: 'active_energy_total', pointId: '0198a36e-4c9d-7b5a-8f2d-4c5e6f708212', pointRevision: 1, pointType: 'COUNTER', unit: 'kWh', periodStart: '2026-10-16T10:00:00.000Z', periodEnd: '2026-10-16T11:00:00.000Z', quality: { good: 60, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 }, completeness: 0.5, counter: { deltaSum: 88.5, deltaCount: 59, resetCount: 0, rolloverCount: 0, excludedTransitionCount: 0 } },
  ],
  metadata: { requestedFrom: query.from, requestedTo: query.to, granularity: query.granularity, timezone: query.timezone, qualityPolicy: query.qualityPolicy, projectionWatermark: query.to, returnedBuckets: 2 },
});

const createHarness = ({ energyFor, registryFailure, semanticFor, alarmsFor, findingsFor, workOrdersFor, timezone = 'America/Phoenix', capabilities = context.capabilities } = {}) => {
  const authorizationInputs = [];
  const registryInputs = [];
  const telemetryInputs = [];
  const historyInputs = [];
  const energyInputs = [];
  const semanticInputs = [];
  const alarmInputs = [];
  const fddInputs = [];
  const workOrderInputs = [];
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
        return ownerResult(input.request, 'registry', 'registry-site:7', 'GOOD', 'registry:site/v1', { kind: 'SITE', site: { ...site, timezone } });
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

  const semanticQueryReader = {
    read: async (input) => {
      semanticInputs.push(input);
      return ownerResult(input.request, 'telemetry-query-service', 'semantic:1', 'GOOD', 'telemetry-query-service:semantic-query/v1', semanticFor(input));
    },
  };

  const alarmReader = {
    read: async (input) => {
      alarmInputs.push(input);
      return ownerResult(input.request, 'alarm-service', 'alarms', 'GOOD', 'alarm-service:alarm-list/v1', alarmsFor(input));
    },
  };

  const fddReader = {
    read: async (input) => {
      fddInputs.push(input);
      return ownerResult(input.request, 'fdd-service', 'fdd-findings', 'GOOD', 'fdd-service:findings/v1', findingsFor(input));
    },
  };
  const workOrderReader = {
    read: async (input) => {
      workOrderInputs.push(input);
      return ownerResult(input.request, 'work-order-service', 'work-order-list', 'GOOD', 'work-order-service:work-order-list/v1', workOrdersFor(input));
    },
  };

  const tools = createHvacReadTools({
    capabilities,
    authorization,
    toolAuthorizationReader,
    registryReader,
    deviceTelemetryReader,
    telemetryHistoryReader,
    energyAnalyticsReader,
    semanticQueryReader,
    alarmReader,
    fddReader,
    workOrderReader,
    limits: { maxAssets: 2, maxEnergyPoints: 48, maxEnergyRangeMs: 31 * 24 * 60 * 60 * 1000, maxResultBytes: 32_768, timeoutMs: 2_000 },
  });

  const describer = createSiteDescriber({ toolAuthorizationReader, registryReader });
  const describeSite = (runContext) => describer(authorization, runContext);

  return { tools, describeSite, authorizationInputs, registryInputs, telemetryInputs, historyInputs, energyInputs, semanticInputs, alarmInputs, fddInputs, workOrderInputs, maxActiveEnergyReads: () => maxActiveEnergyReads };
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
  assert.deepEqual(full, ['assets_list', 'equipment_list', 'telemetry_get_current', 'telemetry_query_history', 'energy_query_series', 'energy_compare_periods', 'energy_by_equipment', 'energy_semantic_query', 'alarm_list', 'fdd_findings', 'work_order_list']);

  const limited = createHvacReadTools({
    capabilities: ['site.read', 'asset.list'],
    authorization,
    toolAuthorizationReader: { authorize: async () => ({ delegationGrant: 'grant' }) },
    registryReader: { read: async () => { throw new Error('not called'); } },
    deviceTelemetryReader: { read: async () => { throw new Error('not called'); } },
    telemetryHistoryReader: { read: async () => { throw new Error('not called'); } },
    energyAnalyticsReader: { read: async () => { throw new Error('not called'); } },
    semanticQueryReader: { read: async () => { throw new Error('not called'); } },
  });
  assert.deepEqual(limited.map((tool) => tool.definition.name), ['assets_list']);
  assert.equal(limited.every((tool) => tool.definition.executionMode === 'parallel' && tool.definition.replayPolicy === 'safe'), true);
});

test('model arguments cannot select trusted identity or infrastructure and direct invocation still rechecks capability', async () => {
  const harness = createHarness();
  const assetsTool = byName(harness.tools, 'assets_list');
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
    () => execute(assetsTool, { siteId: 'other-site' }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID',
  );
  await assert.rejects(
    () => execute(byName(harness.tools, 'energy_query_series'), {
      startDate: '2026-08-01',
      endDate: '2026-08-01',
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
    () => execute(assetsTool, {}, { ...context, capabilities: [] }),
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

test('the Site the prompt names is read from Registry under the Run\'s own authorization', async () => {
  const harness = createHarness();
  const site = await harness.describeSite(context);

  assert.deepEqual(site, { name: 'Phoenix Plant', timezone: 'America/Phoenix' });
  assert.equal(harness.registryInputs.length, 1);
  assert.equal(harness.registryInputs[0].request.input.siteId, siteId);
  assert.equal(harness.registryInputs[0].context.scope.tenantId, tenantId);
  assert.equal(harness.registryInputs[0].context.authorization.delegationGrant, 'grant:registry.getSite');
});

test('reading the Site for the prompt needs the right to read the Site, checked before Registry', async () => {
  const harness = createHarness();
  await assert.rejects(
    () => harness.describeSite({ ...context, capabilities: ['analytics.energy-series.read'] }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_UNAUTHORIZED',
  );
  assert.equal(harness.registryInputs.length, 0);
});

test('asset Tools reauthorize then call Registry with trusted scope and bounded output', async () => {
  const harness = createHarness();
  const assetsResult = await execute(byName(harness.tools, 'assets_list'), {});

  assert.equal(harness.authorizationInputs.length, 1);
  assert.equal(harness.registryInputs.length, 1);
  assert.deepEqual(assetsResult.assets.map(({ name }) => name), ['Chiller 1', 'Chiller 2']);
  assert.equal(assetsResult.note, '只列出前 2 个，共 3 个');
});

test('telemetry_get_current injects trusted Site scope and preserves stale and missing telemetry semantics', async () => {
  const harness = createHarness();
  const result = await execute(byName(harness.tools, 'telemetry_get_current'), {
    deviceId,
    pointKeys: ['condenserPressureKPa', 'leavingWaterTempC'],
  });

  // The Site is read first for its clock, then the device under its own grant.
  const telemetryAuthorization = harness.authorizationInputs.find(({ request }) => request.tool === 'telemetry.current.getDeviceObservationSnapshot');
  assert.equal(telemetryAuthorization.request.input.siteId, siteId);
  assert.equal(telemetryAuthorization.request.input.deviceId, deviceId);
  assert.deepEqual(telemetryAuthorization.request.input.pointKeys, ['condenserPressureKPa', 'leavingWaterTempC']);
  assert.deepEqual(telemetryAuthorization.context.scope, { tenantId, siteId, assetId: null, deviceId });

  assert.equal(harness.telemetryInputs.length, 1);
  assert.equal(harness.telemetryInputs[0].context.authorization.delegationGrant, 'grant:telemetry.current.getDeviceObservationSnapshot');
  assert.equal('deviceId' in result, false);
  assert.equal(result.deviceState, '读数过期');
  assert.equal(result.dataStatus, '不完整：有读数过期或缺失');
  assert.deepEqual(result.values, [
    { key: 'condenserPressureKPa', value: 1280, unit: 'kPa', siteTime: '2026-07-29 23:20', state: '读数过期' },
    { key: 'leavingWaterTempC', state: '没有读数' },
  ]);
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

  const result = await execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-08-01', endDate: '2026-08-01', granularity: 'hour', qualityPolicy: 'VALID_ONLY' });
  assert.equal(harness.energyInputs[0].request.input.tenantId, tenantId);
  assert.equal(harness.energyInputs[0].request.input.siteId, siteId);
  assert.equal(harness.energyInputs[0].request.input.timezone, 'America/Phoenix');
  assert.equal(result.totalKWh, null);
  assert.equal(result.dataStatus, '不完整');
  assert.equal(result.readingNote, '部分读数可疑或无效，没有计入');

  const zeroHarness = createHarness({
    energyFor: async (input) => {
      const payload = energyPayload({ from, to, energyKWh: 0 });
      return ownerResult(input.request, 'telemetry-query-service', 'energy-zero-1', 'GOOD', 'telemetry-query-service:energy-series/v1', payload);
    },
  });
  const zero = await execute(byName(zeroHarness.tools, 'energy_query_series'), { startDate: '2026-08-01', endDate: '2026-08-01' });
  assert.equal(zero.totalKWh, 0);
  assert.equal(zero.dataStatus, '完整');
});

test('energy_compare_periods executes independent Energy reads in parallel and returns bounded summaries', async () => {
  // 2026-08-01 and 08-02 in America/Phoenix (UTC−7).
  const baselineFrom = '2026-08-01T07:00:00.000Z';
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
    baselineStartDate: '2026-08-01',
    baselineEndDate: '2026-08-01',
    currentStartDate: '2026-08-02',
    currentEndDate: '2026-08-02',
  });

  assert.equal(harness.maxActiveEnergyReads(), 2);
  assert.equal(result.baseline.totalKWh, 10);
  assert.equal(result.current.totalKWh, 15);
  assert.deepEqual(result.comparison, { status: '可以比较', absoluteChangeKWh: 5, percentChange: 50 });
  assert.equal('points' in result.baseline, false);
  assert.equal('points' in result.current, false);
});

test('Owner failures are stable Agent Tool failures rather than successful strings or empty data', async () => {
  const { OwnerReadError } = await import('../dist/application/index.js');
  const harness = createHarness({ registryFailure: new OwnerReadError('OWNER_READ_UNAVAILABLE', 'secret upstream detail') });

  await assert.rejects(
    () => execute(byName(harness.tools, 'assets_list'), {}),
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
    { deviceId: chillerDeviceId, name: 'C 机组控制器', machine: null, points: [] },
    {
      deviceId: meterDeviceId,
      name: 'C 机组电表',
      machine: 'Chiller 1',
      points: [
        { key: 'power_total', name: '有功功率', unit: 'kW', kind: '读数' },
        { key: 'active_energy_total', name: '累计电量', unit: 'kWh', kind: '累计量' },
      ],
    },
  ]);
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

  const result = await execute(byName(harness.tools, 'energy_by_equipment'), { startDate: '2026-10-15', endDate: '2026-10-15', granularity: 'hour' });

  const energyRequest = harness.energyInputs[0].request;
  assert.equal(energyRequest.input.breakdown, 'device');
  assert.equal(energyRequest.input.siteId, siteId);
  assert.equal(energyRequest.input.timezone, 'America/Phoenix');
  assert.equal(result.siteTotalKWh, 1634);
  assert.deepEqual(result.devices.map(({ points: _points, ...device }) => device), [
    { name: 'C 机组电表', machine: 'Chiller 1', totalKWh: 879, shareOfSite: 0.538 },
  ]);
  // Points read in the Site's own calendar (America/Phoenix is UTC−7), so hours are not mistaken for UTC.
  assert.deepEqual(result.devices[0].points[0], { siteTime: '2026-10-16 03:00', energyKWh: 300 });
  assert.equal(result.dataStatus, '完整');
  // A card charts the machines as a ranked bar list (#640).
  assert.deepEqual(byName(harness.tools, 'energy_by_equipment').chart(result), {
    kind: 'HORIZONTAL_BAR', title: '10月15日各设备用电', unit: 'kWh', categories: ['C 机组电表'], series: [{ name: '用电', values: [879] }],
  });
});

test('energy tools read cooling energy when asked and say which energy they read', async () => {
  const harness = createHarness({
    energyFor: async (input) => {
      const payload = energyPayload({ from: input.request.input.from, to: input.request.input.to, energyKWh: 7217 });
      return ownerResult(input.request, 'telemetry-query-service', 'cooling-1', 'GOOD', 'telemetry-query-service:energy-series/v1', payload);
    },
  });
  const series = await execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-10-15', endDate: '2026-10-15', energyType: 'cooling' });
  const comparison = await execute(byName(harness.tools, 'energy_compare_periods'), {
    baselineStartDate: '2026-10-08',
    baselineEndDate: '2026-10-08',
    currentStartDate: '2026-10-15',
    currentEndDate: '2026-10-15',
    energyType: 'cooling',
  });
  const electricity = await execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-10-15', endDate: '2026-10-15' });

  assert.deepEqual(harness.energyInputs.map(({ request }) => request.input.energyType), ['cooling', 'cooling', 'cooling', 'electricity']);
  assert.equal(series.query.energy, '供冷');
  assert.equal(comparison.current.query.energy, '供冷');
  assert.equal(electricity.query.energy, '用电');
  // A card charts the two periods side by side (#640).
  assert.deepEqual(byName(harness.tools, 'energy_compare_periods').chart(comparison), {
    kind: 'BAR', title: '两段时间的供冷', unit: 'kWh', categories: ['10月8日', '10月15日'], series: [{ name: '供冷', values: [7217, 7217] }],
  });
});

test('telemetry_query_history aggregates one device readings over time in the Site calendar', async () => {
  const harness = createHarness();
  // 10-16 in America/Phoenix (UTC−7).
  const from = '2026-10-16T07:00:00.000Z';
  const to = '2026-10-17T07:00:00.000Z';

  const result = await execute(byName(harness.tools, 'telemetry_query_history'), {
    deviceId: chillerDeviceId,
    pointKeys: ['chw_supply_temp', 'active_energy_total'],
    startDate: '2026-10-16',
    endDate: '2026-10-16',
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
    { key: 'chw_supply_temp', unit: '°C', kind: '读数', points: [{ siteTime: '2026-10-16 03:00', average: 7.2, minimum: 6.9, maximum: 7.6, coverage: 1 }] },
    { key: 'active_energy_total', unit: 'kWh', kind: '累计量', points: [{ siteTime: '2026-10-16 03:00', increase: 88.5, coverage: 0.5 }] },
  ]);
  // The history owner rated it UNCERTAIN: one bucket is only half covered.
  assert.equal(result.dataStatus, '不完整');
});

test('telemetry_query_history refuses a range that would return more buckets than one result holds, before reading', async () => {
  const harness = createHarness();
  await assert.rejects(
    execute(byName(harness.tools, 'telemetry_query_history'), {
      deviceId: chillerDeviceId,
      pointKeys: ['chw_supply_temp'],
      startDate: '2026-10-15',
      endDate: '2026-10-16',
      granularity: 'quarter_hour',
    }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID' && /粒度|granularity/u.test(error.message),
  );
  assert.equal(harness.historyInputs.length, 0);
});

test('energy_query_series points carry Site-local times', async () => {
  const harness = createHarness();
  const result = await execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-10-15', endDate: '2026-10-15' });
  // The owner's first bucket starts at the requested instant, 10-15 00:00 at the Site.
  assert.deepEqual(result.points, [{ siteTime: '2026-10-15 00:00', energyKWh: 12 }]);
});

test('energy_query_series hands the model the owner\'s estimated cost and the energy it could not price', async () => {
  const from = '2026-10-16T01:00:00.000Z';
  const to = '2026-10-16T03:00:00.000Z';
  const harness = createHarness({
    energyFor: async (input) => ownerResult(input.request, 'telemetry-query-service', 'priced-r1', 'GOOD', 'telemetry-query-service:energy-series/v1', {
      ...energyPayload({ from, to }),
      points: [
        { periodStart: from, periodEnd: '2026-10-16T02:00:00.000Z', energyKWh: 100, cost: { amountCNY: 103, unpricedKWh: 0, periods: [{ periodCode: 'PEAK', energyKWh: 100, amountCNY: 103 }] } },
        { periodStart: '2026-10-16T02:00:00.000Z', periodEnd: to, energyKWh: 50, cost: { amountCNY: 18.6, unpricedKWh: 20, periods: [{ periodCode: 'FLAT', energyKWh: 30, amountCNY: 18.6 }] } },
      ],
    }),
  });

  const result = await execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-10-15', endDate: '2026-10-15' });

  assert.deepEqual(result.points, [
    { siteTime: '2026-10-15 18:00', energyKWh: 100, estimatedCostCNY: 103, unpricedKWh: 0 },
    { siteTime: '2026-10-15 19:00', energyKWh: 50, estimatedCostCNY: 18.6, unpricedKWh: 20 },
  ]);
  assert.equal(result.estimatedCostCNY, 121.6);
  assert.equal(result.unpricedKWh, 20);
});

test('energy and history Tools take Site-local dates with both ends included and read those whole days', async () => {
  const harness = createHarness();
  await execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-10-13', endDate: '2026-10-16', granularity: 'day' });
  await execute(byName(harness.tools, 'energy_by_equipment'), { startDate: '2026-10-15', endDate: '2026-10-15', granularity: 'day' });
  await execute(byName(harness.tools, 'energy_compare_periods'), {
    baselineStartDate: '2026-10-06', baselineEndDate: '2026-10-09', currentStartDate: '2026-10-13', currentEndDate: '2026-10-16', granularity: 'day',
  });
  await execute(byName(harness.tools, 'telemetry_query_history'), { deviceId: chillerDeviceId, pointKeys: ['chw_supply_temp'], startDate: '2026-10-16', endDate: '2026-10-16' });

  // America/Phoenix is UTC−7: 「10月13日到16日」 is 10-13 07:00Z up to 10-17 07:00Z, all four days.
  assert.deepEqual(harness.energyInputs.map(({ request }) => [request.input.from, request.input.to]), [
    ['2026-10-13T07:00:00.000Z', '2026-10-17T07:00:00.000Z'],
    ['2026-10-15T07:00:00.000Z', '2026-10-16T07:00:00.000Z'],
    ['2026-10-06T07:00:00.000Z', '2026-10-10T07:00:00.000Z'],
    ['2026-10-13T07:00:00.000Z', '2026-10-17T07:00:00.000Z'],
  ]);
  assert.deepEqual([harness.historyInputs[0].request.input.from, harness.historyInputs[0].request.input.to], ['2026-10-16T07:00:00.000Z', '2026-10-17T07:00:00.000Z']);
});

test('dates out of order, longer than the budget or given as instants are refused before any read', async () => {
  const harness = createHarness();
  const series = byName(harness.tools, 'energy_query_series');
  for (const args of [
    { startDate: '2026-10-16', endDate: '2026-10-13' },
    { startDate: '2026-09-01', endDate: '2026-10-16' },
    { startDate: '2026-10-32', endDate: '2026-10-33' },
    { from: '2026-10-13T16:00:00.000Z', to: '2026-10-16T16:00:00.000Z' },
  ]) {
    await assert.rejects(() => execute(series, args), (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID', JSON.stringify(args));
  }
  assert.equal(harness.energyInputs.length, 0);
  assert.equal(harness.registryInputs.length, 0);
  // The refusal tells the model what to change.
  await assert.rejects(() => execute(series, { startDate: '2026-10-16', endDate: '2026-10-13' }), (error) => /最后一天早于第一天/u.test(error.hint));
  await assert.rejects(() => execute(series, { startDate: '2026-09-01', endDate: '2026-10-16' }), (error) => /每次最多读 31 天/u.test(error.hint));
  await assert.rejects(() => execute(series, { from: '2026-10-13T16:00:00.000Z', to: '2026-10-16T16:00:00.000Z' }), (error) => /startDate 和 endDate/u.test(error.hint));
});

test('every read Tool answers in the operator\'s words, without internal codes, owners or revisions', async () => {
  const partial = createHarness({
    energyFor: async (input) => ownerResult(input.request, 'telemetry-query-service', 'energy-partial-1', 'UNCERTAIN', 'telemetry-query-service:energy-series/v1', {
      ...energyPayload({ from: input.request.input.from, to: input.request.input.to, energyKWh: 40, qualitySummary: { valid: 3, suspect: 1, invalid: 0 } }),
      metadata: { ...energyPayload({ from: input.request.input.from, to: input.request.input.to }).metadata, partial: true, dataWatermark: '2026-10-16T01:30:00.000Z', qualitySummary: { valid: 3, suspect: 1, invalid: 0 } },
    }),
  });
  const harness = createHarness();
  const run = (tools, name, args) => execute(byName(tools, name), args);
  const results = {
    assets: await run(harness.tools, 'assets_list', {}),
    equipment: await run(harness.tools, 'equipment_list', {}),
    current: await run(harness.tools, 'telemetry_get_current', { deviceId, pointKeys: ['condenserPressureKPa', 'leavingWaterTempC'] }),
    history: await run(harness.tools, 'telemetry_query_history', { deviceId: chillerDeviceId, pointKeys: ['chw_supply_temp', 'active_energy_total'], startDate: '2026-10-16', endDate: '2026-10-16' }),
    series: await run(harness.tools, 'energy_query_series', { startDate: '2026-10-15', endDate: '2026-10-15', energyType: 'cooling' }),
    partialSeries: await run(partial.tools, 'energy_query_series', { startDate: '2026-10-15', endDate: '2026-10-15' }),
    compare: await run(harness.tools, 'energy_compare_periods', { baselineStartDate: '2026-10-08', baselineEndDate: '2026-10-08', currentStartDate: '2026-10-15', currentEndDate: '2026-10-15' }),
    machines: await run(harness.tools, 'energy_by_equipment', { startDate: '2026-10-15', endDate: '2026-10-15' }),
  };
  const internal = /\b(?:COMPLETE|PARTIAL|GOOD|UNCERTAIN|STALE|AVAILABLE|CURRENT|DEGRADED|ONLINE|PRESENT|MISSING|FRESH|NEVER_OBSERVED|TELEMETRY|COUNTER|CHILLER|HVAC_POWER_METER|COOLING_TOWER|ACTIVE|VALID_ONLY|COMPARABLE|electricity|cooling|hour|day)\b|provenance|revision|qualitySummary|telemetry-(?:query|runtime)-service|registry|America\/Phoenix/u;
  for (const [name, result] of Object.entries(results)) assert.doesNotMatch(JSON.stringify(result), internal, name);

  assert.equal(results.series.dataStatus, '完整');
  assert.equal(results.series.query.energy, '供冷');
  // 01:30Z is 18:30 the day before at the Site (UTC−7).
  assert.equal(results.partialSeries.dataStatus, '不完整：数据截至 2026-10-15 18:30');
  assert.equal(results.partialSeries.readingNote, '部分读数可疑或无效，没有计入');
  assert.equal(results.compare.comparison.status, '可以比较');
  assert.deepEqual(results.current.values.map(({ key, state }) => [key, state]), [['condenserPressureKPa', '读数过期'], ['leavingWaterTempC', '没有读数']]);
  assert.equal(results.current.values[0].siteTime, '2026-07-29 23:20');
  assert.deepEqual(results.history.series.map(({ kind }) => kind), ['读数', '累计量']);
  assert.deepEqual(results.equipment.devices[1].points.map(({ kind }) => kind), ['读数', '累计量']);
  assert.equal(results.assets.note, '只列出前 2 个，共 3 个');
});

test('a semantic query names machines and grouping values the way the pages do', async () => {
  const harness = createHarness({
    semanticFor: () => ({
      schemaVersion: 1,
      columns: [
        { name: 'period_end', title: '时段', kind: 'time' },
        { name: 'equipment_id', title: '设备', kind: 'dimension' },
        { name: 'energy_type', title: '能源类型', kind: 'dimension' },
        { name: 'energy_valid_kwh', title: '能耗', kind: 'measure' },
      ],
      rows: [
        ['2026-10-15T07:00:00Z', meterDeviceId, 'electricity', 879],
        ['2026-10-15T07:00:00Z', '0198a36e-4c9d-7b5a-8f2d-4c5e6f7081ff', 'electricity', 2],
      ],
      metadata: { datasetRevision: 'semantic:1', partial: false },
    }),
  });
  const result = await execute(byName(harness.tools, 'energy_semantic_query'), {
    measures: ['equipment_energy.energy_valid_kwh'],
    dimensions: ['equipment_energy.equipment_id', 'equipment_energy.energy_type'],
    granularity: 'day',
    startDate: '2026-10-15',
    endDate: '2026-10-15',
  });
  assert.deepEqual(result.rows, [
    { siteTime: '2026-10-15 00:00', 设备: 'C 机组电表', 能源类型: '用电', 能耗: 879 },
    { siteTime: '2026-10-15 00:00', 设备: '未登记的设备', 能源类型: '用电', 能耗: 2 },
  ]);
  assert.equal(result.dataStatus, '完整');
});

test('Tariff periods read as the pages show them, energy no Tariff covers as unpriced', async () => {
  const harness = createHarness({
    semanticFor: () => ({
      schemaVersion: 1,
      columns: [
        { name: 'period_end', title: '时段', kind: 'time' },
        { name: 'tariff_period', title: '电价时段', kind: 'dimension' },
        { name: 'cost_valid_cny', title: '电费', kind: 'measure' },
      ],
      rows: [['2026-10-15T07:00:00Z', 'SUPER_PEAK', 12], ['2026-10-15T07:00:00Z', 'VALLEY', 3], ['2026-10-15T07:00:00Z', '', null]],
      metadata: { datasetRevision: 'semantic:1', partial: false },
    }),
  });
  const result = await execute(byName(harness.tools, 'energy_semantic_query'), {
    measures: ['site_energy_cost.cost_valid_cny'], dimensions: ['site_energy_cost.tariff_period'], granularity: 'day', startDate: '2026-10-15', endDate: '2026-10-15',
  });
  assert.deepEqual(result.rows.map((row) => row['电价时段']), ['尖峰', '谷', '未计价']);
});

test('one semantic query reads the Site\'s energy and its estimated cost together, one row per period', async () => {
  const views = {
    site_energy: {
      columns: [{ name: 'period_end', title: '时段', kind: 'time' }, { name: 'electricity_valid_kwh', title: '用电量', kind: 'measure' }],
      rows: [['2026-10-15T07:00:00Z', 1634], ['2026-10-16T07:00:00Z', 1499]],
      metadata: { datasetRevision: 'semantic:1', partial: true, dataWatermark: '2026-10-16T09:00:00Z' },
    },
    site_energy_cost: {
      columns: [{ name: 'period_end', title: '时段', kind: 'time' }, { name: 'cost_valid_cny', title: '电费', kind: 'measure' }],
      rows: [['2026-10-16T07:00:00Z', 1157.51], ['2026-10-15T07:00:00Z', 1212.51], ['2026-10-17T07:00:00Z', 30]],
      metadata: { datasetRevision: 'semantic:1', partial: true, dataWatermark: '2026-10-17T09:00:00Z' },
    },
  };
  const harness = createHarness({ semanticFor: (input) => ({ schemaVersion: 1, ...views[input.request.input.view] }) });
  const result = await execute(byName(harness.tools, 'energy_semantic_query'), {
    measures: ['site_energy.electricity_valid_kwh', 'site_energy_cost.cost_valid_cny'], granularity: 'day', startDate: '2026-10-15', endDate: '2026-10-17',
  });
  assert.deepEqual(harness.semanticInputs.map((input) => [input.request.input.view, input.request.input.measures]), [
    ['site_energy', ['electricity_valid_kwh']],
    ['site_energy_cost', ['cost_valid_cny']],
  ]);
  assert.deepEqual(result.columns, ['时段', '用电量', '电费']);
  assert.deepEqual(result.rows, [
    { siteTime: '2026-10-15 00:00', 用电量: 1634, 电费: 1212.51 },
    { siteTime: '2026-10-16 00:00', 用电量: 1499, 电费: 1157.51 },
    // A period one view has no row for is empty in that view's column, not left out.
    { siteTime: '2026-10-17 00:00', 用电量: null, 电费: 30 },
  ]);
  // Both views stop early; the answer reaches only as far as the one that stops first.
  assert.equal(result.dataStatus, '不完整：数据截至 2026-10-16 02:00');
});

test('a semantic query that groups or mixes other views across views is refused before reading, saying what works', async () => {
  const harness = createHarness({ semanticFor: () => { throw new Error('not read'); } });
  for (const args of [
    { measures: ['site_energy.electricity_valid_kwh', 'site_energy_cost.cost_valid_cny'], dimensions: ['site_energy_cost.tariff_period'] },
    { measures: ['site_energy.electricity_valid_kwh', 'equipment_energy.energy_valid_kwh'] },
  ]) {
    await assert.rejects(
      () => execute(byName(harness.tools, 'energy_semantic_query'), { ...args, granularity: 'day', startDate: '2026-10-15', endDate: '2026-10-15' }),
      (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID' && /站点能耗和估算电费/u.test(error.hint),
    );
  }
  assert.equal(harness.semanticInputs.length, 0);
});

test('grouping a semantic query by device needs the right to read the devices, and says so before reading', async () => {
  const capabilities = ['site.read', 'analytics.energy-series.read'];
  const harness = createHarness({ capabilities, semanticFor: () => { throw new Error('not read'); } });
  await assert.rejects(
    () => execute(byName(harness.tools, 'energy_semantic_query'), {
      measures: ['equipment_energy.energy_valid_kwh'], dimensions: ['equipment_energy.equipment_id'], granularity: 'day', startDate: '2026-10-15', endDate: '2026-10-15',
    }, { ...context, capabilities }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID' && /设备/u.test(error.hint),
  );
  assert.equal(harness.semanticInputs.length, 0);
});

test('a Site whose clocks skip midnight still reads its whole local day', async () => {
  // America/Santiago moves from 00:00 to 01:00 on 2026-09-06: that day starts at 01:00 (UTC−3).
  const harness = createHarness({ timezone: 'America/Santiago' });
  await execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-09-06', endDate: '2026-09-06', granularity: 'day' });
  assert.deepEqual([harness.energyInputs[0].request.input.from, harness.energyInputs[0].request.input.to], ['2026-09-06T04:00:00.000Z', '2026-09-07T03:00:00.000Z']);
});

test('a comparison says whether the data was incomplete or its readings were doubtful', async () => {
  const incomplete = createHarness({
    energyFor: async (input) => ownerResult(input.request, 'telemetry-query-service', 'e', 'UNCERTAIN', 'p', energyPayload({ from: input.request.input.from, to: input.request.input.to, partial: true })),
  });
  const doubtful = createHarness({
    energyFor: async (input) => ownerResult(input.request, 'telemetry-query-service', 'e', 'UNCERTAIN', 'p', energyPayload({ from: input.request.input.from, to: input.request.input.to, qualitySummary: { valid: 2, suspect: 1, invalid: 0 } })),
  });
  const args = { baselineStartDate: '2026-10-08', baselineEndDate: '2026-10-08', currentStartDate: '2026-10-15', currentEndDate: '2026-10-15' };
  assert.equal((await execute(byName(incomplete.tools, 'energy_compare_periods'), args)).comparison.status, '数据不完整，不能比较');
  assert.equal((await execute(byName(doubtful.tools, 'energy_compare_periods'), args)).comparison.status, '有读数可疑，不能比较');
});

test('a result too large for one answer tells the model how to narrow it', async () => {
  const harness = createHarness({
    energyFor: async (input) => ownerResult(input.request, 'telemetry-query-service', 'e', 'GOOD', 'p', {
      ...energyPayload({ from: input.request.input.from, to: input.request.input.to }),
      points: Array.from({ length: 49 }, (_, index) => ({ periodStart: new Date(Date.parse(input.request.input.from) + index * 3_600_000).toISOString(), periodEnd: input.request.input.to, energyKWh: 1 })),
    }),
  });
  await assert.rejects(
    () => execute(byName(harness.tools, 'energy_query_series'), { startDate: '2026-10-14', endDate: '2026-10-15', granularity: 'hour' }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_RESULT_TOO_LARGE' && /缩短|粒度/u.test(error.hint),
  );
});

// #594: alarms as the 告警 page shows them — severity and state in words, Site-local times, each
// machine by its device name, no identifiers.
const alarmItem = (overrides = {}) => ({
  deviceId: chillerDeviceId, title: '冷冻水出水温度过高', summary: '出水温度高于 12 ℃ 持续 10 分钟',
  currentSeverity: 'CRITICAL', acknowledged: false, assigned: false, suppressed: false, occurrenceCount: 3,
  firstOccurredAt: '2026-10-17T12:00:00.000Z', lastOccurredAt: '2026-10-17T13:10:00.000Z',
  timeline: [{ operation: 'PUBLISH', occurredAt: '2026-10-17T12:00:00.000Z' }],
  ...overrides,
});

test('alarm_list reads the Site’s active alarms in the operator’s words, each machine by its device name', async () => {
  const harness = createHarness({
    alarmsFor: () => ({
      schemaVersion: 1,
      items: [
        alarmItem(),
        alarmItem({ deviceId: undefined, title: '网关上报中断', summary: '超过 15 分钟没有收到网关数据', currentSeverity: 'WARNING', occurrenceCount: 1, acknowledged: true, assigned: true, timeline: [{ operation: 'PUBLISH', occurredAt: '2026-10-17T12:00:00.000Z' }, { operation: 'ACKNOWLEDGE', occurredAt: '2026-10-17T13:20:00.000Z' }] }),
      ],
      hasMore: false,
    }),
  });
  const result = await execute(byName(harness.tools, 'alarm_list'), {});

  assert.equal(harness.alarmInputs[0].request.tool, 'alarm.listAlarms');
  assert.deepEqual(harness.alarmInputs[0].request.input, { siteId, condition: 'ACTIVE', limit: 50 });
  assert.equal(harness.authorizationInputs.find(({ request }) => request.tool === 'alarm.listAlarms').request.input.siteId, siteId);
  assert.equal(result.status, '正在发生');
  assert.deepEqual(result.alarms, [
    { title: '冷冻水出水温度过高', summary: '出水温度高于 12 ℃ 持续 10 分钟', severity: '紧急', device: 'C 机组控制器', since: '2026-10-17 05:00', latest: '2026-10-17 06:10', times: 3, handled: '未确认', history: ['2026-10-17 05:00 触发'] },
    { title: '网关上报中断', summary: '超过 15 分钟没有收到网关数据', severity: '预警', since: '2026-10-17 05:00', latest: '2026-10-17 06:10', times: 1, handled: '已确认', claimed: '有人认领', history: ['2026-10-17 05:00 触发', '2026-10-17 06:20 确认'] },
  ]);
  assert.doesNotMatch(JSON.stringify(result), /[0-9a-f]{8}-[0-9a-f]{4}-|CRITICAL|WARNING|ACTIVE|SITE_RULE/u);
});

test('alarm_list says plainly when nothing is active, and reads recently cleared alarms when asked', async () => {
  const empty = createHarness({ alarmsFor: () => ({ items: [], hasMore: false }) });
  const none = await execute(byName(empty.tools, 'alarm_list'), {});
  assert.deepEqual(none.alarms, []);
  assert.equal(none.note, '当前没有正在发生的告警');

  const cleared = createHarness({
    alarmsFor: () => ({ items: [alarmItem({ clearedAt: '2026-10-17T13:30:00.000Z' })], hasMore: true }),
  });
  const result = await execute(byName(cleared.tools, 'alarm_list'), { status: 'cleared' });
  assert.equal(cleared.alarmInputs[0].request.input.condition, 'CLEARED');
  assert.equal(result.status, '已恢复');
  assert.equal(result.alarms[0].clearedAt, '2026-10-17 06:30');
  assert.equal(result.note, '只列出最近 50 条，还有更多');
});

test('alarm_list refuses other arguments and tells the model what it takes', async () => {
  const harness = createHarness({ alarmsFor: () => { throw new Error('not read'); } });
  await assert.rejects(
    () => execute(byName(harness.tools, 'alarm_list'), { status: 'all', siteId }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID' && /active|cleared/u.test(error.hint),
  );
  assert.equal(harness.alarmInputs.length, 0);
});

// #595: diagnosis findings and work orders as the 诊断 and 工单 pages name them, no identifiers.
test('fdd_findings reads the Site’s findings in the 诊断 page’s words, each piece of equipment by name', async () => {
  const harness = createHarness({
    findingsFor: () => ({
      items: [{
        assetId: 'asset-1', findingType: 'CHILLED_WATER_LOW_DELTA_T',
        evaluationFrom: '2026-10-17T11:00:00.000Z', evaluationTo: '2026-10-17T12:00:00.000Z',
        observedValue: 3.2, thresholdValue: 5, valueUnit: 'K', linkedToAlarm: true, linkedToWorkOrder: false, createdAt: '2026-10-17T12:01:00.000Z',
      }],
    }),
  });
  const result = await execute(byName(harness.tools, 'fdd_findings'), {});

  assert.deepEqual(harness.fddInputs[0].request.input, { siteId, limit: 50 });
  assert.equal(harness.authorizationInputs.find(({ request }) => request.tool === 'fdd.listFindings').request.input.siteId, siteId);
  assert.deepEqual(result.findings, [{
    rule: '冷冻水低温差', equipment: 'Chiller 1', period: '2026-10-17 04:00 至 2026-10-17 05:00',
    measured: '冷冻水供回水温差 3.2 K，阈值 5 K', deviation: '严重偏离', reportedAt: '2026-10-17 05:01', alarm: '已转成告警',
  }]);
  assert.doesNotMatch(JSON.stringify(result), /CHILLED_WATER|asset-1|[0-9a-f]{8}-[0-9a-f]{4}-/u);

  const none = createHarness({ findingsFor: () => ({ items: [] }) });
  assert.equal((await execute(byName(none.tools, 'fdd_findings'), {})).note, '最近没有故障诊断结果');
});

test('work_order_list reads the Site’s unfinished work orders in the 工单 page’s words, the source device by name', async () => {
  const workOrder = (overrides) => ({
    title: '检查 2 号冷冻水泵', description: '低温差持续，检查水泵频率', priority: 'HIGH', status: 'OPEN', assigned: false,
    fromAlarm: true, createdAt: '2026-10-17T12:00:00.000Z', updatedAt: '2026-10-17T12:30:00.000Z', ...overrides,
  });
  const harness = createHarness({
    workOrdersFor: () => ({
      items: [
        workOrder({}),
        workOrder({ title: '更换温度传感器', status: 'IN_PROGRESS', assigned: true, dueAt: '2026-10-18T09:00:00.000Z', fromAlarm: false, assetId: 'asset-1' }),
        workOrder({ title: '补做巡检', status: 'BLOCKED', fromAlarm: false }),
        workOrder({ title: '清洗过滤器', status: 'COMPLETED' }),
      ],
      hasMore: false,
    }),
  });
  const result = await execute(byName(harness.tools, 'work_order_list'), {});

  assert.deepEqual(harness.workOrderInputs[0].request.input, { siteId, limit: 100 });
  assert.deepEqual(result.workOrders, [
    { title: '检查 2 号冷冻水泵', description: '低温差持续，检查水泵频率', priority: '高', status: '待处理', assigned: '未指派', from: '来自告警', createdAt: '2026-10-17 05:00', updatedAt: '2026-10-17 05:30' },
    { title: '更换温度传感器', description: '低温差持续，检查水泵频率', priority: '高', status: '处理中', assigned: '已指派', dueAt: '2026-10-18 02:00', from: '来自设备 Chiller 1', createdAt: '2026-10-17 05:00', updatedAt: '2026-10-17 05:30' },
    { title: '补做巡检', description: '低温差持续，检查水泵频率', priority: '高', status: '受阻', assigned: '未指派', from: '人工创建', createdAt: '2026-10-17 05:00', updatedAt: '2026-10-17 05:30' },
  ]);
  assert.doesNotMatch(JSON.stringify(result), /asset-1|IN_PROGRESS|BLOCKED/u);

  const none = createHarness({ workOrdersFor: () => ({ items: [], hasMore: false }) });
  assert.equal((await execute(byName(none.tools, 'work_order_list'), {})).note, '现在没有还没处理完的工单');
  // The newest 100 all finished does not mean none is left: older ones were not read.
  const unread = createHarness({ workOrdersFor: () => ({ items: [workOrder({ status: 'COMPLETED' })], hasMore: true }) });
  assert.equal((await execute(byName(unread.tools, 'work_order_list'), {})).note, '最近更新的 100 张工单都已处理完，更早的没有看');
  await assert.rejects(
    () => execute(byName(none.tools, 'work_order_list'), { status: 'closed' }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID',
  );
});

// #640: a card may show a chart of one read; the read's own Tool draws it from what the model read.
test('energy_query_series draws hours as a line and days as bars, and nothing without points', async () => {
  const hour = (start, end, energyKWh) => ({ periodStart: start, periodEnd: end, energyKWh });
  const seriesHarness = (points, granularity) => createHarness({
    energyFor: async (input) => ownerResult(input.request, 'telemetry-query-service', 'energy-chart-1', 'GOOD', 'telemetry-query-service:energy-series/v1', {
      ...energyPayload({ from: points[0]?.periodStart ?? '2026-08-01T07:00:00.000Z', to: points.at(-1)?.periodEnd ?? '2026-08-01T08:00:00.000Z' }),
      points,
      metadata: { ...energyPayload({ from: '2026-08-01T07:00:00.000Z', to: '2026-08-03T07:00:00.000Z' }).metadata, requestedGranularity: granularity, actualGranularity: granularity },
    }),
  });

  // 07:00 UTC is midnight in America/Phoenix.
  const hourly = seriesHarness([hour('2026-08-01T07:00:00.000Z', '2026-08-01T08:00:00.000Z', 12), hour('2026-08-01T08:00:00.000Z', '2026-08-01T09:00:00.000Z', 15.5)], 'hour');
  const hours = byName(hourly.tools, 'energy_query_series');
  const hoursRead = await execute(hours, { startDate: '2026-08-01', endDate: '2026-08-01', granularity: 'hour', qualityPolicy: 'VALID_ONLY' });
  assert.deepEqual(hours.chart(hoursRead), {
    kind: 'LINE', title: '8月1日每小时用电', unit: 'kWh', categories: ['00:00', '01:00'], series: [{ name: '用电', values: [12, 15.5] }],
  });

  const daily = seriesHarness([hour('2026-08-01T07:00:00.000Z', '2026-08-02T07:00:00.000Z', 300), hour('2026-08-02T07:00:00.000Z', '2026-08-03T07:00:00.000Z', 280.25)], 'day');
  const days = byName(daily.tools, 'energy_query_series');
  const daysRead = await execute(days, { energyType: 'cooling', startDate: '2026-08-01', endDate: '2026-08-02', granularity: 'day', qualityPolicy: 'VALID_ONLY' });
  assert.deepEqual(days.chart(daysRead), {
    kind: 'BAR', title: '8月1日–8月2日每天供冷', unit: 'kWh', categories: ['8/1', '8/2'], series: [{ name: '供冷', values: [300, 280.25] }],
  });

  const empty = seriesHarness([], 'hour');
  const nothing = byName(empty.tools, 'energy_query_series');
  assert.equal(nothing.chart(await execute(nothing, { startDate: '2026-08-01', endDate: '2026-08-01', granularity: 'hour', qualityPolicy: 'VALID_ONLY' })), null);
});
