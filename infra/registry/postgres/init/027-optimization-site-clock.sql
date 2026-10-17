-- The optimization service prices its baseline at the Site's latest fully priced day (#575), which
-- it reads from the Telemetry Query Service by Site-local day, so it reads the Site's clock.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE POLICY sites_optimization_scope ON core_registry.sites FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(id));
GRANT SELECT (tenant_id, id, timezone) ON core_registry.sites TO optimization_runtime;

RESET ROLE;
COMMIT;
