\set ON_ERROR_STOP on
BEGIN;
SET LOCAL ROLE connectivity_migrator;

-- One locked lifecycle row serializes generation, issuance, renewal and terminal
-- revocation. No plaintext code, CSR or private key is persisted.
CREATE TABLE connectivity.gateway_enrollments (
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  code_sha256 text CHECK (code_sha256 ~ '^[0-9a-f]{64}$'),
  expires_at timestamptz,
  consumed_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY(tenant_id,gateway_id),
  CHECK ((code_sha256 IS NULL) = (expires_at IS NULL)),
  CHECK (expires_at IS NULL OR expires_at > created_at),
  CHECK (updated_at >= created_at)
);
CREATE TABLE connectivity.gateway_credential_audit (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  actor_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(actor_id)),
  action text NOT NULL CHECK (action IN ('CODE_GENERATED','ISSUED','RENEWED','REVOKED')),
  occurred_at timestamptz NOT NULL
);
ALTER TABLE connectivity.gateway_enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_enrollments FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_credential_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_credential_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY gateway_enrollment_tenant ON connectivity.gateway_enrollments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid);
CREATE POLICY gateway_credential_audit_tenant ON connectivity.gateway_credential_audit
  USING (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT,INSERT,UPDATE ON connectivity.gateway_enrollments TO connectivity_runtime;
GRANT SELECT,INSERT ON connectivity.gateway_credential_audit TO connectivity_runtime;
COMMIT;
