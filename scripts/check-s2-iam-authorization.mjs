import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));
const assert = (condition, message) => {
  if (!condition) throw new Error(`Invalid S2 Ticket 02 IAM authorization baseline: ${message}`);
};
const includesAll = (text, markers, label) => {
  for (const marker of markers) assert(text.includes(marker), `${label} is missing ${marker}`);
};
const exact = (actual, expected) => Array.isArray(actual)
  && actual.length === expected.length
  && actual.every((value, index) => value === expected[index]);

const [
  contract,
  routeRegistry,
  dataRegistry,
  sql,
  scopeGo,
  grantGo,
  authorizationGo,
  postgresGo,
  coreServerGo,
  serverGo,
  runtimeServerGo,
  grantStoreGo,
  auditGo,
  telemetryTests,
  authorizationTests,
  serverTests,
  integrationTests,
  mainGo,
  packageJSON,
  workflow,
] = await Promise.all([
  readJSON('contracts/telemetry/s2-iam-authorization.v1.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readText('infra/s1-registry/postgres/init/006-s2-telemetry-authorization.sql'),
  readText('libs/telemetryauth/scope.go'),
  readText('libs/telemetryauth/grant.go'),
  readText('services/iam-service/internal/iam/telemetry_authorization.go'),
  readText('services/iam-service/internal/iam/postgres_telemetry_authorization.go'),
  readText('services/iam-service/internal/iam/server.go'),
  readText('services/iam-service/internal/iam/telemetry_server.go'),
  readText('services/iam-service/internal/iam/telemetry_runtime_server.go'),
  readText('services/iam-service/internal/iam/telemetry_grant_store.go'),
  readText('services/iam-service/internal/iam/telemetry_audit.go'),
  readText('libs/telemetryauth/telemetry_test.go'),
  readText('services/iam-service/internal/iam/telemetry_authorization_test.go'),
  readText('services/iam-service/internal/iam/telemetry_server_test.go'),
  readText('services/iam-service/internal/iam/telemetry_grant_store_integration_test.go'),
  readText('services/iam-service/cmd/iam-service/main.go'),
  readJSON('package.json'),
  readText('.github/workflows/s2-iam-authorization.yml'),
]);

const actions = [
  'telemetry.snapshot.read',
  'telemetry.batch.read',
  'telemetry.subscribe',
  'telemetry.resubscribe',
  'telemetry.recovery.use',
  'telemetry.recovery.checkpoint',
];
assert(contract.schemaVersion === 1 && contract.ticket === 'S2-02', 'contract identity drifted');
assert(contract.activationStatus === 'expand-baseline' && contract.publicTrafficEnabled === false, 'contract must carry zero public traffic');
assert(contract.ownerService === 'iam-service' && contract.consumerService === 'telemetry-runtime-service' && contract.publicIngress === 'platform-gateway', 'service boundary drifted');
assert(exact(contract.actions, actions), 'action list drifted');
assert(exact(contract.decision?.evaluationOrder, [
  'immutable-principal',
  'acting-organization-membership',
  'role-action',
  'exact-cross-organization-site-binding',
  'exact-device-permission',
  'exact-key-permission',
]), 'evaluation order drifted');
assert(contract.decision?.membershipGrantsAllSites === false && contract.decision?.denyPrecedence === true && contract.decision?.allOrNothing === true, 'authorization safety invariants drifted');
assert(contract.decision?.maximumTargets === 100 && contract.decision?.maximumKeysPerTarget === 64 && contract.decision?.maximumTotalKeys === 2048, 'scope limits drifted');
assert(contract.decision?.stableDenials?.missingOrUnauthorizedDevice === 'RESOURCE_NOT_FOUND', 'Device denial shape drifted');
assert(contract.decision?.stableDenials?.visibleUnknownUnauthorizedOrDeniedKey === 'TELEMETRY_KEY_INVALID', 'key denial shape drifted');
assert(contract.delegationGrant?.maximumLifetimeSeconds === 30 && contract.delegationGrant?.singleUse === true && contract.delegationGrant?.transitive === false, 'grant lifetime/use policy drifted');
for (const binding of ['initiatingPrincipal', 'actingOrganizationId', 'actorChain', 'action', 'scopeDigest', 'policyRevision', 'sessionId', 'parentTokenId', 'requestId', 'traceId', 'publicRoute', 'tokenId']) {
  assert(contract.delegationGrant?.requiredBindings?.includes(binding), `grant binding ${binding} is missing`);
}
for (const condition of ['wrong-presenter', 'wrong-audience', 'wrong-principal', 'wrong-session', 'wrong-action', 'scope-mismatch', 'expired', 'stale-policy-revision', 'revoked', 'replayed', 'status-dependency-unavailable']) {
  assert(contract.delegationGrant?.failClosedConditions?.includes(condition), `grant fail-closed condition ${condition} is missing`);
}
assert(contract.runtimeInterfaces?.consume?.path === '/internal/v1/telemetry/grants:consume' && contract.runtimeInterfaces?.consume?.atomic === true, 'grant consume interface drifted');
assert(contract.runtimeInterfaces?.revocations?.path === '/internal/v1/telemetry/revocations:poll' && contract.runtimeInterfaces?.revocations?.deliveryBudgetSeconds === 10 && contract.runtimeInterfaces?.revocations?.maximumBatchSize === 500, 'revocation interface drifted');
assert(exact(contract.revocationSources, ['MEMBERSHIP', 'ROLE_BINDING', 'SITE_BINDING', 'DEVICE_PERMISSION', 'KEY_PERMISSION', 'GRANT']), 'revocation source list drifted');

const s2Routes = (routeRegistry.routes ?? []).filter((route) => route.owner === 'telemetry-runtime-service');
assert(s2Routes.length === 4, 'exactly four S2 public routes must remain registered');
for (const route of s2Routes) {
  assert(route.publicIngress === 'platform-gateway' && route.activationStatus === 'expand-baseline', `${route.method} ${route.path} ingress/activation drifted`);
  assert(route.rollout?.mode === 'disabled' && route.migrationPhase === 'R0-contract-only', `${route.method} ${route.path} enabled traffic prematurely`);
  assert(route.readOnlyFallback === false && route.readFallbackOwner === undefined, `${route.method} ${route.path} gained request fallback`);
}

includesAll(scopeGo, actions.map((action) => `"${action}"`), 'telemetry action contract');
includesAll(scopeGo, ['MaximumTargets', 'MaximumKeysPerTarget', 'MaximumTotalKeys', 'ScopeDigest', 'CanonicalTargets'], 'telemetry scope implementation');
includesAll(grantGo, ['MaximumGrantLifetime', 'PrincipalID', 'SessionID', 'UseChecker', 'telemetry grant was replayed', 'telemetry grant is revoked', 'telemetry grant policy revision is stale'], 'grant validation');
includesAll(authorizationGo, ['telemetryActionScope', 'telemetryDeviceScope', 'telemetryKeyScope', 'denyTelemetryDecision', 'ReasonResourceNotFound', 'ReasonTelemetryKeyInvalid'], 'authorization evaluator');
includesAll(postgresGo, ['pgx.RepeatableRead', "app.requested_device_ids", 'loadTelemetryRoleBindings', 'loadTelemetrySiteBindings', 'loadTelemetryExplicitDenies', 'postgresTelemetryRegistryActions', 'unsupported action', 'loadTelemetryScopeBindings', 'loadTelemetryKeyBindings'], 'PostgreSQL authorization lookup');
includesAll(`${coreServerGo}\n${serverGo}`, ['TelemetryDecisionPath', 'telemetry:authorize', 'TelemetryGrantSigner', 'TelemetryAuditSink'], 'IAM decision server');
includesAll(`${coreServerGo}\n${runtimeServerGo}`, ['TelemetryGrantConsumePath', 'TelemetryRevocationPollPath', 'telemetryauth.ValidateGrant', 'TelemetryRuntimeSPIFFE', 'IAM_TELEMETRY_GRANT_VERIFIER_UNAVAILABLE', 'IAM_TELEMETRY_GRANT_STATE_UNAVAILABLE'], 'Telemetry Runtime IAM interface');
includesAll(grantStoreGo, ['pgx.Serializable', 'ON CONFLICT (token_id) DO NOTHING', 'telemetry_revocation_facts', 's2_iam_grant_runtime'], 'grant state store');
includesAll(auditGo, ['TargetCount', 'KeyCount', 'ScopeDigest', 'ReasonCode', 'RequestID', 'TraceID'], 'safe authorization audit');
for (const forbidden of ['DelegationGrant string', 'TelemetryKey string', 'TelemetryValue']) assert(!auditGo.includes(forbidden), `audit contract contains forbidden raw field ${forbidden}`);
includesAll(mainGo, ['IAM_TELEMETRY_GRANT_DATABASE_URL', 'IAM_TELEMETRY_RUNTIME_SPIFFE', 'telemetry-runtime-service'], 'IAM runtime wiring');

for (const table of contract.postgres?.forceRlsTables ?? []) {
  includesAll(sql, [`ALTER TABLE iam.${table} ENABLE ROW LEVEL SECURITY`, `ALTER TABLE iam.${table} FORCE ROW LEVEL SECURITY`], `${table} RLS`);
}
includesAll(sql, [
  "CREATE ROLE s2_iam_grant_runtime",
  'devices_iam_exact_request_scope',
  'GRANT SELECT (id, organization_id, site_id, status, revision) ON core_registry.devices TO s1_iam_runtime',
  'emit_membership_telemetry_revocation',
  'emit_role_binding_telemetry_revocation',
  'emit_site_binding_telemetry_revocation',
  'emit_scope_telemetry_revocation',
  'emit_key_telemetry_revocation',
  'NEW.role_key IS DISTINCT FROM OLD.role_key',
  'NEW.site_id IS DISTINCT FROM OLD.site_id',
  'NEW.device_id IS DISTINCT FROM OLD.device_id',
  'NEW.telemetry_key IS DISTINCT FROM OLD.telemetry_key',
], 'S2 IAM PostgreSQL baseline');
assert(!sql.includes('row_value := NEW'), 'revocation triggers must publish the previous authorized scope');

includesAll(telemetryTests, ['TestScopeDigestIsOrderIndependentButExact', 'TestRecoveryGrantValidationFailsClosedForEveryBoundFieldAndReplay', 'cross-principal-cursor', 'cross-device-key-cursor', 'session', 'replayed', 'revoked'], 'grant conformance tests');
includesAll(authorizationTests, ['TestTelemetryAuthorizationRequiresExactDeviceAndKeyScope', 'TestTelemetryAuthorizationDoesNotTreatMembershipAsAllSites', 'TestTelemetryAuthorizationRequiresExactCrossOrganizationSiteBinding', 'TestTelemetryAuthorizationDenyPrecedenceAndStableKeyFailure', 'TestTelemetryAuthorizationIsAllOrNothing', 'TestPostgresTelemetryActionProjectionRejectsUnknownSchemaDrift'], 'authorization tests');
includesAll(serverTests, ['TestIAMTelemetryDecisionIssuesExactNonTransitiveGrant', 'TestIAMTelemetryDecisionDeniesWithoutGrantAndFailsClosedOnDependency', 'TestTelemetryRuntimeConsumesGrantOnceAndPollsRevocations'], 'IAM HTTP tests');
includesAll(integrationTests, ['TestPostgresTelemetryRevocationPoll', 'TestPostgresTelemetryGrantSingleUse'], 'PostgreSQL grant tests');

const expectedResources = ['iam-telemetry-authorization-decision', 'iam-telemetry-grant-use', 'iam-telemetry-revocation-fact'];
for (const name of expectedResources) {
  const resource = dataRegistry.resources?.find((value) => value.kind === 'projection' && value.name === name);
  assert(resource?.writer === 'iam-service', `ownership resource ${name} is missing`);
}
const grantIdentity = dataRegistry.databaseIdentities?.find((identity) => identity.schema === 'iam' && identity.runtimeRole === 's2_iam_grant_runtime');
assert(grantIdentity?.runtimeBypassRls === false && exact(grantIdentity?.restrictedTo, ['telemetry_grant_revocations', 'telemetry_grant_uses', 'telemetry_revocation_facts']), 'grant-state database identity drifted');

assert(packageJSON.scripts?.['test:identity']?.includes('./libs/telemetryauth/...'), 'global identity tests omit telemetryauth');
assert(packageJSON.scripts?.['test:security-negative']?.includes('./libs/telemetryauth/...'), 'global security-negative tests omit telemetryauth');
assert(packageJSON.scripts?.['s2:iam:check'] === 'node scripts/check-s2-iam-authorization.mjs', 's2:iam:check is not wired');
assert(packageJSON.scripts?.['s2:iam:postgres'] === 'node scripts/run-s2-iam-postgres-tests.mjs', 's2:iam:postgres is not wired');
for (const command of ['npm run s2:iam:check', 'npm run s2:iam:postgres', 'test ./libs/telemetryauth/...', 'test ./services/iam-service/...', 'npm run ownership:check', 'npm run lint', 'npm run build']) {
  assert(packageJSON.scripts?.['s2:ticket-02']?.includes(command), `s2:ticket-02 omits ${command}`);
}
includesAll(workflow, ['runs-on: ubuntu-24.04', "go-version: '1.25.12'", "node-version: '22.22.0'", 'npm run s2:ticket-02', 'out/s2-ticket-02/iam-authorization-evidence.json'], 'S2 IAM workflow');

console.log('S2 Ticket 02 IAM authorization passed: exact Device/key grants, single-use consumption, deny precedence and bounded revocation delivery.');
