BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- Undoes 015. Active "rules-unevaluable" Alarms stay as they are; recover them by hand
-- (docs/operations/alarm-delivery.md) before or after rolling back.
DROP FUNCTION IF EXISTS alarm_runtime.tenants_with_unevaluable_rules(timestamptz);
DROP TRIGGER IF EXISTS alarm_evaluation_state_indeterminate_since ON alarm_runtime.alarm_evaluation_state;
DROP FUNCTION IF EXISTS alarm_runtime.track_indeterminate_since();
DROP INDEX IF EXISTS alarm_runtime.alarm_evaluation_state_indeterminate_idx;
ALTER TABLE alarm_runtime.alarm_evaluation_state DROP COLUMN IF EXISTS indeterminate_since;

COMMIT;
