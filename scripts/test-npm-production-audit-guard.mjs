import assert from 'node:assert/strict';
import test from 'node:test';
import {
  expandNpmScriptGraph,
  verifyReactRouterClientOnlyViteSpa,
} from './npm-production-audit-guards.mjs';

const safeScripts = Object.freeze({
  build: 'npm run build:demo',
  'build:demo': 'tsc -b apps/hvac-web/tsconfig.json && vite build apps/hvac-web --config apps/hvac-web/vite.config.ts',
  'build:real': 'tsc -b apps/hvac-web/tsconfig.json && vite build apps/hvac-web --config apps/hvac-web/vite.real.config.ts',
});

const safeInput = Object.freeze({
  scripts: safeScripts,
  compatibilityEntry: "import './demo/main';",
  demoEntry: "import { BrowserRouter } from 'react-router-dom';",
  source: "import { BrowserRouter } from 'react-router-dom';",
});

test('accepts delegated Demo and explicit Real client-only Vite builds', () => {
  assert.doesNotThrow(() => verifyReactRouterClientOnlyViteSpa(safeInput));
  assert.deepEqual(
    expandNpmScriptGraph(safeScripts, 'build').map(({ name }) => name),
    ['build', 'build:demo'],
  );
});

test('rejects a command that only echoes the expected Vite build text', () => {
  assert.throws(
    () => verifyReactRouterClientOnlyViteSpa({
      ...safeInput,
      scripts: { ...safeScripts, 'build:real': 'echo vite build apps/hvac-web' },
    }),
    /build:real to execute the HVAC Web Vite build/,
  );
});

test('rejects an SSR or React Router server build command', () => {
  assert.throws(
    () => verifyReactRouterClientOnlyViteSpa({
      ...safeInput,
      scripts: { ...safeScripts, 'build:real': `${safeScripts['build:real']} --ssr src/server.tsx` },
    }),
    /server build marker.*--ssr/,
  );
});

test('rejects RSC and server APIs anywhere in the application source', () => {
  assert.throws(
    () => verifyReactRouterClientOnlyViteSpa({ ...safeInput, source: 'const router = unstable_RSC();' }),
    /server\/RSC marker: unstable_RSC/,
  );
});

test('rejects a compatibility entry that no longer delegates to the browser Demo entry', () => {
  assert.throws(
    () => verifyReactRouterClientOnlyViteSpa({ ...safeInput, compatibilityEntry: "import './server/main';" }),
    /compatibility entry/,
  );
});

test('fails closed on npm script cycles', () => {
  assert.throws(
    () => expandNpmScriptGraph({ build: 'npm run build:demo', 'build:demo': 'npm run build' }, 'build'),
    /npm script cycle/,
  );
});
