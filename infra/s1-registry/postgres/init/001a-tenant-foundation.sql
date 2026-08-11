BEGIN;

SET LOCAL ROLE s1_iam_migrator;

CREATE OR REPLACE FUNCTION iam.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION iam.enforce_tenant_iana_timezone()
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

CREATE TABLE IF NOT EXISTS iam.tenants (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  code text NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 128),
  timezone text NOT NULL,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  country text NOT NULL CHECK (country ~ '^[A-Z]{2,8}$'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (code),
  CHECK (updated_at >= created_at)
);

DROP TRIGGER IF EXISTS tenants_iana_timezone ON iam.tenants;
CREATE TRIGGER tenants_iana_timezone
BEFORE INSERT OR UPDATE OF timezone ON iam.tenants
FOR EACH ROW EXECUTE FUNCTION iam.enforce_tenant_iana_timezone();

ALTER TABLE iam.organization_memberships ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE iam.role_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE iam.site_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE iam.explicit_denies ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE iam.policies ADD COLUMN tenant_id uuid NOT NULL;

ALTER TABLE iam.organization_memberships
  ADD CONSTRAINT organization_memberships_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);
ALTER TABLE iam.role_bindings
  ADD CONSTRAINT role_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);
ALTER TABLE iam.site_bindings
  ADD CONSTRAINT site_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);
ALTER TABLE iam.explicit_denies
  ADD CONSTRAINT explicit_denies_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);
ALTER TABLE iam.policies
  ADD CONSTRAINT policies_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);

CREATE INDEX organization_memberships_tenant_principal_idx
  ON iam.organization_memberships (tenant_id, principal_id, organization_id, status);
CREATE INDEX role_bindings_tenant_principal_idx
  ON iam.role_bindings (tenant_id, principal_id, organization_id, effect);
CREATE INDEX site_bindings_tenant_principal_idx
  ON iam.site_bindings (tenant_id, principal_id, site_id, effect);
CREATE INDEX explicit_denies_tenant_principal_idx
  ON iam.explicit_denies (tenant_id, principal_id, acting_organization_id, site_id, action);
CREATE INDEX policies_tenant_org_idx
  ON iam.policies (tenant_id, organization_id, policy_key, status);

ALTER TABLE iam.tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenants_runtime_scope ON iam.tenants
  FOR SELECT TO s1_iam_runtime
  USING (id = iam.current_tenant_id());

GRANT SELECT ON iam.tenants TO s1_iam_runtime;
GRANT USAGE ON SCHEMA iam TO s1_core_migrator;
GRANT REFERENCES (id) ON iam.tenants TO s1_core_migrator;
REVOKE ALL ON iam.tenants FROM PUBLIC;

RESET ROLE;
SET LOCAL ROLE s1_core_migrator;

CREATE OR REPLACE FUNCTION core_registry.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;

ALTER TABLE core_registry.organizations ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.sites ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.equipment ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.devices ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.device_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.external_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.legacy_resource_maps ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.migration_provenance ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.migration_quarantine ADD COLUMN tenant_id uuid;

ALTER TABLE core_registry.organizations DROP CONSTRAINT IF EXISTS organizations_code_key;
ALTER TABLE core_registry.organizations
  ADD CONSTRAINT organizations_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT organizations_tenant_code_key UNIQUE (tenant_id, code),
  ADD CONSTRAINT organizations_tenant_id_key UNIQUE (tenant_id, id);

ALTER TABLE core_registry.sites
  ADD CONSTRAINT sites_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT sites_tenant_org_fk FOREIGN KEY (tenant_id, organization_id) REFERENCES core_registry.organizations(tenant_id, id),
  ADD CONSTRAINT sites_tenant_org_id_key UNIQUE (tenant_id, organization_id, id);

ALTER TABLE core_registry.equipment
  ADD CONSTRAINT equipment_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT equipment_tenant_site_fk FOREIGN KEY (tenant_id, organization_id, site_id) REFERENCES core_registry.sites(tenant_id, organization_id, id),
  ADD CONSTRAINT equipment_tenant_site_id_key UNIQUE (tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.devices
  ADD CONSTRAINT devices_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT devices_tenant_site_fk FOREIGN KEY (tenant_id, organization_id, site_id) REFERENCES core_registry.sites(tenant_id, organization_id, id),
  ADD CONSTRAINT devices_tenant_site_id_key UNIQUE (tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.device_bindings
  ADD CONSTRAINT device_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT device_bindings_tenant_device_fk FOREIGN KEY (tenant_id, organization_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT device_bindings_tenant_equipment_fk FOREIGN KEY (tenant_id, organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.external_bindings
  ADD CONSTRAINT external_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT external_bindings_tenant_site_fk FOREIGN KEY (tenant_id, organization_id, site_id) REFERENCES core_registry.sites(tenant_id, organization_id, id);

ALTER TABLE core_registry.legacy_resource_maps
  ADD CONSTRAINT legacy_resource_maps_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);
ALTER TABLE core_registry.migration_provenance
  ADD CONSTRAINT migration_provenance_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);
ALTER TABLE core_registry.migration_quarantine
  ADD CONSTRAINT migration_quarantine_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id);

CREATE INDEX organizations_tenant_page_idx
  ON core_registry.organizations (tenant_id, display_name COLLATE "C", id);
CREATE INDEX sites_tenant_page_idx
  ON core_registry.sites (tenant_id, organization_id, display_name COLLATE "C", id);
CREATE INDEX equipment_tenant_page_idx
  ON core_registry.equipment (tenant_id, organization_id, site_id, display_name COLLATE "C", id);
CREATE INDEX devices_tenant_page_idx
  ON core_registry.devices (tenant_id, organization_id, site_id, display_name COLLATE "C", id);
CREATE INDEX device_bindings_tenant_scope_idx
  ON core_registry.device_bindings (tenant_id, organization_id, site_id, device_id, equipment_id, binding_role);

DROP POLICY IF EXISTS organizations_runtime_scope ON core_registry.organizations;
CREATE POLICY organizations_runtime_scope ON core_registry.organizations
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id()
     AND core_registry.is_authorized_organization(id));

DROP POLICY IF EXISTS sites_runtime_scope ON core_registry.sites;
CREATE POLICY sites_runtime_scope ON core_registry.sites
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id()
     AND core_registry.is_authorized_site(organization_id, id));

DROP POLICY IF EXISTS equipment_runtime_scope ON core_registry.equipment;
CREATE POLICY equipment_runtime_scope ON core_registry.equipment
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id()
     AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS devices_runtime_scope ON core_registry.devices;
CREATE POLICY devices_runtime_scope ON core_registry.devices
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id()
     AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS device_bindings_runtime_scope ON core_registry.device_bindings;
CREATE POLICY device_bindings_runtime_scope ON core_registry.device_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id()
     AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS external_bindings_runtime_scope ON core_registry.external_bindings;
CREATE POLICY external_bindings_runtime_scope ON core_registry.external_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id()
     AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS verified_legacy_maps_runtime_scope ON core_registry.legacy_resource_maps;
CREATE POLICY verified_legacy_maps_runtime_scope ON core_registry.legacy_resource_maps
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id()
     AND mapping_state = 'VERIFIED'
     AND (core_registry.is_authorized_organization(organization_id)
       OR (site_id IS NOT NULL AND core_registry.is_authorized_site(organization_id, site_id))));

RESET ROLE;
COMMIT;
