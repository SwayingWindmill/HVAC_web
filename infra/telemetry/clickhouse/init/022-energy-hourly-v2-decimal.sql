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
    energy_kwh,
    cost_cny,
    interval_count,
    latest_calc_version AS calc_version,
    data_watermark,
    dataset_revision,
    updated_at
FROM (
    SELECT
        f.tenant_id AS tenant_id,
        f.site_id AS site_id,
        f.energy_type AS energy_type,
        f.accounting_scope AS accounting_scope,
        f.load_device_id AS load_device_id,
        f.point_id AS point_id,
        f.quality AS quality,
        f.tariff_period AS tariff_period,
        f.hour AS hour,
        argMax(f.energy_kwh, f.calc_version) AS energy_kwh,
        argMax(f.cost_cny, f.calc_version) AS cost_cny,
        argMax(f.interval_count, f.calc_version) AS interval_count,
        max(f.calc_version) AS latest_calc_version,
        argMax(f.data_watermark, f.calc_version) AS data_watermark,
        argMax(f.dataset_revision, f.calc_version) AS dataset_revision,
        argMax(f.updated_at, f.calc_version) AS updated_at
    FROM analytics.energy_hourly_facts_v2 AS f
    GROUP BY
        f.tenant_id,
        f.site_id,
        f.energy_type,
        f.accounting_scope,
        f.load_device_id,
        f.point_id,
        f.quality,
        f.tariff_period,
        f.hour
);

CREATE OR REPLACE VIEW analytics.thermal_cop_asof_audit
DEFINER = CURRENT_USER SQL SECURITY DEFINER
AS
WITH
    thermal_obs AS (
        SELECT
            assumeNotNull(raw.tenant_id) AS tenant_id,
            assumeNotNull(raw.site_id) AS site_id,
            assumeNotNull(raw.device_id) AS device_id,
            raw.telemetry_key AS telemetry_key,
            raw.sampled_at AS sampled_at,
            assumeNotNull(raw.value_number) AS thermal_kw
        FROM telemetry_history.observations AS raw
        WHERE raw.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
          AND raw.quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
          AND raw.tenant_id IS NOT NULL
          AND raw.site_id IS NOT NULL
          AND raw.device_id IS NOT NULL
          AND raw.value_number IS NOT NULL
          AND isFinite(raw.value_number)
          AND raw.telemetry_key IN (
              'btu_meter.instant_cooling_capacity',
              'instant_cooling_capacity',
              'chiller.cooling_capacity',
              'cooling_capacity',
              'thermal_power_kw'
          )
    ),
    electrical_obs AS (
        SELECT
            assumeNotNull(raw.tenant_id) AS tenant_id,
            assumeNotNull(raw.site_id) AS site_id,
            assumeNotNull(raw.device_id) AS device_id,
            raw.telemetry_key AS telemetry_key,
            raw.sampled_at AS sampled_at,
            assumeNotNull(raw.value_number) AS electrical_kw,
            toUInt8(1) AS matched
        FROM telemetry_history.observations AS raw
        WHERE raw.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
          AND raw.quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
          AND raw.tenant_id IS NOT NULL
          AND raw.site_id IS NOT NULL
          AND raw.device_id IS NOT NULL
          AND raw.value_number IS NOT NULL
          AND isFinite(raw.value_number)
          AND raw.telemetry_key IN (
              'hvac_meter.active_power',
              'plant.active_power',
              'active_power',
              'power_kw',
              'chiller.power'
          )
    ),
    delta_t_obs AS (
        SELECT
            assumeNotNull(raw.tenant_id) AS tenant_id,
            assumeNotNull(raw.site_id) AS site_id,
            raw.sampled_at AS sampled_at,
            assumeNotNull(raw.value_number) AS delta_t_c,
            toUInt8(1) AS matched
        FROM telemetry_history.observations AS raw
        WHERE raw.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
          AND raw.quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
          AND raw.tenant_id IS NOT NULL
          AND raw.site_id IS NOT NULL
          AND raw.value_number IS NOT NULL
          AND isFinite(raw.value_number)
          AND raw.telemetry_key IN (
              'btu_meter.temperature_difference',
              'temperature_difference',
              'delta_t'
          )
    ),
    with_prev_e AS (
        SELECT
            t.tenant_id AS tenant_id,
            t.site_id AS site_id,
            t.device_id AS thermal_device_id,
            t.telemetry_key AS thermal_key,
            t.sampled_at AS thermal_sampled_at,
            t.thermal_kw AS thermal_kw,
            prev_e.matched AS prev_matched,
            prev_e.device_id AS prev_device_id,
            prev_e.telemetry_key AS prev_key,
            prev_e.sampled_at AS prev_sampled_at,
            prev_e.electrical_kw AS prev_electrical_kw
        FROM thermal_obs AS t
        ASOF LEFT JOIN electrical_obs AS prev_e
            ON t.tenant_id = prev_e.tenant_id
           AND t.site_id = prev_e.site_id
           AND t.sampled_at >= prev_e.sampled_at
    ),
    with_next_e AS (
        SELECT
            p.tenant_id AS tenant_id,
            p.site_id AS site_id,
            p.thermal_device_id AS thermal_device_id,
            p.thermal_key AS thermal_key,
            p.thermal_sampled_at AS thermal_sampled_at,
            p.thermal_kw AS thermal_kw,
            p.prev_matched AS prev_matched,
            p.prev_device_id AS prev_device_id,
            p.prev_key AS prev_key,
            p.prev_sampled_at AS prev_sampled_at,
            p.prev_electrical_kw AS prev_electrical_kw,
            next_e.matched AS next_matched,
            next_e.device_id AS next_device_id,
            next_e.telemetry_key AS next_key,
            next_e.sampled_at AS next_sampled_at,
            next_e.electrical_kw AS next_electrical_kw
        FROM with_prev_e AS p
        ASOF LEFT JOIN electrical_obs AS next_e
            ON p.tenant_id = next_e.tenant_id
           AND p.site_id = next_e.site_id
           AND p.thermal_sampled_at <= next_e.sampled_at
    ),
    with_dt AS (
        SELECT
            n.tenant_id AS tenant_id,
            n.site_id AS site_id,
            n.thermal_device_id AS thermal_device_id,
            n.thermal_key AS thermal_key,
            n.thermal_sampled_at AS thermal_sampled_at,
            n.thermal_kw AS thermal_kw,
            if(n.prev_matched = 1 OR n.next_matched = 1, toUInt8(1), toUInt8(0)) AS has_electrical,
            multiIf(
                n.prev_matched = 1 AND n.next_matched = 1,
                if(abs(dateDiff('second', n.thermal_sampled_at, n.prev_sampled_at)) <= abs(dateDiff('second', n.thermal_sampled_at, n.next_sampled_at)), n.prev_device_id, n.next_device_id),
                n.prev_matched = 1, n.prev_device_id,
                n.next_device_id
            ) AS electrical_device_id,
            multiIf(
                n.prev_matched = 1 AND n.next_matched = 1,
                if(abs(dateDiff('second', n.thermal_sampled_at, n.prev_sampled_at)) <= abs(dateDiff('second', n.thermal_sampled_at, n.next_sampled_at)), n.prev_key, n.next_key),
                n.prev_matched = 1, n.prev_key,
                n.next_key
            ) AS electrical_key,
            multiIf(
                n.prev_matched = 1 AND n.next_matched = 1,
                if(abs(dateDiff('second', n.thermal_sampled_at, n.prev_sampled_at)) <= abs(dateDiff('second', n.thermal_sampled_at, n.next_sampled_at)), n.prev_sampled_at, n.next_sampled_at),
                n.prev_matched = 1, n.prev_sampled_at,
                n.next_sampled_at
            ) AS electrical_sampled_at,
            multiIf(
                n.prev_matched = 1 AND n.next_matched = 1,
                if(abs(dateDiff('second', n.thermal_sampled_at, n.prev_sampled_at)) <= abs(dateDiff('second', n.thermal_sampled_at, n.next_sampled_at)), n.prev_electrical_kw, n.next_electrical_kw),
                n.prev_matched = 1, n.prev_electrical_kw,
                n.next_electrical_kw
            ) AS electrical_kw,
            if(
                dt.matched = 1
                AND abs(dateDiff('second', n.thermal_sampled_at, dt.sampled_at)) <= 90,
                dt.delta_t_c,
                CAST(NULL, 'Nullable(Float64)')
            ) AS delta_t_c
        FROM with_next_e AS n
        ASOF LEFT JOIN delta_t_obs AS dt
            ON n.tenant_id = dt.tenant_id
           AND n.site_id = dt.site_id
           AND n.thermal_sampled_at >= dt.sampled_at
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
FROM with_dt
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
