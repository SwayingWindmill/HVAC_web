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

test('Realtime backend changes select the durable realtime PostgreSQL profile', () => {
  const classification = runClassification(['services/telemetry-runtime-service/internal/telemetry/realtime.go']);
  assert.ok(classification.unitProfiles.includes('s2'));
  assert.deepEqual(classification.integrationProfiles, ['s2-realtime']);
  assert.equal(classification.broad, false);
});

test('package and workflow changes fail closed to the broad suite', () => {
  for (const file of ['package.json', '.github/workflows/s2-realtime-backend.yml']) {
    const classification = runClassification([file]);
    assert.equal(classification.broad, true);
    assert.equal(classification.unknown, false);
    assert.ok(classification.integrationProfiles.includes('s2-realtime'));
    assert.ok(classification.unitProfiles.includes('operations-agent'));
    assert.ok(classification.integrationProfiles.includes('operations-agent'));
    assert.ok(classification.unitProfiles.includes('pocs'));
    assert.ok(classification.browserProfiles.includes('rms'));
  }
});

test('unknown paths and automation scripts fail closed rather than selecting no checks', () => {
  for (const file of ['new-platform-area/owner.go', 'scripts/new-automation-wrapper.mjs']) {
    const classification = runClassification([file]);
    assert.equal(classification.broad, true);
    assert.equal(classification.unknown, true);
    assert.equal(classification.contracts, true);
    assert.equal(classification.units, true);
    assert.equal(classification.integrations, true);
    assert.equal(classification.browsers, true);
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
  assert.equal(classification.broad, false);

  assert.deepEqual(runPlan('unit', ['operations-agent']).commands, [
    'npm --prefix services/operations-agent-service ci',
    'npm run operations-agent-service:check',
    'npm run operations-agent:benchmark:test',
  ]);
  assert.deepEqual(runPlan('integration', ['operations-agent']).commands, [
    'npm --prefix services/operations-agent-service ci',
    'npm run operations-agent-service:postgres',
  ]);
});

test('nightly regression preserves its schedule, manual trigger, and complete profile sets', async () => {
  const workflow = await readFile('.github/workflows/nightly-full-regression.yml', 'utf8');
  assert.ok(workflow.includes('schedule:'));
  assert.ok(workflow.includes('cron: "0 18 * * *"'));
  assert.ok(workflow.includes('workflow_dispatch:'));
  for (const command of [
    '--gate=static',
    '--gate=contracts --profiles=core,rms,s1,s2,s3',
    '--gate=unit --profiles=analytics,operations-agent,pocs,s0,s1,s2,s3,web',
    '--gate=integration --profiles=analytics,operations-agent,s0,s1,s2-baseline,s2-history,s2-ingest,s2-realtime,s3',
    '--gate=browser --profiles=rms',
    '--gate=browser --profiles=s0,s1,s2',
  ]) {
    assert.ok(workflow.includes(command), `nightly coverage drifted: ${command}`);
  }
});

test('PR gate workflow always exposes the five stable required checks', async () => {
  const workflow = await readFile('.github/workflows/pr-gates.yml', 'utf8');
  const pullRequestBlock = workflow.split('  pull_request:')[1]?.split('  workflow_dispatch:')[0] ?? '';
  assert.ok(pullRequestBlock.includes('types:'));
  assert.ok(!pullRequestBlock.includes('paths:'));
  for (const check of [
    'pr / static',
    'pr / contracts',
    'pr / affected-unit',
    'pr / affected-integration',
    'pr / affected-browser',
  ]) {
    assert.equal(workflow.split(`name: ${check}`).length - 1, 1, `required check name drifted: ${check}`);
  }
  for (const [job, check] of [
    ['contracts', 'pr / contracts'],
    ['unit', 'pr / affected-unit'],
    ['integration', 'pr / affected-integration'],
    ['browser', 'pr / affected-browser'],
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
});

test('legacy workflows do not fan out on root package manifests', async () => {
  const workflowNames = (await readdir('.github/workflows'))
    .filter((name) => /\.ya?ml$/u.test(name) && name !== 'pr-gates.yml');
  for (const name of workflowNames) {
    const workflow = await readFile(`.github/workflows/${name}`, 'utf8');
    const triggerBlock = workflow.split(/^jobs:\s*$/mu)[0];
    assert.ok(
      !/^\s*-\s*['"]?package(?:-lock)?\.json['"]?\s*$/mu.test(triggerBlock),
      `${name} must delegate root package manifest classification to PR Gates`,
    );
  }
});
