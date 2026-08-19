import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import test from 'node:test';

import {
  OPERATIONS_AGENT_TOOL_CATALOG,
  validateOperationsAgentScenario,
} from '../benchmarks/operations-agent/scenario-contract.v1.mjs';

const scenarioPath = resolve(
  'benchmarks/operations-agent/scenarios/night-energy-insufficient-attribution.v1.json',
);
const scenario = JSON.parse(await readFile(scenarioPath, 'utf8'));

const outcomeById = new Map(
  scenario.groundTruth.outcomes.map((outcome) => [outcome.id, outcome]),
);
const evidenceById = new Map(
  scenario.evidenceRequirements.map((requirement) => [requirement.id, requirement]),
);
const factById = new Map(scenario.inputFacts.map((fact) => [fact.id, fact]));

const expectedForbiddenPaths = [
  'DIRECT_CLICKHOUSE_SQL',
  'ARBITRARY_CUBE_QUERY',
  'THINGSBOARD_READ_THROUGH',
  'LEGACY_AGENT_MOCK',
  'PHYSICAL_COMMAND_EXECUTION',
];

test('night-energy insufficient-attribution fixture satisfies the v1 contract', () => {
  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, true, JSON.stringify(result.errors, null, 2));
  assert.equal(scenario.purpose, 'RETROSPECTIVE');
  assert.equal(scenario.scope.tenantId, '0198f5c0-7c00-7000-8000-000000000001');
  assert.deepEqual(scenario.scope.siteIds, ['0198f5c0-7c00-7000-8000-000000000002']);
  assert.deepEqual(scenario.scope.timeRange, {
    from: '2026-06-30T14:00:00Z',
    to: '2026-07-07T22:00:00Z',
  });
});

test('historical energy Evidence preserves dataset revision, watermark, partial, and quality', () => {
  const fact = factById.get('fact-site-night-energy-comparison');
  const requirement = evidenceById.get('evidence-site-night-energy-comparison');

  assert(fact);
  assert(requirement);
  assert.equal(fact.ownerTool, 'analytics.energy.compareSitePeriods');
  assert.equal(fact.metadata.datasetRevision, 'energy-dataset-r17');
  assert.equal(fact.metadata.watermark, '2026-07-07T22:05:00Z');
  assert.equal(fact.metadata.partial, false);
  assert.equal(fact.metadata.quality, 'GOOD');
  assert.equal(requirement.status, 'AVAILABLE');
  assert.deepEqual(requirement.factIds, ['fact-site-night-energy-comparison']);
  assert.deepEqual(requirement.requiredMetadata, [
    'DATASET_REVISION',
    'WATERMARK',
    'PARTIAL',
    'QUALITY',
    'CAPTURED_AT',
    'PAYLOAD_DIGEST',
  ]);
});

test('Ground Truth confirms Site increase while refusing Asset root-cause attribution', () => {
  const comparison = factById.get('fact-site-night-energy-comparison');
  const siteFact = outcomeById.get('outcome-site-energy-series-available');
  const increase = outcomeById.get('outcome-site-night-energy-increased');
  const boundedInference = outcomeById.get('outcome-site-level-operational-deviation');
  const assetAttribution = outcomeById.get('outcome-asset-root-cause-unavailable');

  assert(comparison);
  const targetKWh = comparison.payload.targetPeriod.energyKWh;
  const baselineKWh = comparison.payload.baselinePeriod.energyKWh;
  const independentlyCalculatedChange = ((targetKWh - baselineKWh) / baselineKWh) * 100;

  assert(targetKWh > baselineKWh);
  assert.equal(independentlyCalculatedChange, 24);
  assert.equal(comparison.payload.changePercent, independentlyCalculatedChange);
  assert.equal(siteFact?.classification, 'FACT');
  assert.equal(increase?.classification, 'ALGORITHM_RESULT');
  assert.equal(increase?.statement, 'Target night energy was 1240 kWh versus a 1000 kWh baseline, a 24% increase.');
  assert.equal(boundedInference?.classification, 'INFERENCE');
  assert.equal(assetAttribution?.classification, 'UNABLE_TO_CONCLUDE');
  assert.deepEqual(assetAttribution?.evidenceRequirementIds, [
    'evidence-site-asset-roster',
    'evidence-asset-energy-bindings-required-next',
    'evidence-asset-energy-series-required-next',
  ]);
});

test('missing Asset attribution Evidence is structured as a verifiable next requirement', () => {
  const binding = evidenceById.get('evidence-asset-energy-bindings-required-next');
  const series = evidenceById.get('evidence-asset-energy-series-required-next');

  assert.equal(binding?.status, 'REQUIRED_NEXT');
  assert.equal(binding?.ownerTool, 'registry.getAssetEnergyBindings');
  assert.deepEqual(binding?.factIds, []);
  assert.equal(series?.status, 'REQUIRED_NEXT');
  assert.equal(series?.ownerTool, 'analytics.energy.getAssetSeries');
  assert.deepEqual(series?.factIds, []);
  assert.equal(OPERATIONS_AGENT_TOOL_CATALOG[binding.ownerTool], 'platform-core-service');
  assert.equal(OPERATIONS_AGENT_TOOL_CATALOG[series.ownerTool], 'telemetry-query-service');
  assert(!scenario.tools.allowed.includes(binding.ownerTool));
  assert(!scenario.tools.allowed.includes(series.ownerTool));
});

test('scenario allows only governed Registry and Telemetry Query work and forbids bypass paths', () => {
  const evidenceOwners = new Set(
    scenario.evidenceRequirements.map(({ ownerTool }) => OPERATIONS_AGENT_TOOL_CATALOG[ownerTool]),
  );

  assert.deepEqual(
    evidenceOwners,
    new Set(['platform-core-service', 'telemetry-query-service']),
  );
  assert.deepEqual(scenario.tools.forbiddenPaths, expectedForbiddenPaths);
  assert(scenario.tools.forbidden.includes('commands.createIntent'));
  assert(!scenario.executionDag.nodes.some(({ tool }) => tool === 'commands.createIntent'));
});
