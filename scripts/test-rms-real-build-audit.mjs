import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { collectRealDependencyGraph, evaluateRealDependencyGraph } from './rms-real-build-audit-lib.mjs';

function fixture(files) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'hvac-rms-real-audit-'));
  const sourceRoot = path.join(root, 'src');
  fs.mkdirSync(sourceRoot, { recursive: true });
  fs.writeFileSync(path.join(root, 'tsconfig.json'), JSON.stringify({
    compilerOptions: {
      module: 'ESNext',
      moduleResolution: 'bundler',
      target: 'ES2020',
      jsx: 'react-jsx',
      baseUrl: '.',
      paths: { '@/*': ['src/*'] },
    },
  }));
  for (const [relative, contents] of Object.entries(files)) {
    const filename = path.join(root, relative);
    fs.mkdirSync(path.dirname(filename), { recursive: true });
    fs.writeFileSync(filename, contents);
  }
  return {
    root,
    graph: () => collectRealDependencyGraph({
      entry: path.join(root, 'src', 'real', 'main.ts'),
      tsconfig: path.join(root, 'tsconfig.json'),
      sourceRoot,
    }),
  };
}

test('allows a Real entry graph containing only authoritative local modules', () => {
  const subject = fixture({
    'src/real/main.ts': "import { value } from '@/platform/client'; console.log(value);",
    'src/platform/client.ts': "export const value = 'authoritative';",
  });
  assert.deepEqual(evaluateRealDependencyGraph(subject.graph()), []);
  fs.rmSync(subject.root, { recursive: true, force: true });
});

test('rejects a Real graph that reaches Demo modules', () => {
  const subject = fixture({
    'src/real/main.ts': "import '@/demo/main';",
    'src/demo/main.ts': "export const mode = 'demo';",
  });
  const violations = evaluateRealDependencyGraph(subject.graph());
  assert.ok(violations.some((violation) => violation.rule === 'demo-entry'));
  fs.rmSync(subject.root, { recursive: true, force: true });
});

test('rejects Demo styles reached from the Real graph', () => {
  const subject = fixture({
    'src/real/main.ts': "import '@/demo/marker.css';",
    'src/demo/marker.css': '.demo { display: block; }',
  });
  const violations = evaluateRealDependencyGraph(subject.graph());
  assert.ok(violations.some((violation) => violation.rule === 'demo-entry'));
  fs.rmSync(subject.root, { recursive: true, force: true });
});

test('rejects non-literal dynamic imports and unresolved local imports', () => {
  const subject = fixture({
    'src/real/main.ts': "const path = './late'; import(path); import('./missing'); import('./missing.css'); import.meta.glob('../demo/*.ts');",
  });
  const violations = evaluateRealDependencyGraph(subject.graph());
  assert.ok(violations.some((violation) => violation.rule === 'non-literal-dynamic-import'));
  assert.equal(violations.filter((violation) => violation.rule === 'unresolved-local-import').length, 2);
  fs.rmSync(subject.root, { recursive: true, force: true });
});
