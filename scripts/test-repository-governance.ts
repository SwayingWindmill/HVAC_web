import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';

import {
  createPackageScriptLongChainBaseline,
  findDocumentationViolations,
  findJavascriptToolingViolations,
  findLinguistViolations,
  findPackageScriptViolations,
  findTrackedArtifactViolations,
  findWorkflowViolations,
} from './check-repository-governance.ts';

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

test('JavaScript tooling governance ratchets legacy paths and rejects product/runtime JavaScript', () => {
  const files = [
    { path: 'scripts/legacy.mjs', bytes: 10 },
    { path: 'services/operations-agent-service/test/runtime.test.mjs', bytes: 20 },
  ];
  const pathSetSha256 = createHash('sha256')
    .update(`${files.map(({ path }) => path).sort().join('\n')}\n`)
    .digest('hex');
  const baseline = {
    schemaVersion: 1,
    policy: 'legacy-js-ratchet',
    trackedExtensions: ['.js', '.mjs', '.cjs', '.jsx'],
    allowedLegacyRoots: {
      'scripts/': { fileCount: 1, maxBytes: 10 },
      'services/operations-agent-service/test/': { fileCount: 1, maxBytes: 20 },
    },
    fileCount: 2,
    maxBytes: 30,
    pathSetSha256,
  };

  assert.deepEqual(findJavascriptToolingViolations({ files, baseline }), []);

  const productJavaScript = [
    ...files,
    { path: 'apps/hvac-web/src/runtime.js', bytes: 1 },
  ];
  const productViolations = findJavascriptToolingViolations({ files: productJavaScript, baseline });
  assert.ok(productViolations.some((violation) => violation.includes('outside reviewed legacy tooling roots')));
  assert.ok(productViolations.some((violation) => violation.includes('JavaScript path set changed')));

  const growthViolations = findJavascriptToolingViolations({
    files: files.map((file) => file.path === 'scripts/legacy.mjs' ? { ...file, bytes: 11 } : file),
    baseline,
  });
  assert.ok(growthViolations.some((violation) => violation.includes('JavaScript bytes grew')));
  assert.ok(growthViolations.some((violation) => violation.includes('scripts/ JavaScript bytes grew')));
});

test('Linguist exclusions cover ancillary tooling without touching product/runtime paths', () => {
  const attributes = [
    'scripts/** -linguist-detectable',
    'benchmarks/** -linguist-detectable',
    'pocs/** -linguist-detectable',
    'services/operations-agent-service/test/** -linguist-detectable',
    '.agents/** -linguist-detectable',
  ].join('\n');
  assert.deepEqual(findLinguistViolations(attributes), []);
  assert.ok(findLinguistViolations('scripts/** -linguist-detectable')
    .some((violation) => violation.includes('benchmarks/** -linguist-detectable')));
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

test('workflow checks require the repository governance gate once', () => {
  assert.deepEqual(findWorkflowViolations(`
    - run: npm run --silent repo:check
  `), []);
  assert.deepEqual(findWorkflowViolations(''), [
    '.github/workflows/pr-gates.yml: missing static gate `npm run --silent repo:check`',
  ]);
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
