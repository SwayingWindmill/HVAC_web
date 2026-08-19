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

CREATE TABLE connectivity.transport_profiles (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  protocol text NOT NULL CHECK (protocol = 'MQTT'),
  broker_origin text NOT NULL CHECK (broker_origin ~ '^tls://[^/?#]+/?$'),
  topic_namespace text NOT NULL CHECK (topic_namespace = 'energy/v1'),
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE connectivity.integration_instances (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  transport_profile_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(transport_profile_id)),
  gateway_external_id text NOT NULL CHECK (length(btrim(gateway_external_id)) BETWEEN 1 AND 128),
  status text NOT NULL CHECK (status IN ('ACTIVE','SUSPENDED','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, site_id, gateway_external_id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, transport_profile_id) REFERENCES connectivity.transport_profiles(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE connectivity.credential_refs (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  credential_kind text NOT NULL CHECK (credential_kind IN ('MTLS_CERTIFICATE','TOKEN_HASH')),
  secret_ref text,
  certificate_fingerprint_sha256 text,
  token_hash_sha256 text,
  status text NOT NULL CHECK (status IN ('ACTIVE','REVOKED','EXPIRED')),
  valid_from timestamptz NOT NULL,
  valid_until timestamptz NOT NULL,
  rotated_from_id uuid,
  revoked_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  FOREIGN KEY (tenant_id, rotated_from_id) REFERENCES connectivity.credential_refs(tenant_id, id),
  CHECK (valid_until > valid_from),
  CHECK (revoked_at IS NULL OR revoked_at >= valid_from),
  CHECK (updated_at >= created_at),
  CHECK (
    (credential_kind = 'MTLS_CERTIFICATE' AND certificate_fingerprint_sha256 ~ '^[a-f0-9]{64}$' AND token_hash_sha256 IS NULL)
    OR
    (credential_kind = 'TOKEN_HASH' AND token_hash_sha256 ~ '^[a-f0-9]{64}$' AND certificate_fingerprint_sha256 IS NULL)
  ),
  CHECK (secret_ref IS NULL OR (length(btrim(secret_ref)) BETWEEN 1 AND 512 AND secret_ref !~* '(password|secret|token)='))
);

CREATE UNIQUE INDEX connectivity_active_certificate_fingerprint_uidx
  ON connectivity.credential_refs (integration_instance_id, certificate_fingerprint_sha256)
  WHERE status = 'ACTIVE' AND certificate_fingerprint_sha256 IS NOT NULL;
CREATE UNIQUE INDEX connectivity_active_token_hash_uidx
  ON connectivity.credential_refs (integration_instance_id, token_hash_sha256)
  WHERE status = 'ACTIVE' AND token_hash_sha256 IS NOT NULL;

CREATE TABLE connectivity.device_bindings (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  device_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(device_id)),
  external_device_id text NOT NULL CHECK (length(btrim(external_device_id)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('ACTIVE','QUARANTINED','RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX connectivity_active_device_external_uidx
  ON connectivity.device_bindings (integration_instance_id, external_device_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;
CREATE UNIQUE INDEX connectivity_active_device_registry_uidx
  ON connectivity.device_bindings (integration_instance_id, device_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE connectivity.gateway_child_bindings (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  gateway_external_id text NOT NULL CHECK (length(btrim(gateway_external_id)) BETWEEN 1 AND 128),
  child_device_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(child_device_id)),
  child_external_id text NOT NULL CHECK (length(btrim(child_external_id)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('ACTIVE','QUARANTINED','RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, child_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX connectivity_active_gateway_child_external_uidx
  ON connectivity.gateway_child_bindings (integration_instance_id, gateway_external_id, child_external_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;
CREATE UNIQUE INDEX connectivity_active_gateway_child_registry_uidx
  ON connectivity.gateway_child_bindings (integration_instance_id, gateway_external_id, child_device_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE connectivity.enrollments (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  device_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(device_id)),
  gateway_external_id text,
  hardware_identity_sha256 text NOT NULL CHECK (hardware_identity_sha256 ~ '^[a-f0-9]{64}$'),
  challenge_hash_sha256 text NOT NULL CHECK (challenge_hash_sha256 ~ '^[a-f0-9]{64}$'),
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  credential_ref_id uuid,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  FOREIGN KEY (tenant_id, credential_ref_id) REFERENCES connectivity.credential_refs(tenant_id, id),
  CHECK (expires_at > created_at),
  CHECK (consumed_at IS NULL OR consumed_at <= expires_at),
  CHECK ((consumed_at IS NULL AND credential_ref_id IS NULL) OR (consumed_at IS NOT NULL AND credential_ref_id IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX connectivity_open_enrollment_device_uidx
  ON connectivity.enrollments (integration_instance_id, device_id)
  WHERE consumed_at IS NULL;

CREATE TABLE connectivity.sessions (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  credential_ref_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(credential_ref_id)),
  credential_revision bigint NOT NULL CHECK (credential_revision > 0),
  gateway_external_id text NOT NULL CHECK (length(btrim(gateway_external_id)) BETWEEN 1 AND 128),
  status text NOT NULL CHECK (status IN ('ACTIVE','CLOSED','INVALIDATED','EXPIRED')),
  opened_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  closed_at timestamptz,
  close_reason text,
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  FOREIGN KEY (tenant_id, credential_ref_id) REFERENCES connectivity.credential_refs(tenant_id, id),
  CHECK (expires_at > opened_at),
  CHECK ((status = 'ACTIVE' AND closed_at IS NULL AND close_reason IS NULL) OR (status <> 'ACTIVE' AND closed_at IS NOT NULL AND length(btrim(close_reason)) BETWEEN 1 AND 256)),
  CHECK (updated_at >= opened_at)
);

CREATE INDEX connectivity_active_sessions_credential_idx
  ON connectivity.sessions (credential_ref_id, expires_at)
  WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX connectivity_active_gateway_session_uidx
  ON connectivity.sessions (integration_instance_id, gateway_external_id)
  WHERE status = 'ACTIVE';

CREATE TABLE connectivity.connector_ownership_leases (
  integration_instance_id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  owner_id text NOT NULL CHECK (length(btrim(owner_id)) BETWEEN 1 AND 256),
  lease_generation bigint NOT NULL CHECK (lease_generation > 0),
  lease_until timestamptz NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id)
);

CREATE TABLE connectivity.command_reply_correlations (
  attempt_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(attempt_id)),
  execution_fence bigint NOT NULL CHECK (execution_fence > 0),
  command_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(command_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  device_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(device_id)),
  point_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(point_id)),
  capability text NOT NULL CHECK (length(btrim(capability)) BETWEEN 1 AND 128),
  external_device_id text NOT NULL CHECK (length(btrim(external_device_id)) BETWEEN 1 AND 256),
  payload_hash text NOT NULL CHECK (payload_hash ~ '^[a-f0-9]{64}$'),
  lease_owner text NOT NULL CHECK (length(btrim(lease_owner)) BETWEEN 1 AND 256),
  lease_until timestamptz NOT NULL,
  owner_generation bigint NOT NULL CHECK (owner_generation > 0),
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
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  CHECK ((state = 'PREPARED' AND commit_armed_at IS NULL AND replied_at IS NULL AND resolved_at IS NULL)
      OR (state = 'MAY_COMMIT' AND commit_armed_at IS NOT NULL AND replied_at IS NULL AND resolved_at IS NULL)
      OR (state = 'REPLIED' AND commit_armed_at IS NOT NULL AND replied_at IS NOT NULL AND reply_sha256 ~ '^[a-f0-9]{64}$' AND reply_status IS NOT NULL AND resolved_at IS NULL)
      OR (state = 'RESOLVED' AND commit_armed_at IS NOT NULL AND replied_at IS NOT NULL AND reply_sha256 ~ '^[a-f0-9]{64}$' AND reply_status IS NOT NULL AND resolved_at IS NOT NULL))
);

CREATE INDEX connectivity_recoverable_command_replies_idx
  ON connectivity.command_reply_correlations (integration_instance_id, state, replied_at)
  WHERE state = 'REPLIED';

CREATE TABLE connectivity.audit_facts (
  event_id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(event_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  event_type text NOT NULL CHECK (event_type IN ('CREDENTIAL_ROTATED','CREDENTIAL_REVOKED','SESSION_OPENED','SESSION_CLOSED','SESSION_INVALIDATED','ENROLLMENT_CONSUMED','OWNERSHIP_ACQUIRED','OWNERSHIP_RENEWED','COMMAND_CORRELATION_RECOVERED')),
  subject_id text NOT NULL CHECK (length(btrim(subject_id)) BETWEEN 1 AND 256),
  revision bigint NOT NULL CHECK (revision > 0),
  evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'object'),
  occurred_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  CHECK (NOT (evidence ?| ARRAY['password','secret','token','privateKey','credentialValue']))
);

ALTER TABLE connectivity.transport_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.transport_profiles FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.integration_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.integration_instances FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.credential_refs ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.credential_refs FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.device_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.device_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_child_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_child_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.enrollments FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.sessions FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.connector_ownership_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.connector_ownership_leases FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.command_reply_correlations ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.command_reply_correlations FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.audit_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.audit_facts FORCE ROW LEVEL SECURITY;

CREATE POLICY connectivity_transport_profiles_tenant_policy ON connectivity.transport_profiles
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_integration_instances_tenant_policy ON connectivity.integration_instances
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_credential_refs_tenant_policy ON connectivity.credential_refs
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_device_bindings_tenant_policy ON connectivity.device_bindings
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_gateway_child_bindings_tenant_policy ON connectivity.gateway_child_bindings
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_enrollments_tenant_policy ON connectivity.enrollments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_sessions_tenant_policy ON connectivity.sessions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_connector_ownership_tenant_policy ON connectivity.connector_ownership_leases
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_command_correlations_tenant_policy ON connectivity.command_reply_correlations
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_audit_tenant_policy ON connectivity.audit_facts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT EXECUTE ON FUNCTION connectivity.is_uuid_v7(uuid) TO connectivity_runtime;
GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA connectivity TO connectivity_runtime;
REVOKE DELETE ON ALL TABLES IN SCHEMA connectivity FROM connectivity_runtime;

COMMIT;
