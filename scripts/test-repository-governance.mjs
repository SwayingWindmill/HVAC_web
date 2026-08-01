import assert from 'node:assert/strict';
import test from 'node:test';

import {
  findDocumentationViolations,
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
