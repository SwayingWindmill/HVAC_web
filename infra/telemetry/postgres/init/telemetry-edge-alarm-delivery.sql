\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.mqtt_runtime_events
  ADD COLUMN IF NOT EXISTS alarm_delivery_state text,
  ADD COLUMN IF NOT EXISTS alarm_available_at timestamptz,
  ADD COLUMN IF NOT EXISTS alarm_attempts integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS alarm_last_error_code text,
  ADD COLUMN IF NOT EXISTS alarm_published_at timestamptz,
  ADD COLUMN IF NOT EXISTS alarm_dead_lettered_at timestamptz,
  ADD COLUMN IF NOT EXISTS alarm_claim_owner text,
  ADD COLUMN IF NOT EXISTS alarm_claim_until timestamptz;

UPDATE telemetry_runtime.mqtt_runtime_events
SET alarm_delivery_state = 'PENDING',
    alarm_available_at = received_at
WHERE event_type IN (
  'EDGE_RULE_ALARM_RAISED',
  'EDGE_RULE_ALARM_CLEARED',
  'EDGE_RULE_EDGE_RULE_ALARM_RAISED',
  'EDGE_RULE_EDGE_RULE_ALARM_CLEARED'
)
  AND alarm_delivery_state IS NULL;

ALTER TABLE telemetry_runtime.mqtt_runtime_events
  DROP CONSTRAINT IF EXISTS mqtt_runtime_events_alarm_delivery_state_check;
ALTER TABLE telemetry_runtime.mqtt_runtime_events
  ADD CONSTRAINT mqtt_runtime_events_alarm_delivery_state_check
  CHECK (alarm_delivery_state IS NULL OR alarm_delivery_state IN ('PENDING', 'PUBLISHED', 'DEAD_LETTER'));

ALTER TABLE telemetry_runtime.mqtt_runtime_events
  DROP CONSTRAINT IF EXISTS mqtt_runtime_events_alarm_attempts_check;
ALTER TABLE telemetry_runtime.mqtt_runtime_events
  ADD CONSTRAINT mqtt_runtime_events_alarm_attempts_check
  CHECK (alarm_attempts >= 0);

ALTER TABLE telemetry_runtime.mqtt_runtime_events
  DROP CONSTRAINT IF EXISTS mqtt_runtime_events_alarm_available_check;
ALTER TABLE telemetry_runtime.mqtt_runtime_events
  ADD CONSTRAINT mqtt_runtime_events_alarm_available_check
  CHECK (alarm_delivery_state IS NULL OR alarm_available_at IS NOT NULL);

ALTER TABLE telemetry_runtime.mqtt_runtime_events
  DROP CONSTRAINT IF EXISTS mqtt_runtime_events_alarm_claim_check;
ALTER TABLE telemetry_runtime.mqtt_runtime_events
  ADD CONSTRAINT mqtt_runtime_events_alarm_claim_check
  CHECK (
    (alarm_claim_owner IS NULL AND alarm_claim_until IS NULL)
    OR (alarm_claim_owner IS NOT NULL AND alarm_claim_until IS NOT NULL)
  );

ALTER TABLE telemetry_runtime.mqtt_runtime_events
  DROP CONSTRAINT IF EXISTS mqtt_runtime_events_alarm_published_check;
ALTER TABLE telemetry_runtime.mqtt_runtime_events
  ADD CONSTRAINT mqtt_runtime_events_alarm_published_check
  CHECK (
    alarm_delivery_state IS NULL
    OR (alarm_delivery_state = 'PENDING' AND alarm_published_at IS NULL AND alarm_dead_lettered_at IS NULL)
    OR (alarm_delivery_state = 'PUBLISHED' AND alarm_published_at IS NOT NULL AND alarm_dead_lettered_at IS NULL)
    OR (alarm_delivery_state = 'DEAD_LETTER' AND alarm_published_at IS NULL AND alarm_dead_lettered_at IS NOT NULL)
  );

CREATE INDEX IF NOT EXISTS mqtt_runtime_events_alarm_pending_idx
  ON telemetry_runtime.mqtt_runtime_events (alarm_available_at, alarm_claim_until, received_at, message_id)
  WHERE alarm_delivery_state = 'PENDING';

CREATE INDEX IF NOT EXISTS mqtt_runtime_events_alarm_dead_letter_idx
  ON telemetry_runtime.mqtt_runtime_events (alarm_dead_lettered_at DESC, message_id)
  WHERE alarm_delivery_state = 'DEAD_LETTER';

GRANT UPDATE (
  alarm_delivery_state,
  alarm_available_at,
  alarm_attempts,
  alarm_last_error_code,
  alarm_published_at,
  alarm_dead_lettered_at,
  alarm_claim_owner,
  alarm_claim_until
) ON telemetry_runtime.mqtt_runtime_events TO s2_telemetry_runtime;

COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_delivery_state IS '边缘规则告警事件向告警中心异步投递的状态：非告警事件为 NULL，告警事件为 PENDING、PUBLISHED 或 DEAD_LETTER。';
COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_available_at IS '下一次允许中继器抢占投递该边缘告警事件的 UTC 时间戳。';
COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_attempts IS '向告警中心投递该边缘告警事件的累计尝试次数。';
COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_last_error_code IS '最近一次投递失败记录的脱敏错误分类码。';
COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_published_at IS '成功投递至告警中心完成投影的 UTC 时间戳。';
COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_dead_lettered_at IS '因永久失败或超过最大重试次数而转入死信状态的 UTC 时间戳。';
COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_claim_owner IS '当前持有该边缘告警事件投递租约的工作实例标识。';
COMMENT ON COLUMN telemetry_runtime.mqtt_runtime_events.alarm_claim_until IS '当前投递租约到期的 UTC 时间戳。';

RESET ROLE;

COMMIT;
