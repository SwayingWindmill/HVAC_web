BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

-- Per-point quality checks (range limits already live on this table) and the
-- health of the latest sample. Old rows keep NULL checks and NULL health;
-- the runtime derives health from the sample's quality until a new sample arrives.
ALTER TABLE telemetry_runtime.freshness_policies
  ADD COLUMN IF NOT EXISTS quality_checks jsonb;
ALTER TABLE telemetry_runtime.freshness_policies
  DROP CONSTRAINT IF EXISTS freshness_policies_quality_checks_check;
ALTER TABLE telemetry_runtime.freshness_policies
  ADD CONSTRAINT freshness_policies_quality_checks_check
  CHECK (quality_checks IS NULL OR (jsonb_typeof(quality_checks) = 'object' AND octet_length(quality_checks::text) <= 4096));

ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  ADD COLUMN IF NOT EXISTS unchanged_since timestamptz,
  ADD COLUMN IF NOT EXISTS health_status text,
  ADD COLUMN IF NOT EXISTS health_score smallint,
  ADD COLUMN IF NOT EXISTS health_reasons text[] NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS health_at timestamptz;
ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  DROP CONSTRAINT IF EXISTS latest_accepted_health_status_check;
ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  ADD CONSTRAINT latest_accepted_health_status_check
  CHECK (health_status IS NULL OR health_status IN ('HEALTHY', 'SUSPECT', 'BAD', 'STALE'));
ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  DROP CONSTRAINT IF EXISTS latest_accepted_health_score_check;
ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  ADD CONSTRAINT latest_accepted_health_score_check
  CHECK (health_score IS NULL OR health_score BETWEEN 0 AND 100);

RESET ROLE;
COMMIT;
