\set ON_ERROR_STOP on

BEGIN;

GRANT USAGE ON SCHEMA iam, core_registry TO connectivity_migrator;
GRANT REFERENCES ON iam.tenants, core_registry.sites, core_registry.devices TO connectivity_migrator;

SET LOCAL ROLE connectivity_migrator;

CREATE OR REPLACE FUNCTION connectivity.is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
  SELECT substring(value::text FROM 15 FOR 1) = '7'
     AND substring(value::text FROM 20 FOR 1) IN ('8', '9', 'a', 'b')
$$;

REVOKE ALL ON FUNCTION connectivity.is_uuid_v7(uuid) FROM PUBLIC;

-- One row per command attempt Connectivity publishes to a Gateway. The row is armed
-- before the MQTT publish, so a crash after publishing is never mistaken for "not sent",
-- and the Gateway's reply on hvac/v1/{gatewayId}/up/reply completes it.
CREATE TABLE connectivity.command_reply_correlations (
  attempt_id uuid NOT NULL,
  execution_fence bigint NOT NULL CHECK (execution_fence > 0),
  command_id uuid NOT NULL,
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  device_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(device_id)),
  point_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(point_id)),
  capability text NOT NULL CHECK (length(btrim(capability)) BETWEEN 1 AND 128),
  external_device_id text NOT NULL CHECK (length(btrim(external_device_id)) BETWEEN 1 AND 256),
  payload_hash text NOT NULL CHECK (payload_hash ~ '^[a-f0-9]{64}$'),
  lease_owner text NOT NULL CHECK (length(btrim(lease_owner)) BETWEEN 1 AND 256),
  lease_until timestamptz NOT NULL,
  mapping_revision text NOT NULL CHECK (length(btrim(mapping_revision)) BETWEEN 1 AND 256),
  binding_revision text NOT NULL CHECK (length(btrim(binding_revision)) BETWEEN 1 AND 256),
  provider_endpoint text NOT NULL CHECK (length(btrim(provider_endpoint)) BETWEEN 1 AND 512),
  provider_method text NOT NULL CHECK (length(btrim(provider_method)) BETWEEN 1 AND 128),
  request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
  state text NOT NULL CHECK (state IN ('PREPARED','MAY_COMMIT','REPLIED','RESOLVED')),
  reply_sha256 text,
  reply_status text,
  reply_event_time timestamptz,
  reply_reason_code text,
  prepared_at timestamptz NOT NULL,
  commit_armed_at timestamptz,
  replied_at timestamptz,
  resolved_at timestamptz,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (attempt_id, execution_fence),
  UNIQUE (command_id, execution_fence),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, gateway_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  CHECK ((state = 'PREPARED' AND commit_armed_at IS NULL AND replied_at IS NULL AND resolved_at IS NULL)
      OR (state = 'MAY_COMMIT' AND commit_armed_at IS NOT NULL AND replied_at IS NULL AND resolved_at IS NULL)
      OR (state = 'REPLIED' AND commit_armed_at IS NOT NULL AND replied_at IS NOT NULL AND reply_sha256 ~ '^[a-f0-9]{64}$' AND reply_status IS NOT NULL AND resolved_at IS NULL)
      OR (state = 'RESOLVED' AND commit_armed_at IS NOT NULL AND replied_at IS NOT NULL AND reply_sha256 ~ '^[a-f0-9]{64}$' AND reply_status IS NOT NULL AND resolved_at IS NOT NULL))
);

CREATE INDEX connectivity_recoverable_command_replies_idx
  ON connectivity.command_reply_correlations (tenant_id, state, replied_at)
  WHERE state = 'REPLIED';

ALTER TABLE connectivity.command_reply_correlations ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.command_reply_correlations FORCE ROW LEVEL SECURITY;

CREATE POLICY connectivity_command_correlations_tenant_policy ON connectivity.command_reply_correlations
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT EXECUTE ON FUNCTION connectivity.is_uuid_v7(uuid) TO connectivity_runtime;
GRANT SELECT, INSERT, UPDATE ON connectivity.command_reply_correlations TO connectivity_runtime;

COMMIT;
