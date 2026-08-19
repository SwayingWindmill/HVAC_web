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
  orthogonalMigration,
  orthogonalRollback,
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
  readText('services/alarm-service/migrations/005_s13_alarm_orthogonal.sql'),
  readText('services/alarm-service/migrations/rollback/005_s13_alarm_orthogonal.sql'),
  readText('libs/alarmauth/authorization.go'),
  readText('services/iam-service/internal/iam/alarm_authorization.go'),
  readText('services/iam-service/internal/iam/postgres_alarm_authorization.go'),
  readText('infra/s1-registry/postgres/init/007-s4-alarm-authorization.sql'),
  readText('services/platform-gateway/internal/gateway/alarm.go'),
  readText('services/platform-gateway/internal/gateway/v212_contract_only.go'),
  readJSON('contracts/architecture/se-api-001-v1.2-runtime-convergence.json'),
]);

assert(openapi.info?.version === '2.2.0', 'Alarm OpenAPI version must be the S13 orthogonal aggregate contract');
assert(openapi['x-authority'] === 'SE-DOMAIN-ALARM-001 S13 ORTHOGONAL AGGREGATE', 'Alarm Domain authority is stale');

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
  assert(route?.rollout?.mode === 'all' && route?.compatibilityMode === 'native', `Alarm route is not native/all: ${method.toUpperCase()} ${path}`);
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
assert(ackRoute?.compatibilityMode === 'native', 'Alarm ACK must remain native without compatibility fallback');

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

assert(model.includes('ConditionActive') && model.includes('ConditionCleared'), 'S13 physical condition facts are missing');
assert(model.includes('CurrentSeverity') && model.includes('PeakSeverity') && model.includes('SeverityMinor'), 'S13 severity facts are incomplete');
assert(model.includes('Acknowledgement') && model.includes('Suppression') && model.includes('Fingerprint'), 'S13 orthogonal aggregate facts are incomplete');
assert(model.includes('input.Operation != OperationAcknowledge && input.ExpectedVersion != alarm.Version'), 'ACK must not require expectedVersion');
assert(model.includes('if result.Acknowledgement != nil') && model.includes('return result, nil'), 'ACK must be naturally idempotent');
assert(!model.includes('OperationClose') && !model.includes('OperationReopen'), 'Retired Alarm Close/Reopen operations remain in the domain model');
assert(model.includes('len(strings.TrimSpace(input.Reason)) > 1000'), 'ACK comment boundary is missing');
assert(model.includes('return items[left].FirstOccurredAt > items[right].FirstOccurredAt'), 'Alarm list must sort by first occurrence time DESC');
assert(model.includes('return items[left].AlarmID > items[right].AlarmID'), 'Alarm list tie-break must sort alarmId DESC');

assert(serviceHTTP.includes('InternalAlarmScopePrefix') && serviceHTTP.includes('AlarmResolveAction'), 'Alarm ownership resolver endpoint is missing');
assert(serviceHTTP.includes('handler.store.ResolveScope') && serviceHTTP.includes('AlarmScope'), 'Alarm Service does not resolve authoritative Alarm scope');
assert(serviceHTTP.includes('func (handler *httpHandler) acknowledge') && serviceHTTP.includes('Idempotency-Key'), 'Alarm ACK service boundary is missing');
assert(serviceHTTP.includes('idempotencyKey != "" && !idempotencyKeyPattern.MatchString'), 'Alarm ACK Idempotency-Key must be optional but validated when present');
assert(serviceHTTP.includes('MaxListLimit') && serviceHTTP.includes('> 200'), 'Alarm Service list maximum must be 200');

assert(store.includes('type AlarmScope struct') && store.includes('ResolveScope('), 'Alarm Store ownership resolver is missing');
assert(store.includes('encodeAlarmCursor') && store.includes('decodeAlarmCursor') && store.includes('alarmFilterFingerprint'), 'Opaque Alarm cursor implementation is missing');
assert(store.includes('key == "" && mutation.Operation != alarmmodel.OperationAcknowledge'), 'ACK must be the only operator mutation that permits no Idempotency-Key');
assert(store.includes('func (store *MemoryStore) Publish') && store.includes('func (store *MemoryStore) ClearActive'), 'S13 incident publish/recovery store seams are missing');
assert(store.includes('current.IncidentCorrelationID != recovery.IncidentCorrelationID'), 'S13 recovery must be bound to the exact Incident correlation');
assert(postgres.includes('ORDER BY first_occurred_at DESC, alarm_id DESC'), 'Alarm Postgres ordering is not first-occurrence DESC / alarmId DESC');
assert(postgres.includes('ON CONFLICT (tenant_id, site_id, fingerprint) WHERE condition = \'ACTIVE\' DO NOTHING'), 'S13 concurrent first-create authority is missing');
assert(postgres.includes('func (store *PostgresStore) ClearActive') && postgres.includes('current.IncidentCorrelationID != recovery.IncidentCorrelationID'), 'S13 governed recovery persistence is not Incident-bound');
assert(postgres.includes('func (store *PostgresStore) ResolveScope'), 'Alarm Postgres ownership resolver is missing');

assert(readMigration.includes("current_setting('app.tenant_id'"), 'Alarm Postgres Tenant RLS is missing');
assert(!readMigration.includes("current_setting('app.organization_id'"), 'Alarm Postgres still depends on Organization RLS');
assert(lifecycleMigration.includes('FOR ALL TO s4_alarm_runtime') && lifecycleMigration.includes('WITH CHECK'), 'Alarm lifecycle Tenant RLS write policy is missing');
assert(orthogonalMigration.includes('alarm_current_pre_s13_backup') && orthogonalMigration.includes('alarm_idempotency_pre_s13_backup') && orthogonalMigration.includes('s13_alarm_migration_report') && orthogonalMigration.includes('s13_alarm_identity_map'), 'S13 backup/migration evidence is missing');
assert(orthogonalMigration.includes('duplicate active fingerprint groups'), 'S13 duplicate-active migration preflight is missing');
assert(orthogonalMigration.includes('alarm_current_migrator_all') && orthogonalMigration.includes('alarm_idempotency_migrator_all'), 'S13 FORCE-RLS migrator policies are missing');
assert(orthogonalRollback.includes('runtime dual-model operation is not supported') && orthogonalRollback.includes('alarm_current_pre_s13_backup') && orthogonalRollback.includes('alarm_idempotency_pre_s13_backup'), 'S13 offline rollback evidence is incomplete');
assert(orthogonalMigration.includes('alarm_current_one_active_fingerprint_uidx') && orthogonalMigration.includes("WHERE condition = 'ACTIVE'"), 'S13 one-active fingerprint uniqueness is missing');
assert(orthogonalMigration.includes('alarm_timeline_immutable') && orthogonalMigration.includes('alarm timeline is append-only'), 'S13 immutable timeline enforcement is missing');
for (const retired of ['DROP COLUMN status', 'DROP COLUMN severity', 'DROP COLUMN transitions', 'DROP COLUMN suppressed_until']) {
  assert(orthogonalMigration.includes(retired), `S13 did not remove retired runtime column: ${retired}`);
}

assert(gatewayAlarm.includes('path == "/api/v1/alarms"') && gatewayAlarm.includes('"/api/v1/alarms/{alarmId}/ack"'), 'Gateway public Alarm route matching is incomplete');
assert(gatewayAlarm.includes('resolveAlarmScope') && gatewayAlarm.includes('checkAlarmSiteVisibility'), 'Gateway BOLA-safe Alarm resolution chain is missing');
assert(gatewayAlarm.includes('registryauth.ActionSiteRead'), 'Gateway does not verify authoritative Site visibility');
assert(gatewayAlarm.includes('alarmauth.ActionAck') && gatewayAlarm.includes('executeAlarmOperation'), 'Gateway ACK execution path is missing');
assert(gatewayAlarm.includes('writeV212Error') || gatewayAlarm.includes('writeAlarmFailure'), 'Gateway Alarm error envelope boundary is missing');
assert(!gatewayAlarm.includes('/telemetry/'), 'Gateway derives Alarm state from Telemetry HTTP');

console.log('S4 Alarm baseline valid: publicRoutes=3; readAction=alarm:read; ackAction=alarm:ack; runtimeContractOnly=10');
