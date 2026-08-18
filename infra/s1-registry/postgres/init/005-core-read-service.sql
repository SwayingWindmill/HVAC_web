BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_core_service') THEN
    CREATE ROLE s1_core_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

GRANT CONNECT ON DATABASE hvac_s1 TO s1_core_service;
GRANT s1_core_runtime TO s1_core_service;

SET LOCAL ROLE s1_iam_migrator;

CREATE TABLE IF NOT EXISTS iam.registry_grant_revocations (
  token_id text PRIMARY KEY CHECK (char_length(token_id) BETWEEN 1 AND 256),
  tenant_id uuid NOT NULL CHECK (iam.is_uuid_v7(tenant_id)),
  revoked_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  reason_code text NOT NULL CHECK (char_length(reason_code) BETWEEN 1 AND 128),
  CHECK (expires_at > revoked_at)
);

CREATE INDEX IF NOT EXISTS registry_grant_revocations_expiry_idx
  ON iam.registry_grant_revocations (expires_at, tenant_id);

ALTER TABLE iam.registry_grant_revocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.registry_grant_revocations FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS registry_grant_revocations_migrator_all ON iam.registry_grant_revocations;
CREATE POLICY registry_grant_revocations_migrator_all ON iam.registry_grant_revocations
  FOR ALL TO s1_iam_migrator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS registry_grant_revocations_runtime_scope ON iam.registry_grant_revocations;
CREATE POLICY registry_grant_revocations_runtime_scope ON iam.registry_grant_revocations
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id = iam.current_tenant_id());

GRANT SELECT ON iam.registry_grant_revocations TO s1_iam_runtime;
REVOKE ALL ON iam.registry_grant_revocations FROM PUBLIC;

RESET ROLE;
COMMIT;
