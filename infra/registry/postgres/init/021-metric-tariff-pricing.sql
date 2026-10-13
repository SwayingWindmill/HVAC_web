-- The daily energy cost Metric prices each hour of a Site's energy at the import Tariff in
-- effect then (TARIFF_ENERGY_COST). The Metric engine reads the Site's grid import boundary, its
-- Tariff assignments, versions and periods, within its Tenant and Site scope.
CREATE POLICY settlement_boundaries_metric_scope ON core_registry.settlement_boundaries FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_assignments_metric_scope ON core_registry.tariff_assignments FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_versions_metric_scope ON core_registry.tariff_versions FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_periods_metric_scope ON core_registry.tariff_periods FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

GRANT SELECT ON core_registry.settlement_boundaries, core_registry.tariff_assignments,
  core_registry.tariff_versions, core_registry.tariff_periods TO metric_engine_runtime;
