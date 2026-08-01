import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createPackageScriptLongChainBaseline,
  findDocumentationViolations,
  findPackageScriptViolations,
  findTrackedArtifactViolations,
  findWorkflowViolations,
} from './check-repository-governance.mjs';

test('tracked artifact checks reject transient content but defer local worktrees', () => {
  assert.deepEqual(findTrackedArtifactViolations([
    'services/platform-gateway/server.log',
    'out/generated-report.json',
    '.worktrees/deferred-checkout/source.go',
    '.scratch/go-data-ai-platform/spec.md',
    'services/platform-gateway/main.go',
  ]), [
    'services/platform-gateway/server.log: transient file type is tracked',
    'out/generated-report.json: generated, local coordination, or archived runtime content is tracked',
  ]);
});

test('documentation checks cover service catalog, runtime modes, and React major version', () => {
  assert.deepEqual(findDocumentationViolations({
    serviceNames: ['alarm-service', 'platform-gateway'],
    rootReadme: '`alarm-service/`\nnpm run dev:demo',
    appReadme: 'Vite + React 18',
    reactVersion: '19.2.7',
  }), [
    'README.md: missing service catalog entry `platform-gateway/`',
    'README.md: missing runtime command `npm run dev:real`',
    'apps/hvac-web/README.md: expected React 19 runtime documentation',
  ]);
});

test('workflow checks require repository governance in the protected static gate', () => {
  assert.deepEqual(findWorkflowViolations(`
    - run: npm run --silent repo:check
  `), [
    '.github/workflows/pr-gates.yml: missing static gate `npm run --silent repo:governance:test`',
  ]);
  assert.deepEqual(findWorkflowViolations(`
    - run: npm run --silent repo:check
    - run: npm run --silent repo:governance:test
  `), []);
});

test('package script governance ratchets long chains and capability delegates', () => {
  const scripts = {
    legacy: 'one && two && three && four && five',
    migrated: 'node scripts/run-capability-task.mjs --task=migrated',
    compact: 'one && two && three && four',
  };
  const baseline = createPackageScriptLongChainBaseline(scripts);
  const capabilityTasks = { migrated: [] };

  assert.deepEqual(findPackageScriptViolations({ scripts, baseline, capabilityTasks }), []);
  assert.deepEqual(Object.keys(baseline.scripts), ['legacy']);

  const newLongChain = {
    ...scripts,
    added: 'one && two && three && four && five',
  };
  assert.ok(findPackageScriptViolations({
    scripts: newLongChain,
    baseline,
    capabilityTasks,
  }).some((violation) => violation.includes('script `added` contains 5 inline commands')));

  const modifiedLongChain = {
    ...scripts,
    legacy: `${scripts.legacy} && six`,
  };
  assert.ok(findPackageScriptViolations({
    scripts: modifiedLongChain,
    baseline,
    capabilityTasks,
  }).some((violation) => violation.includes('long-chain script `legacy` changed')));

  const revertedCapability = {
    ...scripts,
    migrated: 'one && two && three && four && five',
  };
  assert.ok(findPackageScriptViolations({
    scripts: revertedCapability,
    baseline,
    capabilityTasks,
  }).some((violation) => violation.includes('capability task `migrated` must delegate')));

  const removedLegacy = { ...scripts };
  delete removedLegacy.legacy;
  assert.ok(findPackageScriptViolations({
    scripts: removedLegacy,
    baseline,
    capabilityTasks,
  }).some((violation) => violation.includes('stale exemption for `legacy`')));
});
