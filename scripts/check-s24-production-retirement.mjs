import { readFile, readdir } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));
const failures = [];
const assert = (condition, message) => { if (!condition) failures.push(message); };

const forbiddenRuntimeMarkers = [
  'legacy-migration-service',
  'telemetry-shadow-comparator',
  'oidc-test-provider',
  'eg8200-simulator',
  'legacy-hvac-backend',
];

const productionPaths = [
  'deploy/platform/phase1/compose.yaml',
  'deploy/platform/phase1/product-release.v1.json',
  'deploy/platform/production-rollout.v1.json',
  'deploy/s0/staging/kustomization.yaml',
  'deploy/s0/staging/configmap.yaml',
  'deploy/s0/staging/serviceaccounts.yaml',
  'deploy/s0/staging/services.yaml',
  'deploy/s0/staging/networkpolicies.yaml',
  'deploy/s0/staging/workloads/platform-gateway.yaml',
  'deploy/s0/staging/workloads/iam-service.yaml',
  'deploy/s0/staging/workloads/audit-ledger-service.yaml',
  'deploy/s0/staging/workloads/outbox-relay.yaml',
];

for (const path of productionPaths) {
  const text = await read(path);
  for (const marker of forbiddenRuntimeMarkers) {
    assert(!text.includes(marker), `${path} must not contain retired/test runtime marker ${marker}`);
  }
}

const phase1Compose = await read('deploy/platform/phase1/compose.yaml');
assert(!/^  eg8200-simulator:/m.test(phase1Compose), 'canonical Phase 1 Compose must not declare the EG8200 simulator');

const acceptanceCompose = await read('deploy/acceptance/phase1-simulator.compose.yaml');
assert(/^  eg8200-simulator:/m.test(acceptanceCompose), 'simulator acceptance overlay must declare the EG8200 simulator');
assert(acceptanceCompose.includes('profiles: ["simulator-acceptance"]'), 'simulator must require the explicit simulator-acceptance profile');
assert(!/^    ports:/m.test(acceptanceCompose), 'simulator acceptance overlay must not publish host ports');

const s2DeployFiles = await readdir(resolve(root, 'deploy/s2'));
assert(!s2DeployFiles.includes('shadow-comparator-policy.v1.json'), 'retired shadow comparator policy must not remain under deploy/s2');
assert(!s2DeployFiles.includes('shadow-routing-revisions.v1.json'), 'retired shadow routing revisions must not remain under deploy/s2');

const shadowPolicy = await readJSON('deploy/acceptance/s2-shadow-comparator-policy.v1.json');
assert(shadowPolicy.executionMode === 'offline-batch' && shadowPolicy.servingPath === false, 'retired comparator may only remain as offline acceptance evidence');
assert(shadowPolicy.network?.ingress === 'deny-all' && shadowPolicy.network?.egress === 'deny-all', 'retired comparator evidence must remain network-isolated');

const routeRegistry = await readJSON('contracts/ownership/route-ownership.v1.json');
const routeKeys = new Set();
for (const route of routeRegistry.routes ?? []) {
  const key = `${route.method} ${route.path}`;
  assert(!routeKeys.has(key), `production route has more than one owner entry: ${key}`);
  routeKeys.add(key);
  assert(typeof route.owner === 'string' && route.owner.length > 0, `production route is missing an owner: ${key}`);
  assert(route.owner !== 'legacy-hvac-backend', `production route still uses legacy owner: ${key}`);
  assert(route.readFallbackOwner === undefined && route.fallbackOwner === undefined, `production route still exposes fallback owner: ${key}`);
  assert(route.compatibilityMode !== 'legacy-read', `production route still exposes legacy-read compatibility: ${key}`);
}

const retirementEvidence = await readJSON('deploy/acceptance/s24-production-retirement.v1.json');
assert(retirementEvidence.issue === 295 && retirementEvidence.slice === 'S24' && retirementEvidence.status === 'IMPLEMENTED', 'S24 retirement evidence identity is incomplete');
assert(retirementEvidence.productionInvariants?.singleRouteOwner === true && retirementEvidence.productionInvariants?.runtimeFallback === false, 'S24 retirement evidence must record the final single-owner/no-fallback state');
assert(retirementEvidence.rollback?.serviceFallbackAllowed === false, 'S24 rollback must not permit service fallback');

const dataRegistry = await readJSON('contracts/ownership/data-ownership.v1.json');
for (const resource of dataRegistry.resources ?? []) {
  assert(resource.writer !== 'legacy-hvac-backend', `production data resource still uses legacy writer: ${resource.kind}:${resource.name}`);
}
for (const access of dataRegistry.databaseAccess ?? []) {
  assert(access.service !== 'legacy-migration-service' && access.service !== 'legacy-hvac-backend', `production database access still grants legacy runtime: ${access.service}:${access.schema}`);
}

const legacyLock = await readJSON('contracts/ownership/ownership.v1.lock.json');
assert(legacyLock.status === 'SUPERSEDED' && legacyLock.historicalEvidenceOnly === true, 'legacy ownership lock must remain explicitly historical-only');

const stagingBindings = await readJSON('deploy/s0/staging/bindings.schema.json');
assert(stagingBindings.properties?.OIDC_ISSUER && stagingBindings.required?.includes('OIDC_ISSUER'), 'staging must require an externally supplied real OIDC issuer');
assert(!('SIGNED_IMAGE_OIDC_TEST_PROVIDER' in (stagingBindings.properties ?? {})), 'staging bindings must not accept a test IdP image');

if (failures.length) {
  console.error('S24 production retirement check failed:');
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log(`S24 production retirement check passed: routes=${routeKeys.size}, productionPaths=${productionPaths.length}`);
