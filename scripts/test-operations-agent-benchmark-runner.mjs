import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

import {
  OPERATIONS_AGENT_BENCHMARK_REPORT_VERSION,
  formatOperationsAgentBenchmarkSummary,
  runOperationsAgentBenchmark,
} from '../benchmarks/operations-agent/benchmark-runner.v1.mjs';

const scenarioDirectory = resolve('benchmarks/operations-agent/scenarios');

const withTemporaryScenarioDirectory = async (run) => {
  const directory = await mkdtemp(join(tmpdir(), 'operations-agent-benchmark-'));
  try {
    return await run(directory);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
};

const readScenario = async (name) => JSON.parse(await readFile(
  join(scenarioDirectory, name),
  'utf8',
));

const writeScenario = async (directory, name, scenario) => {
  await writeFile(join(directory, name), `${JSON.stringify(scenario, null, 2)}\n`, 'utf8');
};

test('runner discovers and passes all repository Operations Agent scenarios', async () => {
  const report = await runOperationsAgentBenchmark({ scenarioDirectory });

  assert.equal(report.reportVersion, OPERATIONS_AGENT_BENCHMARK_REPORT_VERSION);
  assert.equal(report.status, 'PASSED');
  assert.equal(report.summary.discoveredScenarios, 5);
  assert.equal(report.summary.structureFailures, 0);
  assert.equal(report.summary.blockerFailures, 0);
  assert.deepEqual(
    report.scenarios.map(({ scenarioId }) => scenarioId),
    [
      'site-night-energy-insufficient-equipment-attribution',
      'setpoint-proposal-only',
      'stale-current-telemetry',
      'unauthorized-site-nondiscoverable',
      'untrusted-content-injection-boundary',
    ],
  );
  for (const scenario of report.scenarios) {
    assert.equal(scenario.phases.structure.status, 'PASSED');
    assert.equal(scenario.phases.blockers.status, 'PASSED');
    assert.equal(scenario.phases.scoring.status, 'NOT_EVALUATED');
    assert.equal(scenario.contractVersion, 'operations-agent-scenario/v1');
  }
  assert.match(formatOperationsAgentBenchmarkSummary(report), /5 scenarios passed/);
});

test('structure failure prevents blocker and scoring phases', async () => {
  await withTemporaryScenarioDirectory(async (directory) => {
    await writeFile(join(directory, 'malformed.v1.json'), '{"scenarioId":"broken"}\n', 'utf8');

    const report = await runOperationsAgentBenchmark({ scenarioDirectory: directory });
    const scenario = report.scenarios[0];

    assert.equal(report.status, 'FAILED');
    assert.equal(report.summary.structureFailures, 1);
    assert.equal(scenario.phases.structure.status, 'FAILED');
    assert.equal(scenario.phases.blockers.status, 'NOT_RUN');
    assert.equal(scenario.phases.scoring.status, 'BLOCKED');
    assert(scenario.phases.structure.failures.some(({ code }) => code === 'STRUCTURE_INVALID'));
  });
});

test('deterministic blocker failure cannot be offset by scored criteria', async () => {
  await withTemporaryScenarioDirectory(async (directory) => {
    const scenario = await readScenario('unauthorized-site-nondiscoverable.v1.json');
    scenario.inputFacts[0].payload.resourceExistenceDisclosed = true;
    scenario.acceptance.scored.push({
      id: 'score-polished-answer',
      dimension: 'OPERATIONAL_USEFULNESS',
      description: 'A polished answer must not compensate for authorization disclosure.',
      weight: 100,
    });
    await writeScenario(directory, 'unauthorized-site-nondiscoverable.v1.json', scenario);

    const report = await runOperationsAgentBenchmark({ scenarioDirectory: directory });
    const result = report.scenarios[0];

    assert.equal(report.status, 'FAILED');
    assert.equal(result.phases.structure.status, 'PASSED');
    assert.equal(result.phases.blockers.status, 'FAILED');
    assert.equal(result.phases.scoring.status, 'BLOCKED');
    assert.equal(result.phases.scoring.criteria[0].weight, 100);
    assert.equal(report.summary.scoredCriteriaNotEvaluated, 0);
    assert.equal(report.summary.scoredCriteriaBlocked, 1);
    assert(result.phases.blockers.failures.some(({ code, dimension }) => (
      code === 'UNAUTHORIZED_RESOURCE_DISCLOSURE'
      && dimension === 'AUTHORIZATION_COMPLIANCE'
    )));
  });
});

test('registered blocker profiles reject trust, night-energy, stale-state, and action-safety regressions', async () => {
  const cases = [
    {
      file: 'untrusted-content-injection-boundary.v1.json',
      mutate: (scenario) => {
        delete scenario.trustBoundary;
      },
      code: 'UNTRUSTED_CONTENT_POLICY_MISSING',
    },
    {
      file: 'untrusted-content-injection-boundary.v1.json',
      mutate: (scenario) => {
        scenario.tools.allowed.push('commands.createIntent');
        scenario.tools.forbidden = scenario.tools.forbidden
          .filter((tool) => tool !== 'commands.createIntent');
      },
      code: 'UNTRUSTED_TOOL_SELECTION',
    },
    {
      file: 'night-energy-insufficient-attribution.v1.json',
      mutate: (scenario) => {
        scenario.tools.forbiddenPaths = scenario.tools.forbiddenPaths
          .filter((path) => path !== 'DIRECT_CLICKHOUSE_SQL');
      },
      code: 'FORBIDDEN_PATH_POLICY_MISSING',
    },
    {
      file: 'stale-current-telemetry.v1.json',
      mutate: (scenario) => {
        scenario.groundTruth.outcomes.find(({ id }) => (
          id === 'outcome-current-fault-unavailable'
        )).classification = 'FACT';
      },
      code: 'STALE_TELEMETRY_CURRENT_CLAIM',
    },
    {
      file: 'setpoint-proposal-only.v1.json',
      mutate: (scenario) => {
        scenario.actionLifecycle.formalApproval = 'ALLOWED';
      },
      code: 'ACTION_LIFECYCLE_EXPECTATION_MISMATCH',
    },
  ];

  for (const profileCase of cases) {
    await withTemporaryScenarioDirectory(async (directory) => {
      const scenario = await readScenario(profileCase.file);
      profileCase.mutate(scenario);
      await writeScenario(directory, profileCase.file, scenario);

      const report = await runOperationsAgentBenchmark({ scenarioDirectory: directory });
      const result = report.scenarios[0];

      assert.equal(result.phases.structure.status, 'PASSED');
      assert.equal(result.phases.blockers.status, 'FAILED');
      assert(result.phases.blockers.failures.some(({ code }) => code === profileCase.code));
      assert.equal(result.phases.scoring.status, 'BLOCKED');
    });
  }
});

test('deterministic scenario without a registered evaluator fails closed', async () => {
  await withTemporaryScenarioDirectory(async (directory) => {
    const scenario = await readScenario('night-energy-insufficient-attribution.v1.json');
    scenario.scenarioId = 'unregistered-deterministic-scenario';
    await writeScenario(directory, 'unregistered-deterministic-scenario.v1.json', scenario);

    const report = await runOperationsAgentBenchmark({ scenarioDirectory: directory });
    const result = report.scenarios[0];

    assert.equal(report.status, 'FAILED');
    assert.equal(result.phases.structure.status, 'PASSED');
    assert.equal(result.phases.blockers.status, 'FAILED');
    assert(result.phases.blockers.failures.some(({ code }) => code === 'BLOCKER_EVALUATOR_MISSING'));
    assert.equal(result.phases.scoring.status, 'BLOCKED');
  });
});

test('CLI prints a human summary and writes the versioned machine report', async () => {
  await withTemporaryScenarioDirectory(async (directory) => {
    const reportPath = join(directory, 'report.json');
    const execution = spawnSync(
      process.execPath,
      [
        resolve('scripts/run-operations-agent-benchmark.mjs'),
        `--scenarios=${scenarioDirectory}`,
        `--report=${reportPath}`,
      ],
      { encoding: 'utf8' },
    );

    assert.equal(execution.status, 0, execution.stderr);
    assert.match(execution.stdout, /Operations Agent Benchmark: PASSED/);
    assert.match(execution.stdout, /5 scenarios passed/);

    const report = JSON.parse(await readFile(reportPath, 'utf8'));
    assert.equal(report.reportVersion, OPERATIONS_AGENT_BENCHMARK_REPORT_VERSION);
    assert.equal(report.status, 'PASSED');
    assert.equal(basename(reportPath), 'report.json');
  });
});

test('CLI exits nonzero and still writes a failed machine report', async () => {
  await withTemporaryScenarioDirectory(async (directory) => {
    await writeFile(join(directory, 'broken.v1.json'), '{"scenarioId":"broken"}\n', 'utf8');
    const reportPath = join(directory, 'failed-report.json');
    const execution = spawnSync(
      process.execPath,
      [
        resolve('scripts/run-operations-agent-benchmark.mjs'),
        `--scenarios=${directory}`,
        `--report=${reportPath}`,
      ],
      { encoding: 'utf8' },
    );

    assert.equal(execution.status, 1);
    assert.match(execution.stdout, /Operations Agent Benchmark: FAILED/);
    const report = JSON.parse(await readFile(reportPath, 'utf8'));
    assert.equal(report.status, 'FAILED');
    assert.equal(report.summary.structureFailures, 1);
  });
});
