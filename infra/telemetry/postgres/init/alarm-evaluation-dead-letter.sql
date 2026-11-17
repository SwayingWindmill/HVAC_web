\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD COLUMN IF NOT EXISTS alarm_dead_lettered_at timestamptz;

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_alarm_delivery_state_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_alarm_delivery_state_check
  CHECK (alarm_delivery_state IS NULL OR alarm_delivery_state IN ('PENDING', 'PUBLISHED', 'DEAD_LETTER'));

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_alarm_published_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_alarm_published_check
  CHECK (
    alarm_delivery_state IS NULL
    OR (alarm_delivery_state = 'PUBLISHED' AND alarm_published_at IS NOT NULL AND alarm_dead_lettered_at IS NULL)
    OR (alarm_delivery_state = 'PENDING' AND alarm_published_at IS NULL AND alarm_dead_lettered_at IS NULL)
    OR (alarm_delivery_state = 'DEAD_LETTER' AND alarm_published_at IS NULL AND alarm_dead_lettered_at IS NOT NULL)
  );

CREATE INDEX IF NOT EXISTS telemetry_publication_outbox_alarm_dead_letter_idx
  ON telemetry_runtime.telemetry_publication_outbox (alarm_dead_lettered_at DESC, event_id)
  WHERE subscription_id IS NULL AND alarm_delivery_state = 'DEAD_LETTER';

GRANT UPDATE (alarm_dead_lettered_at)
  ON telemetry_runtime.telemetry_publication_outbox TO s2_telemetry_relay;

COMMENT ON COLUMN telemetry_runtime.telemetry_publication_outbox.alarm_dead_lettered_at IS '告警评估投递因永久失败或重试次数耗尽而转入死信状态的时间。';

RESET ROLE;
COMMIT;
