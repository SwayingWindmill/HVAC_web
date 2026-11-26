import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { readFile, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import {
  findComposePortExposureViolations,
  findDebugMarkerViolations,
  findHandoffBudgetViolations,
  findPackageScriptViolations,
  findRequiredGuardrailFileViolations,
  findSkillMirrorViolations,
  findTrackedArtifactViolations,
  findVerifyFeatureMapViolations,
  maximumHandoffBytes,
  splitArgsByLength,
} from './check-repository-governance.ts';
import { goExecutableName } from './lib/go-workspace.mjs';

interface ClassificationReport {
  changedFiles: string[];
  broad: boolean;
  unknown: boolean;
  contracts: boolean;
  units: boolean;
  unitGoLint: boolean;
  unitWeb: boolean;
  unitDomain: boolean;
  integrations: boolean;
  browsers: boolean;
  contractProfiles: string[];
  unitProfiles: string[];
  unitDomainProfiles: string[];
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
    '.mcp.json',
    'changelog.d/README.md',
    'changelog.d/2026-11-20-sample.md',
    'CLAUDE.md',
    'GLOSSARY.md',
    '.agents/skills/correct/SKILL.md',
    '.claude/skills/correct/SKILL.md',
    '.opencodereview/rule.json',
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
  for (const file of [
    'apps/hvac-web/src/app/router.ts',
    'scripts/check-design-system.ts',
    'scripts/check-verify-runtime.ts',
    'scripts/test-verify-runtime.ts',
    'scripts/verify-live-web.ts',
    'scripts/test-verify-live-web.ts',
    'scripts/lib/local-administrator-browser.d.mts',
  ]) {
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
  const exprClassification = runClassification(['libs/alarmexpr/expr.go']);
  assert.ok(exprClassification.unitProfiles.includes('go'), 'expected libs/alarmexpr changes to run go lint');
  assert.ok(exprClassification.unitProfiles.includes('alarm'), 'expected libs/alarmexpr changes to run alarm unit profile');
  assert.ok(exprClassification.unitProfiles.includes('command'), 'expected libs/alarmexpr changes to run command/edgecontrol unit profile');
  assert.ok(exprClassification.integrationProfiles.includes('alarm'), 'expected libs/alarmexpr changes to run alarm integration profile');

  const classification = runClassification(['libs/grant/grant.go']);
  assert.deepEqual(classification.unitProfiles, ['command', 'go', 'platform', 'registry', 'telemetry']);
  assert.equal(classification.broad, false);
});

test('semantic plant twin library changes select registry, command, and analytics gates', () => {
  const classification = runClassification(['libs/planttwin/twin.go']);
  assert.deepEqual(classification.unitProfiles, ['analytics', 'command', 'go', 'registry']);
  assert.deepEqual(classification.integrationProfiles, ['analytics', 'command', 'registry']);
  assert.equal(classification.unknown, false);
  assert.equal(classification.broad, false);
});

test('Phase 1 migration changes rerun the migration integration test and the freeze check without broad fan-out', () => {
  for (const file of ['deploy/platform/phase1/migrations/manifest.v1.json', 'modules/alarm/migrations/001_s4_alarm_runtime.sql', 'services/notification-service/migrations/002_wechat_reminders.sql']) {
    const classification = runClassification([file]);
    assert.ok(classification.integrationProfiles.includes('platform'), file);
    assert.ok(classification.contractProfiles.includes('core'), file);
  }
  const manifestClassification = runClassification(['deploy/platform/phase1/migrations/manifest.v1.json']);
  assert.equal(manifestClassification.broad, false);
  assert.deepEqual(manifestClassification.contractProfiles, ['core']);
  assert.deepEqual(manifestClassification.integrationProfiles, ['platform']);
  assert.equal(manifestClassification.unitWeb, false);

  const plan = runPlan('integration', ['platform']);
  assert.ok(plan.commands.includes('npm run deployment:phase1:migration:test'));
  const contractCommands = runPlan('contracts', ['core']).commands;
  assert.ok(contractCommands.includes('npm run migrations:freeze:check'));
  assert.ok(contractCommands.includes('npm run migrations:conventions:check'));

  for (const scriptFile of ['scripts/check-migration-conventions.ts', 'scripts/migration-conventions-baseline.json']) {
    const scriptClassification = runClassification([scriptFile]);
    assert.ok(scriptClassification.contractProfiles.includes('core'), scriptFile);
  }

  const cdClassification = runClassification(['deploy/platform/phase1/cd/smoke-release.sh']);
  assert.equal(cdClassification.unknown, false);
  assert.equal(cdClassification.broad, false);
  assert.deepEqual(cdClassification.unitDomainProfiles, ['platform']);

  for (const backupFile of ['deploy/platform/phase1/backup/sync-offsite-restic.sh', 'deploy/platform/phase1/observability/host-textfile.sh']) {
    const backupClassification = runClassification([backupFile]);
    assert.equal(backupClassification.unknown, false);
    assert.equal(backupClassification.broad, false);
    assert.deepEqual(backupClassification.unitDomainProfiles, ['platform'], backupFile);
  }

  const promoClassification = runClassification(['tools/promo-video/package-lock.json']);
  assert.equal(promoClassification.unknown, false);
  assert.equal(promoClassification.broad, false);
  assert.equal(promoClassification.units, false);
});

test('unit classification splits go lint, web, and domain Go tests cleanly', () => {
  const mixed = runClassification(['apps/hvac-web/src/app/router.ts', 'modules/alarm/pkg/alarmrules/rules.go']);
  assert.equal(mixed.unitGoLint, true);
  assert.equal(mixed.unitWeb, true);
  assert.equal(mixed.unitDomain, true);
  assert.deepEqual(mixed.unitDomainProfiles, ['alarm']);

  const webOnly = runClassification(['apps/hvac-web/src/app/router.ts']);
  assert.equal(webOnly.unitGoLint, false);
  assert.equal(webOnly.unitWeb, true);
  assert.equal(webOnly.unitDomain, false);
  assert.deepEqual(webOnly.unitDomainProfiles, []);
});

test('package, workflow, and central task-matrix changes fail closed to broad contract and unit coverage', () => {
  for (const file of [
    'package.json',
    '.github/workflows/pr-gates.yml',
    'scripts/domain-task-matrix.ts',
  ]) {
    const classification = runClassification([file]);
    assert.equal(classification.broad, true);
    assert.equal(classification.unknown, false);
    assert.ok(classification.unitProfiles.includes('operations-agent'));
    assert.deepEqual(classification.integrationProfiles, []);
    assert.deepEqual(classification.browserProfiles, []);
  }
  assert.deepEqual(findPackageScriptViolations({ ok: 'a && b && c && d' }), []);
  assert.equal(findPackageScriptViolations({ bad: 'a && b && c && d && e', 's1:old': 'echo' }).length, 2);
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
  for (const file of ['.golangci.yml', 'go.mod', 'go.sum', 'go.work', 'libs/edgecontrol/go.mod', 'tools/eg8200-simulator/go.sum', 'cmd/energy-api/main.go', 'scripts/run-go.mjs', 'scripts/run-go-lint.mjs', 'scripts/run-go-vulnerability-scan.mjs']) {
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
      'scripts/web-file-lines-baseline.json',
      'scripts/coverage-baseline.json',
      'scripts/check-coverage-regression.ts',
      'scripts/verify-live-web.ts',
      '.agents/skills/command-safety/SKILL.md',
      '.claude/skills/command-safety/SKILL.md',
      'scripts/run-command-formal-check.ts',
      'scripts/migration-conventions-baseline.json',
      'scripts/check-migration-conventions.ts',
      'scripts/sync-phase1-migrations.ts',
      'scripts/test-sync-phase1-migrations.ts',
      'scripts/check-clickhouse-sql.ts',
      'scripts/test-clickhouse-sql.ts',
      'scripts/check-go-test-hygiene.ts',
      'scripts/go-test-sleep-baseline.json',
      'scripts/test-go-test-hygiene.ts',
      'scripts/run-preflight.ts',
      'changelog.d/README.md',
      'scripts/changelog-fragments.ts',
      'scripts/test-changelog-fragments.ts',
    ]).length,
    0,
  );
  assert.equal(findRequiredGuardrailFileViolations(['.agents/hooks.json']).length, 24);

  const verifyMapSandbox = mkdtempSync(join(tmpdir(), 'hvac-verify-map-'));
  try {
    mkdirSync(join(verifyMapSandbox, '.agents/skills/verify/features'), { recursive: true });
    mkdirSync(join(verifyMapSandbox, 'apps/hvac-web/e2e'), { recursive: true });
    const readmePath = '.agents/skills/verify/features/README.md';
    const sampleMapPath = '.agents/skills/verify/features/sample.md';
    const sampleSpecPath = 'apps/hvac-web/e2e/sample.spec.ts';
    const validMapBody = [
      '# Sample',
      '## Sub-features',
      '- Demo',
      '## How to get to it (user POV)',
      '- Open `/sample`',
      '## Driving it with Playwright',
      '- `e2e/sample.spec.ts`',
      '## Gotchas',
      '- None',
      '',
    ].join('\n');
    writeFileSync(join(verifyMapSandbox, readmePath), '- [Sample](sample.md)\n');
    writeFileSync(join(verifyMapSandbox, sampleMapPath), validMapBody);
    writeFileSync(join(verifyMapSandbox, sampleSpecPath), '// spec\n');

    assert.deepEqual(
      findVerifyFeatureMapViolations({
        root: verifyMapSandbox,
        trackedFiles: [readmePath, sampleMapPath, sampleSpecPath],
      }),
      [],
    );

    const unmappedSpec = findVerifyFeatureMapViolations({
      root: verifyMapSandbox,
      trackedFiles: [readmePath, sampleMapPath, sampleSpecPath, 'apps/hvac-web/e2e/foo.spec.ts'],
    });
    assert.equal(unmappedSpec.length, 1);
    assert.match(unmappedSpec[0], /apps\/hvac-web\/e2e\/foo\.spec\.ts: unmapped Playwright spec/);

    writeFileSync(join(verifyMapSandbox, readmePath), '# Empty index\n');
    writeFileSync(join(verifyMapSandbox, sampleMapPath), '# Sample\n## Sub-features\n- Demo\n');
    const brokenMap = findVerifyFeatureMapViolations({
      root: verifyMapSandbox,
      trackedFiles: [readmePath, sampleMapPath],
    });
    assert.equal(brokenMap.length, 4);
    assert.ok(brokenMap.some((v) => v.includes('not indexed in')));
    assert.ok(brokenMap.some((v) => v.includes('missing required heading "## Gotchas"')));
  } finally {
    rmSync(verifyMapSandbox, { recursive: true, force: true });
  }

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
    'scripts/web-file-lines-baseline.json',
    'scripts/coverage-baseline.json',
    'scripts/check-coverage-regression.ts',
    'scripts/sync-phase1-migrations.ts',
    'scripts/test-sync-phase1-migrations.ts',
    'scripts/check-clickhouse-sql.ts',
    'scripts/test-clickhouse-sql.ts',
    'scripts/check-go-test-hygiene.ts',
    'scripts/go-test-sleep-baseline.json',
    'scripts/test-go-test-hygiene.ts',
    'scripts/run-preflight.ts',
  ]) {
    const classification = runClassification([file]);
    assert.equal(classification.unknown, false, file);
    assert.equal(classification.broad, false, file);
  }

  const tsconfigClassification = runClassification(['tsconfig.scripts.json']);
  assert.equal(tsconfigClassification.unknown, false, 'tsconfig.scripts.json');
  assert.equal(tsconfigClassification.broad, true, 'tsconfig.scripts.json');

  const connectivityStore = runClassification(['modules/connectivity/pkg/connectivity/store.go']);
  assert.deepEqual(connectivityStore.unitProfiles, ['command', 'go', 'telemetry']);
  assert.deepEqual(connectivityStore.integrationProfiles, ['telemetry']);
  assert.equal(connectivityStore.broad, false);

  const formalRunner = runClassification(['scripts/run-command-formal-check.ts']);
  assert.deepEqual(formalRunner.unitProfiles, ['command']);
  assert.equal(formalRunner.unknown, false);
  assert.equal(formalRunner.broad, false);

  const siteManifest = runClassification(['deploy/sites/songyaxuan.site.json']);
  assert.deepEqual(siteManifest.unitProfiles, ['alarm', 'command', 'registry', 'telemetry']);
  assert.equal(siteManifest.unknown, false);
  assert.equal(siteManifest.broad, false);

  const templateCatalog = runClassification(['deploy/templates/devices/hvac.power-meter.acrel-dtsd1352.json']);
  assert.deepEqual(templateCatalog.unitProfiles, ['alarm', 'command', 'registry', 'telemetry']);
  assert.equal(templateCatalog.unknown, false);
  assert.equal(templateCatalog.broad, false);

  const onboardingControl = runClassification(['scripts/lib/site-onboarding-control.ts']);
  assert.deepEqual(onboardingControl.unitProfiles, ['command', 'registry', 'telemetry']);
  assert.equal(onboardingControl.unknown, false);
  assert.equal(onboardingControl.broad, false);

  for (const connFile of [
    'scripts/run-mqtt-broker-acl-tests.mjs',
    'scripts/run-mqtt-broker-crl-kick-tests.ts',
  ]) {
    const concClassification = runClassification([connFile]);
    assert.deepEqual(concClassification.unitProfiles, ['telemetry']);
    assert.equal(concClassification.unknown, false, connFile);
    assert.equal(concClassification.broad, false, connFile);
  }

  assert.equal(goExecutableName('gofmt', 'win32'), 'gofmt.exe');
  assert.equal(goExecutableName('gofmt.exe', 'win32'), 'gofmt.exe');
  assert.equal(goExecutableName('gofmt', 'linux'), 'gofmt');
  assert.deepEqual(splitArgsByLength(['a.go', 'b.go', 'c.go', 'd.go'], 10), [
    ['a.go', 'b.go'],
    ['c.go', 'd.go'],
  ]);
  assert.deepEqual(splitArgsByLength([]), []);
});

test('repository governance blocks non-loopback Compose port mappings outside exempted phase1 public listeners', () => {
  const composeSandbox = mkdtempSync(join(tmpdir(), 'hvac-compose-ports-'));
  try {
    mkdirSync(join(composeSandbox, 'infra/alarm'), { recursive: true });
    mkdirSync(join(composeSandbox, 'infra/telemetry'), { recursive: true });
    mkdirSync(join(composeSandbox, 'deploy/platform/phase1'), { recursive: true });
    mkdirSync(join(composeSandbox, 'semantic/cube'), { recursive: true });

    writeFileSync(
      join(composeSandbox, 'infra/alarm/compose.yaml'),
      [
        'services:',
        '  postgres:',
        '    ports:',
        '      - "127.0.0.1:${S4_POSTGRES_HOST_PORT:-55434}:5432"',
        '',
      ].join('\n'),
    );
    writeFileSync(
      join(composeSandbox, 'deploy/platform/phase1/compose.yaml'),
      [
        'services:',
        '  nginx:',
        '    ports:',
        '      - "${HTTPS_PORT:-443}:443"',
        '  mqtt-broker:',
        '    ports:',
        '      - "${MQTT_TLS_PORT:-8883}:8883"',
        '',
      ].join('\n'),
    );
    writeFileSync(
      join(composeSandbox, 'semantic/cube/compose.yaml'),
      [
        'services:',
        '  cube:',
        '    ports: ["127.0.0.1:${CUBE_HOST_PORT:-4000}:4000"]',
        '',
      ].join('\n'),
    );

    assert.deepEqual(
      findComposePortExposureViolations({
        root: composeSandbox,
        trackedFiles: [
          'infra/alarm/compose.yaml',
          'deploy/platform/phase1/compose.yaml',
          'semantic/cube/compose.yaml',
        ],
      }),
      [],
    );

    writeFileSync(
      join(composeSandbox, 'infra/alarm/compose.yaml'),
      [
        'services:',
        '  postgres:',
        '    ports:',
        '      - "${S4_POSTGRES_HOST_PORT:-55434}:5432"',
        '',
      ].join('\n'),
    );
    writeFileSync(
      join(composeSandbox, 'infra/telemetry/compose.yaml'),
      [
        'services:',
        '  pg:',
        '    ports:',
        '      - "0.0.0.0:5432:5432"',
        '  ch:',
        '    ports: ["8123:8123"]',
        '  long:',
        '    ports:',
        '      - target: 6379',
        '        published: 6379',
        '',
      ].join('\n'),
    );
    writeFileSync(
      join(composeSandbox, 'deploy/platform/phase1/ai-solver.compose.yaml'),
      [
        'services:',
        '  ai-solver:',
        '    ports:',
        '      - "${HTTPS_PORT:-443}:443"',
        '',
      ].join('\n'),
    );
    writeFileSync(
      join(composeSandbox, 'deploy/platform/phase1/wsl.override.yaml'),
      [
        'services:',
        '  postgres:',
        '    ports: ["5432:5432"]',
        '  web:',
        '    ports:',
        '      - 8080:80',
        '',
      ].join('\n'),
    );

    const violations = findComposePortExposureViolations({
      root: composeSandbox,
      trackedFiles: [
        'infra/alarm/compose.yaml',
        'infra/telemetry/compose.yaml',
        'deploy/platform/phase1/compose.yaml',
        'deploy/platform/phase1/ai-solver.compose.yaml',
        'deploy/platform/phase1/wsl.override.yaml',
      ],
    });
    assert.equal(violations.length, 7);
    assert.ok(violations.some((v) => v.includes('infra/alarm/compose.yaml:4:')));
    assert.ok(violations.some((v) => v.includes('0.0.0.0:5432:5432')));
    assert.ok(violations.some((v) => v.includes('8123:8123')));
    assert.ok(violations.some((v) => v.includes('host_ip=<unset> target=6379')));
    assert.ok(violations.some((v) => v.includes('deploy/platform/phase1/ai-solver.compose.yaml:4:')));
    assert.ok(violations.some((v) => v.includes('deploy/platform/phase1/wsl.override.yaml:3:')));
    assert.ok(violations.some((v) => v.includes('deploy/platform/phase1/wsl.override.yaml:6:') && v.includes('"8080:80"')));
  } finally {
    rmSync(composeSandbox, { recursive: true, force: true });
  }
});

test('ClickHouse migration governance, restore drill, and observability alerts classify without unknown fallback', () => {
  const chMigration = runClassification(['deploy/platform/phase1/clickhouse-migrations/run-clickhouse-migrations.sh']);
  assert.equal(chMigration.broad, true);
  assert.equal(chMigration.unknown, false);
  assert.ok(chMigration.contractProfiles.includes('core'));
  assert.ok(chMigration.integrationProfiles.includes('platform'));
  assert.ok(chMigration.integrationProfiles.includes('analytics'));

  const chCheck = runClassification(['scripts/check-clickhouse-migrations.ts']);
  assert.equal(chCheck.unknown, false);
  assert.ok(chCheck.contractProfiles.includes('core'));

  const chRestore = runClassification(['scripts/run-phase1-clickhouse-restore-test.ts']);
  assert.equal(chRestore.unknown, false);
  assert.deepEqual(chRestore.unitProfiles, ['platform']);
  assert.deepEqual(chRestore.integrationProfiles, ['platform']);

  const obs = runClassification(['infra/observability/alerts/platform-dependencies.yaml']);
  assert.equal(obs.unknown, false);
  assert.deepEqual(obs.contractProfiles, ['core']);

  const coreContracts = runPlan('contracts', ['core']).commands;
  assert.ok(coreContracts.includes('npm run changelog:check'));
  assert.ok(coreContracts.includes('npm run migrations:clickhouse:check'));
  const platformIntegration = runPlan('integration', ['platform']).commands;
  assert.ok(platformIntegration.includes('npm run deployment:phase1:clickhouse:restore:test'));
});

test('CHANGELOG.md and changelog-fragments tooling run contracts core without broad fan-out', () => {
  for (const file of ['CHANGELOG.md', 'scripts/changelog-fragments.ts', 'scripts/test-changelog-fragments.ts']) {
    const classification = runClassification([file]);
    assert.deepEqual(classification.contractProfiles, ['core'], file);
    assert.equal(classification.units, false, file);
    assert.equal(classification.broad, false, file);
    assert.equal(classification.unknown, false, file);
  }
});

test('Python AI solver service changes run analytics unit tests cleanly without broad or unknown fallback', () => {
  const classification = runClassification(['services/ai-solver-service/ai_solver/solver.py']);
  assert.equal(classification.unknown, false);
  assert.equal(classification.broad, false);
  assert.deepEqual(classification.contractProfiles, ['core']);
  assert.deepEqual(classification.unitProfiles, ['analytics']);
  assert.deepEqual(classification.integrationProfiles, []);
  assert.deepEqual(classification.browserProfiles, []);

  const analyticsUnitCommands = runPlan('unit', ['analytics']).commands;
  assert.ok(analyticsUnitCommands.includes('npm run ai-solver:test'));
});


test('edge-gateway daemon and packaging changes stay scoped to command and go gates', () => {
  const daemonGo = runClassification(['cmd/edge-gateway/main.go']);
  assert.deepEqual(daemonGo.unitProfiles, ['command', 'go']);
  assert.equal(daemonGo.broad, false);
  assert.equal(daemonGo.unknown, false);

  const daemonDeploy = runClassification(['deploy/edge-gateway/hvac-edge-gateway.service']);
  assert.deepEqual(daemonDeploy.unitProfiles, ['command']);
  assert.equal(daemonDeploy.broad, false);
  assert.equal(daemonDeploy.unknown, false);
});
