import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  capabilityTaskMatrix,
  domainTaskProfiles,
  gateProfileSets,
  resolveCapabilityTask,
  resolveDomainCommands,
  resolveGateCommands,
  resolveGateProfileSet,
} from './domain-task-matrix.mjs';

const labels = (commands) => commands.map(({ label }) => label);

const runDomainPlan = (domain, layers) => {
  const result = spawnSync(process.execPath, [
    'scripts/run-domain-task.mjs',
    `--domain=${domain}`,
    `--layers=${layers.join(',')}`,
    '--dry-run=true',
  ], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.trim());
};

const runGateProfileSetPlan = (gate, profileSet) => {
  const result = spawnSync(process.execPath, [
    'scripts/run-pr-gate.mjs',
    `--gate=${gate}`,
    `--profile-set=${profileSet}`,
    '--dry-run=true',
  ], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.trim());
};

const runChecker = (script) => {
  const result = spawnSync(process.execPath, [script], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
};

const runCapabilityPlan = (task) => {
  const result = spawnSync(process.execPath, [
    'scripts/run-capability-task.mjs',
    `--task=${task}`,
    '--dry-run=true',
  ], {
    cwd: process.cwd(),
    encoding: 'utf8',
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.trim());
};

const assertCapabilityPlan = (task, expectedCommands) => {
  const plan = runCapabilityPlan(task);
  assert.deepEqual(plan.commands, expectedCommands);
  assert.deepEqual(plan.commands, labels(resolveCapabilityTask(task)));
};

test('domain matrix exposes stable product domains and layers', () => {
  assert.deepEqual(Object.keys(domainTaskProfiles), [
    'web',
    'platform',
    'registry',
    'telemetry',
    'command',
    'analytics',
    'operations-agent',
    'pocs',
  ]);
  for (const profiles of Object.values(domainTaskProfiles)) {
    assert.deepEqual(Object.keys(profiles), ['contracts', 'unit', 'integration', 'browser']);
  }
});

test('named gate profile sets preserve full and platform-specific coverage', () => {
  assert.deepEqual(gateProfileSets.all.contracts, ['core', 'rms', 's1', 's2', 's3']);
  assert.deepEqual(gateProfileSets.all.unit, [
    'analytics',
    'operations-agent',
    'pocs',
    's0',
    's1',
    's2',
    's3',
    'web',
  ]);
  assert.deepEqual(resolveGateProfileSet('browser', 'browser-windows'), ['rms']);
  assert.deepEqual(resolveGateProfileSet('browser', 'browser-linux'), [
    'operations-agent',
    's0',
    's1',
    's2',
  ]);
});

test('profile-set CLI expansion matches the explicit full profile list', () => {
  const plan = runGateProfileSetPlan('contracts', 'all');
  assert.equal(plan.profileSet, 'all');
  assert.deepEqual(plan.profiles, gateProfileSets.all.contracts);
  assert.deepEqual(
    plan.commands,
    labels(resolveGateCommands('contracts', gateProfileSets.all.contracts)),
  );
});

test('migrated capability tasks preserve their public package entry points', () => {
  assert.deepEqual(Object.keys(capabilityTaskMatrix), [
    's2:telemetry-baseline',
    's2:iam-authorization',
    's2:telemetry-runtime-snapshot',
    's2:history',
    's2:telemetry-ingest',
    's2:gateway-snapshot',
    's2:realtime-backend',
    's3:command-safety',
    's3:command-authority',
    's3:command-api',
    's3:thingsboard-contract',
    's3:command-ux',
    's5:work-order:create-assign',
  ]);
  const packageJson = JSON.parse(readFileSync('package.json', 'utf8'));
  for (const task of Object.keys(capabilityTaskMatrix)) {
    assert.equal(
      packageJson.scripts[task],
      `node scripts/run-capability-task.mjs --task=${task}`,
    );
  }
});

test('capability task CLI preserves command order for migrated aggregates', () => {
  const baselinePlan = runCapabilityPlan('s2:telemetry-baseline');
  assert.deepEqual(baselinePlan.commands, [
    'npm run s2:topology:check',
    'npm run s2:contracts:check',
    'npm run ownership:check',
    'npm run s2:baseline:check',
    'npm run s2:ownership:check',
    'npm run s2:public-contract:check',
    'npm run s2:rollout-gates:check',
    'npm run s2:implementation-plan:check',
    'npm run contracts:check',
    'npm run release:evidence-assets',
    'npm run s1:registry:check',
    'npm run test:ownership',
    'npm run test:registry-routing',
    'node scripts/run-go.mjs test ./services/telemetry-runtime-service/...',
    'npm run lint',
    'npm run build',
    'npm run s2:postgres',
  ]);
  assert.deepEqual(baselinePlan.commands, labels(resolveCapabilityTask('s2:telemetry-baseline')));

  assertCapabilityPlan('s2:iam-authorization', [
    'npm run s2:topology:check',
    'npm run s2:iam:check',
    'node scripts/run-go.mjs test ./libs/telemetryauth/...',
    'node scripts/run-go.mjs test ./services/iam-service/...',
    'npm run ownership:check',
    'npm run s2:baseline:check',
    'npm run s1:registry:check',
    'npm run release:evidence-assets',
    'npm run lint',
    'npm run build',
    'npm run s2:iam:postgres',
  ]);

  assertCapabilityPlan('s2:telemetry-runtime-snapshot', [
    'npm run s2:topology:check',
    'npm run s2:runtime:check',
    'node scripts/run-go.mjs test ./services/telemetry-runtime-service/...',
    'node scripts/run-go.mjs vet ./services/telemetry-runtime-service/...',
    'npm run build:telemetry-runtime',
    'npm run ownership:check',
    'npm run s2:baseline:check',
    'npm run s2:iam:check',
    'npm run s2:contracts:check',
    'npm run contracts:check',
    'npm run release:evidence-assets',
    'npm run lint',
    'npm run build',
    'npm run s2:runtime:postgres',
  ]);

  assertCapabilityPlan('s2:history', [
    'npm run s2:history:check',
    'node scripts/run-go.mjs test ./services/telemetry-runtime-service/...',
    'node scripts/run-go.mjs vet ./services/telemetry-runtime-service/...',
    'npm run build:telemetry-history-projector',
    'npm run s2:history:integration',
  ]);

  assertCapabilityPlan('s2:telemetry-ingest', [
    'npm run s2:topology:check',
    'npm run s2:ingest:check',
    'node scripts/run-go.mjs test ./services/telemetry-runtime-service/...',
    'node scripts/run-go.mjs vet ./services/telemetry-runtime-service/...',
    'npm run build:telemetry-runtime',
    'npm run ownership:check',
    'npm run s2:baseline:check',
    'npm run s2:runtime:check',
    'npm run s2:iam:check',
    'npm run s2:contracts:check',
    'npm run contracts:check',
    'npm run release:evidence-assets',
    'npm run lint',
    'npm run build',
    'npm run s2:ingest:postgres',
  ]);

  assertCapabilityPlan('s2:gateway-snapshot', [
    'npm run s2:topology:check',
    'npm run s2:gateway:check',
    'npm run s2:contracts:check',
    'npm run ownership:check',
    'npm run s2:baseline:check',
    'npm run s2:ownership:check',
    'npm run s2:public-contract:check',
    'npm run s2:rollout-gates:check',
    'npm run s2:implementation-plan:check',
    'npm run s2:iam:check',
    'npm run s2:runtime:check',
    'npm run contracts:check',
    'npm run release:evidence-assets',
    'npm run test:gateway',
    'node scripts/run-go.mjs test ./libs/telemetryauth/... ./services/iam-service/... ./services/telemetry-runtime-service/...',
    'node scripts/run-go.mjs vet ./services/platform-gateway/...',
    'npm run build:gateway',
    'npm run lint',
    'npm run build',
    'npm run s2:gateway:browser',
  ]);

  const realtimePlan = runCapabilityPlan('s2:realtime-backend');
  assert.deepEqual(realtimePlan.commands, [
    'npm run s2:topology:check',
    'npm run s2:realtime:check',
    'npm run s2:contracts:check',
    'npm run ownership:check',
    'npm run s2:baseline:check',
    'npm run s2:ownership:check',
    'npm run s2:public-contract:check',
    'npm run s2:rollout-gates:check',
    'npm run s2:iam:check',
    'npm run s2:runtime:check',
    'npm run s2:centrifugo:check',
    'npm run contracts:check',
    'npm run release:evidence-assets',
    'npm run test:gateway',
    'node scripts/run-go.mjs test ./libs/telemetryauth/... ./services/iam-service/... ./services/telemetry-runtime-service/...',
    'node scripts/run-go.mjs vet ./services/platform-gateway/... ./services/telemetry-runtime-service/...',
    'npm run build:gateway',
    'npm run build:telemetry-runtime',
    'npm run lint',
    'npm run build',
    'npm run s2:realtime:postgres',
    'npm run s2:realtime:config',
    'npm run s2:realtime:transport',
  ]);
  assert.deepEqual(realtimePlan.commands, labels(resolveCapabilityTask('s2:realtime-backend')));

  const safetyPlan = runCapabilityPlan('s3:command-safety');
  assert.deepEqual(safetyPlan.commands, [
    'npm run s3:baseline:check',
    'npm run ownership:check',
    'node scripts/run-go.mjs test ./libs/commandmodel/... ./services/command-service/... ./services/command-dispatcher/... ./services/thingsboard-connector-control/...',
    'node scripts/run-go.mjs vet ./libs/commandmodel/... ./services/command-service/... ./services/command-dispatcher/... ./services/thingsboard-connector-control/...',
  ]);
  assert.deepEqual(safetyPlan.commands, labels(resolveCapabilityTask('s3:command-safety')));

  const commandPlan = runCapabilityPlan('s3:command-authority');
  assert.deepEqual(commandPlan.commands, [
    'npm run s3:postgres:check',
    'npm run s3:governance-dispatch:check',
    'npm run s3:verification:check',
    'npm run ownership:check',
    'npm run s3:postgres',
    'node scripts/run-go.mjs test ./libs/commandauth/... ./libs/commandmodel/... ./services/command-service/... ./services/command-dispatcher/... ./services/thingsboard-connector-control/...',
    'node scripts/run-go.mjs vet ./libs/commandauth/... ./libs/commandmodel/... ./services/command-service/... ./services/command-dispatcher/... ./services/thingsboard-connector-control/...',
    'npm run lint',
    'npm run build',
  ]);
  assert.deepEqual(commandPlan.commands, labels(resolveCapabilityTask('s3:command-authority')));

  assertCapabilityPlan('s3:command-api', [
    'npm run s3:gateway:check',
    'npm run ownership:check',
    'node scripts/run-go.mjs test ./libs/commandauth/... ./services/iam-service/... ./services/command-service/... ./services/platform-gateway/...',
    'node scripts/run-go.mjs vet ./libs/commandauth/... ./services/iam-service/... ./services/command-service/... ./services/platform-gateway/...',
  ]);

  assertCapabilityPlan('s3:thingsboard-contract', [
    'npm run s3:thingsboard:check',
    'npm run ownership:check',
    'node scripts/run-go.mjs test ./services/thingsboard-connector-control/...',
    'node scripts/run-go.mjs vet ./services/thingsboard-connector-control/...',
    'npm run s3:thingsboard:local',
  ]);

  assertCapabilityPlan('s3:command-ux', [
    'npm run s3:command-ux:check',
    'npm run s3:gateway:check',
    'npm run s3:verification:check',
    'npm run ownership:check',
    'node scripts/run-go.mjs test ./services/command-service/... ./services/platform-gateway/...',
    'node scripts/run-go.mjs vet ./services/command-service/... ./services/platform-gateway/...',
    'npm run lint',
    'npm run build',
  ]);

  assertCapabilityPlan('s5:work-order:create-assign', [
    'npm run s5:work-order',
    'npm run s5:work-order:read-canary:check',
    'npm run s5:work-order:create-assign:check',
    'node scripts/run-go.mjs test -count=1 ./libs/workorderauth/... ./libs/workordermodel/... ./libs/identitycontext/... ./libs/ownershipregistry/... ./services/iam-service/... ./services/platform-gateway/... ./services/work-order-service/...',
    'node scripts/run-go.mjs vet ./libs/workorderauth/... ./libs/workordermodel/... ./libs/identitycontext/... ./libs/ownershipregistry/... ./services/iam-service/... ./services/platform-gateway/... ./services/work-order-service/...',
    'node scripts/run-s5-work-order-postgres-tests.mjs',
    'npm run s5:work-order:create-assign:browser',
  ]);
});

test('S2 capability checkers resolve delegated aggregate commands', () => {
  for (const checker of [
    'scripts/check-s2-telemetry-baseline.mjs',
    'scripts/check-s2-iam-authorization.mjs',
    'scripts/check-s2-telemetry-runtime-snapshot.mjs',
    'scripts/check-s2-telemetry-ingest.mjs',
    'scripts/check-s2-gateway-snapshot.mjs',
    'scripts/check-s2-realtime-backend.mjs',
  ]) runChecker(checker);
});

test('PR gate resolution remains compatible with the previous Operations Agent plan', () => {
  assert.deepEqual(labels(resolveGateCommands('unit', ['operations-agent'])), [
    'npm --prefix services/operations-agent-service ci',
    'npm run operations-agent-service:check',
    'npm run operations-agent:benchmark:test',
    'npm run operations-agent:gateway:check',
    'npm run operations-workspace:test',
    'npm run test:gateway',
  ]);
});

test('domain plans deduplicate shared setup across layers', () => {
  const plan = resolveDomainCommands('operations-agent', ['unit', 'integration']);
  assert.equal(
    labels(plan.commands).filter((label) => label === 'npm --prefix services/operations-agent-service ci').length,
    1,
  );
  assert.ok(labels(plan.commands).includes('npm run operations-agent-service:postgres'));
});

test('telemetry integration maps to all durable telemetry fixtures', () => {
  const plan = runDomainPlan('telemetry', ['integration']);
  assert.deepEqual(plan.profiles.integration, [
    's2-baseline',
    's2-ingest',
    's2-realtime',
    's2-history',
  ]);
  assert.deepEqual(plan.commands, [
    'npm run s2:postgres',
    'npm run s2:ingest:postgres',
    'npm run s2:realtime:postgres',
    'npm run s2:history:integration',
  ]);
});

test('command browser layer is explicitly empty rather than borrowing another domain suite', () => {
  const plan = runDomainPlan('command', ['browser']);
  assert.deepEqual(plan.commands, []);
});
