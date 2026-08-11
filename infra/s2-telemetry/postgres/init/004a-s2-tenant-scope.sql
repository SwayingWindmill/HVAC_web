BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.registry_device_bindings
  ADD COLUMN tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id));
ALTER TABLE telemetry_runtime.iam_scope_projections
  ADD COLUMN tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id));
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

DROP INDEX IF EXISTS telemetry_runtime.registry_device_bindings_tenant_idx;
CREATE INDEX registry_device_bindings_tenant_idx
  ON telemetry_runtime.registry_device_bindings (tenant_id, owning_organization_id, site_id, device_id);
CREATE UNIQUE INDEX registry_device_bindings_tenant_device_uidx
  ON telemetry_runtime.registry_device_bindings (tenant_id, device_id);

CREATE INDEX iam_scope_projections_tenant_lookup_idx
  ON telemetry_runtime.iam_scope_projections
  (tenant_id, principal_id, acting_organization_id, device_id, action, valid_until)
  WHERE revoked_at IS NULL;

CREATE INDEX source_observations_tenant_device_key_time_idx
  ON telemetry_runtime.source_observations
  (tenant_id, device_id, telemetry_key, sampled_at DESC, observation_id)
  WHERE tenant_id IS NOT NULL AND device_id IS NOT NULL;

CREATE INDEX ingest_quarantine_tenant_open_idx
  ON telemetry_runtime.ingest_quarantine
  (tenant_id, integration_instance_id, external_entity_type, external_id, detected_at)
  WHERE resolved_at IS NULL;

RESET ROLE;
COMMIT;
