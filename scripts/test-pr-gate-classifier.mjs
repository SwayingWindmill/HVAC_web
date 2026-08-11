import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFile, readdir } from 'node:fs/promises';
import test from 'node:test';

const runClassification = (files) => {
  const result = spawnSync(process.execPath, ['scripts/classify-pr-gates.mjs', `--files=${files.join(',')}`], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.trim());
};

const runPlan = (gate, profiles) => {
  const result = spawnSync(process.execPath, ['scripts/run-pr-gate.mjs', `--gate=${gate}`, `--profiles=${profiles.join(',')}`, '--dry-run=true'], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.trim());
};

test('documentation-only changes keep expensive affected gates idle', () => {
  const classification = runClassification(['docs/architecture/overview.md']);
  assert.equal(classification.contracts, false);
  assert.equal(classification.units, false);
  assert.equal(classification.integrations, false);
  assert.equal(classification.browsers, false);
  assert.equal(classification.broad, false);
});

test('package-lock changes compile and unit test without database or browser fan-out', () => {
  const classification = runClassification(['package-lock.json']);
  assert.deepEqual(classification.unitProfiles, ['web']);
  assert.deepEqual(classification.integrationProfiles, []);
  assert.deepEqual(classification.browserProfiles, []);
  assert.equal(classification.broad, false);
});

test('HVAC Web changes select browser and web unit profiles on the correct runner platforms', () => {
  const classification = runClassification(['apps/hvac-web/src/App.tsx']);
  assert.deepEqual(classification.unitProfiles, ['web']);
  assert.deepEqual(classification.browserWindowsProfiles, ['rms']);
  assert.deepEqual(classification.browserLinuxProfiles, ['s0', 's1', 's2']);
  assert.equal(classification.integrations, false);
});

test('Operations Workspace changes select dedicated unit and Linux browser profiles', () => {
  const classification = runClassification(['apps/hvac-web/src/real/operations/OperationsInvestigationAgent.ts']);
  assert.ok(classification.unitProfiles.includes('web'));
  assert.ok(classification.unitProfiles.includes('operations-agent'));
  assert.deepEqual(classification.browserWindowsProfiles, ['rms']);
  assert.ok(classification.browserLinuxProfiles.includes('operations-agent'));
});

test('Realtime backend changes select the durable realtime PostgreSQL profile', () => {
  const classification = runClassification(['services/telemetry-runtime-service/internal/telemetry/realtime.go']);
  assert.ok(classification.unitProfiles.includes('s2'));
  assert.deepEqual(classification.integrationProfiles, ['s2-realtime']);
  assert.equal(classification.broad, false);
});

test('package, workflow, and central task-matrix changes fail closed to broad static, contract, and unit coverage only', () => {
  for (const file of [
    'package.json',
    '.github/workflows/s2-realtime-backend.yml',
    'scripts/domain-task-matrix.mjs',
    'scripts/package-script-long-chain-baseline.json',
    'scripts/run-capability-task.mjs',
  ]) {
    const classification = runClassification([file]);
    assert.equal(classification.broad, true);
    assert.equal(classification.unknown, false);
    assert.ok(classification.unitProfiles.includes('operations-agent'));
    assert.ok(classification.unitProfiles.includes('pocs'));
    assert.deepEqual(classification.integrationProfiles, []);
    assert.deepEqual(classification.browserProfiles, []);
  }
});

test('unknown paths and automation scripts fail closed without launching database or browser matrices', () => {
  for (const file of ['new-platform-area/owner.go', 'scripts/new-automation-wrapper.mjs']) {
    const classification = runClassification([file]);
    assert.equal(classification.broad, true);
    assert.equal(classification.unknown, true);
    assert.equal(classification.contracts, true);
    assert.equal(classification.units, true);
    assert.equal(classification.integrations, false);
    assert.equal(classification.browsers, false);
  }
});

test('integration plans use domain-specific durable fixtures', () => {
  const plan = runPlan('integration', ['s2-realtime', 's3']);
  assert.ok(plan.commands.includes('npm run s2:realtime:postgres'));
  assert.ok(plan.commands.includes('npm run s3:postgres'));
  assert.ok(!plan.commands.includes('npm run s2:postgres'));
});

test('Operations Agent changes select dedicated unit and PostgreSQL profiles', () => {
  const classification = runClassification(['services/operations-agent-service/src/index.ts']);
  assert.deepEqual(classification.unitProfiles, ['operations-agent']);
  assert.deepEqual(classification.integrationProfiles, ['operations-agent']);
  assert.deepEqual(classification.browserLinuxProfiles, ['operations-agent']);
  assert.equal(classification.broad, false);

  assert.deepEqual(runPlan('unit', ['operations-agent']).commands, [
    'npm --prefix services/operations-agent-service ci',
    'npm run operations-agent-service:check',
    'npm run operations-agent:benchmark:test',
    'npm run operations-agent:gateway:check',
    'npm run operations-workspace:test',
    'npm run test:gateway',
  ]);
  assert.deepEqual(runPlan('integration', ['operations-agent']).commands, [
    'npm --prefix services/operations-agent-service ci',
    'npm run operations-agent-service:postgres',
  ]);
  assert.deepEqual(runPlan('browser', ['operations-agent']).commands, [
    'npm run operations-workspace:browser',
  ]);
});

test('nightly regression preserves its schedule, manual trigger, and complete profile sets', async () => {
  const workflow = await readFile('.github/workflows/nightly-full-regression.yml', 'utf8');
  assert.ok(workflow.includes('schedule:'));
  assert.ok(workflow.includes('cron: "0 18 * * *"'));
  assert.ok(workflow.includes('workflow_dispatch:'));
  for (const command of [
    '--gate=static',
    '--gate=contracts --profile-set=all',
    '--gate=unit --profile-set=all',
    '--gate=integration --profile-set=all',
    '--gate=browser --profile-set=browser-windows',
    '--gate=browser --profile-set=browser-linux',
  ]) {
    assert.ok(workflow.includes(command), `nightly coverage drifted: ${command}`);
  }
});

test('PR gate workflow always exposes the three stable required checks', async () => {
  const workflow = (await readFile('.github/workflows/pr-gates.yml', 'utf8')).replace(/\r\n?/gu, '\n');
  const pullRequestBlock = workflow.split('  pull_request:')[1]?.split('  workflow_dispatch:')[0] ?? '';
  assert.ok(pullRequestBlock.includes('types:'));
  assert.ok(!pullRequestBlock.includes('paths:'));
  for (const check of [
    'pr / static',
    'pr / contracts',
    'pr / affected-unit',
  ]) {
    assert.equal(workflow.split(`name: ${check}`).length - 1, 1, `required check name drifted: ${check}`);
  }
  for (const [job, check] of [
    ['contracts', 'pr / contracts'],
    ['unit', 'pr / affected-unit'],
  ]) {
    const marker = `  ${job}:\n    name: ${check}`;
    const start = workflow.indexOf(marker);
    assert.notEqual(start, -1, `aggregate job is missing: ${job}`);
    const tail = workflow.slice(start + marker.length);
    const nextJobMatch = /\n  [a-z][a-z0-9_]*:\n/u.exec(tail);
    const end = nextJobMatch ? start + marker.length + nextJobMatch.index : workflow.length;
    const block = workflow.slice(start, end);
    assert.ok(block.includes('if: ${{ always() }}'), `${job} must always report a result`);
  }
  assert.ok(!workflow.includes('pr / affected-integration'), 'database integration must not be a pull-request required check');
  assert.ok(!workflow.includes('internal / affected integration'), 'PR workflow must not launch database integration suites');
  assert.ok(!workflow.includes('pr / affected-browser'), 'browser regression must not be a pull-request required check');
  assert.ok(!workflow.includes('internal / affected browser'), 'PR workflow must not launch browser certification');
});

test('legacy workflows delegate pull requests to PR Gates and do not fan out on root package manifests', async () => {
  const workflowNames = (await readdir('.github/workflows'))
    .filter((name) => /\.ya?ml$/u.test(name) && name !== 'pr-gates.yml');
  for (const name of workflowNames) {
    const workflow = await readFile(`.github/workflows/${name}`, 'utf8');
    const triggerBlock = workflow.split(/^jobs:\s*$/mu)[0];
    assert.ok(
      !/^\s*-\s*['"]?package(?:-lock)?\.json['"]?\s*$/mu.test(triggerBlock),
      `${name} must delegate root package manifest classification to PR Gates`,
    );
    if (/^\s{2}pull_request:\s*$/mu.test(triggerBlock)) {
      assert.ok(
        /^\s{4}branches-ignore:\s*\["\*\*"\]\s*$/mu.test(triggerBlock),
        `${name} must disable legacy pull-request execution in favor of PR Gates`,
      );
    }
  }
});

test('formal S2 release certification is explicit rather than automatic on main', async () => {
  const workflow = await readFile('.github/workflows/s2-telemetry-release.yml', 'utf8');
  const triggerBlock = workflow.split(/^permissions:\s*$/mu)[0];
  assert.ok(triggerBlock.includes('workflow_dispatch:'), 'release certification must keep an explicit dispatch entry point');
  assert.ok(!/^\s{2}push:\s*$/mu.test(triggerBlock), 'release certification must not auto-run after ordinary main pushes');
});
