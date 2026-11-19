-- Hourly site meter balance view comparing physical SITE electricity meters against BRANCH sub-meters,
-- plus hourly operating condition anchors for weather, load, and AI closed-loop share normalization.

CREATE OR REPLACE VIEW analytics.site_meter_balance_hourly
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
WITH
  site_hours AS (
    SELECT
      e.tenant_id AS tenant_id,
      e.site_id AS site_id,
      e.hour AS hour,
      toFloat64(sum(e.energy_kwh)) AS site_total_kwh
    FROM analytics.energy_hourly AS e
    WHERE e.energy_type = 'electricity'
      AND e.quality = 'VALID'
      AND e.accounting_scope = 'SITE'
    GROUP BY e.tenant_id, e.site_id, e.hour
  ),
  branch_hours AS (
    SELECT
      e.tenant_id AS tenant_id,
      e.site_id AS site_id,
      e.hour AS hour,
      toFloat64(sum(e.energy_kwh)) AS branch_total_kwh,
      toUInt64(uniqExact(e.point_id)) AS branch_point_count
    FROM analytics.energy_hourly AS e
    WHERE e.energy_type = 'electricity'
      AND e.quality = 'VALID'
      AND e.accounting_scope = 'BRANCH'
    GROUP BY e.tenant_id, e.site_id, e.hour
  ),
  joined AS (
    SELECT
      tenant_id,
      site_id,
      hour,
      toFloat64(ifNull(site_hours.site_total_kwh, 0.0)) AS main_kwh,
      toFloat64(ifNull(branch_hours.branch_total_kwh, 0.0)) AS submeter_kwh,
      toUInt64(ifNull(branch_hours.branch_point_count, 0)) AS meter_count
    FROM site_hours
    FULL OUTER JOIN branch_hours USING (tenant_id, site_id, hour)
  )
SELECT
  tenant_id,
  site_id,
  hour,
  main_kwh AS site_kwh,
  submeter_kwh AS submeter_sum_kwh,
  main_kwh - submeter_kwh AS diff_kwh,
  if(
    main_kwh > 0,
    (main_kwh - submeter_kwh) / main_kwh,
    CAST(NULL, 'Nullable(Float64)')
  ) AS imbalance_ratio,
  meter_count AS submeter_count,
  CAST(
    multiIf(
      main_kwh <= 0, 'NO_MAIN_METER',
      abs((main_kwh - submeter_kwh) / main_kwh) <= 0.05, 'BALANCED',
      (main_kwh - submeter_kwh) / main_kwh > 0.05, 'UNMETERED_GAP',
      'SUBMETER_EXCEEDS_MAIN'
    ),
    'LowCardinality(String)'
  ) AS imbalance_status
FROM joined;

CREATE TABLE IF NOT EXISTS analytics.site_condition_anchors_hourly (
  tenant_id UUID,
  site_id UUID,
  hour DateTime('UTC'),
  outdoor_wet_bulb_c Nullable(Float64),
  outdoor_dry_bulb_c Nullable(Float64),
  plant_load_ratio Nullable(Float64),
  ai_closed_loop_share Nullable(Float64),
  sample_count UInt32,
  calc_version UInt64,
  updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(calc_version)
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, site_id, hour);

CREATE OR REPLACE VIEW analytics.site_condition_anchors_hourly_latest
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id,
  site_id,
  hour,
  outdoor_wet_bulb_c,
  outdoor_dry_bulb_c,
  plant_load_ratio,
  ai_closed_loop_share,
  sample_count,
  latest_calc_version AS calc_version,
  updated_at
FROM (
  SELECT
    f.tenant_id AS tenant_id,
    f.site_id AS site_id,
    f.hour AS hour,
    argMax(tuple(f.outdoor_wet_bulb_c), f.calc_version).1 AS outdoor_wet_bulb_c,
    argMax(tuple(f.outdoor_dry_bulb_c), f.calc_version).1 AS outdoor_dry_bulb_c,
    argMax(tuple(f.plant_load_ratio), f.calc_version).1 AS plant_load_ratio,
    argMax(tuple(f.ai_closed_loop_share), f.calc_version).1 AS ai_closed_loop_share,
    argMax(f.sample_count, f.calc_version) AS sample_count,
    max(f.calc_version) AS latest_calc_version,
    argMax(f.updated_at, f.calc_version) AS updated_at
  FROM analytics.site_condition_anchors_hourly AS f
  GROUP BY f.tenant_id, f.site_id, f.hour
);

GRANT SELECT ON analytics.site_meter_balance_hourly TO analytics_projector_reader, cube_analytics_reader, fdd_service_reader;
GRANT INSERT ON analytics.site_condition_anchors_hourly TO analytics_projector_writer;
GRANT SELECT ON analytics.site_condition_anchors_hourly TO analytics_projector_reader, mv_service_reader;
GRANT SELECT ON analytics.site_condition_anchors_hourly_latest TO analytics_projector_reader, mv_service_reader;
