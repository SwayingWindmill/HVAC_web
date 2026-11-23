\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

CREATE INDEX IF NOT EXISTS source_observations_source_accepted_received_idx
  ON telemetry_runtime.source_observations (source_id, received_at DESC)
  WHERE acceptance_status = 'ACCEPTED';

CREATE INDEX IF NOT EXISTS mqtt_runtime_events_gateway_received_idx
  ON telemetry_runtime.mqtt_runtime_events (gateway_id, received_at DESC, message_id);

RESET ROLE;
COMMIT;
