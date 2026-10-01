import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import test from 'node:test';

import {
  domainTaskProfiles,
  gateProfileSets,
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

test('domain matrix exposes stable product domains and standard layers', () => {
  assert.deepEqual(Object.keys(domainTaskProfiles), [
    'web',
    'platform',
    'registry',
    'telemetry',
    'command',
    'alarm',
    'workorder',
    'analytics',
    'operations-agent',
    'pocs',
  ]);
  for (const profiles of Object.values(domainTaskProfiles)) {
    assert.deepEqual(Object.keys(profiles), ['contracts', 'unit', 'integration', 'browser']);
  }
});

test('gate profiles use product domains rather than implementation stage numbers', () => {
  assert.deepEqual(gateProfileSets.all.contracts, ['command', 'core', 'registry', 'telemetry', 'web']);
  assert.deepEqual(gateProfileSets.all.unit, [
    'alarm',
    'analytics',
    'command',
    'operations-agent',
    'platform',
    'pocs',
    'registry',
    'telemetry',
    'web',
    'workorder',
  ]);
  assert.deepEqual(resolveGateProfileSet('browser', 'all'), [
    'platform',
    'telemetry',
    'workorder',
  ]);

  const serialized = JSON.stringify({ gateProfileSets, domainTaskProfiles });
  assert.doesNotMatch(serialized, /"(?:rms|s\d+(?:-[^"]*)?)"/u);
});

test('static gate has one canonical repository check chain', () => {
  assert.deepEqual(labels(resolveGateCommands('static')), [
    'npm run repo:check',
    'npm run design:check',
    'npm run lint',
    'npm run build',
  ]);
});

test('profile-set CLI expansion matches the matrix', () => {
  const plan = runGateProfileSetPlan('contracts', 'all');
  assert.equal(plan.profileSet, 'all');
  assert.deepEqual(plan.profiles, gateProfileSets.all.contracts);
  assert.deepEqual(plan.commands, labels(resolveGateCommands('contracts', gateProfileSets.all.contracts)));
});

test('telemetry integration is one domain profile with all durable fixtures', () => {
  const plan = runDomainPlan('telemetry', ['integration']);
  assert.deepEqual(plan.profiles.integration, ['telemetry']);
  assert.deepEqual(plan.commands, [
    'node scripts/run-s2-telemetry-postgres-tests.mjs',
    'node scripts/run-s2-telemetry-ingest-postgres-tests.mjs',
    'node scripts/run-s2-realtime-postgres-tests.mjs',
    'node scripts/run-s2-telemetry-history-tests.mjs',
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

test('command browser layer is explicitly empty', () => {
  const plan = runDomainPlan('command', ['browser']);
  assert.deepEqual(plan.commands, []);
});

test('root npm task surface rejects historical stage-number commands', () => {
  const packageJson = JSON.parse(readFileSync('package.json', 'utf8'));
  const historical = Object.keys(packageJson.scripts).filter((name) => /^s\d+:/u.test(name));
  assert.deepEqual(historical, []);
});

test('ticket capability runner is physically retired', () => {
  assert.equal(existsSync('scripts/run-capability-task.mjs'), false);
});
