BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_iam_grant_runtime') THEN
    CREATE ROLE s2_iam_grant_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

GRANT CONNECT ON DATABASE hvac_s1 TO s2_iam_grant_runtime;
GRANT USAGE ON SCHEMA iam TO s2_iam_grant_runtime;

SET LOCAL ROLE s1_iam_migrator;

CREATE TABLE IF NOT EXISTS iam.telemetry_scope_bindings (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  device_id uuid CHECK (device_id IS NULL OR iam.is_uuid_v7(device_id)),
  actions text[] NOT NULL CHECK (cardinality(actions) > 0),
  effect text NOT NULL CHECK (effect IN ('ALLOW', 'DENY')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REVOKED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.telemetry_key_bindings (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  device_id uuid NOT NULL CHECK (iam.is_uuid_v7(device_id)),
  telemetry_key text NOT NULL CHECK (telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  actions text[] NOT NULL CHECK (cardinality(actions) > 0),
  effect text NOT NULL CHECK (effect IN ('ALLOW', 'DENY')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REVOKED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (principal_id, tenant_id, device_id, telemetry_key, effect),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.telemetry_grant_revocations (
  token_id text PRIMARY KEY CHECK (char_length(token_id) BETWEEN 1 AND 256),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  principal_id uuid NOT NULL CHECK (iam.is_uuid_v7(principal_id)),
  revoked_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  reason_code text NOT NULL CHECK (char_length(reason_code) BETWEEN 1 AND 128),
  CHECK (expires_at > revoked_at)
);

CREATE TABLE IF NOT EXISTS iam.telemetry_grant_uses (
  token_id text PRIMARY KEY CHECK (char_length(token_id) BETWEEN 1 AND 256),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  principal_id uuid NOT NULL CHECK (iam.is_uuid_v7(principal_id)),
  scope_digest text NOT NULL CHECK (scope_digest ~ '^[a-f0-9]{64}$'),
  consumed_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  CHECK (expires_at > consumed_at)
);

CREATE TABLE IF NOT EXISTS iam.telemetry_revocation_facts (
  sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  principal_id uuid NOT NULL CHECK (iam.is_uuid_v7(principal_id)),
  source_type text NOT NULL CHECK (source_type IN ('MEMBERSHIP','ROLE_BINDING','SITE_BINDING','DEVICE_PERMISSION','KEY_PERMISSION','GRANT')),
  source_id uuid,
  device_id uuid,
  telemetry_key text,
  action text,
  policy_revision text NOT NULL,
  reason_code text NOT NULL CHECK (char_length(reason_code) BETWEEN 1 AND 128),
  occurred_at timestamptz NOT NULL,
  CHECK (source_id IS NULL OR iam.is_uuid_v7(source_id)),
  CHECK (device_id IS NULL OR iam.is_uuid_v7(device_id)),
  CHECK (telemetry_key IS NULL OR telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$')
);

CREATE INDEX IF NOT EXISTS telemetry_scope_bindings_lookup_idx
  ON iam.telemetry_scope_bindings (principal_id, tenant_id, site_id, device_id, effect);
CREATE INDEX IF NOT EXISTS telemetry_key_bindings_lookup_idx
  ON iam.telemetry_key_bindings (principal_id, tenant_id, device_id, telemetry_key, effect);
CREATE INDEX IF NOT EXISTS telemetry_revocation_facts_cursor_idx
  ON iam.telemetry_revocation_facts (tenant_id, sequence);
CREATE INDEX IF NOT EXISTS telemetry_grant_uses_expiry_idx
  ON iam.telemetry_grant_uses (expires_at, tenant_id);

ALTER TABLE iam.telemetry_scope_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_key_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_grant_revocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_grant_uses ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_revocation_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_scope_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_key_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_grant_revocations FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_grant_uses FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.telemetry_revocation_facts FORCE ROW LEVEL SECURITY;

CREATE POLICY telemetry_scope_bindings_runtime_scope ON iam.telemetry_scope_bindings
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id() AND tenant_id = iam.current_tenant_id());
CREATE POLICY telemetry_key_bindings_runtime_scope ON iam.telemetry_key_bindings
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id() AND tenant_id = iam.current_tenant_id());
CREATE POLICY telemetry_grant_revocations_runtime_scope ON iam.telemetry_grant_revocations
  FOR SELECT TO s2_iam_grant_runtime
  USING (tenant_id = iam.current_tenant_id());
CREATE POLICY telemetry_grant_uses_runtime_scope ON iam.telemetry_grant_uses
  FOR SELECT TO s2_iam_grant_runtime
  USING (tenant_id = iam.current_tenant_id());
CREATE POLICY telemetry_grant_uses_runtime_insert ON iam.telemetry_grant_uses
  FOR INSERT TO s2_iam_grant_runtime
  WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY telemetry_revocation_facts_runtime_scope ON iam.telemetry_revocation_facts
  FOR SELECT TO s2_iam_grant_runtime
  USING (tenant_id = iam.current_tenant_id());

CREATE POLICY role_bindings_s2_migrator_all ON iam.role_bindings
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY site_bindings_s2_migrator_all ON iam.site_bindings
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY explicit_denies_s2_migrator_all ON iam.explicit_denies
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY policies_s2_migrator_all ON iam.policies
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY telemetry_scope_bindings_migrator_all ON iam.telemetry_scope_bindings
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY telemetry_key_bindings_migrator_all ON iam.telemetry_key_bindings
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY telemetry_grant_revocations_migrator_all ON iam.telemetry_grant_revocations
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY telemetry_grant_uses_migrator_all ON iam.telemetry_grant_uses
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY telemetry_revocation_facts_migrator_all ON iam.telemetry_revocation_facts
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);

GRANT SELECT ON iam.telemetry_scope_bindings, iam.telemetry_key_bindings TO s1_iam_runtime;
GRANT SELECT ON iam.telemetry_grant_revocations, iam.telemetry_grant_uses, iam.telemetry_revocation_facts TO s2_iam_grant_runtime;
GRANT INSERT ON iam.telemetry_grant_uses TO s2_iam_grant_runtime;
GRANT USAGE, SELECT ON SEQUENCE iam.telemetry_revocation_facts_sequence_seq TO s1_iam_migrator;
REVOKE ALL ON iam.telemetry_scope_bindings, iam.telemetry_key_bindings, iam.telemetry_grant_revocations,
  iam.telemetry_grant_uses, iam.telemetry_revocation_facts FROM PUBLIC;

RESET ROLE;
SET LOCAL ROLE s1_core_migrator;

CREATE OR REPLACE FUNCTION core_registry.current_iam_requested_device_ids()
RETURNS uuid[]
LANGUAGE sql
STABLE
AS $body$
  SELECT COALESCE(NULLIF(current_setting('app.requested_device_ids', true), ''), '{}')::uuid[]
$body$;

CREATE POLICY devices_iam_exact_request_scope ON core_registry.devices
  FOR SELECT TO s1_iam_runtime
  USING (id = ANY (core_registry.current_iam_requested_device_ids()));
GRANT USAGE ON SCHEMA core_registry TO s1_iam_runtime;
GRANT SELECT (id, tenant_id, site_id, status, revision) ON core_registry.devices TO s1_iam_runtime;

RESET ROLE;
SET LOCAL ROLE s1_iam_migrator;

CREATE OR REPLACE FUNCTION iam.active_telemetry_policy_revision(tenant_value uuid)
RETURNS text
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $policy$
  SELECT policy_key || ':' || policy_revision::text
  FROM iam.policies
  WHERE tenant_id = tenant_value
    AND policy_key = 'telemetry-access'
    AND status = 'ACTIVE'
  ORDER BY policy_revision DESC
  LIMIT 1
$policy$;

GRANT EXECUTE ON FUNCTION iam.active_telemetry_policy_revision(uuid) TO s2_iam_grant_runtime;

CREATE OR REPLACE FUNCTION iam.emit_membership_telemetry_revocation()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $$
DECLARE
  row_value iam.tenant_memberships%ROWTYPE;
BEGIN
  row_value := OLD;
  IF TG_OP = 'DELETE'
     OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
     OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
     OR NEW.status IS DISTINCT FROM OLD.status
     OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
     OR NEW.valid_to IS DISTINCT FROM OLD.valid_to THEN
    INSERT INTO iam.telemetry_revocation_facts
      (tenant_id, principal_id, source_type, source_id, policy_revision, reason_code, occurred_at)
    VALUES
      (row_value.tenant_id, row_value.principal_id, 'MEMBERSHIP', row_value.id,
       COALESCE(iam.active_telemetry_policy_revision(row_value.tenant_id), 'telemetry-access:unavailable'),
       'MEMBERSHIP_CHANGED', clock_timestamp());
  END IF;
  RETURN row_value;
END
$$;

CREATE OR REPLACE FUNCTION iam.emit_scope_telemetry_revocation()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $$
DECLARE
  row_value iam.telemetry_scope_bindings%ROWTYPE;
BEGIN
  row_value := OLD;
  IF TG_OP = 'DELETE'
     OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
     OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
     OR NEW.site_id IS DISTINCT FROM OLD.site_id
     OR NEW.device_id IS DISTINCT FROM OLD.device_id
     OR NEW.actions IS DISTINCT FROM OLD.actions
     OR NEW.effect IS DISTINCT FROM OLD.effect
     OR NEW.status IS DISTINCT FROM OLD.status
     OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
     OR NEW.valid_to IS DISTINCT FROM OLD.valid_to THEN
    INSERT INTO iam.telemetry_revocation_facts
      (tenant_id, principal_id, source_type, source_id, device_id, policy_revision, reason_code, occurred_at)
    VALUES
      (row_value.tenant_id, row_value.principal_id, 'DEVICE_PERMISSION', row_value.id, row_value.device_id,
       COALESCE(iam.active_telemetry_policy_revision(row_value.tenant_id), 'telemetry-access:unavailable'),
       'DEVICE_SCOPE_CHANGED', clock_timestamp());
  END IF;
  RETURN row_value;
END
$$;

CREATE OR REPLACE FUNCTION iam.emit_key_telemetry_revocation()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $$
DECLARE
  row_value iam.telemetry_key_bindings%ROWTYPE;
BEGIN
  row_value := OLD;
  IF TG_OP = 'DELETE'
     OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
     OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
     OR NEW.device_id IS DISTINCT FROM OLD.device_id
     OR NEW.telemetry_key IS DISTINCT FROM OLD.telemetry_key
     OR NEW.actions IS DISTINCT FROM OLD.actions
     OR NEW.effect IS DISTINCT FROM OLD.effect
     OR NEW.status IS DISTINCT FROM OLD.status
     OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
     OR NEW.valid_to IS DISTINCT FROM OLD.valid_to THEN
    INSERT INTO iam.telemetry_revocation_facts
      (tenant_id, principal_id, source_type, source_id, device_id, telemetry_key, policy_revision, reason_code, occurred_at)
    VALUES
      (row_value.tenant_id, row_value.principal_id, 'KEY_PERMISSION', row_value.id, row_value.device_id, row_value.telemetry_key,
       COALESCE(iam.active_telemetry_policy_revision(row_value.tenant_id), 'telemetry-access:unavailable'),
       'KEY_SCOPE_CHANGED', clock_timestamp());
  END IF;
  RETURN row_value;
END
$$;

CREATE OR REPLACE FUNCTION iam.emit_role_binding_telemetry_revocation()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $role_revocation$
DECLARE
  row_value iam.role_bindings%ROWTYPE;
BEGIN
  row_value := OLD;
  IF TG_OP = 'DELETE'
     OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
     OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
     OR NEW.role_key IS DISTINCT FROM OLD.role_key
     OR NEW.actions IS DISTINCT FROM OLD.actions
     OR NEW.effect IS DISTINCT FROM OLD.effect
     OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
     OR NEW.valid_to IS DISTINCT FROM OLD.valid_to THEN
    INSERT INTO iam.telemetry_revocation_facts
      (tenant_id, principal_id, source_type, source_id, action, policy_revision, reason_code, occurred_at)
    VALUES
      (row_value.tenant_id, row_value.principal_id, 'ROLE_BINDING', row_value.id, 'telemetry.*',
       COALESCE(iam.active_telemetry_policy_revision(row_value.tenant_id), 'telemetry-access:unavailable'),
       'ROLE_BINDING_CHANGED', clock_timestamp());
  END IF;
  RETURN row_value;
END
$role_revocation$;

CREATE OR REPLACE FUNCTION iam.emit_site_binding_telemetry_revocation()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $site_revocation$
DECLARE
  row_value iam.site_bindings%ROWTYPE;
BEGIN
  row_value := OLD;
  IF TG_OP = 'DELETE'
     OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
     OR NEW.site_id IS DISTINCT FROM OLD.site_id
     OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
     OR NEW.actions IS DISTINCT FROM OLD.actions
     OR NEW.effect IS DISTINCT FROM OLD.effect
     OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
     OR NEW.valid_to IS DISTINCT FROM OLD.valid_to THEN
    INSERT INTO iam.telemetry_revocation_facts
      (tenant_id, principal_id, source_type, source_id, policy_revision, reason_code, occurred_at)
    VALUES
      (row_value.tenant_id, row_value.principal_id, 'SITE_BINDING', row_value.id,
       COALESCE(iam.active_telemetry_policy_revision(row_value.tenant_id), 'telemetry-access:unavailable'),
       'SITE_BINDING_CHANGED', clock_timestamp());
  END IF;
  RETURN row_value;
END
$site_revocation$;

DROP TRIGGER IF EXISTS tenant_memberships_telemetry_revocation ON iam.tenant_memberships;
CREATE TRIGGER tenant_memberships_telemetry_revocation
AFTER UPDATE OR DELETE ON iam.tenant_memberships
FOR EACH ROW EXECUTE FUNCTION iam.emit_membership_telemetry_revocation();
DROP TRIGGER IF EXISTS telemetry_scope_bindings_revocation ON iam.telemetry_scope_bindings;
CREATE TRIGGER telemetry_scope_bindings_revocation
AFTER UPDATE OR DELETE ON iam.telemetry_scope_bindings
FOR EACH ROW EXECUTE FUNCTION iam.emit_scope_telemetry_revocation();
DROP TRIGGER IF EXISTS telemetry_key_bindings_revocation ON iam.telemetry_key_bindings;
CREATE TRIGGER telemetry_key_bindings_revocation
AFTER UPDATE OR DELETE ON iam.telemetry_key_bindings
FOR EACH ROW EXECUTE FUNCTION iam.emit_key_telemetry_revocation();
DROP TRIGGER IF EXISTS role_bindings_telemetry_revocation ON iam.role_bindings;
CREATE TRIGGER role_bindings_telemetry_revocation
AFTER UPDATE OR DELETE ON iam.role_bindings
FOR EACH ROW EXECUTE FUNCTION iam.emit_role_binding_telemetry_revocation();
DROP TRIGGER IF EXISTS site_bindings_telemetry_revocation ON iam.site_bindings;
CREATE TRIGGER site_bindings_telemetry_revocation
AFTER UPDATE OR DELETE ON iam.site_bindings
FOR EACH ROW EXECUTE FUNCTION iam.emit_site_binding_telemetry_revocation();

RESET ROLE;
COMMIT;
