\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

GRANT DELETE ON TABLE core_registry.site_calendar_holidays TO s1_core_writer;

GRANT SELECT ON TABLE core_registry.meter_bindings TO s1_core_writer;
DROP POLICY IF EXISTS meter_bindings_writer_select ON core_registry.meter_bindings;
CREATE POLICY meter_bindings_writer_select
  ON core_registry.meter_bindings
  FOR SELECT TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

RESET ROLE;

COMMIT;
