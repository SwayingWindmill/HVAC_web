import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const [
  openapi,
  routes,
  dataOwnership,
  ownershipLock,
  model,
  serviceHTTP,
  store,
  postgres,
  readMigration,
  lifecycleMigration,
  postgresFixture,
  postgresRunner,
  alarmAuth,
  iamAuthorization,
  iamServer,
  iamPostgres,
  iamMigration,
  gatewayAlarm,
  gatewayServer,
  webAPI,
  realAlarms,
  localLifecycle,
  siteScopedShell,
  realApp,
  publicBrowser,
  lifecycleBrowser,
  promotionEnvelope,
  promotionRunner,
  promotionVerifier,
  promotionRunbook,
] = await Promise.all([
  readJSON('contracts/http/s4-alarm-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readText('libs/alarmmodel/model.go'),
  readText('services/alarm-service/pkg/alarmservice/http.go'),
  readText('services/alarm-service/pkg/alarmservice/store.go'),
  readText('services/alarm-service/pkg/alarmservice/postgres.go'),
  readText('services/alarm-service/migrations/001_s4_alarm_runtime.sql'),
  readText('services/alarm-service/migrations/002_s4_alarm_lifecycle.sql'),
  readText('infra/s4-alarm/compose.yaml'),
  readText('scripts/run-s4-alarm-postgres-tests.mjs'),
  readText('libs/alarmauth/authorization.go'),
  readText('services/iam-service/internal/iam/alarm_authorization.go'),
  readText('services/iam-service/internal/iam/alarm_server.go'),
  readText('services/iam-service/internal/iam/postgres_alarm_authorization.go'),
  readText('infra/s1-registry/postgres/init/007-s4-alarm-authorization.sql'),
  readText('services/platform-gateway/internal/gateway/alarm.go'),
  readText('services/platform-gateway/internal/gateway/server.go'),
  readText('apps/hvac-web/src/api/alarms.ts'),
  readText('apps/hvac-web/src/real/RealAlarms.tsx'),
  readText('apps/hvac-web/src/real/LocalAlarmLifecycle.tsx'),
  readText('apps/hvac-web/src/real/SiteScopedShell.tsx'),
  readText('apps/hvac-web/src/real/RealApp.tsx'),
  readText('scripts/run-real-alarms-browser-audit.mjs'),
  readText('scripts/run-real-alarm-lifecycle-browser-audit.mjs'),
  readJSON('deploy/s4/alarm-read-promotion-envelope.v1.json'),
  readText('scripts/run-s4-alarm-read-promotion-certification.mjs'),
  readText('scripts/verify-s4-alarm-read-promotion-certification.mjs'),
  readText('docs/operations/s4-alarm-read-promotion.md'),
]);

assert(openapi.info?.version === '0.3.0-internal-read-canary', 'Alarm OpenAPI is not the S4 internal read canary contract');
const listPath = '/api/v1/sites/{siteId}/alarms';
const detailPath = '/api/v1/sites/{siteId}/alarms/{alarmId}';
const readActions = new Map([[listPath, 'alarm:list'], [detailPath, 'alarm:read']]);
for (const [path, action] of readActions) {
  const operation = openapi.paths?.[path]?.get;
  assert(operation?.['x-owner'] === 'alarm-service', `Alarm owner is missing for ${path}`);
  assert(operation?.['x-public-ingress'] === 'platform-gateway', `Gateway ingress is missing for ${path}`);
  assert(operation?.['x-production-traffic-percent'] === 1, `Alarm read canary is not exactly 1% for ${path}`);
  assert(operation?.['x-migration-phase'] === 'S4-R1-internal-read-only', `Alarm read phase is invalid for ${path}`);
  assert(operation?.['x-iam-action'] === action && operation?.['x-no-fallback'] === true, `Alarm IAM action or no-fallback marker is missing for ${path}`);
  const route = routes.routes?.find((entry) => entry.method === 'GET' && entry.path === path);
  assert(route?.owner === 'alarm-service' && route?.publicIngress === 'platform-gateway', `Route Ownership Registry is missing ${path}`);
  assert(route?.revision === 2 && route?.activationStatus === 'internal-canary', `Alarm route ${path} is not the reviewed revision-2 internal canary`);
  assert(route?.rollout?.mode === 'percentage' && route?.rollout?.percentage === 1, `Alarm route ${path} is not the exact 1% canary`);
  assert(route?.rollout?.fallbackOwner === undefined && route?.rollout?.cohortSalt === 's4-alarm-read-canary-v1', `Alarm route ${path} has fallback or an unexpected cohort salt`);
  assert(route?.migrationPhase === 'S4-R1-internal-read-only', `Alarm route ${path} has the wrong phase`);
  assert(route?.shadowSideEffectPolicy === 'NONE' && route?.readOnlyFallback === false, `Alarm route ${path} permits fallback or side effects`);
  for (const scope of ['organization', 'site', 'principal']) assert(route?.allowedScopeDimensions?.includes(scope), `Alarm route ${path} is missing scope ${scope}`);
  assert(route?.fallbackForbiddenResults?.includes('AUTHORIZATION_DENIED') && route?.fallbackForbiddenResults?.includes('RESOURCE_NOT_FOUND'), `Alarm route ${path} has incomplete non-leaking results`);
  assert(ownershipLock.routes?.[`GET ${path}`]?.owner === 'alarm-service' && ownershipLock.routes?.[`GET ${path}`]?.revision === 2, `Alarm ownership lock is missing revision 2 for ${path}`);
}

const lifecycleSuffixes = ['acknowledge', 'assign', 'unassign', 'suppress', 'unsuppress', 'close', 'reopen'];
for (const suffix of lifecycleSuffixes) {
  const path = `${detailPath}:${suffix}`;
  const operation = openapi.paths?.[path]?.post;
  assert(operation?.['x-owner'] === 'alarm-service', `Alarm lifecycle owner is missing for ${path}`);
  assert(operation?.['x-public-ingress'] === 'platform-gateway', `Gateway ingress is missing for ${path}`);
  assert(operation?.['x-production-traffic-percent'] === 0, `Alarm lifecycle production traffic is enabled for ${path}`);
  assert(operation?.['x-migration-phase'] === 'S4-R0-contract-only', `Alarm lifecycle phase is not contract-only for ${path}`);
  assert(operation?.parameters?.some((parameter) => parameter.$ref === '#/components/parameters/IdempotencyKey'), `Idempotency-Key is missing for ${path}`);
  assert(operation?.requestBody?.required === true, `Alarm lifecycle body is optional for ${path}`);
  for (const status of ['409', '422', '503']) assert(operation?.responses?.[status], `Alarm lifecycle response ${status} is missing for ${path}`);
  const route = routes.routes?.find((entry) => entry.method === 'POST' && entry.path === path);
  assert(route?.owner === 'alarm-service' && route?.publicIngress === 'platform-gateway', `Route Ownership Registry is missing ${path}`);
  assert(route?.rollout?.mode === 'disabled' && route?.migrationPhase === 'S4-R0-contract-only', `Alarm lifecycle route ${path} is not disabled contract-only`);
  assert(route?.shadowSideEffectPolicy === 'SYNTHETIC_ONLY' && route?.readOnlyFallback === false, `Alarm lifecycle route ${path} permits unsafe shadow or fallback`);
  for (const scope of ['organization', 'site', 'alarm', 'principal', 'key']) assert(route?.allowedScopeDimensions?.includes(scope), `Alarm lifecycle route ${path} is missing scope ${scope}`);
  for (const result of ['AUTHORIZATION_DENIED', 'RESOURCE_NOT_FOUND', 'VERSION_CONFLICT', 'IDEMPOTENCY_CONFLICT']) assert(route?.fallbackForbiddenResults?.includes(result), `Alarm lifecycle fallback is not forbidden for ${result} at ${path}`);
  assert(ownershipLock.routes?.[`POST ${path}`]?.owner === 'alarm-service' && ownershipLock.routes?.[`POST ${path}`]?.revision === 1, `Alarm lifecycle ownership lock drifted for ${path}`);
}

assert(dataOwnership.registryRevision >= 16 && ownershipLock.dataRegistryRevision >= 16, 'Alarm IAM ownership revision was not advanced');
for (const name of ['iam-alarm-permission', 'iam-alarm-authorization-decision']) {
  const resource = dataOwnership.resources?.find((entry) => entry.name === name);
  assert(resource?.kind === 'projection' && resource?.writer === 'iam-service' && resource?.revision === 1, `Alarm IAM ownership resource ${name} is missing`);
  assert(ownershipLock.resources?.[`projection:${name}`]?.writer === 'iam-service', `Alarm IAM ownership lock is missing ${name}`);
}

const alarmSchema = openapi.components?.schemas?.Alarm;
for (const field of ['sourceType', 'sourceReference', 'severity', 'status', 'occurrenceCount', 'evidence', 'transitions', 'version']) {
  assert(alarmSchema?.required?.includes(field), `Alarm contract does not require ${field}`);
}
assert(alarmSchema?.properties?.assigneeId && alarmSchema?.properties?.suppressedUntil, 'Alarm lifecycle projection fields are missing');
const transitionSchema = openapi.components?.schemas?.AlarmTransition;
for (const field of ['operation', 'actorId', 'assigneeId', 'suppressedUntil', 'policyRevision', 'correlationId']) {
  assert(transitionSchema?.properties?.[field], `Alarm transition evidence field ${field} is missing`);
}

assert(alarmAuth.includes('ActionList Action = "alarm:list"') && alarmAuth.includes('ActionRead Action = "alarm:read"'), 'Alarm IAM action vocabulary is incomplete');
assert(alarmAuth.includes('DecisionRequest') && alarmAuth.includes('Decision') && alarmAuth.includes('ReasonAllowExactScope'), 'Alarm IAM decision contract is incomplete');
assert(iamAuthorization.includes('LookupAlarmAuthorization') && iamAuthorization.includes('ReasonDenyExplicit') && iamAuthorization.includes('alarmCapabilityAllowed'), 'IAM exact Alarm authorization is incomplete');
assert(iamServer.includes('handleAlarmDecision') && iamServer.includes('AlarmDecisionAudit') && iamServer.includes('IAM_AUTHORIZATION_AUDIT_UNAVAILABLE'), 'IAM Alarm decision or audit fail-closed boundary is missing');
assert(iamPostgres.includes('iam.alarm_permissions') && iamPostgres.includes('iam.alarm_authorization_decisions'), 'IAM Alarm Postgres store is incomplete');
assert(iamMigration.includes('FORCE ROW LEVEL SECURITY') && iamMigration.includes('GRANT SELECT ON iam.alarm_permissions') && iamMigration.includes('GRANT INSERT ON iam.alarm_authorization_decisions'), 'IAM Alarm RLS or least-privilege grants are missing');
assert(!iamMigration.includes('GRANT UPDATE ON iam.alarm_permissions TO s1_iam_runtime') && !iamMigration.includes('GRANT DELETE'), 'IAM Alarm runtime has excessive privileges');

assert(gatewayServer.includes('X-Alarm-Read-Context') && gatewayServer.includes('X-Alarm-Write-Context'), 'Gateway does not reject browser Alarm authority headers');
assert(gatewayAlarm.includes('alarm:authorize') && gatewayAlarm.includes('alarmDecisionPath'), 'Gateway does not request exact IAM Alarm authorization');
assert(gatewayAlarm.includes('X-Alarm-Read-Context') && gatewayAlarm.includes('SignDelegation'), 'Gateway does not sign the Alarm read context');
assert(gatewayAlarm.includes('validatePublicAlarmQuery') && gatewayAlarm.includes('maximumAlarmQueryLength'), 'Gateway Alarm query bounds are missing');
assert(gatewayAlarm.includes('response.Validate(session.ActingOrganizationID, route.siteID, limit)') && gatewayAlarm.includes('alarm.OrganizationID != session.ActingOrganizationID'), 'Gateway does not revalidate Alarm response scope');
assert(gatewayAlarm.includes('Cache-Control') && gatewayAlarm.includes('private, no-store'), 'Gateway Alarm responses are not protected from caching');
assert(!gatewayAlarm.includes('/telemetry/'), 'Gateway derives Alarm state from Telemetry HTTP');

for (const symbol of ['OperationAcknowledge', 'OperationAssign', 'OperationUnassign', 'OperationSuppress', 'OperationUnsuppress', 'OperationClose', 'OperationReopen']) {
  assert(model.includes(symbol), `Alarm lifecycle model is missing ${symbol}`);
}
assert(model.includes('ExpectedVersion') && model.includes('ErrVersionConflict'), 'Alarm optimistic concurrency model is missing');
assert(model.includes('ApplyOperation') && model.includes('statusBeforeSuppression'), 'Alarm authoritative state machine is missing');
assert(model.includes('PolicyRevision') && model.includes('CorrelationID'), 'Alarm transition audit evidence is missing');
assert(!model.includes('/telemetry/'), 'Alarm model depends on Telemetry HTTP');

assert(serviceHTTP.includes('X-Alarm-Read-Context') && serviceHTTP.includes('X-Alarm-Write-Context'), 'Alarm Service signed read/write contexts are incomplete');
assert(serviceHTTP.includes('len(claims.Actions) != 1') && serviceHTTP.includes('claims.Actions[0] != action'), 'Alarm Service does not require one exact action');
assert(serviceHTTP.includes('sameStringSet(claims.Scopes, expectedScopes)'), 'Alarm Service does not require exact scope');
assert(serviceHTTP.includes('Idempotency-Key') && serviceHTTP.includes('ExpectedVersion'), 'Alarm Service mutation request boundary is incomplete');
assert(serviceHTTP.includes('ALARM_VERSION_CONFLICT') && serviceHTTP.includes('ALARM_IDEMPOTENCY_CONFLICT'), 'Alarm Service mutation conflict taxonomy is incomplete');
assert(!serviceHTTP.includes('/telemetry/'), 'Alarm Service derives Alarm state from Telemetry HTTP');

assert(store.includes('idempotencyRecord') && store.includes('mutationDigest'), 'Alarm Store idempotency binding is missing');
assert(store.includes('ErrIdempotencyConflict') && store.includes('Replayed: true'), 'Alarm Store idempotent replay is missing');
assert(postgres.includes('FOR UPDATE') && postgres.includes('RowsAffected() != 1'), 'Alarm Postgres optimistic concurrency is missing');
assert(postgres.includes('alarm_runtime.alarm_idempotency') && postgres.includes('request_digest'), 'Alarm Postgres idempotency persistence is missing');
assert(readMigration.includes('ENABLE ROW LEVEL SECURITY') && readMigration.includes("current_setting('app.organization_id'"), 'Alarm Postgres Organization RLS is missing');
assert(lifecycleMigration.includes('FOR ALL TO s4_alarm_runtime') && lifecycleMigration.includes('WITH CHECK'), 'Alarm lifecycle RLS write policy is missing');
assert(lifecycleMigration.includes('GRANT SELECT, UPDATE') && lifecycleMigration.includes('GRANT SELECT, INSERT ON alarm_runtime.alarm_idempotency'), 'Alarm runtime least-privilege write grants are missing');
assert(!lifecycleMigration.includes('GRANT DELETE') && !lifecycleMigration.includes('GRANT INSERT ON alarm_runtime.alarm_current'), 'Alarm runtime has excessive write grants');
assert(postgresFixture.includes('postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412'), 'Alarm PostgreSQL fixture is not digest pinned');
assert(postgresFixture.includes('001_s4_alarm_runtime.sql') && postgresFixture.includes('002_s4_alarm_lifecycle.sql'), 'Alarm PostgreSQL fixture does not apply both migrations');
assert(postgresRunner.includes('S4_ALARM_TEST_DATABASE_URL'), 'Alarm PostgreSQL integration does not activate the real Store');
assert(postgresRunner.includes('runtime login bypassed explicit role activation') && postgresRunner.includes('runtime can insert Alarm authority rows'), 'Alarm PostgreSQL integration does not prove activation and least privilege');
assert(postgresRunner.includes('durable lifecycle projection') && postgresRunner.includes('durable idempotency record'), 'Alarm PostgreSQL integration does not prove durable mutation convergence');

assert(webAPI.includes("ALARM_PUBLIC_ROUTES_ENABLED = API_MODE === 'real'"), 'Alarm Web public GET route is not enabled in Real mode');
assert(webAPI.includes('if (!ALARM_LOCAL_ROUTES_ENABLED)') && webAPI.includes('ALARM_LIFECYCLE_DISABLED'), 'Alarm Web mutations are not local-only fail-closed');
assert(webAPI.includes("'X-CSRF-Token'") && webAPI.includes("'Idempotency-Key'"), 'Alarm Web local mutation does not require CSRF and idempotency');
assert(realAlarms.includes("capabilities.includes('alarm.list')") && realAlarms.includes("capabilities.includes('alarm.read')"), 'Real Alarm does not honor IAM list/detail capabilities');
assert(realAlarms.includes("lazy(() => import('./LocalAlarmLifecycle'))") && !realAlarms.includes('useMutation'), 'Real Alarm production component still instantiates lifecycle mutation hooks');
assert(realAlarms.includes('1% INTERNAL CANARY') && realAlarms.includes('只读 Alarm canary'), 'Real Alarm production read canary boundary is missing');
assert(localLifecycle.includes('useMutation') && localLifecycle.includes('registerUnsavedDraft'), 'Local Alarm lifecycle workbench or draft protection is missing');
assert(localLifecycle.includes('ALARM_VERSION_CONFLICT') && localLifecycle.includes('stableIdempotencyRequest'), 'Local Alarm conflict recovery or stable retry key is missing');
assert(realAlarms.includes('Telemetry、Presence 和 Device 状态不会在浏览器中转译为 Alarm'), 'Real Alarm non-inference boundary is missing');
assert(siteScopedShell.includes("effectiveCapabilities.includes('alarm.list')"), 'Site Alarm navigation is not capability gated');
assert(siteScopedShell.includes('registerUnsavedDraft={registerUnsavedDraft}'), 'Site shell does not protect local Alarm lifecycle drafts');
assert(realApp.includes("platformNavigation.filter((item) => item.id === 'system')") && realApp.includes('buildSiteNavigation(selectedSite'), 'Site navigation does not isolate platform-global navigation from Site-scoped Alarm navigation');

assert(publicBrowser.includes("delete process.env.VITE_S4_LOCAL_ALARMS") && publicBrowser.includes('public-gateway-get-only-no-local-or-telemetry-inference'), 'Public Alarm browser certification is not Gateway-only');
assert(publicBrowser.includes('capability-denial-generic-boundary-and-cache-purge') && publicBrowser.includes('lifecycleWrites: false'), 'Public Alarm browser certification does not prove denial and no writes');
assert(lifecycleBrowser.includes("process.env.VITE_S4_LOCAL_ALARMS = 'true'") && lifecycleBrowser.includes('stable-suppression-payload-and-idempotency'), 'Local Alarm lifecycle browser certification was not preserved');

assert(promotionEnvelope.schemaVersion === 1 && promotionEnvelope.issue === 187 && promotionEnvelope.formalPromotionRequired === true, 'Alarm read promotion envelope is invalid');
assert(promotionEnvelope.repositoryMutationByCertification === false, 'Alarm promotion certification may not mutate repository routing');
assert(promotionEnvelope.routeGroup?.source?.phase === 'S4-R1-internal-read-only' && promotionEnvelope.routeGroup?.source?.trafficPercent === 1 && promotionEnvelope.routeGroup?.source?.routeRevision === 2, 'Alarm promotion source phase drifted');
assert(promotionEnvelope.routeGroup?.target?.phase === 'S4-R2-site-canary' && promotionEnvelope.routeGroup?.target?.trafficPercent === 5 && promotionEnvelope.routeGroup?.target?.routeRevision === 3, 'Alarm promotion target phase drifted');
assert(promotionEnvelope.routeGroup?.rollback?.phase === 'S4-R1-internal-read-only' && promotionEnvelope.routeGroup?.rollback?.trafficPercent === 1 && promotionEnvelope.routeGroup?.rollback?.routeRevision === 4, 'Alarm promotion rollback phase drifted');
assert(promotionEnvelope.sourceCanary?.minimumHoldMinutes === 1440 && promotionEnvelope.sourceCanary?.minimumListRequests === 1000 && promotionEnvelope.sourceCanary?.minimumDetailRequests === 200, 'Alarm promotion source evidence thresholds drifted');
assert(promotionEnvelope.serviceLevelObjectives?.availabilityFractionMinimum === 0.999 && promotionEnvelope.serviceLevelObjectives?.p95MillisecondsMaximum === 500 && promotionEnvelope.serviceLevelObjectives?.p99MillisecondsMaximum === 1500, 'Alarm promotion SLOs drifted');
assert(promotionEnvelope.rollbackObjectives?.maximumDecisionMinutes === 5 && promotionEnvelope.rollbackObjectives?.maximumRouteRollbackMinutes === 15, 'Alarm promotion rollback objectives drifted');
assert(promotionEnvelope.approval?.distinctApproversRequired === 2, 'Alarm promotion does not require two distinct approvers');
for (const counter of ['crossOrganizationResponses', 'crossSiteResponses', 'crossAlarmResponses', 'authorizationMismatches', 'stalePolicyAccepts', 'responseScopeMismatches', 'fallbackSelections', 'localSeamRequests', 'demoContaminationEvents', 'unauditedRouteDecisions']) {
  assert(promotionEnvelope.zeroCounters?.includes(counter), `Alarm promotion zero counter ${counter} is missing`);
}
for (const evidence of ['source-canary-report.json', 'slo-report.json', 'security-zero-report.json', 'route-promotion-plan.json', 'rollback-report.json', 'promotion-approvals.json', 's4-alarm-read-promotion-attestation.json', 's4-alarm-read-promotion.intoto.json', 'SHA256SUMS']) {
  assert(promotionEnvelope.requiredEvidence?.includes(evidence), `Alarm promotion evidence ${evidence} is missing`);
}
assert(promotionRunner.includes("profile === 'formal'") && promotionRunner.includes('synthetic evidence cannot certify') && promotionRunner.includes('routesPromotedTogether'), 'Alarm formal promotion runner is incomplete');
assert(promotionRunner.includes('repositoryMutationPerformed: false') && promotionRunner.includes('twoPersonApprovalPassed: true'), 'Alarm promotion runner overclaims mutation or omits approval evidence');
assert(promotionVerifier.includes('SHA256SUMS') && promotionVerifier.includes('reviewerCanVerifyOffline') && promotionVerifier.includes('digest mismatch'), 'Alarm promotion offline verifier is incomplete');
for (const marker of ['## Repository preflight', '## Formal attestation', '## Evidence bundle', '## Promotion and rollback', '## Explicit exclusions']) {
  assert(promotionRunbook.includes(marker), `Alarm promotion runbook is missing ${marker}`);
}

console.log('S4 Alarm read activation and promotion preflight passed: exact IAM authorization, durable audit, no-fallback 1% Gateway reads, formal 1%-to-5% evidence gating, production read-only Web and 0% lifecycle writes are present.');
