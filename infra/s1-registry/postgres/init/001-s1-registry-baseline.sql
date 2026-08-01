BEGIN;

SET LOCAL ROLE s1_iam_migrator;

CREATE OR REPLACE FUNCTION iam.is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT (get_byte(uuid_send(value), 6) >> 4) = 7
     AND (get_byte(uuid_send(value), 8) >> 6) = 2
$$;

CREATE OR REPLACE FUNCTION iam.current_principal_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.principal_id', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION iam.current_acting_organization_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.acting_organization_id', true), '')::uuid
$$;

CREATE TABLE IF NOT EXISTS iam.principals (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  external_issuer text NOT NULL,
  external_subject text NOT NULL,
  display_name text NOT NULL,
  email text NOT NULL,
  status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (external_issuer, external_subject),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.organization_memberships (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (iam.is_uuid_v7(organization_id)),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REVOKED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, principal_id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.role_bindings (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (iam.is_uuid_v7(organization_id)),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  role_key text NOT NULL,
  actions text[] NOT NULL CHECK (cardinality(actions) > 0),
  effect text NOT NULL CHECK (effect IN ('ALLOW', 'DENY')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, principal_id, role_key),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.site_bindings (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  acting_organization_id uuid NOT NULL CHECK (iam.is_uuid_v7(acting_organization_id)),
  owning_organization_id uuid NOT NULL CHECK (iam.is_uuid_v7(owning_organization_id)),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  actions text[] NOT NULL CHECK (cardinality(actions) > 0),
  effect text NOT NULL CHECK (effect IN ('ALLOW', 'DENY')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (acting_organization_id, site_id, principal_id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.explicit_denies (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  acting_organization_id uuid NOT NULL CHECK (iam.is_uuid_v7(acting_organization_id)),
  owning_organization_id uuid NOT NULL CHECK (iam.is_uuid_v7(owning_organization_id)),
  site_id uuid CHECK (site_id IS NULL OR iam.is_uuid_v7(site_id)),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  action text NOT NULL,
  reason_code text NOT NULL,
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.policies (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (iam.is_uuid_v7(organization_id)),
  policy_key text NOT NULL,
  policy_revision bigint NOT NULL CHECK (policy_revision > 0),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'RETIRED')),
  document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, policy_key, policy_revision),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS organization_memberships_principal_org_idx
  ON iam.organization_memberships (principal_id, organization_id, status);
CREATE INDEX IF NOT EXISTS role_bindings_principal_org_idx
  ON iam.role_bindings (principal_id, organization_id, effect);
CREATE INDEX IF NOT EXISTS site_bindings_principal_site_idx
  ON iam.site_bindings (principal_id, site_id, effect);
CREATE INDEX IF NOT EXISTS explicit_denies_principal_scope_idx
  ON iam.explicit_denies (principal_id, acting_organization_id, site_id, action);

ALTER TABLE iam.principals ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.organization_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.role_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.site_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.explicit_denies ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.principals FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.organization_memberships FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.role_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.site_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.explicit_denies FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.policies FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS principal_self_scope ON iam.principals;
CREATE POLICY principal_self_scope ON iam.principals
  FOR SELECT TO s1_iam_runtime
  USING (id = iam.current_principal_id());

DROP POLICY IF EXISTS memberships_principal_scope ON iam.organization_memberships;
CREATE POLICY memberships_principal_scope ON iam.organization_memberships
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id());

DROP POLICY IF EXISTS role_bindings_principal_scope ON iam.role_bindings;
CREATE POLICY role_bindings_principal_scope ON iam.role_bindings
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id());

DROP POLICY IF EXISTS site_bindings_principal_scope ON iam.site_bindings;
CREATE POLICY site_bindings_principal_scope ON iam.site_bindings
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id());

DROP POLICY IF EXISTS explicit_denies_principal_scope ON iam.explicit_denies;
CREATE POLICY explicit_denies_principal_scope ON iam.explicit_denies
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id());

DROP POLICY IF EXISTS policies_acting_organization_scope ON iam.policies;
CREATE POLICY policies_acting_organization_scope ON iam.policies
  FOR SELECT TO s1_iam_runtime
  USING (organization_id = iam.current_acting_organization_id());

GRANT SELECT ON iam.principals, iam.organization_memberships, iam.role_bindings,
  iam.site_bindings, iam.explicit_denies, iam.policies TO s1_iam_runtime;
REVOKE ALL ON ALL TABLES IN SCHEMA iam FROM PUBLIC;

RESET ROLE;
SET LOCAL ROLE s1_core_migrator;

CREATE OR REPLACE FUNCTION core_registry.is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT (get_byte(uuid_send(value), 6) >> 4) = 7
     AND (get_byte(uuid_send(value), 8) >> 6) = 2
$$;

CREATE OR REPLACE FUNCTION core_registry.current_authorized_organization_ids()
RETURNS uuid[]
LANGUAGE sql
STABLE
AS $$
  SELECT COALESCE(NULLIF(current_setting('app.authorized_organization_ids', true), ''), '{}')::uuid[]
$$;

CREATE OR REPLACE FUNCTION core_registry.current_authorized_site_ids()
RETURNS uuid[]
LANGUAGE sql
STABLE
AS $$
  SELECT COALESCE(NULLIF(current_setting('app.authorized_site_ids', true), ''), '{}')::uuid[]
$$;

CREATE OR REPLACE FUNCTION core_registry.is_authorized_organization(value uuid)
RETURNS boolean
LANGUAGE sql
STABLE
STRICT
AS $$
  SELECT value = ANY (core_registry.current_authorized_organization_ids())
$$;

CREATE OR REPLACE FUNCTION core_registry.is_authorized_site(organization_value uuid, site_value uuid)
RETURNS boolean
LANGUAGE sql
STABLE
STRICT
AS $$
  SELECT core_registry.is_authorized_organization(organization_value)
      OR site_value = ANY (core_registry.current_authorized_site_ids())
$$;

CREATE OR REPLACE FUNCTION core_registry.enforce_iana_timezone()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_timezone_names WHERE name = NEW.timezone) THEN
    RAISE EXCEPTION 'invalid IANA timezone' USING ERRCODE = '22023';
  END IF;
  RETURN NEW;
END
$$;

CREATE TABLE IF NOT EXISTS core_registry.organizations (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  code text NOT NULL,
  display_name text NOT NULL,
  status text NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (code),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.sites (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  code text NOT NULL,
  display_name text NOT NULL,
  timezone text NOT NULL,
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, code),
  UNIQUE (organization_id, id),
  FOREIGN KEY (organization_id) REFERENCES core_registry.organizations(id),
  CHECK (updated_at >= created_at)
);

DROP TRIGGER IF EXISTS sites_iana_timezone ON core_registry.sites;
CREATE TRIGGER sites_iana_timezone
BEFORE INSERT OR UPDATE OF timezone ON core_registry.sites
FOR EACH ROW EXECUTE FUNCTION core_registry.enforce_iana_timezone();

CREATE TABLE IF NOT EXISTS core_registry.equipment (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  code text NOT NULL,
  display_name text NOT NULL,
  equipment_type text NOT NULL,
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, site_id, code),
  UNIQUE (organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id) REFERENCES core_registry.sites(organization_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.devices (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  code text NOT NULL,
  display_name text NOT NULL,
  device_type text NOT NULL,
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, site_id, code),
  UNIQUE (organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id) REFERENCES core_registry.sites(organization_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.device_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  equipment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(equipment_id)),
  binding_role text NOT NULL,
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id, device_id) REFERENCES core_registry.devices(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(organization_id, site_id, id),
  CHECK (device_id <> equipment_id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS device_bindings_active_device_role_uidx
  ON core_registry.device_bindings (organization_id, site_id, device_id, binding_role)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.external_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(integration_instance_id)),
  provider text NOT NULL,
  external_entity_type text NOT NULL,
  external_id text NOT NULL,
  binding_status text NOT NULL CHECK (binding_status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id) REFERENCES core_registry.sites(organization_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS external_bindings_active_external_key_uidx
  ON core_registry.external_bindings (integration_instance_id, external_entity_type, external_id)
  WHERE binding_status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.legacy_resource_maps (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid CHECK (site_id IS NULL OR core_registry.is_uuid_v7(site_id)),
  source_system text NOT NULL,
  source_table text NOT NULL,
  source_key text NOT NULL,
  target_resource_type text NOT NULL CHECK (target_resource_type IN ('ORGANIZATION', 'SITE', 'EQUIPMENT', 'DEVICE', 'DEVICE_BINDING', 'EXTERNAL_BINDING')),
  target_resource_id uuid CHECK (target_resource_id IS NULL OR core_registry.is_uuid_v7(target_resource_id)),
  mapping_state text NOT NULL CHECK (mapping_state IN ('DISCOVERED', 'MAPPED', 'VERIFIED', 'QUARANTINED', 'RETIRED')),
  transformation_version text NOT NULL,
  batch_id text NOT NULL,
  source_watermark text NOT NULL,
  source_row_hash text NOT NULL CHECK (source_row_hash ~ '^[a-f0-9]{64}$'),
  relation_evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(relation_evidence) = 'object'),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (source_system, source_table, source_key),
  CHECK ((mapping_state IN ('DISCOVERED', 'QUARANTINED') AND target_resource_id IS NULL)
      OR (mapping_state IN ('MAPPED', 'VERIFIED', 'RETIRED') AND target_resource_id IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.migration_provenance (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  legacy_resource_map_id uuid NOT NULL REFERENCES core_registry.legacy_resource_maps(id),
  source_system text NOT NULL,
  source_table text NOT NULL,
  source_key text NOT NULL,
  target_resource_type text NOT NULL,
  target_resource_id uuid CHECK (target_resource_id IS NULL OR core_registry.is_uuid_v7(target_resource_id)),
  transformation_version text NOT NULL,
  batch_id text NOT NULL,
  source_watermark text NOT NULL,
  source_row_hash text NOT NULL CHECK (source_row_hash ~ '^[a-f0-9]{64}$'),
  result text NOT NULL CHECK (result IN ('IMPORTED', 'SKIPPED', 'QUARANTINED', 'VERIFIED')),
  created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS core_registry.migration_quarantine (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid CHECK (organization_id IS NULL OR core_registry.is_uuid_v7(organization_id)),
  source_system text NOT NULL,
  source_table text NOT NULL,
  source_key text NOT NULL,
  reason_code text NOT NULL,
  source_row_hash text NOT NULL CHECK (source_row_hash ~ '^[a-f0-9]{64}$'),
  payload_metadata jsonb NOT NULL CHECK (jsonb_typeof(payload_metadata) = 'object'),
  detected_at timestamptz NOT NULL,
  resolved_at timestamptz,
  resolution text,
  CHECK ((resolved_at IS NULL AND resolution IS NULL) OR (resolved_at IS NOT NULL AND resolution IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS organizations_registry_page_idx
  ON core_registry.organizations (display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS sites_registry_page_idx
  ON core_registry.sites (organization_id, display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS equipment_registry_page_idx
  ON core_registry.equipment (organization_id, site_id, display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS devices_registry_page_idx
  ON core_registry.devices (organization_id, site_id, display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS device_bindings_registry_page_idx
  ON core_registry.device_bindings (organization_id, site_id, binding_role COLLATE "C", id);
CREATE INDEX IF NOT EXISTS legacy_resource_maps_scope_state_idx
  ON core_registry.legacy_resource_maps (organization_id, site_id, mapping_state, source_system, source_table, source_key);
CREATE INDEX IF NOT EXISTS migration_quarantine_open_idx
  ON core_registry.migration_quarantine (detected_at, source_system, source_table)
  WHERE resolved_at IS NULL;

ALTER TABLE core_registry.organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sites ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.equipment ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.external_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.legacy_resource_maps ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.migration_provenance ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.migration_quarantine ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.organizations FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sites FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.equipment FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.devices FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.external_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.legacy_resource_maps FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.migration_provenance FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.migration_quarantine FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS organizations_runtime_scope ON core_registry.organizations;
CREATE POLICY organizations_runtime_scope ON core_registry.organizations
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_organization(id));

DROP POLICY IF EXISTS sites_runtime_scope ON core_registry.sites;
CREATE POLICY sites_runtime_scope ON core_registry.sites
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, id));

DROP POLICY IF EXISTS equipment_runtime_scope ON core_registry.equipment;
CREATE POLICY equipment_runtime_scope ON core_registry.equipment
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS devices_runtime_scope ON core_registry.devices;
CREATE POLICY devices_runtime_scope ON core_registry.devices
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS device_bindings_runtime_scope ON core_registry.device_bindings;
CREATE POLICY device_bindings_runtime_scope ON core_registry.device_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS external_bindings_runtime_scope ON core_registry.external_bindings;
CREATE POLICY external_bindings_runtime_scope ON core_registry.external_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS verified_legacy_maps_runtime_scope ON core_registry.legacy_resource_maps;
CREATE POLICY verified_legacy_maps_runtime_scope ON core_registry.legacy_resource_maps
  FOR SELECT TO s1_core_runtime
  USING (mapping_state = 'VERIFIED'
     AND (core_registry.is_authorized_organization(organization_id)
       OR (site_id IS NOT NULL AND core_registry.is_authorized_site(organization_id, site_id))));

DROP POLICY IF EXISTS legacy_maps_operator_scope ON core_registry.legacy_resource_maps;
CREATE POLICY legacy_maps_operator_scope ON core_registry.legacy_resource_maps
  FOR ALL TO s1_migration_operator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS provenance_operator_scope ON core_registry.migration_provenance;
CREATE POLICY provenance_operator_scope ON core_registry.migration_provenance
  FOR ALL TO s1_migration_operator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS quarantine_operator_scope ON core_registry.migration_quarantine;
CREATE POLICY quarantine_operator_scope ON core_registry.migration_quarantine
  FOR ALL TO s1_migration_operator
  USING (true)
  WITH CHECK (true);

GRANT SELECT ON core_registry.organizations, core_registry.sites, core_registry.equipment,
  core_registry.devices, core_registry.device_bindings, core_registry.external_bindings,
  core_registry.legacy_resource_maps TO s1_core_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.legacy_resource_maps,
  core_registry.migration_provenance, core_registry.migration_quarantine TO s1_migration_operator;
REVOKE ALL ON ALL TABLES IN SCHEMA core_registry FROM PUBLIC;

RESET ROLE;
COMMIT;
