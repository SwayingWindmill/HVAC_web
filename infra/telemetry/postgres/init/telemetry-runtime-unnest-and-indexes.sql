\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.latest_accepted_telemetry SET (fillfactor = 80);
ALTER TABLE telemetry_runtime.device_observation_snapshots SET (fillfactor = 80);
ALTER TABLE telemetry_runtime.device_presence SET (fillfactor = 80);
ALTER TABLE telemetry_runtime.source_positions SET (fillfactor = 80);

CREATE INDEX IF NOT EXISTS source_observations_rejected_idx
  ON telemetry_runtime.source_observations (device_id, sampled_at DESC)
  WHERE acceptance_status = 'REJECTED';

RESET ROLE;
COMMIT;
