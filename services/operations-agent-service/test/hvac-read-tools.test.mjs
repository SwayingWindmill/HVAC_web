import assert from 'node:assert/strict';
import test from 'node:test';

import { AgentToolError } from '../dist/agent/index.js';
import { createHvacReadTools } from '../dist/tools/index.js';

const tenantId = 'tenant-1';
const siteId = 'site-1';

const context = Object.freeze({
  tenantId,
  siteId,
  principalId: 'principal-1',
  capabilities: Object.freeze(['site.read', 'asset.list', 'analytics.energy-series.read']),
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

const createHarness = ({ energyFor, registryFailure } = {}) => {
  const authorizationInputs = [];
  const registryInputs = [];
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
      return ownerResult(input.request, 'registry', 'registry-assets:9', 'GOOD', 'registry:assets/v1', { kind: 'SITE_ASSETS', siteId, assets });
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
    energyAnalyticsReader,
    limits: { maxAssets: 2, maxEnergyPoints: 48, maxEnergyRangeMs: 31 * 24 * 60 * 60 * 1000, maxResultBytes: 32_768, timeoutMs: 2_000 },
  });

  return { tools, authorizationInputs, registryInputs, energyInputs, maxActiveEnergyReads: () => maxActiveEnergyReads };
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

test('first HVAC catalog contains only the four capability-filtered READ Tools', () => {
  const full = createHarness().tools.map((tool) => tool.definition.name);
  assert.deepEqual(full, ['site.get_context', 'assets.list', 'energy.query_series', 'energy.compare_periods']);

  const limited = createHvacReadTools({
    capabilities: ['site.read'],
    authorization,
    toolAuthorizationReader: { authorize: async () => ({ delegationGrant: 'grant' }) },
    registryReader: { read: async () => { throw new Error('not called'); } },
    energyAnalyticsReader: { read: async () => { throw new Error('not called'); } },
  });
  assert.deepEqual(limited.map((tool) => tool.definition.name), ['site.get_context']);
  assert.equal(limited.every((tool) => tool.definition.executionMode === 'parallel' && tool.definition.replayPolicy === 'safe'), true);
});

test('model arguments cannot select trusted identity or infrastructure and direct invocation still rechecks capability', async () => {
  const harness = createHarness();
  const siteTool = byName(harness.tools, 'site.get_context');
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
    () => execute(byName(harness.tools, 'energy.query_series'), {
      from: '2026-08-01T00:00:00.000Z',
      to: '2026-08-01T01:00:00.000Z',
      tenantId: 'other-tenant',
    }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_ARGUMENTS_INVALID',
  );
  assert.equal(harness.authorizationInputs.length, 0);
  assert.equal(harness.registryInputs.length, 0);

  await assert.rejects(
    () => execute(siteTool, {}, { ...context, capabilities: [] }),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_UNAUTHORIZED',
  );
  assert.equal(harness.authorizationInputs.length, 0);
});

test('site and asset Tools reauthorize then call Registry with trusted scope and bounded output', async () => {
  const harness = createHarness();
  const siteResult = await execute(byName(harness.tools, 'site.get_context'), {});
  const assetsResult = await execute(byName(harness.tools, 'assets.list'), {});

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

test('energy.query_series injects authoritative timezone and keeps partial data distinct from zero', async () => {
  const from = '2026-08-01T00:00:00.000Z';
  const to = '2026-08-01T01:00:00.000Z';
  const harness = createHarness({
    energyFor: async (input) => {
      const payload = energyPayload({ from, to, partial: true, energyKWh: null, qualitySummary: { valid: 0, suspect: 1, invalid: 0 } });
      return ownerResult(input.request, 'telemetry-query-service', 'energy-partial-1', 'UNCERTAIN', 'telemetry-query-service:energy-series/v1', payload);
    },
  });

  const result = await execute(byName(harness.tools, 'energy.query_series'), { from, to, granularity: 'hour', qualityPolicy: 'VALID_ONLY' });
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
  const zero = await execute(byName(zeroHarness.tools, 'energy.query_series'), { from, to });
  assert.equal(zero.totalKWh, 0);
  assert.equal(zero.completeness, 'COMPLETE');
});

test('energy.compare_periods executes independent Energy reads in parallel and returns bounded summaries', async () => {
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

  const result = await execute(byName(harness.tools, 'energy.compare_periods'), {
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
    () => execute(byName(harness.tools, 'site.get_context'), {}),
    (error) => error instanceof AgentToolError && error.code === 'TOOL_OWNER_UNAVAILABLE' && !error.message.includes('secret upstream detail'),
  );
});
