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
});

test('HVAC Web changes select stable domain profiles on the Linux browser runner', () => {
  const classification = runClassification(['apps/hvac-web/src/app/router.ts']);
  assert.deepEqual(classification.unitProfiles, ['web']);
  assert.deepEqual(classification.browserProfiles, ['platform', 'registry', 'telemetry', 'web']);
  assert.equal(classification.integrations, false);
});

test('Operations Workspace changes select dedicated unit and browser profiles', () => {
  const classification = runClassification(['apps/hvac-web/src/features/operations/OperationsInvestigation.tsx']);
  assert.ok(classification.unitProfiles.includes('web'));
  assert.ok(classification.unitProfiles.includes('operations-agent'));
  assert.deepEqual(classification.browserProfiles, ['operations-agent', 'platform', 'registry', 'telemetry', 'web']);
});

test('telemetry changes select telemetry unit and durable integration profiles', () => {
  const classification = runClassification(['modules/telemetry/internal/telemetry/realtime.go']);
  assert.deepEqual(classification.unitProfiles, ['telemetry']);
  assert.deepEqual(classification.integrationProfiles, ['telemetry']);
  assert.equal(classification.broad, false);
});

test('domain module changes stay scoped to product domains', () => {
  for (const [file, unitProfile, integrationProfile] of [
    ['modules/iot/internal/adapter/runtime.go', 'telemetry', 'telemetry'],
    ['modules/alarm/pkg/alarmservice/http.go', 'alarm', 'alarm'],
    ['modules/workorder/pkg/workorderservice/http.go', 'workorder', 'workorder'],
  ]) {
    const classification = runClassification([file]);
    assert.deepEqual(classification.unitProfiles, [unitProfile]);
    assert.deepEqual(classification.integrationProfiles, [integrationProfile]);
    assert.equal(classification.broad, false);
  }
});

test('retired migration evidence does not trigger product gates', () => {
  for (const file of ['tools/legacy-registry-migrator/internal/migration/types.go', 'pocs/telemetry-shadow-comparator/internal/comparison/comparison.go']) {
    const classification = runClassification([file]);
    assert.deepEqual(classification.unitProfiles, []);
    assert.deepEqual(classification.integrationProfiles, []);
    assert.equal(classification.broad, false);
  }
});

test('package, workflow, and central task-matrix changes fail closed to broad contract and unit coverage', () => {
  for (const file of [
    'package.json',
    '.github/workflows/pr-gates.yml',
    'scripts/domain-task-matrix.mjs',
    'scripts/package-script-long-chain-baseline.json',
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

test('unknown paths and automation scripts fail closed without database or browser matrices', () => {
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

test('integration plans resolve through stable domain profiles', () => {
  const plan = runPlan('integration', ['telemetry', 'command']);
  assert.deepEqual(plan.commands, [
    'node scripts/run-s2-telemetry-postgres-tests.mjs',
    'node scripts/run-s2-telemetry-ingest-postgres-tests.mjs',
    'node scripts/run-s2-realtime-postgres-tests.mjs',
    'node scripts/run-s2-telemetry-history-tests.mjs',
    'node --experimental-strip-types scripts/run-s3-command-postgres-tests.ts',
  ]);
});

test('Operations Agent changes select dedicated domain gates', () => {
  const classification = runClassification(['services/operations-agent-service/src/index.ts']);
  assert.deepEqual(classification.unitProfiles, ['operations-agent']);
  assert.deepEqual(classification.integrationProfiles, ['operations-agent']);
  assert.deepEqual(classification.browserProfiles, ['operations-agent']);
  assert.equal(classification.broad, false);

  assert.deepEqual(runPlan('browser', ['operations-agent']).commands, [
    'npm run operations-workspace:browser',
  ]);
});

test('nightly regression keeps the complete stable profile sets', async () => {
  const workflow = await readFile('.github/workflows/nightly-full-regression.yml', 'utf8');
  assert.ok(workflow.includes('schedule:'));
  assert.ok(workflow.includes('cron: "0 18 * * *"'));
  for (const command of [
    '--gate=static',
    '--gate=contracts --profile-set=all',
    '--gate=unit --profile-set=all',
    '--gate=integration --profile-set=all',
    '--gate=browser --profile-set=all',
  ]) {
    assert.ok(workflow.includes(command), `nightly coverage drifted: ${command}`);
  }
});

test('PR workflow exposes only the stable required checks', async () => {
  const workflow = (await readFile('.github/workflows/pr-gates.yml', 'utf8')).replace(/\r\n?/gu, '\n');
  for (const check of ['pr / static', 'pr / contracts', 'pr / affected-unit']) {
    assert.equal(workflow.split(`name: ${check}`).length - 1, 1, `required check name drifted: ${check}`);
  }
  assert.ok(!workflow.includes('pr / affected-integration'));
  assert.ok(!workflow.includes('pr / affected-browser'));
});

test('active workflow filenames no longer encode historical implementation stages', async () => {
  const workflowNames = (await readdir('.github/workflows')).filter((name) => /\.ya?ml$/u.test(name));
  assert.deepEqual(workflowNames.filter((name) => /^(?:s\d+|rms)-/u.test(name)), []);
});
