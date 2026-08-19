\set ON_ERROR_STOP on

BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_admin') THEN
    CREATE ROLE s1_iam_admin LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

GRANT CONNECT ON DATABASE hvac_s1 TO s1_iam_admin;
GRANT USAGE ON SCHEMA iam TO s1_iam_admin;

SET LOCAL ROLE s1_iam_migrator;

CREATE TABLE iam.capability_catalog_revisions (
  revision bigint PRIMARY KEY CHECK (revision > 0),
  catalog_key text NOT NULL UNIQUE,
  capabilities text[] NOT NULL CHECK (cardinality(capabilities) > 0),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'RETIRED')),
  created_at timestamptz NOT NULL
);

INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
VALUES (
  1,
  's04-v1',
  ARRAY[
    'registry.read', 'site.list', 'site.read', 'asset.list', 'asset.read', 'device.list', 'device.read',
    'device-binding.list', 'asset-model.read',
    'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read',
    'telemetry.resubscribe', 'telemetry.recovery.use', 'telemetry.recovery.checkpoint',
    'analytics.energy-series.read', 'alarm:read', 'alarm:ack',
    'work-order:list', 'work-order:read', 'work-order:create', 'work-order:assign', 'work-order:plan',
    'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete', 'work-order:cancel', 'work-order:reopen',
    'session.revoke', 'audit.read', 'iam.admin', 'api-credential.manage'
  ],
  'ACTIVE',
  '2026-08-18T00:00:00Z'
);

CREATE UNIQUE INDEX capability_catalog_one_active_idx
  ON iam.capability_catalog_revisions ((status))
  WHERE status = 'ACTIVE';

CREATE TABLE iam.authorization_revisions (
  tenant_id uuid PRIMARY KEY REFERENCES iam.tenants(id) CHECK (iam.is_uuid_v7(tenant_id)),
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL
);

INSERT INTO iam.authorization_revisions (tenant_id, revision, updated_at)
SELECT id, 1, updated_at FROM iam.tenants;

CREATE OR REPLACE FUNCTION iam.active_telemetry_policy_revision(tenant_value uuid)
RETURNS text
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $policy$
  SELECT policy.policy_key || ':' || policy.policy_revision::text || '/iam:' || authorization.revision::text
  FROM iam.policies policy
  JOIN iam.authorization_revisions authorization ON authorization.tenant_id = policy.tenant_id
  WHERE policy.tenant_id = tenant_value
    AND policy.policy_key = 'telemetry-access'
    AND policy.status = 'ACTIVE'
  ORDER BY policy.policy_revision DESC
  LIMIT 1
$policy$;

CREATE TABLE iam.role_templates (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (iam.is_uuid_v7(tenant_id)),
  role_key text NOT NULL CHECK (char_length(role_key) BETWEEN 1 AND 128),
  display_name text NOT NULL CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 256),
  capabilities text[] NOT NULL CHECK (cardinality(capabilities) > 0),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, role_key),
  CHECK (updated_at >= created_at)
);

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM iam.role_bindings
    GROUP BY tenant_id, role_key
    HAVING count(DISTINCT actions) > 1
  ) THEN
    RAISE EXCEPTION 'S04 cannot infer a Role Template from divergent role binding actions';
  END IF;
END
$$;

INSERT INTO iam.role_templates (
  id, tenant_id, role_key, display_name, capabilities, status, revision, created_at, updated_at
)
SELECT DISTINCT ON (tenant_id, role_key)
  id, tenant_id, role_key, role_key, actions, 'ACTIVE', revision, created_at, updated_at
FROM iam.role_bindings
ORDER BY tenant_id, role_key, revision DESC, updated_at DESC;

ALTER TABLE iam.role_bindings
  ADD COLUMN role_template_id uuid REFERENCES iam.role_templates(id),
  ADD COLUMN status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REVOKED'));

UPDATE iam.role_bindings binding
SET role_template_id = template.id
FROM iam.role_templates template
WHERE template.tenant_id = binding.tenant_id
  AND template.role_key = binding.role_key;

INSERT INTO iam.explicit_denies (
  id, tenant_id, site_id, principal_id, action, reason_code,
  valid_from, valid_to, revision, created_at, updated_at
)
SELECT
  overlay(gen_random_uuid()::text placing '7' from 15 for 1)::uuid,
  binding.tenant_id,
  NULL,
  binding.principal_id,
  capability,
  'MIGRATED_ROLE_BINDING_DENY',
  binding.valid_from,
  binding.valid_to,
  binding.revision,
  binding.created_at,
  binding.updated_at
FROM iam.role_bindings binding
JOIN iam.role_templates template ON template.id = binding.role_template_id
CROSS JOIN LATERAL unnest(template.capabilities) AS capability
WHERE binding.effect = 'DENY';

DELETE FROM iam.role_bindings WHERE effect = 'DENY';

ALTER TABLE iam.role_bindings
  ALTER COLUMN role_template_id SET NOT NULL,
  DROP CONSTRAINT role_bindings_tenant_id_principal_id_role_key_key,
  DROP COLUMN role_key,
  DROP COLUMN actions,
  DROP COLUMN effect;

CREATE UNIQUE INDEX role_bindings_principal_template_idx
  ON iam.role_bindings (tenant_id, principal_id, role_template_id);

ALTER TABLE iam.site_bindings
  ADD COLUMN status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REVOKED'));

-- Runtime may discover only Tenant contexts for the current Principal. A public
-- header cannot select a Tenant; the BFF must still create a signed delegation.
DROP POLICY tenants_runtime_scope ON iam.tenants;
CREATE POLICY tenants_runtime_scope ON iam.tenants
  FOR SELECT TO s1_iam_runtime
  USING (EXISTS (
    SELECT 1
    FROM iam.tenant_memberships membership
    WHERE membership.tenant_id = tenants.id
      AND membership.principal_id = iam.current_principal_id()
      AND membership.status = 'ACTIVE'
      AND membership.valid_from <= statement_timestamp()
      AND (membership.valid_to IS NULL OR membership.valid_to > statement_timestamp())
  ));

CREATE TABLE iam.service_accounts (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (iam.is_uuid_v7(tenant_id)),
  display_name text NOT NULL CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 256),
  purpose text NOT NULL CHECK (char_length(btrim(purpose)) BETWEEN 1 AND 512),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_by_principal_id uuid NOT NULL REFERENCES iam.principals(id),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (updated_at >= created_at)
);

CREATE TABLE iam.api_credentials (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (iam.is_uuid_v7(tenant_id)),
  service_account_id uuid NOT NULL REFERENCES iam.service_accounts(id),
  secret_hash text NOT NULL CHECK (secret_hash ~ '^[a-f0-9]{64}$'),
  capabilities text[] NOT NULL CHECK (cardinality(capabilities) > 0),
  site_ids uuid[] NOT NULL DEFAULT '{}',
  catalog_revision bigint NOT NULL REFERENCES iam.capability_catalog_revisions(revision),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'REVOKED', 'EXPIRED')),
  expires_at timestamptz NOT NULL,
  rotated_from_id uuid REFERENCES iam.api_credentials(id),
  revision bigint NOT NULL CHECK (revision > 0),
  created_by_principal_id uuid NOT NULL REFERENCES iam.principals(id),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (expires_at > created_at),
  CHECK (updated_at >= created_at)
);

CREATE INDEX api_credentials_service_account_idx
  ON iam.api_credentials (tenant_id, service_account_id, status, expires_at);

CREATE TABLE iam.admin_audit_intents (
  event_id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(event_id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (iam.is_uuid_v7(tenant_id)),
  actor_principal_id uuid NOT NULL REFERENCES iam.principals(id),
  action text NOT NULL CHECK (char_length(action) BETWEEN 1 AND 128),
  resource_type text NOT NULL CHECK (char_length(resource_type) BETWEEN 1 AND 128),
  resource_id text NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 256),
  outcome text NOT NULL CHECK (outcome IN ('SUCCEEDED', 'DENIED')),
  policy_revision text NOT NULL CHECK (char_length(policy_revision) BETWEEN 1 AND 128),
  correlation_id text NOT NULL,
  trace_id text NOT NULL,
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[a-f0-9]{64}$'),
  occurred_at timestamptz NOT NULL
);

CREATE TABLE iam.admin_outbox (
  message_id uuid PRIMARY KEY REFERENCES iam.admin_audit_intents(event_id),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (iam.is_uuid_v7(tenant_id)),
  topic text NOT NULL DEFAULT 'audit.events',
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  created_at timestamptz NOT NULL,
  published_at timestamptz
);

ALTER TABLE iam.capability_catalog_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.authorization_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.role_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.service_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.api_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.admin_audit_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.admin_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.capability_catalog_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.authorization_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.role_templates FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.service_accounts FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.api_credentials FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.admin_audit_intents FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.admin_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY capability_catalog_runtime_read ON iam.capability_catalog_revisions
  FOR SELECT TO s1_iam_runtime USING (status = 'ACTIVE');
CREATE POLICY capability_catalog_admin_read ON iam.capability_catalog_revisions
  FOR SELECT TO s1_iam_admin USING (status = 'ACTIVE');
CREATE POLICY authorization_revisions_runtime_read ON iam.authorization_revisions
  FOR SELECT TO s1_iam_runtime USING (tenant_id = iam.current_tenant_id());
CREATE POLICY role_templates_runtime_read ON iam.role_templates
  FOR SELECT TO s1_iam_runtime USING (tenant_id = iam.current_tenant_id());

CREATE POLICY tenants_admin_scope ON iam.tenants
  FOR ALL TO s1_iam_admin
  USING (id = iam.current_tenant_id())
  WITH CHECK (id = iam.current_tenant_id());
CREATE POLICY principals_admin_scope ON iam.principals
  FOR ALL TO s1_iam_admin
  USING (id = iam.current_principal_id() OR EXISTS (
    SELECT 1 FROM iam.tenant_memberships membership
    WHERE membership.principal_id = principals.id AND membership.tenant_id = iam.current_tenant_id()
  ))
  WITH CHECK (true);
CREATE POLICY memberships_admin_scope ON iam.tenant_memberships
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY role_templates_admin_scope ON iam.role_templates
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY role_bindings_admin_scope ON iam.role_bindings
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY site_bindings_admin_scope ON iam.site_bindings
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY explicit_denies_admin_scope ON iam.explicit_denies
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY authorization_revisions_admin_scope ON iam.authorization_revisions
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY service_accounts_admin_scope ON iam.service_accounts
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY api_credentials_admin_scope ON iam.api_credentials
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY admin_audit_intents_admin_scope ON iam.admin_audit_intents
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());
CREATE POLICY admin_outbox_admin_scope ON iam.admin_outbox
  FOR ALL TO s1_iam_admin USING (tenant_id = iam.current_tenant_id()) WITH CHECK (tenant_id = iam.current_tenant_id());

GRANT SELECT ON iam.capability_catalog_revisions, iam.authorization_revisions, iam.role_templates TO s1_iam_runtime;
GRANT EXECUTE ON FUNCTION iam.resolve_principal_identity(text, text) TO s1_iam_admin;
GRANT SELECT, UPDATE (display_name, timezone, currency, country, status, revision, updated_at) ON iam.tenants TO s1_iam_admin;
GRANT SELECT, INSERT, UPDATE ON iam.principals, iam.tenant_memberships, iam.role_templates,
  iam.role_bindings, iam.site_bindings, iam.explicit_denies, iam.authorization_revisions,
  iam.service_accounts, iam.api_credentials, iam.admin_audit_intents, iam.admin_outbox TO s1_iam_admin;

REVOKE ALL ON iam.capability_catalog_revisions, iam.authorization_revisions, iam.role_templates,
  iam.service_accounts, iam.api_credentials, iam.admin_audit_intents, iam.admin_outbox FROM PUBLIC;

RESET ROLE;
COMMIT;
