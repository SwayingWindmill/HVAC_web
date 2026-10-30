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
  assert.deepEqual(classification.browserProfiles, ['telemetry']);
  assert.equal(classification.integrations, false);
});

test('telemetry changes select telemetry unit and durable integration profiles', () => {
  const classification = runClassification(['modules/telemetry/pkg/telemetry/realtime.go']);
  assert.deepEqual(classification.unitProfiles, ['go', 'telemetry']);
  assert.deepEqual(classification.integrationProfiles, ['telemetry']);
  assert.equal(classification.broad, false);
});

test('domain module changes stay scoped to product domains', () => {
  for (const [file, unitProfile, integrationProfile] of [
    ['modules/connectivity/internal/adapter/runtime.go', 'telemetry', 'telemetry'],
    ['cmd/connectivity/main.go', 'telemetry', 'telemetry'],
    ['modules/alarm/pkg/alarmservice/http.go', 'alarm', 'alarm'],
    ['modules/workorder/pkg/workorderservice/http.go', 'workorder', 'workorder'],
  ]) {
    const classification = runClassification([file]);
    assert.deepEqual(classification.unitProfiles, ['go', unitProfile].sort());
    assert.deepEqual(classification.integrationProfiles, [integrationProfile]);
    assert.equal(classification.broad, false);
  }
});

test('shared grant signing changes select every domain that signs or verifies grants', () => {
  const classification = runClassification(['libs/grant/grant.go']);
  assert.deepEqual(classification.unitProfiles, ['command', 'go', 'platform', 'registry', 'telemetry']);
  assert.equal(classification.broad, false);
});

test('Phase 1 migration changes rerun the migration integration test and the freeze check', () => {
  for (const file of ['deploy/platform/phase1/migrations/manifest.v1.json', 'modules/alarm/migrations/001_s4_alarm_runtime.sql', 'services/notification-service/migrations/002_wechat_reminders.sql']) {
    const classification = runClassification([file]);
    assert.ok(classification.integrationProfiles.includes('platform'), file);
    assert.ok(classification.contractProfiles.includes('core'), file);
  }
  const plan = runPlan('integration', ['platform']);
  assert.ok(plan.commands.includes('npm run deployment:phase1:migration:test'));
  assert.ok(runPlan('contracts', ['core']).commands.includes('npm run migrations:freeze:check'));
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
  assert.deepEqual(classification.browserProfiles, []);
  assert.equal(classification.broad, false);
});

test('PR workflow exposes only the stable required checks', async () => {
  const workflow = (await readFile('.github/workflows/pr-gates.yml', 'utf8')).replace(/\r\n?/gu, '\n');
  for (const check of ['pr / static', 'pr / contracts', 'pr / affected-unit', 'pr / affected-integration']) {
    assert.equal(workflow.split(`name: ${check}`).length - 1, 1, `required check name drifted: ${check}`);
  }
  assert.ok(!workflow.includes('pr / affected-browser'));
});

test('Go code, Go dependencies, lint rules and the Go check scripts select the go unit profile', () => {
  for (const file of ['.golangci.yml', 'go.work', 'libs/edgecontrol/go.mod', 'modules/telemetry/go.sum', 'cmd/energy-api/main.go', 'scripts/run-go-lint.mjs', 'scripts/run-go-vulnerability-scan.mjs']) {
    const classification = runClassification([file]);
    assert.ok(classification.unitProfiles.includes('go'), file);
    assert.equal(classification.unknown, false, file);
  }
  for (const file of ['apps/hvac-web/src/main.tsx', 'docs/HANDOFF.md', 'services/operations-agent-service/src/index.ts']) {
    assert.ok(!runClassification([file]).unitProfiles.includes('go'), file);
  }
});

test('active workflow filenames no longer encode historical implementation stages', async () => {
  const workflowNames = (await readdir('.github/workflows')).filter((name) => /\.ya?ml$/u.test(name));
  assert.deepEqual(workflowNames.filter((name) => /^(?:s\d+|rms)-/u.test(name)), []);
});
