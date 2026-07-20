import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const readJSON = async (relativePath) => JSON.parse(await readFile(path.join(root, relativePath), 'utf8'));

const routeRegistry = await readJSON('contracts/ownership/route-ownership.v1.json');
const dataRegistry = await readJSON('contracts/ownership/data-ownership.v1.json');
const lock = await readJSON('contracts/ownership/ownership.v1.lock.json');

const errors = [];
const allowedOwners = new Set(['platform-gateway', 'legacy-hvac-backend']);
const allowedScopes = new Set(['organization', 'principal', 'site']);
const allowedCompatibility = new Set(['native', 'legacy-read']);
const allowedMethods = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE']);

if (routeRegistry.registryVersion !== 1) errors.push('route registry version must be 1');
if (!Number.isInteger(routeRegistry.registryRevision) || routeRegistry.registryRevision < lock.routeRegistryRevision) {
  errors.push('route registry revision regressed');
}

const routeKeys = new Set();
for (const route of routeRegistry.routes ?? []) {
  const key = `${route.method} ${route.path}`;
  if (!allowedMethods.has(route.method)) errors.push(`${key}: unsupported method`);
  if (typeof route.path !== 'string' || !route.path.startsWith('/api/v1/')) errors.push(`${key}: public path must start with /api/v1/`);
  if (routeKeys.has(key)) errors.push(`${key}: duplicate route owner`);
  routeKeys.add(key);
  if (!allowedOwners.has(route.owner)) errors.push(`${key}: unknown owner ${route.owner}`);
  if (!Number.isInteger(route.revision) || route.revision < 1) errors.push(`${key}: invalid revision`);
  if (!allowedCompatibility.has(route.compatibilityMode)) errors.push(`${key}: invalid compatibility mode`);
  for (const scope of route.allowedScopeDimensions ?? []) {
    if (!allowedScopes.has(scope)) errors.push(`${key}: invalid scope dimension ${scope}`);
  }
  const rollout = route.rollout ?? {};
  if (rollout.mode === 'all') {
    for (const forbidden of ['percentage', 'fallbackOwner', 'cohortSalt']) {
      if (rollout[forbidden] !== undefined) errors.push(`${key}: all rollout cannot declare ${forbidden}`);
    }
  } else if (rollout.mode === 'percentage') {
    if (!Number.isInteger(rollout.percentage) || rollout.percentage < 0 || rollout.percentage > 100) errors.push(`${key}: rollout percentage must be 0..100`);
    if (!allowedOwners.has(rollout.fallbackOwner) || rollout.fallbackOwner === route.owner) errors.push(`${key}: invalid fallback owner`);
    if (typeof rollout.cohortSalt !== 'string' || rollout.cohortSalt.length < 8) errors.push(`${key}: cohort salt is required`);
    if (!(route.allowedScopeDimensions ?? []).includes('organization') || !(route.allowedScopeDimensions ?? []).includes('principal')) {
      errors.push(`${key}: percentage rollout requires organization and principal scope dimensions`);
    }
  } else {
    errors.push(`${key}: rollout mode must be all or percentage`);
  }
  const locked = lock.routes?.[key];
  if (!locked) {
    errors.push(`${key}: route is missing from compatibility lock`);
  } else {
    if (route.revision < locked.revision) errors.push(`${key}: owner revision regressed`);
    if (route.owner !== locked.owner && route.revision === locked.revision) errors.push(`${key}: owner changed without revision increase`);
  }
}
for (const key of Object.keys(lock.routes ?? {})) {
  if (!routeKeys.has(key)) errors.push(`${key}: locked route was removed`);
}

if (dataRegistry.registryVersion !== 1) errors.push('data registry version must be 1');
if (!Number.isInteger(dataRegistry.registryRevision) || dataRegistry.registryRevision < lock.dataRegistryRevision) {
  errors.push('data registry revision regressed');
}
const resources = new Map();
for (const resource of dataRegistry.resources ?? []) {
  const key = `${resource.kind}:${resource.name}`;
  if (resources.has(key)) errors.push(`${key}: duplicate writer`);
  resources.set(key, resource);
  if (typeof resource.writer !== 'string' || resource.writer.length === 0) errors.push(`${key}: writer is required`);
  if (!Number.isInteger(resource.revision) || resource.revision < 1) errors.push(`${key}: invalid revision`);
  const locked = lock.resources?.[key];
  if (!locked) {
    errors.push(`${key}: resource is missing from compatibility lock`);
  } else {
    if (resource.revision < locked.revision) errors.push(`${key}: writer revision regressed`);
    if (resource.writer !== locked.writer && resource.revision === locked.revision) errors.push(`${key}: writer changed without revision increase`);
  }
}
for (const key of Object.keys(lock.resources ?? {})) {
  if (!resources.has(key)) errors.push(`${key}: locked resource was removed`);
}

for (const access of dataRegistry.databaseAccess ?? []) {
  const schema = resources.get(`schema:${access.schema}`);
  if (!schema) {
    errors.push(`${access.service}:${access.schema}: schema has no declared writer`);
    continue;
  }
  if (access.mode === 'write' && access.service !== schema.writer) {
    errors.push(`${access.service}:${access.schema}: forbidden cross-service writer`);
  } else if (access.mode === 'relay') {
    if (access.service !== 'outbox-relay' || access.schema !== 'gateway') errors.push(`${access.service}:${access.schema}: invalid relay access`);
  } else if (access.mode !== 'write' && access.mode !== 'read' && access.mode !== 'relay') {
    errors.push(`${access.service}:${access.schema}: invalid access mode`);
  }
}

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('Route and Data Ownership Registry checks passed.');
