\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

CREATE INDEX IF NOT EXISTS source_observations_accepted_point_sample_idx
  ON telemetry_runtime.source_observations (point_id, sampled_at, observation_id)
  WHERE point_id IS NOT NULL AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER');

RESET ROLE;
COMMIT;
