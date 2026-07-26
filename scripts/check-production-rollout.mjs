import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = async (path) => readFile(resolve(root, path), 'utf8');
const [routeRaw, rolloutRaw, adr, runbook] = await Promise.all([
  read('contracts/ownership/route-ownership.v1.json'),
  read('deploy/platform/production-rollout.v1.json'),
  read('docs/adr/0005-hvac-backend-non-production-reference.md'),
  read('docs/operations/go-platform-production-rollout.md'),
]);

const routes = JSON.parse(routeRaw);
const rollout = JSON.parse(rolloutRaw);
const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};

assert(rollout.schemaVersion === 1 && rollout.decision === 'ADR-0005', 'production rollout decision drifted');
assert(rollout.legacyReference?.productionDependency === false, 'NestJS became a production dependency');
assert(rollout.legacyReference?.architectureSource === false, 'NestJS became an architecture source');
assert(rollout.legacyReference?.routeFallback === false, 'NestJS route fallback is enabled');
assert(rollout.legacyReference?.migrationSource === false, 'NestJS became a migration source');
assert(rollout.legacyReference?.disasterRecoveryDependency === false, 'disaster recovery depends on NestJS');

const expectedPhases = [
  ['P0-contract-ready', 0, 0],
  ['P1-internal-synthetic', 1, 120],
  ['P2-internal-site', 5, 240],
  ['P3-limited-production', 25, 480],
  ['P4-broad-production', 50, 720],
  ['P5-primary', 100, 1440],
  ['P6-operationally-certified', 100, 10080],
];
assert(rollout.phases?.length === expectedPhases.length, 'production rollout phase count drifted');
for (let index = 0; index < expectedPhases.length; index += 1) {
  const [id, trafficPercent, minimumHoldMinutes] = expectedPhases[index];
  const phase = rollout.phases[index];
  assert(phase.id === id, `production rollout phase ${id} is missing or reordered`);
  assert(phase.trafficPercent === trafficPercent, `production rollout phase ${id} traffic drifted`);
  assert(phase.minimumHoldMinutes === minimumHoldMinutes, `production rollout phase ${id} hold drifted`);
  assert(typeof phase.rollbackTarget === 'string' && phase.rollbackTarget.length > 0, `production rollout phase ${id} has no rollback target`);
}

for (const [name, value] of Object.entries(rollout.hardGates ?? {})) {
  assert(value === 0, `hard gate ${name} must remain zero`);
}
for (const evidence of ['capacity-report.json', 'failure-injection-report.json', 'restore-report.json', 'route-rollback-report.json', 'command-fence-report.json', 'security-zero-report.json', 'promotion-approvals.json', 'production-acceptance.json', 'SHA256SUMS']) {
  assert(rollout.requiredEvidence?.includes(evidence), `required production evidence ${evidence} is missing`);
}
assert(rollout.rollback?.maximumDecisionMinutes === 5, 'rollback decision objective drifted');
assert(rollout.rollback?.maximumRouteRollbackMinutes === 15, 'route rollback objective drifted');
assert(rollout.rollback?.futureCommandsOnly === true, 'command rollback must affect future commands only');
assert(rollout.rollback?.acceptedCommandsRemainWithOriginalOwner === true, 'accepted commands may not change owner during rollback');

const activeRoutes = routes.routes ?? [];
for (const route of activeRoutes) {
  const key = `${route.method} ${route.path}`;
  assert(route.owner !== 'legacy-hvac-backend', `${key} still selects legacy-hvac-backend`);
  assert(route.rollout?.fallbackOwner !== 'legacy-hvac-backend', `${key} still falls back to legacy-hvac-backend`);
  assert(route.readFallbackOwner !== 'legacy-hvac-backend', `${key} still declares a Legacy read fallback`);
}

const s1Paths = new Set([
  '/api/v1/organizations',
  '/api/v1/organizations/{organizationId}',
  '/api/v1/organizations/{organizationId}/sites',
  '/api/v1/sites/{siteId}',
  '/api/v1/sites/{siteId}/equipment',
  '/api/v1/equipment/{equipmentId}',
  '/api/v1/sites/{siteId}/devices',
  '/api/v1/devices/{deviceId}',
]);
for (const route of activeRoutes.filter((candidate) => s1Paths.has(candidate.path))) {
  assert(route.owner === 'platform-core-service', `${route.path} is not Core-owned`);
  assert(route.rollout?.mode === 'all', `${route.path} is not fully Go-routed`);
  assert(route.migrationPhase === 'GO_PRIMARY', `${route.path} is not in GO_PRIMARY`);
  assert(route.readOnlyFallback === false, `${route.path} still advertises runtime fallback`);
}

for (const marker of ['non-production behavioral reference', 'must never be selected by the active Route Ownership Registry', 'Production Cohort Rollout & Operational Hardening']) {
  assert(adr.includes(marker), `ADR 0005 is missing ${marker}`);
}
for (const marker of ['NestJS is not a rollback target', 'P6 Operationally certified', 'zero blind retry after `OUTCOME_UNKNOWN`']) {
  assert(runbook.includes(marker), `production rollout runbook is missing ${marker}`);
}

console.log('Go platform production rollout and non-Legacy dependency checks passed.');
