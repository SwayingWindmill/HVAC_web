import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  parseCommandFormalOptions,
  summarizeCommandFormalOutput,
} from './run-command-formal-check.ts';

test('command-formal options parser accepts --json and rejects unknown flags', () => {
  assert.deepEqual(parseCommandFormalOptions([]), { json: false });
  assert.deepEqual(parseCommandFormalOptions(['--json']), { json: true });
  assert.throws(() => parseCommandFormalOptions(['--bogus']), /Unknown flag/);
});

test('command-formal output parser extracts passing suites and counterexamples', () => {
  const passSummary = summarizeCommandFormalOutput(
    [
      '=== RUN   TestSingleCommandProtocolInvariants',
      '--- PASS: TestSingleCommandProtocolInvariants (0.02s)',
      '--- PASS: TestMultiCommandDeviceExclusivityInvariants (0.00s)',
      'PASS',
    ].join('\n'),
    0,
  );
  assert.equal(passSummary.ok, true);
  assert.deepEqual(passSummary.passedTests, [
    'TestSingleCommandProtocolInvariants',
    'TestMultiCommandDeviceExclusivityInvariants',
  ]);
  assert.deepEqual(passSummary.counterexamples, []);

  const failSummary = summarizeCommandFormalOutput(
    [
      '=== RUN   TestSingleCommandProtocolInvariants',
      '    formal_test.go:11: CheckSingleCommandProtocol failed: [SingleCommandProtocol/P2] correlation state MAY_COMMIT allowed publish action 2; trace: Submit(lowRisk) -> PrepareDispatch(validGrant) -> Correlation(PREPARED) -> Correlation(MAY_COMMIT) -> RecoverCorrelation',
      '--- FAIL: TestSingleCommandProtocolInvariants (0.01s)',
      'FAIL',
    ].join('\n'),
    1,
  );
  assert.equal(failSummary.ok, false);
  assert.deepEqual(failSummary.failedTests, ['TestSingleCommandProtocolInvariants']);
  assert.equal(failSummary.counterexamples.length, 1);
  assert.match(failSummary.counterexamples[0], /\[SingleCommandProtocol\/P2\]/);
});

test('command-safety skill exists in mirror parity and references existing covered files', () => {
  const root = process.cwd();
  const agentSkillPath = join(root, '.agents/skills/command-safety/SKILL.md');
  const claudeSkillPath = join(root, '.claude/skills/command-safety/SKILL.md');
  assert.equal(existsSync(agentSkillPath), true, 'missing .agents/skills/command-safety/SKILL.md');
  assert.equal(existsSync(claudeSkillPath), true, 'missing .claude/skills/command-safety/SKILL.md');

  const agentBytes = readFileSync(agentSkillPath);
  const claudeBytes = readFileSync(claudeSkillPath);
  assert.equal(agentBytes.equals(claudeBytes), true, 'command-safety skill mirror drifted');

  const text = agentBytes.toString('utf8');
  assert.match(text, /syncCommit[`:\s*]+[0-9a-f]{40}/u);
  for (const coveredPath of [
    'libs/commandmodel/model.go',
    'modules/command/pkg/commandservice/service.go',
    'modules/command/pkg/commandservice/governance.go',
    'modules/command/pkg/commandservice/transitions.go',
    'modules/command/pkg/commandservice/postgres_dispatch.go',
    'modules/command/pkg/commandservice/postgres_verification.go',
    'modules/command/pkg/commandservice/postgres_reconcile.go',
    'modules/command/pkg/mqttconnector/connector.go',
    'modules/connectivity/pkg/connectivity/store.go',
  ]) {
    assert.ok(text.includes(coveredPath), `skill missing covered path ${coveredPath}`);
    assert.equal(existsSync(join(root, coveredPath)), true, `covered path missing on disk: ${coveredPath}`);
  }
});
