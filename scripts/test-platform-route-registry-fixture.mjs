import assert from 'node:assert/strict';
import test from 'node:test';
import { seedLegacyPlatformStatusRoute } from './s0-route-registry-fixture.mjs';

const registryFixture = () => ({
  registryVersion: 1,
  registryRevision: 10,
  routes: [
    { method: 'GET', path: '/api/v1/health', owner: 'platform-gateway', revision: 1, rollout: { mode: 'all' }, compatibilityMode: 'native' },
    { method: 'GET', path: '/api/v1/platform/status', owner: 'platform-gateway', revision: 2, rollout: { mode: 'all' }, compatibilityMode: 'native' },
  ],
});

test('seeds an isolated Legacy platform-status scenario with adjacent revisions', () => {
  const source = registryFixture();
  const seeded = seedLegacyPlatformStatusRoute(source);
  const route = seeded.routes.find((entry) => entry.path === '/api/v1/platform/status');

  assert.deepEqual(source, registryFixture());
  assert.equal(seeded.registryRevision, 11);
  assert.deepEqual(route, {
    method: 'GET',
    path: '/api/v1/platform/status',
    owner: 'legacy-hvac-backend',
    revision: 3,
    rollout: { mode: 'all' },
    compatibilityMode: 'legacy-read',
  });
  assert.deepEqual(seeded.routes[0], source.routes[0]);
});

test('fails closed for missing routes and invalid revisions', () => {
  assert.throws(() => seedLegacyPlatformStatusRoute({ registryRevision: 1, routes: [] }), /platform status route is missing/);
  assert.throws(() => seedLegacyPlatformStatusRoute({ registryRevision: 0, routes: registryFixture().routes }), /registry revision/);
  const invalidRoute = registryFixture();
  invalidRoute.routes[1].revision = Number.NaN;
  assert.throws(() => seedLegacyPlatformStatusRoute(invalidRoute), /platform status route revision/);
});
