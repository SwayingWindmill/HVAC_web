-- Runtime wiring for v2 hourly energy facts, layered efficiency / meter balance diagnostics,
-- DEFINER-scoped active power & operating condition views, and reader grants across analytics,
-- telemetry query, and Cube identities.

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

CREATE OR REPLACE VIEW analytics.hourly_active_power_profile
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  profile.profile_tenant_id AS tenant_id,
  profile.profile_site_id AS site_id,
  profile.profile_device_id AS device_id,
  profile.profile_hour_utc AS hour_utc,
  profile.profile_mean_kw AS mean_kw,
  profile.profile_sample_count AS sample_count
FROM (
  SELECT
    assumeNotNull(raw.tenant_id) AS profile_tenant_id,
    assumeNotNull(raw.site_id) AS profile_site_id,
    assumeNotNull(raw.device_id) AS profile_device_id,
    toDateTime(toStartOfHour(raw.sampled_at), 'UTC') AS profile_hour_utc,
    toFloat64(avg(assumeNotNull(raw.value_number))) AS profile_mean_kw,
    toUInt64(count()) AS profile_sample_count
  FROM telemetry_history.observations AS raw
  WHERE raw.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND raw.quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
    AND raw.tenant_id IS NOT NULL
    AND raw.site_id IS NOT NULL
    AND raw.device_id IS NOT NULL
    AND raw.value_number IS NOT NULL
    AND isFinite(raw.value_number)
    AND raw.value_number > 0
    AND raw.telemetry_key IN (
      'hvac_meter.active_power',
      'plant.active_power',
      'active_power',
      'power_kw',
      'chiller.power'
    )
  GROUP BY
    profile_tenant_id,
    profile_site_id,
    profile_device_id,
    profile_hour_utc
) AS profile;

CREATE OR REPLACE VIEW analytics.operating_condition_observations
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  cond.cond_tenant_id AS tenant_id,
  cond.cond_site_id AS site_id,
  cond.cond_device_id AS device_id,
  cond.cond_telemetry_key AS telemetry_key,
  cond.cond_sampled_at AS sampled_at,
  cond.cond_hour_utc AS hour_utc,
  cond.cond_value_number AS value_number,
  cond.cond_value_text AS value_text
FROM (
  SELECT
    assumeNotNull(raw.tenant_id) AS cond_tenant_id,
    assumeNotNull(raw.site_id) AS cond_site_id,
    assumeNotNull(raw.device_id) AS cond_device_id,
    raw.telemetry_key AS cond_telemetry_key,
    raw.sampled_at AS cond_sampled_at,
    toDateTime(toStartOfHour(raw.sampled_at), 'UTC') AS cond_hour_utc,
    raw.value_number AS cond_value_number,
    coalesce(
      raw.value_string,
      if(
        raw.value_boolean IS NOT NULL,
        toNullable(if(assumeNotNull(raw.value_boolean) != 0, 'true', 'false')),
        CAST(NULL, 'Nullable(String)')
      ),
      raw.value_json
    ) AS cond_value_text
  FROM telemetry_history.observations AS raw
  WHERE raw.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND raw.quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
    AND raw.tenant_id IS NOT NULL
    AND raw.site_id IS NOT NULL
    AND raw.device_id IS NOT NULL
    AND raw.telemetry_key IN (
      'outdoor_wet_bulb_temp',
      'weather.wet_bulb_temp',
      'ambient.wet_bulb_temp',
      'wet_bulb_temp',
      'outdoor_dry_bulb_temp',
      'weather.dry_bulb_temp',
      'ambient.dry_bulb_temp',
      'dry_bulb_temp',
      'ambient_temp',
      'outdoor_temp',
      'plant.control_mode',
      'ai.control_mode',
      'control_mode',
      'ai_closed_loop',
      'ai_enabled',
      'plant.ai_enabled',
      'chilled_water.rated_capacity_kw',
      'plant.rated_capacity_kw'
    )
) AS cond;

GRANT SELECT ON analytics.hourly_active_power_profile TO analytics_projector_reader;
GRANT SELECT ON analytics.operating_condition_observations TO analytics_projector_reader;

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
