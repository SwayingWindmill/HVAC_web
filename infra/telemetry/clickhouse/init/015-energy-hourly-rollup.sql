-- F3: point-scoped Counter delta reads and the hourly Energy rollup.
--
-- Applied by docker-entrypoint-initdb.d on an empty volume only. An existing server needs it
-- applied once by hand, with telemetry-worker (which runs the Energy projector) stopped; the
-- order and the backfill are in docs/operations/energy-hourly-rollup.md. Every statement is
-- idempotent.

-- counter_deltas_from used to scope Points through counter_observations, whose point_id is
-- assumeNotNull(point_id). ClickHouse resolves a WHERE identifier to a SELECT alias first, so the
-- filter became has({points}, assumeNotNull(point_id)), which the observations sort key
-- (... ifNull(point_id, zero) ...) cannot use: every call read every Counter observation of every
-- Point, ~7.5M rows for a 400-Point Site. The scope now filters the raw Nullable column, which
-- the key analysis maps onto ifNull(point_id, zero), so a call reads only its Points' granules.
-- The output is unchanged: the definition below differs from 004 in the scoped CTE only.
CREATE OR REPLACE VIEW telemetry_history.counter_deltas_from
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
WITH
scoped AS (
  SELECT
    assumeNotNull(raw.tenant_id) AS tenant_id,
    assumeNotNull(raw.site_id) AS site_id,
    assumeNotNull(raw.point_id) AS point_id,
    assumeNotNull(raw.device_id) AS device_id,
    raw.sensor_id AS sensor_id,
    raw.telemetry_key AS telemetry_key,
    raw.unit AS unit,
    raw.point_revision AS point_revision,
    raw.counter_decrease_mode AS counter_decrease_mode,
    raw.counter_rollover_modulus AS counter_rollover_modulus,
    raw.sampled_at AS sampled_at,
    raw.received_at AS received_at,
    raw.observation_id AS observation_id,
    raw.source_event_id AS source_event_id,
    raw.source_partition AS source_partition,
    raw.source_offset AS source_offset,
    raw.quality AS quality,
    raw.quality_reasons AS quality_reasons,
    raw.value_number AS value_number
  FROM telemetry_history.observations AS raw
  WHERE (empty({points:Array(UUID)}) OR raw.point_id IN {points:Array(UUID)})
    AND raw.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND raw.point_type = 'COUNTER'
    AND raw.value_number IS NOT NULL
    AND raw.tenant_id IS NOT NULL
    AND raw.site_id IS NOT NULL
    AND raw.device_id IS NOT NULL
    AND raw.point_id IS NOT NULL
),
window_input AS (
  SELECT
    'RANGE' AS row_kind,
    tenant_id,
    site_id,
    point_id,
    device_id,
    sensor_id,
    telemetry_key,
    unit,
    point_revision,
    counter_decrease_mode,
    counter_rollover_modulus,
    sampled_at,
    received_at,
    observation_id,
    source_event_id,
    source_partition,
    source_offset,
    quality,
    quality_reasons,
    value_number
  FROM scoped
  WHERE sampled_at >= {since:DateTime64(3, 'UTC')} AND sampled_at <= {until:DateTime64(3, 'UTC')}
  UNION ALL
  SELECT
    'ANCHOR' AS row_kind,
    tenant_id,
    site_id,
    point_id,
    last.1 AS device_id,
    last.2 AS sensor_id,
    last.3 AS telemetry_key,
    last.4 AS unit,
    last.5 AS point_revision,
    last.6 AS counter_decrease_mode,
    last.7 AS counter_rollover_modulus,
    last.8 AS sampled_at,
    last.9 AS received_at,
    last.10 AS observation_id,
    last.11 AS source_event_id,
    last.12 AS source_partition,
    last.13 AS source_offset,
    last.14 AS quality,
    last.15 AS quality_reasons,
    last.16 AS value_number
  FROM (
    SELECT tenant_id, site_id, point_id, argMax(tuple(device_id, sensor_id, telemetry_key, unit, point_revision, counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id, source_event_id, source_partition, source_offset, quality, quality_reasons, value_number), (sampled_at, observation_id)) AS last
    FROM scoped
    WHERE sampled_at < {since:DateTime64(3, 'UTC')}
    GROUP BY tenant_id, site_id, point_id
  )
  UNION ALL
  SELECT
    'PREFIX_MAX' AS row_kind,
    tenant_id,
    site_id,
    point_id,
    last.1 AS device_id,
    last.2 AS sensor_id,
    last.3 AS telemetry_key,
    last.4 AS unit,
    last.5 AS point_revision,
    last.6 AS counter_decrease_mode,
    last.7 AS counter_rollover_modulus,
    last.8 AS sampled_at,
    last.9 AS received_at,
    last.10 AS observation_id,
    last.11 AS source_event_id,
    last.12 AS source_partition,
    last.13 AS source_offset,
    last.14 AS quality,
    last.15 AS quality_reasons,
    prefix_max_value AS value_number
  FROM (
    SELECT tenant_id, site_id, point_id, argMax(tuple(device_id, sensor_id, telemetry_key, unit, point_revision, counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id, source_event_id, source_partition, source_offset, quality, quality_reasons, value_number), (sampled_at, observation_id)) AS last, max(value_number) AS prefix_max_value
    FROM scoped
    WHERE sampled_at < {since:DateTime64(3, 'UTC')}
    GROUP BY tenant_id, site_id, point_id, point_revision, unit
  )
  UNION ALL
  SELECT
    'NEXT' AS row_kind,
    tenant_id,
    site_id,
    point_id,
    first.1 AS device_id,
    first.2 AS sensor_id,
    first.3 AS telemetry_key,
    first.4 AS unit,
    first.5 AS point_revision,
    first.6 AS counter_decrease_mode,
    first.7 AS counter_rollover_modulus,
    first.8 AS sampled_at,
    first.9 AS received_at,
    first.10 AS observation_id,
    first.11 AS source_event_id,
    first.12 AS source_partition,
    first.13 AS source_offset,
    first.14 AS quality,
    first.15 AS quality_reasons,
    first.16 AS value_number
  FROM (
    SELECT tenant_id, site_id, point_id, argMin(tuple(device_id, sensor_id, telemetry_key, unit, point_revision, counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id, source_event_id, source_partition, source_offset, quality, quality_reasons, value_number), (sampled_at, observation_id)) AS first
    FROM scoped
    WHERE sampled_at > {until:DateTime64(3, 'UTC')}
    GROUP BY tenant_id, site_id, point_id
  )
),
ordered AS (
  SELECT
    *,
    lagInFrame(toNullable(observation_id)) OVER point_window AS previous_observation_id,
    lagInFrame(value_number) OVER point_window AS previous_value,
    lagInFrame(sampled_at) OVER point_window AS previous_sampled_at,
    lagInFrame(quality, 1, '') OVER point_window AS previous_quality,
    lagInFrame(quality_reasons, 1, []) OVER point_window AS previous_quality_reasons,
    lagInFrame(point_revision) OVER point_window AS previous_point_revision,
    lagInFrame(unit) OVER point_window AS previous_unit,
    max(value_number) OVER revision_window AS previous_max_value
  FROM window_input
  WINDOW point_window AS (
    PARTITION BY tenant_id, site_id, point_id, row_kind = 'PREFIX_MAX'
    ORDER BY sampled_at, observation_id
    ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
  ),
  revision_window AS (
    PARTITION BY tenant_id, site_id, point_id, point_revision, unit
    ORDER BY sampled_at, observation_id
    ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
  )
)
SELECT
  assumeNotNull(tenant_id) AS tenant_id,
  assumeNotNull(site_id) AS site_id,
  assumeNotNull(device_id) AS device_id,
  assumeNotNull(point_id) AS point_id,
  sensor_id,
  telemetry_key,
  unit,
  assumeNotNull(point_revision) AS point_revision,
  counter_decrease_mode,
  counter_rollover_modulus,
  sampled_at,
  received_at,
  observation_id,
  quality,
  quality_reasons,
  source_event_id,
  source_partition,
  source_offset,
  value_number AS counter_value,
  previous_observation_id,
  previous_value,
  previous_sampled_at,
  previous_quality,
  previous_quality_reasons,
  multiIf(
    previous_value IS NULL, 'INITIAL',
    previous_point_revision IS NULL OR point_revision != previous_point_revision, 'REVISION_BOUNDARY',
    ifNull(unit, '') != ifNull(previous_unit, ''), 'UNIT_BOUNDARY',
    counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND value_number < previous_max_value, 'INVALID_DECREASE',
    counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND previous_value < previous_max_value AND value_number >= previous_max_value, 'RECOVERY',
    value_number = previous_value, 'UNCHANGED',
    value_number > previous_value, 'INCREASE',
    counter_decrease_mode = 'RESET_TO_ZERO', 'RESET',
    counter_decrease_mode = 'ROLLOVER'
      AND counter_rollover_modulus IS NOT NULL
      AND previous_value >= 0
      AND value_number >= 0
      AND previous_value < counter_rollover_modulus
      AND value_number < counter_rollover_modulus, 'ROLLOVER',
    'INVALID_DECREASE'
  ) AS transition_type,
  multiIf(
    previous_value IS NULL, CAST(NULL, 'Nullable(Float64)'),
    previous_point_revision IS NULL OR point_revision != previous_point_revision, CAST(NULL, 'Nullable(Float64)'),
    ifNull(unit, '') != ifNull(previous_unit, ''), CAST(NULL, 'Nullable(Float64)'),
    counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND value_number < previous_max_value, CAST(NULL, 'Nullable(Float64)'),
    counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND previous_value < previous_max_value AND value_number >= previous_max_value, toNullable(value_number - previous_max_value),
    value_number >= previous_value, toNullable(value_number - previous_value),
    counter_decrease_mode = 'RESET_TO_ZERO', toNullable(value_number),
    counter_decrease_mode = 'ROLLOVER'
      AND counter_rollover_modulus IS NOT NULL
      AND previous_value >= 0
      AND value_number >= 0
      AND previous_value < counter_rollover_modulus
      AND value_number < counter_rollover_modulus,
      toNullable(counter_rollover_modulus - previous_value + value_number),
    CAST(NULL, 'Nullable(Float64)')
  ) AS delta_value
FROM ordered
WHERE row_kind IN ('RANGE', 'NEXT');

-- The hourly Energy rollup Cube reads instead of grouping every fact revision (ADR 0026). A row
-- is one (Tenant, Site, Point, UTC hour, accounting scope, energy type, quality, load Device)
-- and holds signed sums, so a fact correction can take its predecessor back out: see the two
-- materialized views below. load_device_id is the zero UUID for a Site meter, because a sort key
-- column cannot be Nullable; analytics.energy_hourly turns it back into NULL.
CREATE TABLE IF NOT EXISTS analytics.energy_hourly_states (
  tenant_id UUID,
  site_id UUID,
  point_id UUID,
  hour DateTime64(3, 'UTC'),
  accounting_scope LowCardinality(String),
  energy_type LowCardinality(String),
  quality LowCardinality(String),
  load_device_id UUID,
  energy_kwh SimpleAggregateFunction(sum, Float64),
  interval_count SimpleAggregateFunction(sum, Int64),
  data_watermark SimpleAggregateFunction(max, DateTime64(3, 'UTC')),
  dataset_revision SimpleAggregateFunction(max, UInt64)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, site_id, point_id, hour, accounting_scope, energy_type, quality, load_device_id)
SETTINGS non_replicated_deduplication_window = 10000;

-- Every inserted fact revision adds its energy to its hour.
CREATE MATERIALIZED VIEW IF NOT EXISTS analytics.energy_facts_to_hourly
TO analytics.energy_hourly_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id,
  site_id,
  point_id,
  toStartOfHour(period_end) AS hour,
  accounting_scope,
  energy_type,
  quality,
  ifNull(load_device_id, toUUID('00000000-0000-0000-0000-000000000000')) AS load_device_id,
  energy_kwh,
  toInt64(1) AS interval_count,
  data_watermark,
  dataset_revision
FROM analytics.energy_interval_facts;

-- A materialized view reads the inserted block wherever its query names the source table, so a
-- correction cannot join its predecessor through analytics.energy_interval_facts itself. This
-- view is the stored table under another name, and the correction view joins through it.
CREATE OR REPLACE VIEW analytics.energy_interval_fact_revisions
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id, site_id, meter_id, meter_binding_id, period_end, source_current_observation_id, fact_revision,
  point_id, accounting_scope, energy_type, quality, load_device_id, energy_kwh
FROM analytics.energy_interval_facts;

-- A correction (fact_revision r > 0) supersedes revision r - 1 of the same logical fact (009's
-- grouping key), so its predecessor's energy and interval leave the predecessor's own hour and
-- quality. The IN holds the join to the corrected meter bindings, a prefix of the facts sort
-- key, and their months; an insert without corrections reads no stored part. The predecessor's watermark and dataset revision stay in
-- their max: a correction only ever raises both.
CREATE MATERIALIZED VIEW IF NOT EXISTS analytics.energy_fact_corrections_to_hourly
TO analytics.energy_hourly_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  prior.tenant_id AS tenant_id,
  prior.site_id AS site_id,
  prior.point_id AS point_id,
  toStartOfHour(prior.period_end) AS hour,
  prior.accounting_scope AS accounting_scope,
  prior.energy_type AS energy_type,
  prior.quality AS quality,
  ifNull(prior.load_device_id, toUUID('00000000-0000-0000-0000-000000000000')) AS load_device_id,
  -prior.energy_kwh AS energy_kwh,
  toInt64(-1) AS interval_count,
  toDateTime64(0, 3, 'UTC') AS data_watermark,
  toUInt64(0) AS dataset_revision
FROM analytics.energy_interval_facts AS correction
INNER JOIN (
  SELECT *
  FROM analytics.energy_interval_fact_revisions
  WHERE (tenant_id, site_id, meter_id, meter_binding_id, period_end) IN (
    SELECT tenant_id, site_id, meter_id, meter_binding_id, period_end
    FROM analytics.energy_interval_facts
    WHERE fact_revision > 0
  )
) AS prior
  ON prior.tenant_id = correction.tenant_id
  AND prior.site_id = correction.site_id
  AND prior.meter_binding_id = correction.meter_binding_id
  AND prior.period_end = correction.period_end
  AND prior.source_current_observation_id = correction.source_current_observation_id
  AND prior.fact_revision + 1 = correction.fact_revision
WHERE correction.fact_revision > 0;

-- The rollup as Cube reads it: states merged per hour, hours whose every interval was corrected
-- away dropped, and a Site meter's load Device NULL again as in the facts.
CREATE OR REPLACE VIEW analytics.energy_hourly AS
SELECT
  tenant_id,
  site_id,
  point_id,
  hour,
  accounting_scope,
  energy_type,
  quality,
  nullIf(load_device_id, toUUID('00000000-0000-0000-0000-000000000000')) AS load_device_id,
  sum(energy_kwh) AS energy_kwh,
  sum(interval_count) AS interval_count,
  max(data_watermark) AS data_watermark,
  max(dataset_revision) AS dataset_revision
FROM analytics.energy_hourly_states
GROUP BY tenant_id, site_id, point_id, hour, accounting_scope, energy_type, quality, load_device_id
HAVING interval_count != 0;

GRANT SELECT ON analytics.energy_hourly_states TO cube_analytics_reader;
GRANT SELECT ON analytics.energy_hourly TO cube_analytics_reader;
