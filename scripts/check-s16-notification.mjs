import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const [model, postgres, scheduler, http, reminders, alarmOutbox, alarmEvaluator, alarmMigration] = await Promise.all([
  read('services/notification-service/pkg/notificationservice/model.go'),
  read('services/notification-service/pkg/notificationservice/postgres.go'),
  read('services/notification-service/pkg/notificationservice/scheduler.go'),
  read('services/notification-service/pkg/notificationservice/http.go'),
  read('services/notification-service/pkg/notificationservice/wechat_alarm_reminders.go'),
  read('modules/alarm/pkg/alarmservice/notification_outbox.go'),
  read('modules/alarm/pkg/alarmservice/evaluator_postgres.go'),
  read('modules/alarm/migrations/007_s16_notification_outbox.sql'),
]);

for (const action of ['CREATED', 'SEVERITY_CHANGED', 'ACKNOWLEDGED', 'CLEARED']) {
  assert(model.includes(`AlarmAction = "${action}"`), `Notification Alarm action ${action} is missing`);
}
assert(reminders.includes('ON CONFLICT (tenant_id, alarm_id) DO NOTHING'), 'an Alarm seen again must not be reminded twice');
assert(scheduler.includes('s16_notification_scheduler'), 'the WeChat reminder worker must run as the bounded scheduler role');
assert(![model, postgres, scheduler, http, reminders].some((source) => source.includes('alarm_runtime')), 'Notification owner directly reads the Alarm database');
assert(alarmOutbox.includes('s4_alarm_notification_relay') && alarmMigration.includes('GRANT SELECT, UPDATE ON alarm_runtime.notification_outbox TO s4_alarm_notification_relay'), 'Alarm notification relay is not least-privilege outbox-only');
assert(alarmEvaluator.includes('NotificationCreated'), 'Alarm owner does not tell Notification about a new Alarm');
assert(http.includes('principal:') && http.includes('sameStringSet(claims.Scopes, expectedScopes)'), 'Notification owner context is not exactly principal scoped');

console.log('Notification baseline valid: new Alarms through the Alarm outbox, one WeChat reminder per Alarm, principal-scoped reminder context');
