BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

CREATE TABLE IF NOT EXISTS telemetry_runtime.registry_point_bindings (
  projection_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(projection_id)),
  tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(site_id)),
  point_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(point_id)),
  sensor_id uuid CHECK (sensor_id IS NULL OR telemetry_runtime.is_uuid_v7(sensor_id)),
  device_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(device_id)),
  telemetry_key text NOT NULL CHECK (telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  point_type text NOT NULL CHECK (point_type IN ('TELEMETRY','COUNTER','STATE','SETTING','COMMAND')),
  value_type text NOT NULL CHECK (value_type IN ('NUMBER','STRING','BOOLEAN','JSON')),
  unit text CHECK (unit IS NULL OR char_length(unit) BETWEEN 1 AND 64),
  binding_status text NOT NULL CHECK (binding_status IN ('ACTIVE','RETIRED','QUARANTINED')),
  point_revision bigint NOT NULL CHECK (point_revision > 0),
  source_registry_revision bigint NOT NULL CHECK (source_registry_revision > 0),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, device_id)
    REFERENCES telemetry_runtime.registry_device_bindings(tenant_id, device_id),
  CHECK (valid_to IS NULL OR valid_to > valid_from)
);

CREATE UNIQUE INDEX registry_point_bindings_current_key_uidx
  ON telemetry_runtime.registry_point_bindings (tenant_id, device_id, telemetry_key)
  WHERE binding_status = 'ACTIVE' AND valid_to IS NULL;
CREATE UNIQUE INDEX registry_point_bindings_current_point_uidx
  ON telemetry_runtime.registry_point_bindings (tenant_id, point_id)
  WHERE binding_status = 'ACTIVE' AND valid_to IS NULL;
CREATE INDEX registry_point_bindings_asof_idx
  ON telemetry_runtime.registry_point_bindings
  (tenant_id, device_id, telemetry_key, valid_from DESC, point_revision DESC);

ALTER TABLE telemetry_runtime.registry_point_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.registry_point_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY registry_point_bindings_migrator_all
  ON telemetry_runtime.registry_point_bindings FOR ALL TO s2_telemetry_migrator
  USING (true) WITH CHECK (true);
CREATE POLICY registry_point_bindings_runtime_all
  ON telemetry_runtime.registry_point_bindings FOR ALL TO s2_telemetry_runtime
  USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_runtime.registry_point_bindings TO s2_telemetry_runtime;
REVOKE ALL ON telemetry_runtime.registry_point_bindings FROM PUBLIC;

ALTER TABLE telemetry_runtime.source_observations
  ADD COLUMN point_id uuid CHECK (point_id IS NULL OR telemetry_runtime.is_uuid_v7(point_id));
ALTER TABLE telemetry_runtime.source_observations
  ADD COLUMN sensor_id uuid CHECK (sensor_id IS NULL OR telemetry_runtime.is_uuid_v7(sensor_id));
ALTER TABLE telemetry_runtime.source_observations
  ADD CONSTRAINT source_observations_accepted_point_identity_check
  CHECK (acceptance_status <> 'ACCEPTED' OR point_id IS NOT NULL);
ALTER TABLE telemetry_runtime.source_observations
  ADD CONSTRAINT source_observations_sensor_point_check
  CHECK (sensor_id IS NULL OR point_id IS NOT NULL);
CREATE INDEX source_observations_tenant_point_time_idx
  ON telemetry_runtime.source_observations
  (tenant_id, point_id, sampled_at DESC, observation_id)
  WHERE tenant_id IS NOT NULL AND point_id IS NOT NULL;

ALTER TABLE telemetry_runtime.ingest_quarantine
  DROP CONSTRAINT IF EXISTS ingest_quarantine_reason_code_check;
ALTER TABLE telemetry_runtime.ingest_quarantine
  ADD CONSTRAINT ingest_quarantine_reason_code_check
  CHECK (reason_code IN (
    'MAPPING_NOT_FOUND', 'MAPPING_CONFLICT', 'MAPPING_QUARANTINED', 'MAPPING_RETIRED',
    'POINT_MAPPING_NOT_FOUND', 'POINT_MAPPING_CONFLICT', 'POINT_MAPPING_QUARANTINED',
    'POLICY_NOT_CONFIGURED', 'SOURCE_UNTRUSTED', 'TYPE_MISMATCH', 'UNIT_MISMATCH',
    'OUT_OF_RANGE', 'CLOCK_AHEAD', 'CLOCK_BEHIND'
  ));

RESET ROLE;
COMMIT;
