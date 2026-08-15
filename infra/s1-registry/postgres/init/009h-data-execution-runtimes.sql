BEGIN;
SET LOCAL ROLE s1_core_migrator;

-- Metric execution reads released definitions and owns only calculation-run state.
CREATE POLICY metrics_metric_engine_scope ON core_registry.metrics
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_versions_metric_engine_scope ON core_registry.metric_versions
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_dependencies_metric_engine_scope ON core_registry.metric_dependencies
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_bindings_metric_engine_scope ON core_registry.metric_bindings
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_calculation_runs_metric_engine_scope ON core_registry.metric_calculation_runs
  FOR ALL TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

GRANT SELECT ON core_registry.metrics, core_registry.metric_versions,
  core_registry.metric_dependencies, core_registry.metric_bindings TO metric_engine_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.metric_calculation_runs TO metric_engine_runtime;

-- Settlement execution consumes released Metric results plus Tariff and Boundary
-- definitions. Energy Edge and Meter Binding reads below are lineage/accounting-boundary
-- evidence only; settlement values are never recalculated from raw Telemetry facts.
CREATE POLICY energy_edges_settlement_lineage_scope ON core_registry.energy_edges
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY meter_bindings_settlement_lineage_scope ON core_registry.meter_bindings
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_boundaries_runtime_exec_scope ON core_registry.settlement_boundaries
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_boundary_edges_runtime_exec_scope ON core_registry.settlement_boundary_edges
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariffs_runtime_exec_scope ON core_registry.tariffs
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_versions_runtime_exec_scope ON core_registry.tariff_versions
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_periods_runtime_exec_scope ON core_registry.tariff_periods
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_assignments_runtime_exec_scope ON core_registry.tariff_assignments
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_metric_bindings_runtime_exec_scope ON core_registry.settlement_metric_bindings
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_bindings_settlement_scope ON core_registry.metric_bindings
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_versions_settlement_scope ON core_registry.metric_versions
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id());

CREATE POLICY settlement_periods_runtime_exec_scope ON core_registry.settlement_periods
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_snapshots_runtime_exec_scope ON core_registry.settlement_snapshots
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_change_candidates_runtime_exec_scope ON core_registry.settlement_change_candidates
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_revisions_runtime_exec_scope ON core_registry.settlement_revisions
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

GRANT SELECT ON core_registry.energy_edges, core_registry.meter_bindings,
  core_registry.settlement_boundaries, core_registry.settlement_boundary_edges,
  core_registry.tariffs, core_registry.tariff_versions, core_registry.tariff_periods,
  core_registry.tariff_assignments, core_registry.settlement_metric_bindings,
  core_registry.metric_bindings, core_registry.metric_versions TO settlement_runtime;
GRANT SELECT, UPDATE ON core_registry.settlement_periods TO settlement_runtime;
GRANT SELECT, INSERT ON core_registry.settlement_snapshots TO settlement_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.settlement_change_candidates TO settlement_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.settlement_revisions TO settlement_runtime;

-- Forecast execution owns Forecast Job/Snapshot publication state and reads the
-- released deployment/model/input lineage required by the ForecastEngine.
CREATE POLICY forecast_models_runtime_exec_scope ON core_registry.forecast_models
  FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY forecast_model_versions_runtime_exec_scope ON core_registry.forecast_model_versions
  FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_feature_set_versions_runtime_exec_scope ON core_registry.forecast_feature_set_versions
  FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY forecast_deployments_runtime_exec_scope ON core_registry.forecast_deployments
  FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_input_snapshots_runtime_exec_scope ON core_registry.forecast_input_snapshots
  FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_jobs_runtime_exec_scope ON core_registry.forecast_jobs
  FOR ALL TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_snapshots_runtime_exec_scope ON core_registry.forecast_snapshots
  FOR ALL TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT ON core_registry.forecast_models, core_registry.forecast_model_versions,
  core_registry.forecast_feature_set_versions, core_registry.forecast_deployments,
  core_registry.forecast_input_snapshots TO forecast_runtime;
GRANT SELECT, UPDATE ON core_registry.forecast_jobs TO forecast_runtime;
GRANT SELECT, INSERT ON core_registry.forecast_snapshots TO forecast_runtime;

-- Optimization computes plans and evaluation evidence only. It has no Command or
-- MQTT grants, preserving Control/Safety as the sole execution authority.
CREATE POLICY optimization_policy_versions_runtime_exec_scope ON core_registry.optimization_policy_versions
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY optimization_input_snapshots_runtime_exec_scope ON core_registry.optimization_input_snapshots
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY optimization_input_resources_runtime_exec_scope ON core_registry.optimization_input_resources
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY optimization_runs_runtime_exec_scope ON core_registry.optimization_runs
  FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY dispatch_plans_optimization_exec_scope ON core_registry.dispatch_plans
  FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY dispatch_intervals_optimization_exec_scope ON core_registry.dispatch_intervals
  FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT ON core_registry.optimization_policy_versions, core_registry.optimization_input_snapshots,
  core_registry.optimization_input_resources TO optimization_runtime;
GRANT SELECT, UPDATE ON core_registry.optimization_runs TO optimization_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.dispatch_plans TO optimization_runtime;
GRANT SELECT, INSERT ON core_registry.dispatch_intervals TO optimization_runtime;

RESET ROLE;
COMMIT;
