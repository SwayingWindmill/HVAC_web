-- The notification inbox, rules (policies), audiences and templates are gone (ADR 0026): no rule
-- was ever set up on the server, so the inbox stayed empty. Notification only sends WeChat
-- reminders now (002, 003), so the tables 001 made for the rest go, children first.
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s16_notification_migrator;

DROP TABLE notification_runtime.inbox_item;
DROP TABLE notification_runtime.intent_event;
DROP TABLE notification_runtime.notification_intent;
DROP TABLE notification_runtime.policy_assignment;
DROP TABLE notification_runtime.policy_revision;
DROP TABLE notification_runtime.audience_revision;
DROP TABLE notification_runtime.template_revision;
DROP TABLE notification_runtime.source_alarm_event;
DROP TABLE notification_runtime.user_preference;
DROP FUNCTION notification_runtime.reject_notification_history_mutation();
DROP FUNCTION notification_runtime.enforce_notification_intent_frozen_snapshot();

RESET ROLE;
COMMIT;
