-- Runtime wiring for v2 hourly energy facts, layered efficiency / meter balance diagnostics,
-- and condition anchor readers across analytics, telemetry query, and Cube identities.

GRANT SELECT ON telemetry_history.observations TO analytics_projector_reader;
GRANT SELECT ON analytics.energy_hourly TO analytics_projector_reader;
GRANT SELECT ON analytics.energy_hourly_v2 TO analytics_projector_reader;
GRANT SELECT ON analytics.site_condition_anchors_hourly_latest TO analytics_projector_reader;
GRANT SELECT ON analytics.site_meter_balance_hourly TO analytics_projector_reader;

GRANT SELECT ON analytics.energy_hourly TO telemetry_query_history_reader;
GRANT SELECT ON analytics.energy_hourly_v2 TO telemetry_query_history_reader;
GRANT SELECT ON analytics.site_meter_balance_hourly TO telemetry_query_history_reader;
GRANT SELECT ON analytics.site_condition_anchors_hourly_latest TO telemetry_query_history_reader;
GRANT SELECT ON analytics.thermal_cop_asof_audit TO telemetry_query_history_reader;
GRANT SELECT ON analytics.energy_interval_facts_visible TO telemetry_query_history_reader;
GRANT SELECT ON analytics.site_total_submeters_visible TO telemetry_query_history_reader;

GRANT SELECT ON analytics.site_condition_anchors_hourly_latest TO cube_analytics_reader;

CREATE OR REPLACE VIEW analytics.energy_hourly_effective
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  v2_agg.v2_tenant_id AS tenant_id,
  v2_agg.v2_site_id AS site_id,
  v2_agg.v2_point_id AS point_id,
  v2_agg.v2_hour AS hour,
  v2_agg.v2_accounting_scope AS accounting_scope,
  v2_agg.v2_energy_type AS energy_type,
  v2_agg.v2_quality AS quality,
  v2_agg.v2_load_device_id AS load_device_id,
  v2_agg.v2_energy_kwh AS energy_kwh,
  v2_agg.v2_interval_count AS interval_count,
  v2_agg.v2_data_watermark AS data_watermark,
  v2_agg.v2_dataset_revision AS dataset_revision
FROM (
  SELECT
    f.tenant_id AS v2_tenant_id,
    f.site_id AS v2_site_id,
    f.point_id AS v2_point_id,
    toDateTime64(f.hour, 3, 'UTC') AS v2_hour,
    f.accounting_scope AS v2_accounting_scope,
    f.energy_type AS v2_energy_type,
    f.quality AS v2_quality,
    nullIf(f.load_device_id, toUUID('00000000-0000-0000-0000-000000000000')) AS v2_load_device_id,
    toFloat64(sum(f.energy_kwh)) AS v2_energy_kwh,
    sum(f.interval_count) AS v2_interval_count,
    max(f.data_watermark) AS v2_data_watermark,
    max(f.dataset_revision) AS v2_dataset_revision
  FROM analytics.energy_hourly_v2 AS f
  GROUP BY
    f.tenant_id,
    f.site_id,
    f.point_id,
    f.hour,
    f.accounting_scope,
    f.energy_type,
    f.quality,
    f.load_device_id
  HAVING sum(f.interval_count) != 0
) AS v2_agg
UNION ALL
SELECT
  v1.tenant_id AS tenant_id,
  v1.site_id AS site_id,
  v1.point_id AS point_id,
  v1.hour AS hour,
  v1.accounting_scope AS accounting_scope,
  v1.energy_type AS energy_type,
  v1.quality AS quality,
  v1.load_device_id AS load_device_id,
  v1.energy_kwh AS energy_kwh,
  v1.interval_count AS interval_count,
  v1.data_watermark AS data_watermark,
  v1.dataset_revision AS dataset_revision
FROM analytics.energy_hourly AS v1
WHERE (
  v1.tenant_id,
  v1.site_id,
  v1.point_id,
  v1.hour,
  v1.accounting_scope,
  v1.energy_type,
  v1.quality
) NOT IN (
  SELECT
    v2.tenant_id,
    v2.site_id,
    v2.point_id,
    toDateTime64(v2.hour, 3, 'UTC'),
    v2.accounting_scope,
    v2.energy_type,
    v2.quality
  FROM analytics.energy_hourly_v2 AS v2
);

GRANT SELECT ON analytics.energy_hourly_effective TO analytics_projector_reader, cube_analytics_reader, telemetry_query_history_reader;
