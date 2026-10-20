BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- Per reading key, when the reading took the value it still has, so a rule can tell a reading
-- that has not moved for hours (#616). Rows written before this start empty and fill in with
-- the next snapshot of each device.
ALTER TABLE alarm_runtime.telemetry_evaluation_input
  ADD COLUMN unchanged_since jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(unchanged_since) = 'object');

COMMIT;
