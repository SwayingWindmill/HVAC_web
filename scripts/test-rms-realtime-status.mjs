import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

function compile(file) {
  const sourcePath = path.resolve(file);
  return {
    sourcePath,
    code: ts.transpileModule(fs.readFileSync(sourcePath, 'utf8'), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2020,
        strict: true,
      },
      fileName: sourcePath,
    }).outputText,
  };
}

function evaluate(compiled, requireFn = () => { throw new Error('unexpected require'); }) {
  const module = { exports: {} };
  vm.runInNewContext(compiled.code, {
    module,
    exports: module.exports,
    require: requireFn,
    Error,
    Object,
  }, { filename: compiled.sourcePath });
  return module.exports;
}

const siteRouting = evaluate(compile('apps/hvac-web/src/real/site-routing.ts'));
const realtime = evaluate(
  compile('apps/hvac-web/src/real/realtime-status.ts'),
  (specifier) => {
    if (specifier === './site-routing') return siteRouting;
    throw new Error(`unexpected require: ${specifier}`);
  },
);
const siteId = '01900000-0001-7000-8000-000000000001';

const expected = [
  ['idle', 'Idle — not subscribed'],
  ['connecting', 'Connecting'],
  ['live', 'Live'],
  ['reconnecting', 'Reconnecting'],
  ['resync-required', 'Resync required'],
  ['unavailable', 'Unavailable'],
];

test('describes every subscription-scoped realtime state without global health claims', () => {
  for (const [state, label] of expected) {
    const status = realtime.createRealtimeStatus({ state, siteId });
    assert.equal(status.state, state);
    assert.equal(status.siteId, siteId);
    assert.equal(realtime.realtimeStatusLabel(status), label);
    assert.equal('platformStatus' in status, false);
    assert.equal('healthy' in status, false);
  }
});

test('idle without an active subscription remains explicitly unscoped', () => {
  const status = realtime.createIdleRealtimeStatus();
  assert.equal(status.state, 'idle');
  assert.equal(status.siteId, undefined);
  assert.equal(realtime.realtimeStatusLabel(status), 'Idle — not subscribed');
});

test('maps transport states to the four user-visible operational states', () => {
  assert.equal(realtime.realtimeStatusPresentation(realtime.createRealtimeStatus({ state: 'live', siteId })).code, 'CONNECTED');
  assert.equal(realtime.realtimeStatusPresentation(realtime.createRealtimeStatus({ state: 'connecting', siteId })).code, 'RECONNECTING');
  assert.equal(realtime.realtimeStatusPresentation(realtime.createRealtimeStatus({ state: 'resync-required', siteId })).code, 'DEGRADED');
  assert.equal(realtime.realtimeStatusPresentation(realtime.createRealtimeStatus({ state: 'unavailable', siteId })).code, 'DISCONNECTED');
});

test('rejects malformed and cross-Site realtime updates', () => {
  assert.throws(
    () => realtime.createRealtimeStatus({ state: 'live', siteId: 'not-a-site' }),
    /validated Site/i,
  );
  assert.throws(
    () => realtime.assertRealtimeStatusForSite(
      realtime.createRealtimeStatus({ state: 'live', siteId }),
      '01900000-0002-7000-8000-000000000002',
    ),
    /active Site/i,
  );
});

test('status snapshots are frozen and expose no transport detail surface', () => {
  const status = realtime.createRealtimeStatus({ state: 'reconnecting', siteId });
  assert.equal('detail' in status, false);
  assert.equal(Object.isFrozen(status), true);
});
