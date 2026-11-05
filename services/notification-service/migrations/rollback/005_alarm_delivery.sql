-- Undo 005_alarm_delivery.sql. Only what Notification knew of Alarms for delivery and its message
-- log go; Alarms themselves live with the Alarm owner. Stop the dispatcher first
-- (ALARM_DELIVERY_MODE=off).
\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s16_notification_migrator;
DROP TABLE notification_runtime.alarm_delivery_attempt;
DROP TABLE notification_runtime.alarm_delivery;
RESET ROLE;
COMMIT;
