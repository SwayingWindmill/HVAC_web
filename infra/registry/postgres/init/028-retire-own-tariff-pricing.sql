-- The Metric engine and savings verification no longer price energy at the Tariff themselves
-- (#575): the Telemetry Query Service prices it in Cube. Their reads of the grid import
-- boundary and the Tariff tables, granted in 020 and 021, go.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

DROP POLICY IF EXISTS settlement_boundaries_metric_scope ON core_registry.settlement_boundaries;
DROP POLICY IF EXISTS tariff_assignments_metric_scope ON core_registry.tariff_assignments;
DROP POLICY IF EXISTS tariff_versions_metric_scope ON core_registry.tariff_versions;
DROP POLICY IF EXISTS tariff_periods_metric_scope ON core_registry.tariff_periods;
REVOKE SELECT ON core_registry.settlement_boundaries, core_registry.tariff_assignments,
  core_registry.tariff_versions, core_registry.tariff_periods FROM metric_engine_runtime;

DROP POLICY IF EXISTS settlement_boundaries_mv_scope ON core_registry.settlement_boundaries;
DROP POLICY IF EXISTS tariffs_mv_scope ON core_registry.tariffs;
DROP POLICY IF EXISTS tariff_versions_mv_scope ON core_registry.tariff_versions;
DROP POLICY IF EXISTS tariff_periods_mv_scope ON core_registry.tariff_periods;
DROP POLICY IF EXISTS tariff_assignments_mv_scope ON core_registry.tariff_assignments;
REVOKE SELECT ON core_registry.settlement_boundaries, core_registry.tariffs, core_registry.tariff_versions,
  core_registry.tariff_periods, core_registry.tariff_assignments FROM mv_runtime;

RESET ROLE;
COMMIT;
