BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

-- Runtime retention (telemetry-worker) prunes history events an hour after ClickHouse has them, then
-- received observations a week old whose event is gone. The runtime reads the outbox to see whether an
-- observation's event is still there and deletes only delivered events.
DROP POLICY IF EXISTS telemetry_history_outbox_runtime_select ON telemetry_runtime.telemetry_history_outbox;
CREATE POLICY telemetry_history_outbox_runtime_select
  ON telemetry_runtime.telemetry_history_outbox FOR SELECT TO s2_telemetry_runtime
  USING (true);
DROP POLICY IF EXISTS telemetry_history_outbox_runtime_delete ON telemetry_runtime.telemetry_history_outbox;
CREATE POLICY telemetry_history_outbox_runtime_delete
  ON telemetry_runtime.telemetry_history_outbox FOR DELETE TO s2_telemetry_runtime
  USING (delivery_state = 'PUBLISHED');
GRANT SELECT (event_id, delivery_state, published_at), DELETE ON telemetry_runtime.telemetry_history_outbox TO s2_telemetry_runtime;

CREATE INDEX IF NOT EXISTS telemetry_history_outbox_published_idx
  ON telemetry_runtime.telemetry_history_outbox (published_at)
  WHERE delivery_state = 'PUBLISHED';
CREATE INDEX IF NOT EXISTS source_observations_created_idx
  ON telemetry_runtime.source_observations (created_at);

RESET ROLE;
COMMIT;
