\set ON_ERROR_STOP on

-- Gateway Credentials and uplink quarantine (ADR 0015). A Gateway's messages are
-- accepted only while it holds an active credential; everything Connectivity cannot
-- accept from a Gateway is kept as quarantine evidence.
BEGIN;
SET LOCAL ROLE connectivity_migrator;

CREATE TABLE connectivity.gateway_credentials (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  certificate_fingerprint_sha256 text NOT NULL CHECK (certificate_fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'REVOKED')),
  valid_from timestamptz NOT NULL,
  valid_until timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (certificate_fingerprint_sha256),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (valid_until > valid_from),
  CHECK ((status = 'REVOKED') = (revoked_at IS NOT NULL)),
  CHECK (updated_at >= created_at)
);
CREATE INDEX gateway_credentials_gateway_idx
  ON connectivity.gateway_credentials (tenant_id, gateway_id, valid_until DESC);

-- Tenant is unknown when the Gateway itself is not in the Registry gateway directory.
CREATE TABLE connectivity.uplink_quarantine (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid CHECK (tenant_id IS NULL OR connectivity.is_uuid_v7(tenant_id)),
  gateway_id text NOT NULL CHECK (length(gateway_id) BETWEEN 1 AND 128),
  topic text NOT NULL CHECK (length(topic) BETWEEN 1 AND 512),
  reason_code text NOT NULL CHECK (reason_code IN ('GATEWAY_UNKNOWN', 'GATEWAY_CREDENTIAL_INACTIVE', 'MESSAGE_INVALID')),
  detail text NOT NULL CHECK (length(detail) BETWEEN 1 AND 1024),
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
  payload_bytes integer NOT NULL CHECK (payload_bytes >= 0),
  received_at timestamptz NOT NULL
);
CREATE INDEX uplink_quarantine_gateway_idx
  ON connectivity.uplink_quarantine (gateway_id, received_at DESC);

ALTER TABLE connectivity.gateway_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_credentials FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.uplink_quarantine ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.uplink_quarantine FORCE ROW LEVEL SECURITY;

CREATE POLICY connectivity_gateway_credentials_tenant_policy ON connectivity.gateway_credentials
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY connectivity_uplink_quarantine_tenant_policy ON connectivity.uplink_quarantine
  USING (tenant_id IS NOT DISTINCT FROM NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id IS NOT DISTINCT FROM NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON connectivity.gateway_credentials TO connectivity_runtime;
GRANT SELECT, INSERT ON connectivity.uplink_quarantine TO connectivity_runtime;

COMMIT;
