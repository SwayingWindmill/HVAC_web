import assert from 'node:assert/strict';
import test from 'node:test';

import {
  DEFAULT_RUN_RESOURCE_BUDGET_POLICY,
  OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV,
  createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator,
  createOperationsAgentFindingModelRuntimeFromEnvironment,
} from '../dist/index.js';
import { createFakeFindingSynthesizer } from '../dist/model/index.js';
import { createFakeOperationsAgentEnvironment } from './support/fake-operations-agent-environment.mjs';

const model = 'gpt-5.5-2026-07-15';
const accessVariable = ['OPENAI', 'API', 'KEY'].join('_');

const enabledEnvironment = (overrides = {}) => ({
  [OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV]: 'openai',
  [OPERATIONS_AGENT_FINDING_MODEL_ENV]: model,
  [OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV]: model,
  [accessVariable]: 'test-server-access-value',
  ...overrides,
});

const organizationId = '0198f5c0-7c00-7000-8000-000000000001';
const siteId = '0198f5c0-7c00-7000-8000-000000000002';
const investigationId = 'investigation-001';
const scope = Object.freeze({ organizationId, siteId, equipmentId: null, deviceId: null });

const energySeries = (request, energyPerHour) => {
  const from = Date.parse(request.input.from);
  const to = Date.parse(request.input.to);
  const hours = Math.round((to - from) / 3_600_000);
  return {
    schemaVersion: 1,
    points: Array.from({ length: hours }, (_value, index) => ({
      periodStart: new Date(from + index * 3_600_000).toISOString(),
      periodEnd: new Date(from + (index + 1) * 3_600_000).toISOString(),
      energyKWh: energyPerHour,
    })),
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      dataWatermark: new Date(to + 300_000).toISOString(),
      aggregateWatermark: new Date(to + 300_000).toISOString(),
      datasetRevision: 'energy-dataset-r17',
      partial: false,
      qualitySummary: { valid: hours, suspect: 0, invalid: 0 },
    },
  };
};

const ownerResultFactory = async (request) => {
  if (request.tool === 'registry.getSite') {
    return {
      requestId: request.requestId,
      owner: 'registry',
      scope,
      revision: 'registry-site:17',
      quality: 'GOOD',
      provenance: 'platform-core-service:registry-site/v1',
      payload: {
        kind: 'SITE',
        site: { id: siteId, owningOrganizationId: organizationId, timezone: 'Asia/Tokyo' },
      },
    };
  }
  if (request.tool === 'registry.listSiteEquipment') {
    return {
      requestId: request.requestId,
      owner: 'registry',
      scope,
      revision: 'registry-equipment:29',
      quality: 'GOOD',
      provenance: 'platform-core-service:registry-site-equipment/v1',
      payload: {
        kind: 'SITE_EQUIPMENT',
        siteId,
        equipment: [{ id: '0198f5c0-7c00-7000-8000-000000000010' }],
      },
    };
  }
  const target = request.requestId.endsWith('energy-target');
  return {
    requestId: request.requestId,
    owner: 'telemetry-query-service',
    scope,
    revision: 'energy-dataset-r17',
    quality: 'GOOD',
    provenance: 'telemetry-query-service:energy-series/v1',
    payload: energySeries(request, target ? 155 : 125),
  };
};

const createEnvironment = () => createFakeOperationsAgentEnvironment({
  scope,
  initialTime: Date.parse('2026-07-31T00:00:00.000Z'),
  leaseDurationMs: 86_400_000,
  ownerDelayMs: 1,
  ownerResultFactory,
  runtimeSteps: [{
    stepId: 'collect-registry-context',
    plan: {
      batches: [{
        batchId: 'registry-context',
        requests: [{
          requestId: `${investigationId}:registry-site`,
          tool: 'registry.getSite',
          input: { siteId },
        }, {
          requestId: `${investigationId}:registry-equipment`,
          tool: 'registry.listSiteEquipment',
          input: { siteId },
        }],
      }],
    },
    checkpointPosition: 'complete',
  }],
});

test('Finding model runtime is disabled by default and rejects partial disabled configuration', () => {
  const disabled = createOperationsAgentFindingModelRuntimeFromEnvironment({ environment: {} });
  assert.deepEqual(disabled, {
    status: 'DISABLED',
    provider: null,
    model: null,
    findingSynthesisTimeoutMs: null,
    maximumOutputTokens: null,
    findingSynthesizer: null,
  });

  assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: { [OPERATIONS_AGENT_FINDING_MODEL_ENV]: model },
  }), { name: 'OperationsAgentFindingModelConfigurationError' });
  assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: {
      [OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV]: 'disabled',
      [OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV]: model,
    },
  }), { name: 'OperationsAgentFindingModelConfigurationError' });
});

test('enabled Finding model configuration requires supported Provider, access, and exact allowlist', () => {
  assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: { [OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV]: 'other' },
  }), { name: 'OperationsAgentFindingModelConfigurationError' });
  assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: {
      [OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV]: 'openai',
      [OPERATIONS_AGENT_FINDING_MODEL_ENV]: model,
      [OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV]: model,
    },
  }), { name: 'OperationsAgentFindingModelConfigurationError' });
  assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: enabledEnvironment({
      [OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV]: 'gpt-5.5-2026-08-01',
    }),
  }), { name: 'OperationsAgentFindingModelConfigurationError' });
  assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: enabledEnvironment({
      [OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV]: `${model},${model}`,
    }),
  }), { name: 'OperationsAgentFindingModelConfigurationError' });
  assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: enabledEnvironment(),
  }), { name: 'OperationsAgentFindingModelConfigurationError' });
});

test('enabled Finding model runtime applies bounded defaults and passes only non-sensitive configuration', () => {
  const calls = [];
  const synthesizer = createFakeFindingSynthesizer();
  const runtime = createOperationsAgentFindingModelRuntimeFromEnvironment({
    environment: enabledEnvironment(),
    createOpenAiSynthesizer(options) {
      calls.push(options);
      return synthesizer;
    },
  });

  assert.equal(runtime.status, 'ENABLED');
  assert.equal(runtime.provider, 'openai');
  assert.equal(runtime.model, model);
  assert.equal(runtime.findingSynthesisTimeoutMs, 5_000);
  assert.equal(runtime.maximumOutputTokens, 512);
  assert.equal(runtime.findingSynthesizer, synthesizer);
  assert.deepEqual(calls, [{
    model,
    requestTimeoutMs: 5_000,
    maxOutputTokens: 512,
  }]);
  assert.equal(JSON.stringify(calls).includes('test-server-access-value'), false);
});

test('Finding model bounds fail closed before Provider construction', () => {
  let factoryCalls = 0;
  const factory = () => {
    factoryCalls += 1;
    return createFakeFindingSynthesizer();
  };
  const invalid = [{
    [OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV]: '99',
  }, {
    [OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV]: '30001',
  }, {
    [OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV]: '63',
  }, {
    [OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV]: '2049',
  }];
  for (const overrides of invalid) {
    assert.throws(() => createOperationsAgentFindingModelRuntimeFromEnvironment({
      environment: enabledEnvironment(overrides),
      createOpenAiSynthesizer: factory,
    }), { name: 'OperationsAgentFindingModelConfigurationError' });
  }
  assert.equal(factoryCalls, 0);
});

test('environment composition injects one bounded Synthesizer into the real Investigation flow', async () => {
  const environment = createEnvironment();
  const synthesizer = createFakeFindingSynthesizer({
    output(input) {
      return {
        classification: input.expectedClassification,
        statement: 'Environment-configured model summary: Site night energy was above baseline.',
        evidenceIds: input.evidence.map(({ id }) => id),
        limitations: [],
      };
    },
  });
  const coordinator = createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator(
    environment.ports,
    {
      environment: enabledEnvironment({
        [OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV]: '4000',
        [OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV]: '300',
      }),
      createOpenAiSynthesizer(options) {
        assert.deepEqual(options, {
          model,
          requestTimeoutMs: 4_000,
          maxOutputTokens: 300,
        });
        return synthesizer;
      },
    },
  );

  const started = await coordinator.start({
    organizationId: '0198f5c0-7c00-7000-8000-000000000001',
    siteId: '0198f5c0-7c00-7000-8000-000000000002',
  });
  const completed = await coordinator.advance({ investigationId: started.id });
  assert.equal(completed.status, 'COMPLETED');
  assert.equal(completed.findings[0].statement.startsWith('Environment-configured model summary:'), true);
  assert.equal(synthesizer.calls.length, 1);
  assert.equal(JSON.stringify(completed).includes('openai'), false);
  assert.notEqual(started.activeRun, null);
  const budget = await environment.ports.budgetGuard.get(started.id, started.activeRun.id);
  assert.equal(budget.usage.modelInvocations, 2);
});

test('Finding Provider is blocked before invocation when the Run model budget is exhausted', async () => {
  const environment = createEnvironment();
  const synthesizer = createFakeFindingSynthesizer();
  const resourceBudgetPolicy = Object.freeze({
    ...DEFAULT_RUN_RESOURCE_BUDGET_POLICY,
    revision: 'operations-agent-run-resource-policy/model-one',
    limits: Object.freeze({
      ...DEFAULT_RUN_RESOURCE_BUDGET_POLICY.limits,
      modelInvocations: 1,
    }),
  });
  const coordinator = createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator(
    { ...environment.ports, resourceBudgetPolicy },
    {
      environment: enabledEnvironment(),
      createOpenAiSynthesizer: () => synthesizer,
    },
  );

  const started = await coordinator.start({ organizationId, siteId });
  const blocked = await coordinator.advance({ investigationId: started.id });
  assert.equal(synthesizer.calls.length, 0);
  assert.equal(blocked.findings.length, 0);
  assert.deepEqual(blocked.resourceBudget, {
    schemaVersion: 1,
    policyRevision: resourceBudgetPolicy.revision,
    outcome: 'PARTIAL',
    exhaustedDimension: 'MODEL_INVOCATIONS',
    consumed: 2,
    limit: 1,
  });
});

test('environment composition rejects duplicate or conflicting injection', () => {
  const environment = createEnvironment();
  const synthesizer = createFakeFindingSynthesizer();
  assert.throws(() => createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator(
    { ...environment.ports, findingSynthesizer: synthesizer },
    {
      environment: enabledEnvironment(),
      createOpenAiSynthesizer: () => synthesizer,
    },
  ), { name: 'OperationsAgentFindingModelConfigurationError' });

  assert.throws(() => createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator(
    environment.ports,
    {
      environment: enabledEnvironment(),
      policy: { findingSynthesisTimeoutMs: 2_000 },
      createOpenAiSynthesizer: () => synthesizer,
    },
  ), { name: 'OperationsAgentFindingModelConfigurationError' });
});
