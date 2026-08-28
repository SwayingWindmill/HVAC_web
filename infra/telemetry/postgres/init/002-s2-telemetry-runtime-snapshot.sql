BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.registry_device_bindings
  ADD COLUMN IF NOT EXISTS presence_applicability text NOT NULL DEFAULT 'APPLICABLE'
  CHECK (presence_applicability IN ('APPLICABLE', 'NOT_APPLICABLE'));

ALTER TABLE telemetry_runtime.presence_policies
  ADD COLUMN IF NOT EXISTS accepted_signal_types text[] NOT NULL
  DEFAULT ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT']::text[];
ALTER TABLE telemetry_runtime.presence_policies
  ADD CONSTRAINT presence_policies_accepted_signal_types_check
  CHECK (
    cardinality(accepted_signal_types) BETWEEN 1 AND 3
    AND accepted_signal_types <@ ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT']::text[]
  ) NOT VALID;
ALTER TABLE telemetry_runtime.presence_policies
  VALIDATE CONSTRAINT presence_policies_accepted_signal_types_check;

ALTER TABLE telemetry_runtime.device_observation_snapshots
  ADD COLUMN IF NOT EXISTS state_sha256 text;
UPDATE telemetry_runtime.device_observation_snapshots
SET state_sha256 = snapshot_sha256
WHERE state_sha256 IS NULL;
ALTER TABLE telemetry_runtime.device_observation_snapshots
  ALTER COLUMN state_sha256 SET NOT NULL;
ALTER TABLE telemetry_runtime.device_observation_snapshots
  ADD CONSTRAINT device_observation_snapshots_state_sha256_check
  CHECK (state_sha256 ~ '^[a-f0-9]{64}$') NOT VALID;
ALTER TABLE telemetry_runtime.device_observation_snapshots
  VALIDATE CONSTRAINT device_observation_snapshots_state_sha256_check;

CREATE TABLE IF NOT EXISTS telemetry_runtime.presence_signals (
  signal_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(signal_id)),
  device_id uuid NOT NULL REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  signal_type text NOT NULL CHECK (signal_type IN ('SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT')),
  observed_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL,
  accepted boolean NOT NULL,
  policy_revision bigint NOT NULL CHECK (policy_revision >= 1),
  source_event_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(source_event_id)),
  created_at timestamptz NOT NULL,
  UNIQUE (device_id, source_event_id),
  CHECK (received_at >= observed_at - interval '24 hours')
);
CREATE INDEX IF NOT EXISTS presence_signals_device_observed_idx
  ON telemetry_runtime.presence_signals (device_id, observed_at DESC, signal_id)
  WHERE accepted;

CREATE TABLE IF NOT EXISTS telemetry_runtime.observation_coverage (
  device_id uuid PRIMARY KEY REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  available boolean NOT NULL,
  continuous_since timestamptz,
  reason_code text CHECK (reason_code IS NULL OR reason_code IN ('SOURCE_UNAVAILABLE', 'OBSERVATION_COVERAGE_GAP', 'POLICY_UNAVAILABLE', 'OWNER_DEPENDENCY_UNAVAILABLE')),
  source_revision bigint NOT NULL CHECK (source_revision >= 1),
  updated_at timestamptz NOT NULL,
  CHECK ((available AND continuous_since IS NOT NULL AND reason_code IS NULL)
      OR (NOT available AND reason_code IS NOT NULL))
);

ALTER TABLE telemetry_runtime.presence_signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.presence_signals FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.observation_coverage ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.observation_coverage FORCE ROW LEVEL SECURITY;

CREATE POLICY presence_signals_migrator_all ON telemetry_runtime.presence_signals FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY presence_signals_runtime_all ON telemetry_runtime.presence_signals FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY observation_coverage_migrator_all ON telemetry_runtime.observation_coverage FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY observation_coverage_runtime_all ON telemetry_runtime.observation_coverage FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);

GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_runtime.presence_signals TO s2_telemetry_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_runtime.observation_coverage TO s2_telemetry_runtime;
REVOKE ALL ON telemetry_runtime.presence_signals, telemetry_runtime.observation_coverage FROM PUBLIC;

RESET ROLE;
COMMIT;
