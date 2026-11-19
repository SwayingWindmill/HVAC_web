-- V2 hourly energy facts using fixed-precision Decimal64(6) and ReplacingMergeTree(calc_version)
-- so dirty-window recalculations remain idempotent without ALTER TABLE ... DELETE mutations,
-- plus an ASOF JOIN audit view aligning thermal cooling power and electrical input power.

CREATE TABLE IF NOT EXISTS analytics.energy_hourly_facts_v2
(
    tenant_id UUID,
    site_id UUID,
    energy_type LowCardinality(String),
    accounting_scope LowCardinality(String),
    load_device_id UUID,
    point_id UUID,
    quality LowCardinality(String),
    tariff_period LowCardinality(String),
    hour DateTime('UTC'),
    energy_kwh Decimal64(6),
    cost_cny Decimal64(6),
    interval_count Int64,
    calc_version UInt64,
    data_watermark DateTime64(3, 'UTC'),
    dataset_revision UInt64,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(calc_version)
PARTITION BY toYYYYMM(hour)
ORDER BY (
    tenant_id,
    site_id,
    energy_type,
    accounting_scope,
    load_device_id,
    point_id,
    quality,
    tariff_period,
    hour
);

CREATE OR REPLACE VIEW analytics.energy_hourly_v2
DEFINER = CURRENT_USER SQL SECURITY DEFINER
AS
SELECT
    tenant_id,
    site_id,
    energy_type,
    accounting_scope,
    load_device_id,
    point_id,
    quality,
    tariff_period,
    hour,
    argMax(energy_kwh, calc_version) AS energy_kwh,
    argMax(cost_cny, calc_version) AS cost_cny,
    argMax(interval_count, calc_version) AS interval_count,
    max(calc_version) AS calc_version,
    argMax(data_watermark, calc_version) AS data_watermark,
    argMax(dataset_revision, calc_version) AS dataset_revision,
    argMax(updated_at, calc_version) AS updated_at
FROM analytics.energy_hourly_facts_v2
GROUP BY
    tenant_id,
    site_id,
    energy_type,
    accounting_scope,
    load_device_id,
    point_id,
    quality,
    tariff_period,
    hour;

CREATE OR REPLACE VIEW analytics.thermal_cop_asof_audit
DEFINER = CURRENT_USER SQL SECURITY DEFINER
AS
WITH
    thermal_obs AS (
        SELECT
            assumeNotNull(tenant_id) AS tenant_id,
            assumeNotNull(site_id) AS site_id,
            assumeNotNull(device_id) AS device_id,
            telemetry_key,
            sampled_at,
            assumeNotNull(value_number) AS thermal_kw
        FROM telemetry_history.observations
        WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
          AND quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
          AND tenant_id IS NOT NULL
          AND site_id IS NOT NULL
          AND device_id IS NOT NULL
          AND value_number IS NOT NULL
          AND isFinite(value_number)
          AND telemetry_key IN (
              'btu_meter.instant_cooling_capacity',
              'instant_cooling_capacity',
              'chiller.cooling_capacity',
              'cooling_capacity',
              'thermal_power_kw'
          )
    ),
    electrical_obs AS (
        SELECT
            assumeNotNull(tenant_id) AS tenant_id,
            assumeNotNull(site_id) AS site_id,
            assumeNotNull(device_id) AS device_id,
            telemetry_key,
            sampled_at,
            assumeNotNull(value_number) AS electrical_kw,
            toUInt8(1) AS matched
        FROM telemetry_history.observations
        WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
          AND quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
          AND tenant_id IS NOT NULL
          AND site_id IS NOT NULL
          AND device_id IS NOT NULL
          AND value_number IS NOT NULL
          AND isFinite(value_number)
          AND telemetry_key IN (
              'hvac_meter.active_power',
              'plant.active_power',
              'active_power',
              'power_kw',
              'chiller.power'
          )
    ),
    delta_t_obs AS (
        SELECT
            assumeNotNull(tenant_id) AS tenant_id,
            assumeNotNull(site_id) AS site_id,
            sampled_at,
            assumeNotNull(value_number) AS delta_t_c,
            toUInt8(1) AS matched
        FROM telemetry_history.observations
        WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
          AND quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
          AND tenant_id IS NOT NULL
          AND site_id IS NOT NULL
          AND value_number IS NOT NULL
          AND isFinite(value_number)
          AND telemetry_key IN (
              'btu_meter.temperature_difference',
              'temperature_difference',
              'delta_t'
          )
    ),
    matched AS (
        SELECT
            t.tenant_id AS tenant_id,
            t.site_id AS site_id,
            t.device_id AS thermal_device_id,
            t.telemetry_key AS thermal_key,
            t.sampled_at AS thermal_sampled_at,
            t.thermal_kw AS thermal_kw,
            if(prev_e.matched = 1 OR next_e.matched = 1, toUInt8(1), toUInt8(0)) AS has_electrical,
            multiIf(
                prev_e.matched = 1 AND next_e.matched = 1,
                if(abs(dateDiff('second', t.sampled_at, prev_e.sampled_at)) <= abs(dateDiff('second', t.sampled_at, next_e.sampled_at)), prev_e.device_id, next_e.device_id),
                prev_e.matched = 1, prev_e.device_id,
                next_e.device_id
            ) AS electrical_device_id,
            multiIf(
                prev_e.matched = 1 AND next_e.matched = 1,
                if(abs(dateDiff('second', t.sampled_at, prev_e.sampled_at)) <= abs(dateDiff('second', t.sampled_at, next_e.sampled_at)), prev_e.telemetry_key, next_e.telemetry_key),
                prev_e.matched = 1, prev_e.telemetry_key,
                next_e.telemetry_key
            ) AS electrical_key,
            multiIf(
                prev_e.matched = 1 AND next_e.matched = 1,
                if(abs(dateDiff('second', t.sampled_at, prev_e.sampled_at)) <= abs(dateDiff('second', t.sampled_at, next_e.sampled_at)), prev_e.sampled_at, next_e.sampled_at),
                prev_e.matched = 1, prev_e.sampled_at,
                next_e.sampled_at
            ) AS electrical_sampled_at,
            multiIf(
                prev_e.matched = 1 AND next_e.matched = 1,
                if(abs(dateDiff('second', t.sampled_at, prev_e.sampled_at)) <= abs(dateDiff('second', t.sampled_at, next_e.sampled_at)), prev_e.electrical_kw, next_e.electrical_kw),
                prev_e.matched = 1, prev_e.electrical_kw,
                next_e.electrical_kw
            ) AS electrical_kw,
            if(
                dt.matched = 1
                AND abs(dateDiff('second', t.sampled_at, dt.sampled_at)) <= 90,
                dt.delta_t_c,
                CAST(NULL, 'Nullable(Float64)')
            ) AS delta_t_c
        FROM thermal_obs AS t
        ASOF LEFT JOIN electrical_obs AS prev_e
            ON t.tenant_id = prev_e.tenant_id
           AND t.site_id = prev_e.site_id
           AND t.sampled_at >= prev_e.sampled_at
        ASOF LEFT JOIN electrical_obs AS next_e
            ON t.tenant_id = next_e.tenant_id
           AND t.site_id = next_e.site_id
           AND t.sampled_at <= next_e.sampled_at
        ASOF LEFT JOIN delta_t_obs AS dt
            ON t.tenant_id = dt.tenant_id
           AND t.site_id = dt.site_id
           AND t.sampled_at >= dt.sampled_at
    )
SELECT
    tenant_id,
    site_id,
    thermal_device_id,
    thermal_key,
    thermal_sampled_at,
    thermal_kw,
    electrical_device_id,
    electrical_key,
    electrical_sampled_at,
    dateDiff('second', electrical_sampled_at, thermal_sampled_at) AS time_skew_seconds,
    electrical_kw,
    delta_t_c,
    if(
        electrical_kw > 5.0
        AND thermal_kw >= 0.0
        AND (delta_t_c IS NULL OR delta_t_c >= 0.5),
        thermal_kw / electrical_kw,
        CAST(NULL, 'Nullable(Float64)')
    ) AS instant_cop
FROM matched
WHERE has_electrical = 1
  AND abs(dateDiff('second', thermal_sampled_at, electrical_sampled_at)) <= 90
  AND (
      electrical_kw > 5.0
      OR (delta_t_c IS NOT NULL AND delta_t_c >= 0.5)
  );

GRANT INSERT ON analytics.energy_hourly_facts_v2 TO analytics_projector_writer;
GRANT SELECT ON analytics.energy_hourly_facts_v2 TO analytics_projector_reader, cube_analytics_reader;
GRANT SELECT ON analytics.energy_hourly_v2 TO analytics_projector_reader, cube_analytics_reader;
GRANT SELECT ON analytics.thermal_cop_asof_audit TO analytics_projector_reader, cube_analytics_reader;
