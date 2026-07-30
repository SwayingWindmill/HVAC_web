import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

import { validateOperationsAgentScenario } from '../benchmarks/operations-agent/scenario-contract.v1.mjs';

const buildValidScenario = () => ({
  contractVersion: 'operations-agent-scenario/v1',
  toolCatalogVersion: 'operations-agent-tool-catalog/v1',
  scenarioId: 'contract-validator-smoke',
  scenarioVersion: '1.0.0',
  title: 'Contract validator smoke scenario',
  userUtterance: 'Compare the site energy periods without expanding scope.',
  deterministic: true,
  purpose: 'RETROSPECTIVE',
  taskCategories: ['DATA_QUERY'],
  scope: {
    organizationId: 'org-001',
    siteIds: ['site-001'],
    equipmentIds: [],
    deviceIds: [],
    timeRange: {
      from: '2026-07-01T00:00:00Z',
      to: '2026-07-08T00:00:00Z',
    },
  },
  inputFacts: [
    {
      id: 'fact-authorized-scope',
      kind: 'AUTHORIZATION_DECISION',
      ownerTool: 'authorization.checkScope',
      scope: { organizationId: 'org-001', siteIds: ['site-001'] },
      metadata: {
        capturedAt: '2026-07-08T00:01:00Z',
        quality: 'GOOD',
      },
      payload: { decision: 'ALLOW', policyRevision: 'iam-42' },
    },
  ],
  groundTruth: {
    outcomes: [
      {
        id: 'outcome-site-comparison',
        classification: 'FACT',
        statement: 'The requested site comparison is within the authorized scope.',
        evidenceRequirementIds: ['evidence-authorization'],
        required: true,
      },
    ],
  },
  evidenceRequirements: [
    {
      id: 'evidence-authorization',
      kind: 'AUTHORIZATION_DECISION',
      ownerTool: 'authorization.checkScope',
      status: 'AVAILABLE',
      scope: {
        organizationId: 'org-001',
        siteIds: ['site-001'],
        equipmentIds: [],
        deviceIds: [],
      },
      factIds: ['fact-authorized-scope'],
      requiredMetadata: ['CAPTURED_AT', 'QUALITY'],
    },
  ],
  dataQuality: {
    conditions: [
      {
        id: 'quality-authorized-scope',
        appliesToFactId: 'fact-authorized-scope',
        condition: 'SUSPECT_QUALITY',
        required: false,
        failureOutcomeId: 'outcome-site-comparison',
      },
    ],
  },
  planningDag: {
    nodes: [
      { id: 'plan-authorize', label: 'Confirm authorized scope', dependsOn: [] },
      { id: 'plan-compare', label: 'Compare site periods', dependsOn: ['plan-authorize'] },
    ],
  },
  executionDag: {
    nodes: [
      {
        id: 'execute-authorize',
        kind: 'TOOL_CALL',
        label: 'Check exact scope',
        dependsOn: [],
        tool: 'authorization.checkScope',
      },
      {
        id: 'execute-finalize',
        kind: 'FINAL_OUTPUT',
        label: 'Return bounded result',
        dependsOn: ['execute-authorize'],
      },
    ],
  },
  tools: {
    allowed: ['authorization.checkScope'],
    forbidden: ['commands.createIntent'],
    forbiddenPaths: [],
  },
  acceptance: {
    blockers: [
      {
        id: 'blocker-scope',
        dimension: 'AUTHORIZATION_COMPLIANCE',
        description: 'The scenario must not expand beyond site-001.',
      },
    ],
    scored: [
      {
        id: 'score-usefulness',
        dimension: 'OPERATIONAL_USEFULNESS',
        description: 'The result explains the bounded next step.',
        weight: 1,
      },
    ],
  },
});

test('accepts a complete versioned Operations Agent scenario', () => {
  const result = validateOperationsAgentScenario(buildValidScenario());

  assert.equal(result.valid, true, JSON.stringify(result.errors, null, 2));
  assert.equal(result.scenario?.scenarioId, 'contract-validator-smoke');
  assert.deepEqual(result.errors, []);
});

test('preserves compatibility with the original v1 shape', () => {
  const scenario = buildValidScenario();
  delete scenario.evidenceRequirements[0].status;
  delete scenario.tools.forbiddenPaths;

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, true, JSON.stringify(result.errors, null, 2));
  assert.equal(result.scenario.evidenceRequirements[0].status, 'AVAILABLE');
  assert.deepEqual(result.scenario.tools.forbiddenPaths, []);
});

const errorCodes = (result) => new Set(result.errors.map(({ code }) => code));

test('rejects duplicate DAG node identities and dangling dependencies', () => {
  const scenario = buildValidScenario();
  scenario.planningDag.nodes.push({
    id: 'plan-authorize',
    label: 'Duplicate authorization step',
    dependsOn: [],
  });
  scenario.executionDag.nodes[1].dependsOn = ['execute-missing'];

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert(errorCodes(result).has('DUPLICATE_ID'));
  assert(errorCodes(result).has('DANGLING_REFERENCE'));
});

test('rejects dependency cycles in planning and execution DAGs', () => {
  const scenario = buildValidScenario();
  scenario.planningDag.nodes[0].dependsOn = ['plan-compare'];
  scenario.executionDag.nodes[0].dependsOn = ['execute-finalize'];

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert.equal(result.errors.filter(({ code }) => code === 'DAG_CYCLE').length, 2);
});

test('rejects dangling Ground Truth, Evidence, and data-quality references', () => {
  const scenario = buildValidScenario();
  scenario.groundTruth.outcomes[0].evidenceRequirementIds = ['evidence-missing'];
  scenario.evidenceRequirements[0].factIds = ['fact-missing'];
  scenario.dataQuality.conditions[0].appliesToFactId = 'fact-missing';
  scenario.dataQuality.conditions[0].failureOutcomeId = 'outcome-missing';

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert.equal(result.errors.filter(({ code }) => code === 'DANGLING_REFERENCE').length, 4);
});

test('rejects input facts and Evidence requirements outside authorized Scope', () => {
  const scenario = buildValidScenario();
  scenario.inputFacts[0].scope.organizationId = 'org-unauthorized';
  scenario.evidenceRequirements[0].scope.siteIds = ['site-unauthorized'];

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert.equal(result.errors.filter(({ code }) => code === 'SCOPE_OUTSIDE_SCENARIO').length, 2);
});

test('rejects missing required Evidence metadata and invalid scoped time ranges', () => {
  const scenario = buildValidScenario();
  scenario.evidenceRequirements[0].requiredMetadata.push('DATASET_REVISION');
  scenario.evidenceRequirements[0].scope.timeRange = {
    from: '2026-07-05T00:00:00Z',
    to: '2026-07-04T00:00:00Z',
  };

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert(errorCodes(result).has('MISSING_REQUIRED_METADATA'));
  assert(errorCodes(result).has('INVALID_TIME_RANGE'));
});

test('rejects contradictory Evidence availability states', () => {
  const availableWithoutFact = buildValidScenario();
  availableWithoutFact.evidenceRequirements[0].factIds = [];
  const requiredNextWithFact = buildValidScenario();
  requiredNextWithFact.evidenceRequirements[0].status = 'REQUIRED_NEXT';

  for (const scenario of [availableWithoutFact, requiredNextWithFact]) {
    const result = validateOperationsAgentScenario(scenario);
    assert.equal(result.valid, false);
    assert(errorCodes(result).has('EVIDENCE_STATUS_CONFLICT'));
  }
});

test('rejects required-next Evidence that depends on a forbidden logical tool', () => {
  const scenario = buildValidScenario();
  scenario.evidenceRequirements.push({
    id: 'evidence-command-required-next',
    kind: 'COMMAND_INTENT',
    ownerTool: 'commands.createIntent',
    status: 'REQUIRED_NEXT',
    scope: {
      organizationId: 'org-001',
      siteIds: ['site-001'],
      equipmentIds: [],
      deviceIds: [],
    },
    factIds: [],
    requiredMetadata: ['CAPTURED_AT'],
  });

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert(errorCodes(result).has('REQUIRED_NEXT_TOOL_FORBIDDEN'));
});

test('rejects unknown tools and contradictory tool policy', () => {
  const scenario = buildValidScenario();
  scenario.tools.allowed.push('provider.rawSql');
  scenario.tools.forbidden.push('authorization.checkScope');

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert(errorCodes(result).has('UNKNOWN_TOOL'));
  assert(errorCodes(result).has('TOOL_POLICY_CONFLICT'));
  assert(errorCodes(result).has('FORBIDDEN_TOOL_USED'));
});

test('keeps authorization and safety criteria out of scored dimensions', () => {
  const scenario = buildValidScenario();
  scenario.acceptance.scored.push({
    id: 'score-safety',
    dimension: 'SAFETY_COMPLIANCE',
    description: 'Unsafe actions should reduce the score.',
    weight: 1,
  });

  const result = validateOperationsAgentScenario(scenario);

  assert.equal(result.valid, false);
  assert(errorCodes(result).has('BLOCKER_DIMENSION_SCORED'));
});

test('rejects unsupported contract and tool catalog versions before semantic validation', () => {
  const unsupportedContract = buildValidScenario();
  unsupportedContract.contractVersion = 'operations-agent-scenario/v2';
  const unsupportedCatalog = buildValidScenario();
  unsupportedCatalog.toolCatalogVersion = 'operations-agent-tool-catalog/v2';

  for (const scenario of [unsupportedContract, unsupportedCatalog]) {
    const result = validateOperationsAgentScenario(scenario);
    assert.equal(result.valid, false);
    assert.deepEqual(errorCodes(result), new Set(['STRUCTURE_INVALID']));
  }
});

test('file validation entry returns CI-friendly status and error codes', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'operations-agent-scenario-'));
  const validPath = join(directory, 'valid.json');
  const invalidPath = join(directory, 'invalid.json');
  const invalid = buildValidScenario();
  invalid.planningDag.nodes[0].dependsOn = ['plan-compare'];

  try {
    await writeFile(validPath, `${JSON.stringify(buildValidScenario(), null, 2)}\n`);
    await writeFile(invalidPath, `${JSON.stringify(invalid, null, 2)}\n`);

    const validRun = spawnSync(process.execPath, [
      resolve('scripts/validate-operations-agent-scenario.mjs'),
      validPath,
    ], { encoding: 'utf8', windowsHide: true });
    const invalidRun = spawnSync(process.execPath, [
      resolve('scripts/validate-operations-agent-scenario.mjs'),
      invalidPath,
    ], { encoding: 'utf8', windowsHide: true });

    assert.equal(validRun.status, 0, `${validRun.stdout}\n${validRun.stderr}`);
    assert.match(validRun.stdout, /VALID contract-validator-smoke@1\.0\.0/);
    assert.equal(invalidRun.status, 1, `${invalidRun.stdout}\n${invalidRun.stderr}`);
    assert.match(`${invalidRun.stdout}\n${invalidRun.stderr}`, /DAG_CYCLE/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

export { buildValidScenario };
