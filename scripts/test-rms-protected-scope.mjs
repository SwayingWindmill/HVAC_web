import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const sourcePath = path.resolve('apps/hvac-web/src/real/protected-scope.ts');
const source = fs.readFileSync(sourcePath, 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
    strict: true,
  },
  fileName: sourcePath,
}).outputText;

const module = { exports: {} };
vm.runInNewContext(compiled, {
  module,
  exports: module.exports,
  AbortController,
  Error,
  Promise,
}, { filename: sourcePath });
const scopeModule = module.exports;

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

test('reports registered dirty drafts before any protected purge', () => {
  const scope = scopeModule.createProtectedScopeCoordinator();
  scope.activate('01900000-0001-7000-8000-000000000001');
  let dirty = true;
  scope.registerDraft({ id: 'command-form', label: 'Unsaved command', isDirty: () => dirty });
  scope.registerDraft({ id: 'notes', label: 'Operator notes', isDirty: () => false });

  assert.equal(scope.current().siteId, '01900000-0001-7000-8000-000000000001');
  assert.equal(scope.current().draftCount, 2);
  assert.equal(scope.dirtyDrafts().length, 1);
  assert.equal(scope.dirtyDrafts()[0].id, 'command-form');
  assert.equal(scope.dirtyDrafts()[0].label, 'Unsaved command');

  dirty = false;
  assert.equal(scope.dirtyDrafts().length, 0);
});

test('purge aborts old requests, closes resources in safety order, and preserves visual preferences', async () => {
  const events = [];
  const visualPreferences = { theme: 'dark', sidebarCollapsed: true };
  const scope = scopeModule.createProtectedScopeCoordinator();
  scope.activate('01900000-0001-7000-8000-000000000001');
  const token = scope.requestToken();

  for (const [kind, id] of [
    ['temporary-state', 'draft-buffer'],
    ['selection', 'selected-device'],
    ['query-cache', 'site-query-cache'],
    ['realtime', 's2-session'],
  ]) {
    scope.registerResource({
      id,
      kind,
      purge: (reason) => events.push(`${kind}:${id}:${reason}`),
    });
  }

  const purge = scope.purge('SITE_CHANGE');
  assert.equal(token.signal.aborted, true);
  assert.equal(scope.current().state, 'purging');
  assert.equal(scope.current().siteId, undefined);
  const outcome = await purge;

  assert.equal(outcome.status, 'completed');
  assert.deepEqual(events, [
    'realtime:s2-session:SITE_CHANGE',
    'query-cache:site-query-cache:SITE_CHANGE',
    'selection:selected-device:SITE_CHANGE',
    'temporary-state:draft-buffer:SITE_CHANGE',
  ]);
  assert.equal(scope.current().state, 'idle');
  assert.equal(scope.current().resourceCount, 0);
  assert.equal(scope.current().draftCount, 0);
  assert.equal(visualPreferences.theme, 'dark');
  assert.equal(visualPreferences.sidebarCollapsed, true);
});

test('late old-Site responses cannot commit after generation is revoked', async () => {
  const scope = scopeModule.createProtectedScopeCoordinator();
  scope.activate('01900000-0001-7000-8000-000000000001');
  const oldToken = scope.requestToken();
  const committed = [];

  assert.equal(oldToken.commit(() => committed.push('old-before-purge')), true);
  await scope.purge('SITE_CHANGE');
  scope.activate('01900000-0002-7000-8000-000000000002');

  assert.equal(oldToken.commit(() => committed.push('old-after-purge')), false);
  const newToken = scope.requestToken();
  assert.equal(newToken.commit(() => committed.push('new-site')), true);
  assert.deepEqual(committed, ['old-before-purge', 'new-site']);
});

test('purge failure remains fail-closed and still attempts every registered cleanup', async () => {
  const events = [];
  const scope = scopeModule.createProtectedScopeCoordinator();
  scope.activate('01900000-0001-7000-8000-000000000001');
  const token = scope.requestToken();

  scope.registerResource({
    id: 's2-session',
    kind: 'realtime',
    purge: () => {
      events.push('realtime');
      throw new Error('close failed');
    },
  });
  scope.registerResource({
    id: 'query-cache',
    kind: 'query-cache',
    purge: () => events.push('query-cache'),
  });
  scope.registerResource({
    id: 'selection',
    kind: 'selection',
    purge: () => events.push('selection'),
  });

  const outcome = await scope.purge('POLICY_CHANGE');

  assert.equal(token.signal.aborted, true);
  assert.deepEqual(events, ['realtime', 'query-cache', 'selection']);
  assert.equal(outcome.status, 'failed');
  assert.equal(outcome.failure.code, 'PROTECTED_SCOPE_PURGE_FAILED');
  assert.equal(scope.current().state, 'failed');
  assert.equal(scope.current().siteId, undefined);
  assert.equal(scope.current().resourceCount, 0);
});

test('a second purge is rejected while the first purge is still active', async () => {
  const pending = deferred();
  const scope = scopeModule.createProtectedScopeCoordinator();
  scope.activate('01900000-0001-7000-8000-000000000001');
  scope.registerResource({ id: 's2-session', kind: 'realtime', purge: () => pending.promise });

  const first = scope.purge('SITE_CHANGE');
  const second = await scope.purge('SESSION_LOSS');

  assert.equal(second.status, 'busy');
  pending.resolve();
  assert.equal((await first).status, 'completed');
});
