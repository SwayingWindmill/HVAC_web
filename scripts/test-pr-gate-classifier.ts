import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { readFile, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import {
  findDebugMarkerViolations,
  findHandoffBudgetViolations,
  findRequiredGuardrailFileViolations,
  findSkillMirrorViolations,
  findTrackedArtifactViolations,
  maximumHandoffBytes,
} from './check-repository-governance.ts';

interface ClassificationReport {
  changedFiles: string[];
  broad: boolean;
  unknown: boolean;
  contracts: boolean;
  units: boolean;
  integrations: boolean;
  browsers: boolean;
  contractProfiles: string[];
  unitProfiles: string[];
  integrationProfiles: string[];
  browserProfiles: string[];
}

interface GatePlan {
  commands: string[];
}

const runClassification = (files: readonly string[]): ClassificationReport => {
  const result = spawnSync(process.execPath, ['--experimental-strip-types', 'scripts/classify-pr-gates.ts', `--files=${files.join(',')}`], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.trim()) as ClassificationReport;
};

const runPlan = (gate: string, profiles: readonly string[]): GatePlan => {
  const result = spawnSync(process.execPath, ['--experimental-strip-types', 'scripts/run-pr-gate.ts', `--gate=${gate}`, `--profiles=${profiles.join(',')}`, '--dry-run=true'], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.trim()) as GatePlan;
};

test('documentation-only changes keep expensive affected gates idle', () => {
  for (const file of [
    'docs/architecture/overview.md',
    '.mwgrc',
    'CLAUDE.md',
    '.agents/skills/correct/SKILL.md',
    '.claude/skills/correct/SKILL.md',
  ]) {
    const classification = runClassification([file]);
    assert.equal(classification.contracts, false);
    assert.equal(classification.units, false);
    assert.equal(classification.integrations, false);
    assert.equal(classification.browsers, false);
    assert.equal(classification.broad, false);
    assert.equal(classification.unknown, false);
  }
});

test('package-lock changes compile and unit test without database or browser fan-out', () => {
  const classification = runClassification(['package-lock.json']);
  assert.deepEqual(classification.unitProfiles, ['web']);
  assert.deepEqual(classification.integrationProfiles, []);
  assert.deepEqual(classification.browserProfiles, []);
});

test('HVAC Web changes select stable domain profiles on the Linux browser runner', () => {
  for (const file of ['apps/hvac-web/src/app/router.ts', 'scripts/check-design-system.ts']) {
    const classification = runClassification([file]);
    assert.deepEqual(classification.unitProfiles, ['web']);
    assert.deepEqual(classification.browserProfiles, ['telemetry']);
    assert.equal(classification.integrations, false);
    assert.equal(classification.unknown, false);
  }
});

test('telemetry changes select telemetry unit and durable integration profiles', () => {
  const classification = runClassification(['modules/telemetry/pkg/telemetry/realtime.go']);
  assert.deepEqual(classification.unitProfiles, ['go', 'telemetry']);
  assert.deepEqual(classification.integrationProfiles, ['telemetry']);
  assert.equal(classification.broad, false);
});

test('ClickHouse schema and Cube model changes run the analytics integration profile', () => {
  for (const file of ['infra/telemetry/clickhouse/init/015-energy-hourly-rollup.sql', 'infra/telemetry/clickhouse/ops/backfill-energy-hourly-rollup.sh', 'semantic/cube/model/cubes/energy_facts.yml']) {
    const classification = runClassification([file]);
    assert.ok(classification.unitProfiles.includes('analytics'), `${file} skipped analytics units`);
    assert.ok(classification.integrationProfiles.includes('analytics'), `${file} skipped analytics integration`);
  }
});

test('domain module changes stay scoped to product domains', () => {
  for (const [file, unitProfile, integrationProfile] of [
    ['libs/telemetryhistorymodel/aggregation.go', 'telemetry', 'telemetry'],
    ['modules/connectivity/internal/adapter/runtime.go', 'telemetry', 'telemetry'],
    ['cmd/connectivity/main.go', 'telemetry', 'telemetry'],
    ['cmd/telemetry-worker/main.go', 'telemetry', 'telemetry'],
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
    'scripts/domain-task-matrix.ts',
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

test('repository governance blocks tracked .audit scratch files and oversized docs/HANDOFF.md', () => {
  assert.equal(findTrackedArtifactViolations(['.audit/check.todo.md']).length, 1);
  assert.equal(findHandoffBudgetViolations({ bytes: maximumHandoffBytes }).length, 0);
  const overflow = findHandoffBudgetViolations({ bytes: maximumHandoffBytes + 1 });
  assert.equal(overflow.length, 1);
  assert.ok(overflow[0].includes('exceeds 28672-byte single-read budget'));
});

test('repository governance enforces 1:1 skill parity, required hook files, and clean hook gate classification', () => {
  const scratchRoot = mkdtempSync(join(tmpdir(), 'hvac-skill-parity-'));
  try {
    mkdirSync(join(scratchRoot, '.agents/skills/sample'), { recursive: true });
    mkdirSync(join(scratchRoot, '.claude/skills/sample'), { recursive: true });
    writeFileSync(join(scratchRoot, '.agents/skills/sample/SKILL.md'), 'same\n');
    writeFileSync(join(scratchRoot, '.claude/skills/sample/SKILL.md'), 'same\n');

    assert.deepEqual(
      findSkillMirrorViolations({
        root: scratchRoot,
        trackedFiles: [
          '.agents/skills/MATTPOCOCK-LICENSE.txt',
          '.agents/skills/PSTACK-LICENSE.txt',
          '.claude/skills/README.md',
          '.agents/skills/sample/SKILL.md',
          '.claude/skills/sample/SKILL.md',
        ],
      }),
      [],
    );

    assert.equal(
      findSkillMirrorViolations({
        root: scratchRoot,
        trackedFiles: ['.agents/skills/stray.md', '.agents/skills/foo/SKILL.md', '.claude/skills/bar/SKILL.md'],
      }).length,
      3,
    );

    writeFileSync(join(scratchRoot, '.claude/skills/sample/SKILL.md'), 'drifted\n');
    const driftViolations = findSkillMirrorViolations({
      root: scratchRoot,
      trackedFiles: ['.agents/skills/sample/SKILL.md', '.claude/skills/sample/SKILL.md'],
    });
    assert.equal(driftViolations.length, 1);
    assert.match(driftViolations[0], /byte content drifted/);

    rmSync(join(scratchRoot, '.claude/skills/sample/SKILL.md'));
    const missingDiskViolations = findSkillMirrorViolations({
      root: scratchRoot,
      trackedFiles: ['.agents/skills/sample/SKILL.md', '.claude/skills/sample/SKILL.md'],
    });
    assert.equal(missingDiskViolations.length, 1);
    assert.match(missingDiskViolations[0], /missing on disk/);
  } finally {
    rmSync(scratchRoot, { recursive: true, force: true });
  }

  assert.equal(
    findRequiredGuardrailFileViolations([
      '.agents/hooks.json',
      '.claude/settings.json',
      'scripts/hooks/pre-tool-guard.ts',
      'tsconfig.scripts.json',
      'scripts/web-as-cast-baseline.json',
      'scripts/coverage-baseline.json',
      'scripts/check-coverage-regression.ts',
    ]).length,
    0,
  );
  assert.equal(findRequiredGuardrailFileViolations(['.agents/hooks.json']).length, 6);

  const debugSandbox = mkdtempSync(join(tmpdir(), 'hvac-debug-marker-'));
  try {
    mkdirSync(join(debugSandbox, 'apps/hvac-web/src'), { recursive: true });
    writeFileSync(join(debugSandbox, 'apps/hvac-web/src/leak.ts'), `console.log('[DEBUG-' + '9999]');\n`);
    assert.equal(findDebugMarkerViolations({ root: debugSandbox, trackedFiles: ['apps/hvac-web/src/leak.ts'] }).length, 0);
    writeFileSync(join(debugSandbox, 'apps/hvac-web/src/leak.ts'), `console.log('[DEBUG-${'1234'}]');\n`);
    const markerViolations = findDebugMarkerViolations({ root: debugSandbox, trackedFiles: ['apps/hvac-web/src/leak.ts'] });
    assert.equal(markerViolations.length, 1);
    assert.ok(markerViolations[0].includes('leftover debug marker'));
  } finally {
    rmSync(debugSandbox, { recursive: true, force: true });
  }

  for (const file of [
    '.agents/hooks.json',
    '.claude/settings.json',
    'scripts/hooks/pre-tool-guard.ts',
    'scripts/test-pre-tool-guard.ts',
    'scripts/javascript-tooling-baseline.json',
    'scripts/web-as-cast-baseline.json',
    'scripts/coverage-baseline.json',
    'scripts/check-coverage-regression.ts',
  ]) {
    const classification = runClassification([file]);
    assert.equal(classification.unknown, false, file);
    assert.equal(classification.broad, false, file);
  }

  const tsconfigClassification = runClassification(['tsconfig.scripts.json']);
  assert.equal(tsconfigClassification.unknown, false, 'tsconfig.scripts.json');
  assert.equal(tsconfigClassification.broad, true, 'tsconfig.scripts.json');
});
