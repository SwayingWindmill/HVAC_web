import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (relativePath) => readFile(path.join(root, relativePath), 'utf8');
const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};

const [publicContract, routeRegistryText, ownershipLockText, postgresFixture] = await Promise.all([
  read('contracts/http/analytics-energy-public.openapi.yaml'),
  read('contracts/ownership/route-ownership.v1.json'),
  read('contracts/ownership/ownership.v1.lock.json'),
  read('infra/s1-registry/postgres/init/002-s1-registry-fixtures.sql'),
]);

const routeRegistry = JSON.parse(routeRegistryText);
const ownershipLock = JSON.parse(ownershipLockText);
const route = routeRegistry.routes.find((entry) => entry.method === 'POST' && entry.path === '/api/v1/analytics/energy-series');
assert(route, 'public Energy Series route is missing from Route Ownership Registry');
assert(route.owner === 'telemetry-query-service', 'Energy Series business owner must be telemetry-query-service');
assert(route.publicIngress === 'platform-gateway', 'Energy Series public ingress must be platform-gateway');
assert(route.activationStatus === 'primary' && route.rollout?.mode === 'all', 'Energy Series route must be primary for all cohorts');
assert(route.compatibilityMode === 'native' && route.readOnlyFallback === false, 'Energy Series route must use the native fail-closed path');
assert(JSON.stringify(route.allowedScopeDimensions) === JSON.stringify(['organization', 'site', 'principal']), 'Energy Series route scopes must be Organization, Site and Principal');

const locked = ownershipLock.routes?.['POST /api/v1/analytics/energy-series'];
assert(locked?.revision === 1 && locked?.owner === 'telemetry-query-service', 'Ownership compatibility lock is missing the Energy Series route');
assert(ownershipLock.routeRegistryRevision === routeRegistry.registryRevision, 'Ownership lock revision must match Route Ownership Registry');

for (const contractMarker of [
  '/api/v1/analytics/energy-series:',
  'operationId: queryEnergySeries',
  'BFFSession:',
  'name: __Host-hvac_session',
  'name: X-CSRF-Token',
  'enum: [electricity]',
  'enum: [hour, day, month]',
  'enum: [VALID_ONLY, VALID_AND_SUSPECT]',
  'description: Exclusive range end; minimum range is 1 millisecond and maximum range is 366 days.',
]) {
  assert(publicContract.includes(contractMarker), `public Energy Analytics contract is missing ${contractMarker}`);
}

const exactSiteBinding = /INSERT INTO iam\.site_bindings[\s\S]*?'018f1e00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000001', ARRAY\['analytics\.energy-series\.read'\]/;
assert(exactSiteBinding.test(postgresFixture), 'PostgreSQL fixture must grant Analytics through an exact same-Organization SiteBinding');
const roleBindingSection = postgresFixture.match(/INSERT INTO iam\.role_bindings[\s\S]*?ON CONFLICT DO NOTHING;/)?.[0] ?? '';
assert(!roleBindingSection.includes('analytics.energy-series.read'), 'Organization RoleBinding must not grant Energy Analytics');

console.log('Analytics Gateway contract check passed.');
