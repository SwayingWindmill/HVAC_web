BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

-- The interval of the latest cooling load or temperature difference. Old rows stay NULL.
ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  ADD COLUMN IF NOT EXISTS measurement jsonb;
ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  DROP CONSTRAINT IF EXISTS latest_accepted_measurement_check;
ALTER TABLE telemetry_runtime.latest_accepted_telemetry
  ADD CONSTRAINT latest_accepted_measurement_check
  CHECK (measurement IS NULL OR (jsonb_typeof(measurement) = 'object' AND octet_length(measurement::text) <= 2048));

-- Configured supply/return pairs. The site file is the editor; onboarding upserts this table.
CREATE TABLE IF NOT EXISTS telemetry_runtime.probe_pairs (
  site_id uuid NOT NULL,
  pair_code text NOT NULL,
  config jsonb NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (site_id, pair_code),
  CONSTRAINT probe_pairs_code_check CHECK (pair_code ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
  CONSTRAINT probe_pairs_config_check CHECK (jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 8192)
);

-- The idle window that is still open. A finished window is deleted and written as an estimate.
CREATE TABLE IF NOT EXISTS telemetry_runtime.probe_pair_windows (
  site_id uuid NOT NULL,
  pair_code text NOT NULL,
  state jsonb NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (site_id, pair_code),
  CONSTRAINT probe_pair_windows_state_check CHECK (jsonb_typeof(state) = 'object' AND octet_length(state::text) <= 4096)
);

-- Suggested supply-versus-return offsets. applied stays false: nothing writes a correction back.
CREATE TABLE IF NOT EXISTS telemetry_runtime.probe_offset_estimates (
  site_id uuid NOT NULL,
  pair_code text NOT NULL,
  window_started_at timestamptz NOT NULL,
  window_ended_at timestamptz NOT NULL,
  sample_count integer NOT NULL,
  offset_k double precision NOT NULL,
  stddev_k double precision NOT NULL,
  supply_mean_c double precision NOT NULL,
  return_mean_c double precision NOT NULL,
  applied boolean NOT NULL DEFAULT false,
  alarm text,
  supply_device_id uuid NOT NULL,
  supply_key text NOT NULL,
  return_device_id uuid NOT NULL,
  return_key text NOT NULL,
  PRIMARY KEY (site_id, pair_code, window_started_at),
  CONSTRAINT probe_offset_estimates_count_check CHECK (sample_count >= 2),
  CONSTRAINT probe_offset_estimates_alarm_check CHECK (alarm IS NULL OR alarm IN ('OFFSET', 'DRIFT', 'OFFSET_AND_DRIFT')),
  CONSTRAINT probe_offset_estimates_applied_check CHECK (applied = false)
);

CREATE INDEX IF NOT EXISTS probe_offset_estimates_point_idx
  ON telemetry_runtime.probe_offset_estimates (supply_device_id, supply_key, window_ended_at DESC);
CREATE INDEX IF NOT EXISTS probe_offset_estimates_return_idx
  ON telemetry_runtime.probe_offset_estimates (return_device_id, return_key, window_ended_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_runtime.probe_pairs TO s2_telemetry_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_runtime.probe_pair_windows TO s2_telemetry_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_runtime.probe_offset_estimates TO s2_telemetry_runtime;

-- Every telemetry_runtime table forces row security. The policies are the same open
-- grant the other runtime tables use: the migrator writes the site file, the runtime
-- reads and folds windows.
ALTER TABLE telemetry_runtime.probe_pairs ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.probe_pairs FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.probe_pair_windows ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.probe_pair_windows FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.probe_offset_estimates ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.probe_offset_estimates FORCE ROW LEVEL SECURITY;
CREATE POLICY probe_pairs_migrator_all ON telemetry_runtime.probe_pairs FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY probe_pairs_runtime_all ON telemetry_runtime.probe_pairs FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY probe_pair_windows_migrator_all ON telemetry_runtime.probe_pair_windows FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY probe_pair_windows_runtime_all ON telemetry_runtime.probe_pair_windows FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY probe_offset_estimates_migrator_all ON telemetry_runtime.probe_offset_estimates FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY probe_offset_estimates_runtime_all ON telemetry_runtime.probe_offset_estimates FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);

RESET ROLE;
COMMIT;
