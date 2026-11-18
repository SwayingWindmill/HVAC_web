\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.telemetry_history_outbox
  ADD COLUMN IF NOT EXISTS dead_lettered_at timestamptz;

UPDATE telemetry_runtime.telemetry_history_outbox
SET dead_lettered_at = COALESCE(dead_lettered_at, available_at, created_at),
    lease_id = NULL,
    leased_until = NULL,
    published_at = NULL
WHERE delivery_state = 'DEAD'
  AND (dead_lettered_at IS NULL OR lease_id IS NOT NULL OR leased_until IS NOT NULL OR published_at IS NOT NULL);

ALTER TABLE telemetry_runtime.telemetry_history_outbox
  DROP CONSTRAINT IF EXISTS telemetry_history_outbox_dead_letter_check;
ALTER TABLE telemetry_runtime.telemetry_history_outbox
  ADD CONSTRAINT telemetry_history_outbox_dead_letter_check
  CHECK (
    (delivery_state = 'DEAD' AND dead_lettered_at IS NOT NULL AND published_at IS NULL AND lease_id IS NULL AND leased_until IS NULL)
    OR (delivery_state <> 'DEAD' AND dead_lettered_at IS NULL)
  );

CREATE INDEX IF NOT EXISTS telemetry_history_outbox_dead_letter_idx
  ON telemetry_runtime.telemetry_history_outbox (dead_lettered_at DESC, event_id)
  WHERE delivery_state = 'DEAD';

GRANT UPDATE (dead_lettered_at)
  ON telemetry_runtime.telemetry_history_outbox TO s2_telemetry_history;

DROP POLICY IF EXISTS telemetry_history_outbox_runtime_delete ON telemetry_runtime.telemetry_history_outbox;
CREATE POLICY telemetry_history_outbox_runtime_delete
  ON telemetry_runtime.telemetry_history_outbox FOR DELETE TO s2_telemetry_runtime
  USING (delivery_state IN ('PUBLISHED', 'DEAD'));

GRANT SELECT (event_id, delivery_state, published_at, dead_lettered_at), DELETE
  ON telemetry_runtime.telemetry_history_outbox TO s2_telemetry_runtime;

GRANT SELECT, DELETE ON telemetry_runtime.mqtt_runtime_events TO s2_telemetry_runtime;
GRANT SELECT, DELETE ON telemetry_runtime.ingest_quarantine, telemetry_runtime.recovery_cursors, telemetry_runtime.telemetry_subscriptions TO s2_telemetry_runtime;

COMMENT ON COLUMN telemetry_runtime.telemetry_history_outbox.dead_lettered_at IS '历史遥测发件箱记录因毒丸解析失败或重试耗尽转入死信状态的时间（UTC）。';

RESET ROLE;
COMMIT;
