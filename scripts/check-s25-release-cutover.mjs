import { createHash } from 'node:crypto';
import { access, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));
const sha256 = async (path) => createHash('sha256').update(await readFile(resolve(root, path))).digest('hex');
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const release = await readJSON('deploy/platform/phase1/product-release.v1.json');
const routeOwnership = await readJSON('contracts/ownership/route-ownership.v1.json');
const dataOwnership = await readJSON('contracts/ownership/data-ownership.v1.json');
const migrations = await readJSON('deploy/platform/phase1/migrations/manifest.v1.json');
const packageJSON = await readJSON('package.json');
const productionRuntimeEnv = await read('deploy/platform/phase1/environments/production.runtime.env.example');

assert(release.schemaVersion === 1 && release.releaseRevision === 1, 'ProductReleaseManifest revision is not exact');
assert(release.product === 'hvac-web' && release.targetDomainModel === 'target-domain-model/v1', 'ProductReleaseManifest identity drifted');

for (const lock of Object.values(release.authorityLock ?? {})) {
  assert(typeof lock.path === 'string' && /^[a-f0-9]{64}$/.test(lock.sha256 ?? ''), 'authority lock is incomplete');
  assert(await sha256(lock.path) === lock.sha256, `authority lock digest drifted: ${lock.path}`);
}
assert(release.authorityLock.routeOwnership.registryRevision === routeOwnership.registryRevision, 'route ownership revision drifted');
assert(release.authorityLock.dataOwnership.registryRevision === dataOwnership.registryRevision, 'data ownership revision drifted');

assert(release.schemaContract.compatibilityMode === 'exact-product-and-manifest' && release.schemaContract.skipAllowed === false, 'schema cutover is not exact/fail-closed');
assert(await sha256(release.schemaContract.migrationManifest) === release.schemaContract.migrationManifestSha256, 'migration manifest digest drifted');
const actualDatabases = (migrations.databases ?? []).map((database) => database.name).sort();
const lockedDatabases = [...(release.schemaContract.requiredDatabases ?? [])].sort();
assert(JSON.stringify(actualDatabases) === JSON.stringify(lockedDatabases), 'required database set drifted from migration manifest');

assert(await sha256(release.platformPolicy.limitPolicy) === release.platformPolicy.limitPolicySha256, 'LimitPolicy digest drifted');
assert(await sha256(release.retirement.evidence) === release.retirement.evidenceSha256, 'S24 retirement evidence digest drifted');
assert(release.retirement.runtimeFallbackAllowed === false, 'retired runtime fallback was permitted');

assert(await sha256(release.edgeRelease.schemaAuthority) === release.edgeRelease.schemaAuthoritySha256, 'Edge release schema authority digest drifted');
assert(release.edgeRelease.desiredObservedReconciliationRequired === true, 'Edge desired/observed reconciliation is not required');
assert(release.edgeRelease.rollbackAuthority === 'previous-signed-edge-release', 'Edge rollback is not bound to a previous signed release');
assert(release.edgeRelease.privateSigningKeyPersistenceAllowed === false, 'Product release permits persisted private signing keys');

assert(release.projectionRecovery.postgresAuthority === 'backup-and-restore', 'PostgreSQL recovery authority drifted');
assert(release.projectionRecovery.redisAuthority === 'rebuild-from-authoritative-postgres-and-owner-events', 'Redis projection recovery must rebuild from authorities');
assert(release.projectionRecovery.redisSnapshotAsBusinessAuthority === false, 'Redis snapshot was promoted to business authority');
assert(productionRuntimeEnv.includes('TELEMETRY_LATEST_CACHE_ENABLED=true'), 'production current telemetry must require Redis Latest');
assert(/^TELEMETRY_LATEST_CACHE_REDIS_URL=redis:\/\/.+$/m.test(productionRuntimeEnv), 'production Redis Latest URL is missing');

const expectedSmoke = [
  'test:identity',
  's1:registry:check',
  's2:hvac-web:check',
  's2:history:check',
  's11:command-readback:check',
  's4:alarm:check',
  's16:notification:check',
  'real-dashboard:test',
];
assert(JSON.stringify(release.criticalSmoke) === JSON.stringify(expectedSmoke), 'critical smoke set drifted');
for (const command of expectedSmoke) assert(typeof packageJSON.scripts?.[command] === 'string', `critical smoke command is missing: ${command}`);
for (const path of release.rollbackEvidence ?? []) await access(resolve(root, path));

for (const route of routeOwnership.routes ?? []) {
  assert(route.owner !== 'legacy-hvac-backend', `Legacy route owner remains: ${route.method} ${route.path}`);
  assert(route.readFallbackOwner === undefined && route.fallbackOwner === undefined && route.readOnlyFallback !== true, `runtime route fallback remains: ${route.method} ${route.path}`);
}
for (const resource of dataOwnership.resources ?? []) assert(resource.writer !== 'legacy-hvac-backend', `Legacy data writer remains: ${resource.kind}:${resource.name}`);

console.log(`S25 release cutover manifest passed: routeRevision=${routeOwnership.registryRevision}, dataRevision=${dataOwnership.registryRevision}, databases=${actualDatabases.length}, smokePaths=${expectedSmoke.length}`);
