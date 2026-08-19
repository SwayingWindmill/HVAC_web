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
  model,
  serviceHTTP,
  store,
  postgres,
  readMigration,
  lifecycleMigration,
  alarmAuth,
  iamAuthorization,
  iamPostgres,
  iamMigration,
  gatewayAlarm,
  contractOnlyGateway,
  convergence,
] = await Promise.all([
  readJSON('contracts/http/s4-alarm-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readText('libs/alarmmodel/model.go'),
  readText('services/alarm-service/pkg/alarmservice/http.go'),
  readText('services/alarm-service/pkg/alarmservice/store.go'),
  readText('services/alarm-service/pkg/alarmservice/postgres.go'),
  readText('services/alarm-service/migrations/001_s4_alarm_runtime.sql'),
  readText('services/alarm-service/migrations/002_s4_alarm_lifecycle.sql'),
  readText('libs/alarmauth/authorization.go'),
  readText('services/iam-service/internal/iam/alarm_authorization.go'),
  readText('services/iam-service/internal/iam/postgres_alarm_authorization.go'),
  readText('infra/s1-registry/postgres/init/007-s4-alarm-authorization.sql'),
  readText('services/platform-gateway/internal/gateway/alarm.go'),
  readText('services/platform-gateway/internal/gateway/v212_contract_only.go'),
  readJSON('contracts/architecture/se-api-001-v1.2-runtime-convergence.json'),
]);

assert(openapi.info?.version === '2.1.2', 'Alarm OpenAPI version must remain V2.1.2');
assert(openapi['x-authority'] === 'SE-DOMAIN-ALARM-001 V1.0 CURRENT CANDIDATE', 'Alarm Domain authority is stale');

const listPath = '/api/v1/alarms';
const detailPath = '/api/v1/alarms/{alarmId}';
const ackPath = '/api/v1/alarms/{alarmId}/ack';
for (const [method, path] of [['get', listPath], ['get', detailPath], ['post', ackPath]]) {
  const operation = openapi.paths?.[path]?.[method];
  assert(operation?.['x-owner'] === 'alarm-service', `Alarm owner is missing for ${method.toUpperCase()} ${path}`);
  assert(operation?.['x-public-ingress'] === 'platform-gateway', `Gateway ingress is missing for ${method.toUpperCase()} ${path}`);
  assert(operation?.['x-architecture-status'] === 'ACTIVE' && operation?.['x-shape-status'] === 'READY', `Alarm route is not active/ready: ${method.toUpperCase()} ${path}`);
  const route = routes.routes?.find((entry) => entry.method === method.toUpperCase() && entry.path === path);
  assert(route?.owner === 'alarm-service' && route?.publicIngress === 'platform-gateway', `Route Ownership Registry is missing ${method.toUpperCase()} ${path}`);
  assert(route?.activationStatus === 'primary' && route?.rollout?.mode === 'all', `Alarm route is not primary/all: ${method.toUpperCase()} ${path}`);
  assert(route?.migrationPhase === 'S4-R3-operationally-certified', `Alarm route is not R3 certified: ${method.toUpperCase()} ${path}`);
  assert(route?.readOnlyFallback === false, `Alarm route permits request fallback: ${method.toUpperCase()} ${path}`);
}

const listOperation = openapi.paths[listPath].get;
assert(listOperation.parameters?.some((parameter) => parameter.$ref === '#/components/parameters/SiteIdRequired'), 'Alarm list must require siteId');
assert(openapi.components?.parameters?.Limit?.schema?.default === 50 && openapi.components?.parameters?.Limit?.schema?.maximum === 200, 'Alarm list limit must be default 50 / max 200');
assert(openapi.components?.schemas?.CursorAlarmListResponse, 'Alarm cursor list response schema is missing');
assert(openapi.components?.parameters?.Cursor?.description?.includes('bound to the original Site'), 'Alarm cursor must be bound to Site and filters');

const ackOperation = openapi.paths[ackPath].post;
assert(ackOperation.requestBody?.required === false, 'Alarm ACK body must be optional');
assert(openapi.components?.schemas?.AckAlarmRequest?.properties?.comment?.maxLength === 1000, 'Alarm ACK comment must be capped at 1000 characters');
assert(ackOperation.parameters?.some((parameter) => parameter.$ref === '#/components/parameters/OptionalIdempotencyKey'), 'Alarm ACK optional Idempotency-Key is missing');
const ackRoute = routes.routes.find((entry) => entry.method === 'POST' && entry.path === ackPath);
assert(!ackRoute?.fallbackForbiddenResults?.includes('VERSION_CONFLICT'), 'Alarm ACK must not advertise VERSION_CONFLICT');
assert(ackRoute?.fallbackForbiddenResults?.includes('IDEMPOTENCY_CONFLICT'), 'Alarm ACK idempotency conflict boundary is missing');

for (const path of [listPath, detailPath, ackPath]) {
  assert(!contractOnlyGateway.includes(`template: "${path}"`), `Active Alarm route remains contract-only: ${path}`);
}
assert(convergence.summary?.classificationAReadyToActivate === 3 && convergence.summary?.runtimeContractOnlyRoutes === 10, 'Alarm runtime convergence summary is stale');

assert(alarmAuth.includes('ActionRead Action = "alarm:read"') && alarmAuth.includes('ActionAck  Action = "alarm:ack"'), 'Alarm IAM action vocabulary must be read/ack');
assert(!alarmAuth.includes('alarm:list'), 'Retired alarm:list action remains in Alarm authorization vocabulary');
assert(iamAuthorization.includes('permission.Action != request.Action'), 'IAM exact Alarm action authorization is missing');
assert(iamPostgres.includes('permission.Action != alarmauth.ActionRead') && iamPostgres.includes('permission.Action != alarmauth.ActionAck'), 'IAM Postgres Alarm action validation is stale');
assert(iamMigration.includes("action IN ('alarm:read', 'alarm:ack')"), 'IAM Alarm SQL action vocabulary is stale');
assert(!iamMigration.includes("'alarm:list'"), 'Retired alarm:list SQL permission remains');

assert(model.includes('input.Operation != OperationAcknowledge && input.ExpectedVersion != alarm.Version'), 'ACK must not require expectedVersion');
assert(model.includes('hasAcknowledgement(result.Transitions)') && model.includes('toStatus = fromStatus'), 'ACK must be naturally idempotent and lifecycle-neutral');
assert(model.includes('len(strings.TrimSpace(input.Reason)) > 1000'), 'ACK comment boundary is missing');
assert(model.includes('return items[left].FirstOccurredAt > items[right].FirstOccurredAt'), 'Alarm list must sort by first trigger time DESC');
assert(model.includes('return items[left].AlarmID > items[right].AlarmID'), 'Alarm list tie-break must sort alarmId DESC');

assert(serviceHTTP.includes('InternalAlarmScopePrefix') && serviceHTTP.includes('AlarmResolveAction'), 'Alarm ownership resolver endpoint is missing');
assert(serviceHTTP.includes('handler.store.ResolveScope') && serviceHTTP.includes('AlarmScope'), 'Alarm Service does not resolve authoritative Alarm scope');
assert(serviceHTTP.includes('func (handler *httpHandler) acknowledge') && serviceHTTP.includes('Idempotency-Key'), 'Alarm ACK service boundary is missing');
assert(serviceHTTP.includes('idempotencyKey != "" && !idempotencyKeyPattern.MatchString'), 'Alarm ACK Idempotency-Key must be optional but validated when present');
assert(serviceHTTP.includes('MaxListLimit') && serviceHTTP.includes('> 200'), 'Alarm Service list maximum must be 200');

assert(store.includes('type AlarmScope struct') && store.includes('ResolveScope('), 'Alarm Store ownership resolver is missing');
assert(store.includes('encodeAlarmCursor') && store.includes('decodeAlarmCursor') && store.includes('alarmFilterFingerprint'), 'Opaque Alarm cursor implementation is missing');
assert(store.includes('key == "" && mutation.Operation != alarmmodel.OperationAcknowledge'), 'ACK must be the only lifecycle mutation that permits no Idempotency-Key');
assert(postgres.includes('ORDER BY first_occurred_at DESC, alarm_id DESC'), 'Alarm Postgres ordering is not trigger-time DESC / alarmId DESC');
assert(postgres.includes('func (store *PostgresStore) ResolveScope'), 'Alarm Postgres ownership resolver is missing');

assert(readMigration.includes("current_setting('app.tenant_id'"), 'Alarm Postgres Tenant RLS is missing');
assert(!readMigration.includes("current_setting('app.organization_id'"), 'Alarm Postgres still depends on Organization RLS');
assert(lifecycleMigration.includes('FOR ALL TO s4_alarm_runtime') && lifecycleMigration.includes('WITH CHECK'), 'Alarm lifecycle Tenant RLS write policy is missing');

assert(gatewayAlarm.includes('path == "/api/v1/alarms"') && gatewayAlarm.includes('"/api/v1/alarms/{alarmId}/ack"'), 'Gateway public Alarm route matching is incomplete');
assert(gatewayAlarm.includes('resolveAlarmScope') && gatewayAlarm.includes('checkAlarmSiteVisibility'), 'Gateway BOLA-safe Alarm resolution chain is missing');
assert(gatewayAlarm.includes('registryauth.ActionSiteRead'), 'Gateway does not verify authoritative Site visibility');
assert(gatewayAlarm.includes('alarmauth.ActionAck') && gatewayAlarm.includes('executeAlarmOperation'), 'Gateway ACK execution path is missing');
assert(gatewayAlarm.includes('writeV212Error') || gatewayAlarm.includes('writeAlarmFailure'), 'Gateway Alarm error envelope boundary is missing');
assert(!gatewayAlarm.includes('/telemetry/'), 'Gateway derives Alarm state from Telemetry HTTP');

console.log('S4 Alarm baseline valid: publicRoutes=3; readAction=alarm:read; ackAction=alarm:ack; runtimeContractOnly=10');
