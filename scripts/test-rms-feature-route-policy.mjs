import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const sourcePath = path.resolve('apps/hvac-web/src/real/route-policy.ts');
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
vm.runInNewContext(compiled, { module, exports: module.exports, URL }, { filename: sourcePath });
const policy = module.exports;

const manifest = [
  {
    id: 'home',
    label: 'Home',
    path: '/',
    delivery: 'implemented',
    availability: 'none',
    requiredCapabilities: [],
  },
  {
    id: 'system',
    label: 'System',
    path: '/system',
    delivery: 'implemented',
    availability: 'platform',
    requiredCapabilities: ['organization.read'],
  },
  {
    id: 'alarms',
    label: 'Alarms',
    path: '/alarms',
    delivery: 'not-integrated',
    availability: 'none',
    requiredCapabilities: ['site.read'],
  },
  {
    id: 'optimization',
    label: 'Optimization',
    path: '/optimization',
    delivery: 'hidden',
    availability: 'none',
    requiredCapabilities: ['site.read'],
  },
];

test('implemented and authorized features resolve READY and appear in navigation', () => {
  const decision = policy.resolveRoute(manifest, '/system', ['organization.read'], 'available');
  assert.equal(decision.state, 'READY');
  assert.equal(decision.feature.id, 'system');

  const navigation = policy.resolveNavigation(manifest, ['organization.read'], 'available');
  assert.deepEqual(navigation.map((item) => item.id), ['home', 'system']);
  assert.equal(navigation[1].kind, 'link');
  assert.equal(navigation[1].degraded, false);
});

test('implemented but unauthorized features are hidden and direct URLs are generic FORBIDDEN', () => {
  const navigation = policy.resolveNavigation(manifest, [], 'available');
  assert.deepEqual(navigation.map((item) => item.id), ['home']);

  const decision = policy.resolveRoute(manifest, '/system', [], 'available');
  assert.equal(decision.state, 'FORBIDDEN');
  assert.equal('feature' in decision, false);
});

test('authorized backend-missing features remain visible as NOT_INTEGRATED', () => {
  const navigation = policy.resolveNavigation(manifest, ['site.read'], 'available');
  assert.deepEqual(navigation.map((item) => item.id), ['home', 'alarms']);
  assert.equal(navigation[1].kind, 'not-integrated');

  const decision = policy.resolveRoute(manifest, '/alarms', ['site.read'], 'available');
  assert.equal(decision.state, 'NOT_INTEGRATED');
  assert.equal(decision.feature.id, 'alarms');
});

test('deployment-hidden and unknown routes both resolve NOT_FOUND', () => {
  const navigation = policy.resolveNavigation(manifest, ['site.read'], 'available');
  assert.equal(navigation.some((item) => item.id === 'optimization'), false);
  assert.equal(policy.resolveRoute(manifest, '/optimization', ['site.read'], 'available').state, 'NOT_FOUND');
  assert.equal(policy.resolveRoute(manifest, '/does-not-exist', ['site.read'], 'available').state, 'NOT_FOUND');
});

test('platform unavailability and degradation remain distinct', () => {
  assert.deepEqual(
    policy.resolveRoute(manifest, '/system', ['organization.read'], 'unavailable').state,
    'UNAVAILABLE',
  );
  assert.equal(
    policy.resolveNavigation(manifest, ['organization.read'], 'unavailable').some((item) => item.id === 'system'),
    false,
  );

  const degraded = policy.resolveRoute(manifest, '/system', ['organization.read'], 'degraded');
  assert.equal(degraded.state, 'DEGRADED');
  const degradedNavigation = policy.resolveNavigation(manifest, ['organization.read'], 'degraded');
  assert.equal(degradedNavigation.find((item) => item.id === 'system').degraded, true);
});

test('features without a server dependency remain usable when the platform status route is unavailable', () => {
  const decision = policy.resolveRoute(manifest, '/', [], 'unavailable');
  assert.equal(decision.state, 'READY');
  assert.equal(decision.feature.id, 'home');
});
