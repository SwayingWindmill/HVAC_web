BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_legacy_migration_service') THEN
    CREATE ROLE s1_legacy_migration_service LOGIN PASSWORD 's1-legacy-migration-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

GRANT CONNECT ON DATABASE hvac_s1 TO s1_legacy_migration_service;
GRANT s1_migration_operator TO s1_legacy_migration_service;
GRANT USAGE ON SCHEMA core_registry TO s1_legacy_migration_service;

DROP POLICY IF EXISTS organizations_migration_scope ON core_registry.organizations;
CREATE POLICY organizations_migration_scope ON core_registry.organizations
  FOR ALL TO s1_migration_operator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS sites_migration_scope ON core_registry.sites;
CREATE POLICY sites_migration_scope ON core_registry.sites
  FOR ALL TO s1_migration_operator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS equipment_migration_scope ON core_registry.equipment;
CREATE POLICY equipment_migration_scope ON core_registry.equipment
  FOR ALL TO s1_migration_operator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS devices_migration_scope ON core_registry.devices;
CREATE POLICY devices_migration_scope ON core_registry.devices
  FOR ALL TO s1_migration_operator
  USING (true)
  WITH CHECK (true);

GRANT SELECT, INSERT ON core_registry.organizations, core_registry.sites,
  core_registry.equipment, core_registry.devices TO s1_migration_operator;
GRANT UPDATE (status, revision, updated_at) ON core_registry.organizations, core_registry.sites,
  core_registry.equipment, core_registry.devices TO s1_migration_operator;

CREATE UNIQUE INDEX IF NOT EXISTS migration_quarantine_open_source_uidx
  ON core_registry.migration_quarantine (source_system, source_table, source_key)
  WHERE resolved_at IS NULL;

COMMIT;
