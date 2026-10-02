BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.source_observations
  ADD COLUMN tenant_id uuid CHECK (tenant_id IS NULL OR telemetry_runtime.is_uuid_v7(tenant_id));
ALTER TABLE telemetry_runtime.source_observations
  ADD CONSTRAINT source_observations_mapped_tenant_check
  CHECK (device_id IS NULL OR tenant_id IS NOT NULL);
ALTER TABLE telemetry_runtime.ingest_quarantine
  ADD COLUMN tenant_id uuid CHECK (tenant_id IS NULL OR telemetry_runtime.is_uuid_v7(tenant_id));
ALTER TABLE telemetry_runtime.ingest_quarantine
  ADD CONSTRAINT ingest_quarantine_mapped_tenant_check
  CHECK (device_id IS NULL OR tenant_id IS NOT NULL);
ALTER TABLE telemetry_runtime.source_delivery_evidence
  ADD COLUMN tenant_id uuid CHECK (tenant_id IS NULL OR telemetry_runtime.is_uuid_v7(tenant_id));

CREATE UNIQUE INDEX devices_tenant_device_uidx
  ON telemetry_runtime.devices (tenant_id, device_id);

CREATE INDEX source_observations_tenant_device_key_time_idx
  ON telemetry_runtime.source_observations
  (tenant_id, device_id, telemetry_key, sampled_at DESC, observation_id)
  WHERE tenant_id IS NOT NULL AND device_id IS NOT NULL;

CREATE INDEX ingest_quarantine_tenant_open_idx
  ON telemetry_runtime.ingest_quarantine
  (tenant_id, source_id, external_entity_type, external_id, detected_at)
  WHERE resolved_at IS NULL;

RESET ROLE;
COMMIT;
