import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const [model, postgres, scheduler, http, migration, alarmOutbox, alarmPostgres, alarmEvaluator, alarmMigration, gateway, gatewayServer, ownership, openapi] = await Promise.all([
  read('services/notification-service/pkg/notificationservice/model.go'),
  read('services/notification-service/pkg/notificationservice/postgres.go'),
  read('services/notification-service/pkg/notificationservice/scheduler.go'),
  read('services/notification-service/pkg/notificationservice/http.go'),
  read('services/notification-service/migrations/001_s16_notification.sql'),
  read('modules/alarm/pkg/alarmservice/notification_outbox.go'),
  read('modules/alarm/pkg/alarmservice/postgres.go'),
  read('modules/alarm/pkg/alarmservice/evaluator_postgres.go'),
  read('modules/alarm/migrations/007_s16_notification_outbox.sql'),
  read('cmd/energy-api/internal/gateway/notification.go'),
  read('cmd/energy-api/internal/gateway/server.go'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/http/platform-gateway.openapi.yaml'),
]);

for (const action of ['CREATED', 'SEVERITY_CHANGED', 'ACKNOWLEDGED', 'CLEARED']) {
  assert(model.includes(`= "${action}"`) || model.includes(`AlarmAction = "${action}"`), `S16 Alarm action ${action} is missing`);
}
assert(model.includes('TemplateDigest') && model.includes('AudienceDigest') && model.includes('PolicyDigest') && model.includes('sha256.Sum256'), 'S16 released artifacts are not SHA-256 content bound');
assert(model.includes('MandatorySafety') && postgres.includes('if mandatory {'), 'mandatory safety notification can be suppressed by ordinary preference');
assert(postgres.includes('ON CONFLICT (tenant_id,source_event_id) DO NOTHING'), 'S16 source event replay is not idempotent');
assert(migration.includes('UNIQUE (tenant_id, source_event_id, assignment_id, assignment_revision, stage)'), 'S16 does not enforce one Intent per source/rule/stage');
assert(postgres.includes("status='CANCELLED'") && !postgres.includes('DELETE FROM notification_runtime.notification_intent'), 'S16 escalation cancellation must retain durable CANCELLED evidence');
assert(postgres.includes("status='EXTERNAL_SUBMITTED'") && postgres.indexOf("status='EXTERNAL_SUBMITTED'") < postgres.indexOf('port.SubmitNotification'), 'S16 does not commit external handoff before S15 effect');
assert(postgres.includes('IdempotencyKey: intent.IntentID') && postgres.includes('SourceAggregateType: "NOTIFICATION_INTENT"'), 'S16 external recovery does not use the frozen Notification Intent identity');
assert(postgres.includes('RecordExternalDisposition') && postgres.includes('IntentOutcomeUnknown') && postgres.includes('DispositionUnknown'), 'S16 does not preserve S15 outcome-unknown semantics');
assert(scheduler.includes('FOR UPDATE SKIP LOCKED') && scheduler.includes('s16_notification_scheduler'), 'S16 durable cross-Tenant stage scheduler is missing');
assert(migration.includes('FORCE ROW LEVEL SECURITY') && migration.includes('notification_intent_scheduler'), 'S16 Tenant RLS or bounded scheduler RLS is missing');
assert(migration.includes('enforce_notification_intent_frozen_snapshot') && migration.includes('GRANT UPDATE (status, lease_owner, lease_until, lease_fence, updated_at)'), 'S16 frozen Intent snapshot or scheduler column-level privilege is missing');
assert(!model.includes('alarm_runtime') && !postgres.includes('alarm_runtime') && !scheduler.includes('alarm_runtime') && !http.includes('alarm_runtime'), 'Notification owner directly reads the Alarm database');
assert(alarmOutbox.includes('s4_alarm_notification_relay') && alarmMigration.includes('GRANT SELECT, UPDATE ON alarm_runtime.notification_outbox TO s4_alarm_notification_relay'), 'Alarm notification relay is not least-privilege outbox-only');
assert(alarmEvaluator.includes('NotificationCreated') && alarmEvaluator.includes('NotificationSeverityChanged') && alarmEvaluator.includes('NotificationCleared'), 'Alarm owner does not emit S16 evaluator lifecycle events');
assert(alarmPostgres.includes('NotificationAcknowledged'), 'Alarm owner does not emit first ACK to S16 outbox');
assert(http.includes('principal:') && http.includes('notification:') && http.includes('NotificationMarkReadAction'), 'S16 Inbox owner context is not exact principal/item scoped');
assert(gateway.includes('validateStateChange') && gateway.includes('X-CSRF-Token') && gateway.includes('session.Principal.Subject'), 'Gateway Notification mark-read is not Session/Origin/CSRF bound');
assert(gatewayServer.includes('OwnerNotification'), 'Gateway Notification route bypasses Route Ownership Registry');
for (const [method, path] of [['GET', '/api/v1/notifications/inbox'], ['POST', '/api/v1/notifications/inbox/{notificationId}/read']]) {
  const route = ownership.routes.find((entry) => entry.method === method && entry.path === path);
  assert(route?.owner === 'notification-service' && route?.publicIngress === 'platform-gateway' && route?.rollout?.mode === 'all' && route?.compatibilityMode === 'native', `S16 route ownership missing for ${method} ${path}`);
}
assert(openapi.paths?.['/api/v1/notifications/inbox']?.get?.['x-owner'] === 'notification-service', 'S16 Inbox OpenAPI owner is missing');
assert(openapi.paths?.['/api/v1/notifications/inbox/{notificationId}/read']?.post?.['x-owner'] === 'notification-service', 'S16 mark-read OpenAPI owner is missing');
assert(openapi.components?.schemas?.NotificationInboxItem, 'S16 public Inbox schema is missing');

console.log('S16 Notification baseline valid: durable Alarm outbox, frozen stage intents, IN_APP Inbox, S15 external owner, mandatory safety, principal-scoped read state');
