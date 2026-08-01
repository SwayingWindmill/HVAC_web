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
  ownershipLock,
  model,
  serviceHTTP,
  store,
  postgres,
  readMigration,
  lifecycleMigration,
  postgresFixture,
  postgresRunner,
  webAPI,
  realAlarms,
  siteScopedShell,
  realApp,
] = await Promise.all([
  readJSON('contracts/http/s4-alarm-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readText('libs/alarmmodel/model.go'),
  readText('services/alarm-service/pkg/alarmservice/http.go'),
  readText('services/alarm-service/pkg/alarmservice/store.go'),
  readText('services/alarm-service/pkg/alarmservice/postgres.go'),
  readText('services/alarm-service/migrations/001_s4_alarm_runtime.sql'),
  readText('services/alarm-service/migrations/002_s4_alarm_lifecycle.sql'),
  readText('infra/s4-alarm/compose.yaml'),
  readText('scripts/run-s4-alarm-postgres-tests.mjs'),
  readText('apps/hvac-web/src/api/alarms.ts'),
  readText('apps/hvac-web/src/real/RealAlarms.tsx'),
  readText('apps/hvac-web/src/real/SiteScopedShell.tsx'),
  readText('apps/hvac-web/src/real/RealApp.tsx'),
]);

assert(openapi.info?.version === '0.2.0-contract-only', 'Alarm OpenAPI is not the S4 lifecycle contract-only baseline');
const listPath = '/api/v1/sites/{siteId}/alarms';
const detailPath = '/api/v1/sites/{siteId}/alarms/{alarmId}';
for (const path of [listPath, detailPath]) {
  const operation = openapi.paths?.[path]?.get;
  assert(operation?.['x-owner'] === 'alarm-service', `Alarm owner is missing for ${path}`);
  assert(operation?.['x-public-ingress'] === 'platform-gateway', `Gateway ingress is missing for ${path}`);
  assert(operation?.['x-production-traffic-percent'] === 0, `Alarm production traffic is enabled for ${path}`);
  assert(operation?.['x-migration-phase'] === 'S4-R0-contract-only', `Alarm phase is not contract-only for ${path}`);
  const route = routes.routes?.find((entry) => entry.method === 'GET' && entry.path === path);
  assert(route?.owner === 'alarm-service', `Route Ownership Registry is missing ${path}`);
  assert(route?.rollout?.mode === 'disabled', `Alarm route ${path} is not disabled`);
  assert(route?.migrationPhase === 'S4-R0-contract-only', `Alarm route ${path} has the wrong phase`);
  assert(route?.shadowSideEffectPolicy === 'NONE' && route?.readOnlyFallback === false, `Alarm route ${path} permits fallback or side effects`);
  assert(route?.fallbackForbiddenResults?.includes('AUTHORIZATION_DENIED') && route?.fallbackForbiddenResults?.includes('RESOURCE_NOT_FOUND'), `Alarm route ${path} has incomplete non-leaking results`);
  assert(ownershipLock.routes?.[`GET ${path}`]?.owner === 'alarm-service', `Alarm ownership lock is missing ${path}`);
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
  assert(ownershipLock.routes?.[`POST ${path}`]?.owner === 'alarm-service', `Alarm lifecycle ownership lock is missing ${path}`);
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

assert(webAPI.includes("'X-CSRF-Token'") && webAPI.includes("'Idempotency-Key'"), 'Alarm Web mutation does not require CSRF and idempotency');
assert(webAPI.includes('expectedVersion') && webAPI.includes('acknowledgeScopedAlarm') && webAPI.includes('reopenScopedAlarm'), 'Alarm Web lifecycle API is incomplete');
const disabledBoundaryIndex = realAlarms.indexOf('if (!ALARM_LOCAL_ROUTES_ENABLED)');
const localWorkbenchIndex = realAlarms.indexOf('function LocalAlarmWorkbench');
assert(disabledBoundaryIndex >= 0 && localWorkbenchIndex > disabledBoundaryIndex, 'Real Alarm production path does not fail closed before local hooks');
assert(realAlarms.includes('useMutation') && realAlarms.includes('registerUnsavedDraft'), 'Real Alarm lifecycle workbench or draft protection is missing');
assert(realAlarms.includes('ALARM_VERSION_CONFLICT') && realAlarms.includes('stableIdempotencyKey'), 'Real Alarm conflict recovery or stable retry key is missing');
assert(realAlarms.includes('不会把设备状态推导成 Alarm'), 'Real Alarm disabled surface does not state the non-inference boundary');
assert(siteScopedShell.includes('registerUnsavedDraft={registerUnsavedDraft}'), 'Site shell does not protect Alarm lifecycle drafts');
assert(realApp.includes("platformNavigation.filter((item) => item.id !== 'alarms')"), 'Site navigation still exposes the obsolete global Alarm placeholder');

console.log('S4 Alarm lifecycle baseline passed: disabled public routes, exact signed scope, durable state machine, optimistic concurrency, idempotent replay, least-privilege RLS and protected local UI are present.');
