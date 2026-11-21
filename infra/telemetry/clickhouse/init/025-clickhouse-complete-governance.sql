-- Complete ClickHouse audit governance:
-- 1) Rebuild telemetry_history.observations as ReplacingMergeTree(received_at) with device-first
--    ORDER BY and LowCardinality(Nullable(String)) unit via atomic EXCHANGE TABLES.
-- 2) Recreate numeric gauge MVs and counter views with explicit CAST(..., 'Nullable(String)') unit.
-- 3) Add metric_binding_id_bf bloom filter, revoke ALTER DELETE from metric engine writers,
--    grant retention cleanup on analytics.metric_result_facts, and enforce 1825-day part-drop TTLs.

DROP TABLE IF EXISTS telemetry_history.observations_v2;

CREATE TABLE telemetry_history.observations_v2 (
  observation_id UUID,
  tenant_id Nullable(UUID),
  site_id Nullable(UUID),
  device_id Nullable(UUID),
  point_id Nullable(UUID),
  sensor_id Nullable(UUID),
  source_id LowCardinality(String),
  source_event_id UUID,
  source_partition LowCardinality(String),
  source_offset UInt64,
  source_path LowCardinality(String),
  telemetry_key LowCardinality(String),
  point_type LowCardinality(Nullable(String)),
  point_revision Nullable(UInt64),
  counter_decrease_mode LowCardinality(Nullable(String)),
  counter_rollover_modulus Nullable(Float64),
  value_type LowCardinality(Nullable(String)),
  unit LowCardinality(Nullable(String)),
  value_json Nullable(String),
  value_number Nullable(Float64),
  value_string Nullable(String),
  value_boolean Nullable(UInt8),
  sampled_at DateTime64(3, 'UTC'),
  received_at DateTime64(3, 'UTC'),
  acceptance_status LowCardinality(String),
  quality LowCardinality(String),
  quality_reasons Array(String),
  payload_sha256 FixedString(64),
  projected_at DateTime64(3, 'UTC') DEFAULT now64(3),
  history_sequence UInt64,
  INDEX history_sequence_minmax history_sequence TYPE minmax GRANULARITY 1,
  INDEX point_id_bf point_id TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX device_telemetry_bf (device_id, telemetry_key) TYPE bloom_filter(0.01) GRANULARITY 1,
  CONSTRAINT accepted_tenant_scope CHECK acceptance_status != 'ACCEPTED' OR (tenant_id IS NOT NULL AND site_id IS NOT NULL AND device_id IS NOT NULL AND point_id IS NOT NULL)
)
ENGINE = ReplacingMergeTree(received_at)
PARTITION BY toYYYYMM(sampled_at)
ORDER BY (
  ifNull(tenant_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(site_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(device_id, toUUID('00000000-0000-0000-0000-000000000000')),
  telemetry_key,
  ifNull(point_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(sensor_id, toUUID('00000000-0000-0000-0000-000000000000')),
  sampled_at
)
SETTINGS index_granularity = 8192,
         non_replicated_deduplication_window = 10000,
         ttl_only_drop_parts = 1;

ALTER TABLE telemetry_history.observations_v2 MODIFY TTL toDateTime(sampled_at) + INTERVAL 730 DAY DELETE;

INSERT INTO telemetry_history.observations_v2 (
  observation_id,
  tenant_id,
  site_id,
  device_id,
  point_id,
  sensor_id,
  source_id,
  source_event_id,
  source_partition,
  source_offset,
  source_path,
  telemetry_key,
  point_type,
  point_revision,
  counter_decrease_mode,
  counter_rollover_modulus,
  value_type,
  unit,
  value_json,
  value_number,
  value_string,
  value_boolean,
  sampled_at,
  received_at,
  acceptance_status,
  quality,
  quality_reasons,
  payload_sha256,
  projected_at,
  history_sequence
)
SELECT
  observation_id,
  tenant_id,
  site_id,
  device_id,
  point_id,
  sensor_id,
  source_id,
  source_event_id,
  source_partition,
  source_offset,
  source_path,
  telemetry_key,
  point_type,
  point_revision,
  counter_decrease_mode,
  counter_rollover_modulus,
  value_type,
  CAST(unit, 'LowCardinality(Nullable(String))') AS unit,
  value_json,
  value_number,
  value_string,
  value_boolean,
  sampled_at,
  received_at,
  acceptance_status,
  quality,
  quality_reasons,
  payload_sha256,
  projected_at,
  history_sequence
FROM telemetry_history.observations
SETTINGS
  max_threads = 1,
  max_insert_threads = 1,
  max_block_size = 8192,
  max_insert_block_size = 32768,
  min_insert_block_size_rows = 32768,
  min_insert_block_size_bytes = 33554432,
  max_bytes_before_external_sort = 134217728;

DROP VIEW IF EXISTS telemetry_history.observations_to_numeric_gauge_1min;
DROP VIEW IF EXISTS telemetry_history.observations_to_numeric_gauge_15min;
EXCHANGE TABLES telemetry_history.observations AND telemetry_history.observations_v2;
DROP TABLE IF EXISTS telemetry_history.observations_v2;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_history.observations_to_numeric_gauge_1min
TO telemetry_history.numeric_gauge_1min_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  toStartOfMinute(obs.sampled_at) AS bucket,
  assumeNotNull(obs.tenant_id) AS tenant_id,
  assumeNotNull(obs.site_id) AS site_id,
  assumeNotNull(obs.device_id) AS device_id,
  assumeNotNull(obs.point_id) AS point_id,
  assumeNotNull(obs.point_revision) AS point_revision,
  obs.telemetry_key AS telemetry_key,
  assumeNotNull(obs.point_type) AS point_type,
  anyState(CAST(obs.unit, 'Nullable(String)')) AS unit_state,
  maxState(obs.projected_at) AS max_projected_at,
  countIfState(obs.quality = 'GOOD') AS good_count,
  countIfState(obs.quality = 'PARTIAL') AS partial_count,
  countIfState(obs.quality = 'ESTIMATED') AS estimated_count,
  countIfState(obs.quality = 'MANUAL') AS manual_count,
  countIfState(obs.quality = 'STALE') AS stale_count,
  countIfState(obs.quality = 'INVALID') AS invalid_count,
  countState() AS total_count,
  countIfState(obs.quality = 'GOOD') AS valid_count,
  avgIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_avg,
  minIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_min,
  maxIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_max,
  argMinIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality = 'GOOD') AS valid_first,
  argMaxIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality = 'GOOD') AS valid_last,
  countIfState(obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_count,
  avgIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_avg,
  minIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_min,
  maxIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_max,
  argMinIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_first,
  argMaxIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_last
FROM telemetry_history.observations AS obs
WHERE obs.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND obs.point_type IN ('TELEMETRY', 'SETTING')
  AND obs.tenant_id IS NOT NULL
  AND obs.site_id IS NOT NULL
  AND obs.device_id IS NOT NULL
  AND obs.point_id IS NOT NULL
  AND obs.point_revision IS NOT NULL
  AND obs.value_type = 'NUMBER'
  AND obs.value_number IS NOT NULL
  AND isFinite(obs.value_number)
GROUP BY bucket, tenant_id, site_id, device_id, point_id, point_revision, telemetry_key, point_type;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_history.observations_to_numeric_gauge_15min
TO telemetry_history.numeric_gauge_15min_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  toStartOfInterval(obs.sampled_at, INTERVAL 15 MINUTE) AS bucket,
  assumeNotNull(obs.tenant_id) AS tenant_id,
  assumeNotNull(obs.site_id) AS site_id,
  assumeNotNull(obs.device_id) AS device_id,
  assumeNotNull(obs.point_id) AS point_id,
  assumeNotNull(obs.point_revision) AS point_revision,
  obs.telemetry_key AS telemetry_key,
  assumeNotNull(obs.point_type) AS point_type,
  anyState(CAST(obs.unit, 'Nullable(String)')) AS unit_state,
  maxState(obs.projected_at) AS max_projected_at,
  countIfState(obs.quality = 'GOOD') AS good_count,
  countIfState(obs.quality = 'PARTIAL') AS partial_count,
  countIfState(obs.quality = 'ESTIMATED') AS estimated_count,
  countIfState(obs.quality = 'MANUAL') AS manual_count,
  countIfState(obs.quality = 'STALE') AS stale_count,
  countIfState(obs.quality = 'INVALID') AS invalid_count,
  countState() AS total_count,
  countIfState(obs.quality = 'GOOD') AS valid_count,
  avgIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_avg,
  minIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_min,
  maxIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_max,
  argMinIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality = 'GOOD') AS valid_first,
  argMaxIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality = 'GOOD') AS valid_last,
  countIfState(obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_count,
  avgIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_avg,
  minIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_min,
  maxIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_max,
  argMinIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_first,
  argMaxIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_last
FROM telemetry_history.observations AS obs
WHERE obs.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND obs.point_type IN ('TELEMETRY', 'SETTING')
  AND obs.tenant_id IS NOT NULL
  AND obs.site_id IS NOT NULL
  AND obs.device_id IS NOT NULL
  AND obs.point_id IS NOT NULL
  AND obs.point_revision IS NOT NULL
  AND obs.value_type = 'NUMBER'
  AND obs.value_number IS NOT NULL
  AND isFinite(obs.value_number)
GROUP BY bucket, tenant_id, site_id, device_id, point_id, point_revision, telemetry_key, point_type;

CREATE OR REPLACE VIEW telemetry_history.counter_observations
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  assumeNotNull(tenant_id) AS tenant_id,
  assumeNotNull(site_id) AS site_id,
  assumeNotNull(point_id) AS point_id,
  assumeNotNull(device_id) AS device_id,
  sensor_id,
  telemetry_key,
  CAST(unit, 'Nullable(String)') AS unit,
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
  value_number,
  history_sequence
FROM telemetry_history.observations
WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_type = 'COUNTER'
  AND value_number IS NOT NULL
  AND tenant_id IS NOT NULL
  AND site_id IS NOT NULL
  AND device_id IS NOT NULL
  AND point_id IS NOT NULL;

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
      CAST(raw.unit, 'Nullable(String)') AS unit,
      ifNull(CAST(raw.unit, 'Nullable(String)'), '') AS unit_key,
      assumeNotNull(raw.point_revision) AS point_revision,
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
      assumeNotNull(raw.value_number) AS value_number,
      multiIf(
        ifNull(raw.unit, '') IN ('Wh', 'W·h'), 100000000.0,
        ifNull(raw.unit, '') IN ('MWh', 'MW·h'), 100.0,
        ifNull(raw.unit, '') = 'GJ', 360.0,
        ifNull(raw.unit, '') = 'MJ', 360000.0,
        ifNull(raw.unit, '') IN ('RTh', 'RT·h'), 30000.0,
        100000.0
      ) AS unit_max_rate,
      multiIf(
        ifNull(raw.unit, '') IN ('Wh', 'W·h'), 500.0,
        ifNull(raw.unit, '') IN ('MWh', 'MW·h'), 0.0005,
        ifNull(raw.unit, '') = 'GJ', 0.15,
        ifNull(raw.unit, '') = 'MJ', 150.0,
        ifNull(raw.unit, '') IN ('m3', 'm³', 'h', 'count'), 0.05,
        0.5
      ) AS unit_jitter_tol
    FROM telemetry_history.observations AS raw
    WHERE (empty({points:Array(UUID)}) OR raw.point_id IN {points:Array(UUID)})
      AND raw.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
      AND raw.point_type = 'COUNTER'
      AND raw.value_number IS NOT NULL
      AND raw.tenant_id IS NOT NULL
      AND raw.site_id IS NOT NULL
      AND raw.device_id IS NOT NULL
      AND raw.point_id IS NOT NULL
      AND raw.point_revision IS NOT NULL
      AND isFinite(raw.value_number)
      AND raw.value_number >= 0
      AND raw.quality != 'INVALID'
      AND NOT has(raw.quality_reasons, 'OUT_OF_RANGE')
      AND NOT has(raw.quality_reasons, 'SPIKE')
  ),
  in_range AS (
    SELECT
      'RANGE' AS row_kind,
      CAST(NULL, 'Nullable(Float64)') AS prefix_max_value,
      tenant_id, site_id, point_id, device_id, sensor_id, telemetry_key, unit, unit_key, point_revision,
      counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id,
      source_event_id, source_partition, source_offset, quality, quality_reasons, value_number,
      unit_max_rate, unit_jitter_tol
    FROM scoped
    WHERE sampled_at >= {since:DateTime64(3, 'UTC')} AND sampled_at <= {until:DateTime64(3, 'UTC')}
  ),
  pre_anchors AS (
    SELECT
      'ANCHOR' AS row_kind,
      CAST(NULL, 'Nullable(Float64)') AS prefix_max_value,
      tenant_id, site_id, point_id, device_id, sensor_id, telemetry_key, unit, unit_key, point_revision,
      counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id,
      source_event_id, source_partition, source_offset, quality, quality_reasons, value_number,
      unit_max_rate, unit_jitter_tol
    FROM scoped
    WHERE sampled_at < {since:DateTime64(3, 'UTC')}
    ORDER BY sampled_at DESC, observation_id DESC
    LIMIT 16 BY tenant_id, site_id, point_id
  ),
  post_next_pool AS (
    SELECT
      tenant_id, site_id, point_id, device_id, sensor_id, telemetry_key, unit, unit_key, point_revision,
      counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id,
      source_event_id, source_partition, source_offset, quality, quality_reasons, value_number,
      unit_max_rate, unit_jitter_tol,
      row_number() OVER (PARTITION BY tenant_id, site_id, point_id ORDER BY sampled_at ASC, observation_id ASC) AS next_rank
    FROM scoped
    WHERE sampled_at > {until:DateTime64(3, 'UTC')}
    ORDER BY sampled_at ASC, observation_id ASC
    LIMIT 8 BY tenant_id, site_id, point_id
  ),
  post_next AS (
    SELECT
      if(next_rank = 1, 'NEXT', 'LOOKAHEAD') AS row_kind,
      CAST(NULL, 'Nullable(Float64)') AS prefix_max_value,
      tenant_id, site_id, point_id, device_id, sensor_id, telemetry_key, unit, unit_key, point_revision,
      counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id,
      source_event_id, source_partition, source_offset, quality, quality_reasons, value_number,
      unit_max_rate, unit_jitter_tol
    FROM post_next_pool
  ),
  prefix_max AS (
    SELECT
      'PREFIX_MAX' AS row_kind,
      toNullable(prefix_max_value) AS prefix_max_value,
      tenant_id,
      site_id,
      point_id,
      last.1 AS device_id,
      last.2 AS sensor_id,
      last.3 AS telemetry_key,
      last.4 AS unit,
      unit_key,
      point_revision,
      last.5 AS counter_decrease_mode,
      last.6 AS counter_rollover_modulus,
      last.7 AS sampled_at,
      last.8 AS received_at,
      last.9 AS observation_id,
      last.10 AS source_event_id,
      last.11 AS source_partition,
      last.12 AS source_offset,
      last.13 AS quality,
      last.14 AS quality_reasons,
      last.15 AS value_number,
      last.16 AS unit_max_rate,
      last.17 AS unit_jitter_tol
    FROM (
      SELECT
        tenant_id,
        site_id,
        point_id,
        point_revision,
        unit_key,
        argMax(
          tuple(device_id, sensor_id, telemetry_key, unit, counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id, source_event_id, source_partition, source_offset, quality, quality_reasons, value_number, unit_max_rate, unit_jitter_tol),
          (sampled_at, observation_id)
        ) AS last,
        max(value_number) AS prefix_max_value
      FROM scoped
      WHERE sampled_at < {since:DateTime64(3, 'UTC')}
      GROUP BY tenant_id, site_id, point_id, point_revision, unit_key
    )
  ),
  seq_base AS (
    SELECT * FROM pre_anchors
    UNION ALL
    SELECT * FROM in_range
    UNION ALL
    SELECT * FROM post_next
  ),
  zero_bounds AS (
    SELECT
      *,
      if(
        countIf(value_number > 0) OVER seg_prev > 0,
        argMaxIf(toNullable(value_number), tuple(sampled_at, observation_id), value_number > 0) OVER seg_prev,
        CAST(NULL, 'Nullable(Float64)')
      ) AS prev_nz_val,
      if(
        countIf(value_number > 0) OVER seg_prev > 0,
        argMaxIf(toNullable(sampled_at), tuple(sampled_at, observation_id), value_number > 0) OVER seg_prev,
        CAST(NULL, 'Nullable(DateTime64(3, \'UTC\'))')
      ) AS prev_nz_at,
      if(
        countIf(value_number > 0) OVER seg_next > 0,
        argMinIf(toNullable(value_number), tuple(sampled_at, observation_id), value_number > 0) OVER seg_next,
        CAST(NULL, 'Nullable(Float64)')
      ) AS next_nz_val
    FROM seq_base
    WINDOW
      seg_prev AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),
      seg_next AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN 1 FOLLOWING AND UNBOUNDED FOLLOWING)
  ),
  zero_flagged AS (
    SELECT
      *,
      if(
        value_number = 0
        AND prev_nz_val IS NOT NULL
        AND prev_nz_val > 0
        AND (
          ifNull(counter_decrease_mode, '') NOT IN ('RESET_TO_ZERO', 'RESET', 'METER_REPLACEMENT', 'ROLLOVER')
          OR next_nz_val IS NULL
          OR next_nz_val >= prev_nz_val - unit_jitter_tol
          OR (
            counter_decrease_mode = 'ROLLOVER'
            AND (
              counter_rollover_modulus IS NULL
              OR counter_rollover_modulus <= 0
              OR prev_nz_at IS NULL
              OR (counter_rollover_modulus - ifNull(prev_nz_val, 0.0)) > unit_max_rate * greatest(dateDiff('millisecond', ifNull(prev_nz_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0)
            )
          )
        ),
        toUInt8(1),
        toUInt8(0)
      ) AS is_zero_drop
    FROM zero_bounds
  ),
  spike1_lag AS (
    SELECT
      *,
      lagInFrame(toNullable(value_number), 1, CAST(NULL, 'Nullable(Float64)')) OVER seg_w AS p1_val,
      lagInFrame(toNullable(sampled_at), 1, CAST(NULL, 'Nullable(DateTime64(3, \'UTC\'))')) OVER seg_w AS p1_at
    FROM zero_flagged
    WHERE is_zero_drop = 0
    WINDOW seg_w AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
  ),
  spike1_flagged AS (
    SELECT
      *,
      if(
        p1_val IS NOT NULL AND p1_at IS NOT NULL AND (
          (value_number > ifNull(p1_val, 0.0) AND (value_number - ifNull(p1_val, 0.0)) > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p1_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0))
          OR (value_number < ifNull(p1_val, 0.0) - unit_jitter_tol AND ifNull(counter_decrease_mode, '') IN ('RESET_TO_ZERO', 'RESET', 'METER_REPLACEMENT') AND value_number > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p1_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0))
          OR (value_number < ifNull(p1_val, 0.0) - unit_jitter_tol AND counter_decrease_mode = 'ROLLOVER' AND counter_rollover_modulus IS NOT NULL AND counter_rollover_modulus > 0 AND (ifNull(p1_val, 0.0) > counter_rollover_modulus OR value_number >= counter_rollover_modulus OR ((counter_rollover_modulus - ifNull(p1_val, 0.0)) + value_number) > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p1_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0)))
        ),
        toUInt8(1),
        toUInt8(0)
      ) AS is_spike_1
    FROM spike1_lag
  ),
  spike2_lag AS (
    SELECT
      *,
      lagInFrame(toNullable(value_number), 1, CAST(NULL, 'Nullable(Float64)')) OVER seg_w AS p2_val,
      lagInFrame(toNullable(sampled_at), 1, CAST(NULL, 'Nullable(DateTime64(3, \'UTC\'))')) OVER seg_w AS p2_at
    FROM spike1_flagged
    WHERE is_spike_1 = 0
    WINDOW seg_w AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
  ),
  spike2_flagged AS (
    SELECT
      *,
      if(
        p2_val IS NOT NULL AND p2_at IS NOT NULL AND (
          (value_number > ifNull(p2_val, 0.0) AND (value_number - ifNull(p2_val, 0.0)) > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p2_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0))
          OR (value_number < ifNull(p2_val, 0.0) - unit_jitter_tol AND ifNull(counter_decrease_mode, '') IN ('RESET_TO_ZERO', 'RESET', 'METER_REPLACEMENT') AND value_number > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p2_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0))
          OR (value_number < ifNull(p2_val, 0.0) - unit_jitter_tol AND counter_decrease_mode = 'ROLLOVER' AND counter_rollover_modulus IS NOT NULL AND counter_rollover_modulus > 0 AND (ifNull(p2_val, 0.0) > counter_rollover_modulus OR value_number >= counter_rollover_modulus OR ((counter_rollover_modulus - ifNull(p2_val, 0.0)) + value_number) > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p2_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0)))
        ),
        toUInt8(1),
        toUInt8(0)
      ) AS is_spike_2,
      if(p2_val IS NOT NULL AND value_number < ifNull(p2_val, 0.0) - unit_jitter_tol, toUInt64(1), toUInt64(0)) AS reset_break
    FROM spike2_lag
  ),
  epoch_tagged AS (
    SELECT
      *,
      sum(reset_break) OVER (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS reset_epoch
    FROM spike2_flagged
    WHERE is_spike_2 = 0
  ),
  jitter_bounds AS (
    SELECT
      *,
      if(
        count() OVER epoch_prev > 0,
        max(toNullable(value_number)) OVER epoch_prev,
        CAST(NULL, 'Nullable(Float64)')
      ) AS epoch_max_before
    FROM epoch_tagged
    WINDOW epoch_prev AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key, reset_epoch ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
  ),
  jitter_flagged AS (
    SELECT
      tenant_id,
      site_id,
      point_id,
      observation_id,
      if(
        epoch_max_before IS NOT NULL AND value_number > 0 AND value_number < ifNull(epoch_max_before, 0.0) AND (ifNull(epoch_max_before, 0.0) - value_number) <= unit_jitter_tol,
        toUInt8(1),
        toUInt8(0)
      ) AS is_jitter_rollback
    FROM jitter_bounds
  ),
  seq_annotated AS (
    SELECT
      z.row_kind AS row_kind,
      z.prefix_max_value AS prefix_max_value,
      z.tenant_id AS tenant_id,
      z.site_id AS site_id,
      z.point_id AS point_id,
      z.device_id AS device_id,
      z.sensor_id AS sensor_id,
      z.telemetry_key AS telemetry_key,
      z.unit AS unit,
      z.unit_key AS unit_key,
      z.point_revision AS point_revision,
      z.counter_decrease_mode AS counter_decrease_mode,
      z.counter_rollover_modulus AS counter_rollover_modulus,
      z.sampled_at AS sampled_at,
      z.received_at AS received_at,
      z.observation_id AS observation_id,
      z.source_event_id AS source_event_id,
      z.source_partition AS source_partition,
      z.source_offset AS source_offset,
      z.quality AS quality,
      z.quality_reasons AS quality_reasons,
      z.value_number AS value_number,
      z.is_zero_drop AS is_zero_drop,
      if(ifNull(s1.is_spike_1, toUInt8(0)) = 1 OR ifNull(s2.is_spike_2, toUInt8(0)) = 1, toUInt8(1), toUInt8(0)) AS is_spike,
      ifNull(jf.is_jitter_rollback, toUInt8(0)) AS is_jitter_rollback,
      if(z.is_zero_drop = 0 AND ifNull(s1.is_spike_1, toUInt8(0)) = 0 AND ifNull(s2.is_spike_2, toUInt8(0)) = 0 AND ifNull(jf.is_jitter_rollback, toUInt8(0)) = 0, toUInt8(1), toUInt8(0)) AS is_valid_anchor
    FROM zero_flagged AS z
    LEFT JOIN spike1_flagged AS s1 ON z.tenant_id = s1.tenant_id AND z.site_id = s1.site_id AND z.point_id = s1.point_id AND z.observation_id = s1.observation_id
    LEFT JOIN spike2_flagged AS s2 ON z.tenant_id = s2.tenant_id AND z.site_id = s2.site_id AND z.point_id = s2.point_id AND z.observation_id = s2.observation_id
    LEFT JOIN jitter_flagged AS jf ON z.tenant_id = jf.tenant_id AND z.site_id = jf.site_id AND z.point_id = jf.point_id AND z.observation_id = jf.observation_id
  ),
  seq_with_valid_prev AS (
    SELECT
      *,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(toNullable(observation_id), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST(NULL, 'Nullable(UUID)')) AS prev_valid_observation_id,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(toNullable(value_number), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST(NULL, 'Nullable(Float64)')) AS prev_valid_value,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(toNullable(sampled_at), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST(NULL, 'Nullable(DateTime64(3, \'UTC\'))')) AS prev_valid_sampled_at,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(quality, tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, '') AS prev_valid_quality,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(quality_reasons, tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST([], 'Array(String)')) AS prev_valid_quality_reasons
    FROM seq_annotated
    WINDOW pt_prev AS (PARTITION BY tenant_id, site_id, point_id ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
  ),
  clean_input AS (
    SELECT
      row_kind, prefix_max_value,
      tenant_id, site_id, point_id, device_id, sensor_id, telemetry_key, unit, unit_key, point_revision,
      counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id,
      source_event_id, source_partition, source_offset, quality, quality_reasons, value_number
    FROM seq_annotated
    WHERE is_valid_anchor = 1
    UNION ALL
    SELECT
      row_kind, prefix_max_value,
      tenant_id, site_id, point_id, device_id, sensor_id, telemetry_key, unit, unit_key, point_revision,
      counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id,
      source_event_id, source_partition, source_offset, quality, quality_reasons, value_number
    FROM prefix_max
  ),
  clean_windowed AS (
    SELECT
      *,
      lagInFrame(toNullable(observation_id)) OVER point_window AS previous_observation_id,
      lagInFrame(toNullable(value_number)) OVER point_window AS previous_value,
      lagInFrame(toNullable(sampled_at)) OVER point_window AS previous_sampled_at,
      lagInFrame(quality, 1, '') OVER point_window AS previous_quality,
      lagInFrame(quality_reasons, 1, CAST([], 'Array(String)')) OVER point_window AS previous_quality_reasons,
      lagInFrame(toNullable(point_revision)) OVER point_window AS previous_point_revision,
      lagInFrame(unit) OVER point_window AS previous_unit,
      max(toNullable(if(row_kind = 'PREFIX_MAX', ifNull(prefix_max_value, value_number), value_number))) OVER revision_window AS previous_max_value
    FROM clean_input
    WINDOW
      point_window AS (
        PARTITION BY tenant_id, site_id, point_id, row_kind = 'PREFIX_MAX'
        ORDER BY sampled_at ASC, observation_id ASC
        ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
      ),
      revision_window AS (
        PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key
        ORDER BY sampled_at ASC, observation_id ASC
        ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
      )
  ),
  clean_deltas AS (
    SELECT
      tenant_id,
      site_id,
      device_id,
      point_id,
      sensor_id,
      telemetry_key,
      unit,
      point_revision,
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
      toString(previous_quality) AS previous_quality,
      previous_quality_reasons,
      multiIf(
        previous_value IS NULL, 'INITIAL',
        previous_point_revision IS NULL OR point_revision != ifNull(previous_point_revision, toUInt32(0)), 'REVISION_BOUNDARY',
        ifNull(unit, '') != ifNull(previous_unit, ''), 'UNIT_BOUNDARY',
        counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND value_number < ifNull(previous_max_value, 0.0), 'INVALID_DECREASE',
        counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND ifNull(previous_value, 0.0) < ifNull(previous_max_value, 0.0) AND value_number >= ifNull(previous_max_value, 0.0), 'RECOVERY',
        value_number = ifNull(previous_value, 0.0), 'UNCHANGED',
        value_number > ifNull(previous_value, 0.0), 'INCREASE',
        ifNull(counter_decrease_mode, '') IN ('RESET_TO_ZERO', 'RESET', 'METER_REPLACEMENT'), 'RESET',
        counter_decrease_mode = 'ROLLOVER'
          AND counter_rollover_modulus IS NOT NULL
          AND ifNull(previous_value, 0.0) >= 0
          AND value_number >= 0
          AND ifNull(previous_value, 0.0) <= counter_rollover_modulus
          AND value_number < counter_rollover_modulus, 'ROLLOVER',
        'INVALID_DECREASE'
      ) AS transition_type,
      multiIf(
        previous_value IS NULL, CAST(NULL, 'Nullable(Float64)'),
        previous_point_revision IS NULL OR point_revision != ifNull(previous_point_revision, toUInt32(0)), CAST(NULL, 'Nullable(Float64)'),
        ifNull(unit, '') != ifNull(previous_unit, ''), CAST(NULL, 'Nullable(Float64)'),
        counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND value_number < ifNull(previous_max_value, 0.0), CAST(NULL, 'Nullable(Float64)'),
        counter_decrease_mode = 'INVALID' AND previous_max_value IS NOT NULL AND ifNull(previous_value, 0.0) < ifNull(previous_max_value, 0.0) AND value_number >= ifNull(previous_max_value, 0.0), toNullable(value_number - ifNull(previous_max_value, 0.0)),
        value_number >= ifNull(previous_value, 0.0), toNullable(value_number - ifNull(previous_value, 0.0)),
        ifNull(counter_decrease_mode, '') IN ('RESET_TO_ZERO', 'RESET', 'METER_REPLACEMENT'), toNullable(value_number),
        counter_decrease_mode = 'ROLLOVER'
          AND counter_rollover_modulus IS NOT NULL
          AND ifNull(previous_value, 0.0) >= 0
          AND value_number >= 0
          AND ifNull(previous_value, 0.0) <= counter_rollover_modulus
          AND value_number < counter_rollover_modulus,
          toNullable((counter_rollover_modulus - ifNull(previous_value, 0.0)) + value_number),
        CAST(NULL, 'Nullable(Float64)')
      ) AS delta_value
    FROM clean_windowed
    WHERE row_kind IN ('RANGE', 'NEXT')
  ),
  flagged_deltas AS (
    SELECT
      tenant_id,
      site_id,
      device_id,
      point_id,
      sensor_id,
      telemetry_key,
      unit,
      point_revision,
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
      prev_valid_observation_id AS previous_observation_id,
      prev_valid_value AS previous_value,
      prev_valid_sampled_at AS previous_sampled_at,
      toString(prev_valid_quality) AS previous_quality,
      prev_valid_quality_reasons AS previous_quality_reasons,
      multiIf(
        is_jitter_rollback = 1, 'UNCHANGED',
        is_zero_drop = 1, 'INVALID_ZERO_DROP',
        'INVALID_SPIKE'
      ) AS transition_type,
      if(is_jitter_rollback = 1, toNullable(0.0), CAST(NULL, 'Nullable(Float64)')) AS delta_value
    FROM seq_with_valid_prev
    WHERE row_kind IN ('RANGE', 'NEXT') AND is_valid_anchor = 0
  )
SELECT * FROM clean_deltas
UNION ALL
SELECT * FROM flagged_deltas;

GRANT SELECT ON telemetry_history.observations TO telemetry_query_history_reader;
GRANT SELECT ON telemetry_history.observations TO metric_engine_reader;
GRANT SELECT ON telemetry_history.observations TO metric_engine_runtime;
GRANT SELECT ON telemetry_history.observations TO forecast_service_reader;
GRANT SELECT ON telemetry_history.observations TO optimization_service_reader;
GRANT SELECT ON telemetry_history.observations TO fdd_service_reader;
GRANT INSERT ON telemetry_history.observations TO telemetry_history_writer;
GRANT SELECT(sampled_at, tenant_id, site_id, device_id, point_id, sensor_id, telemetry_key, unit, value_number, acceptance_status, point_type)
  ON telemetry_history.observations TO telemetry_history_writer;
GRANT SELECT, ALTER DELETE ON telemetry_history.observations TO telemetry_retention_writer;

ALTER TABLE analytics.metric_result_facts
  ADD INDEX IF NOT EXISTS metric_binding_id_bf metric_binding_id TYPE bloom_filter(0.01) GRANULARITY 1;

REVOKE ALTER DELETE ON analytics.metric_result_facts FROM metric_engine_writer, metric_engine_runtime;
GRANT SELECT, ALTER DELETE ON analytics.metric_result_facts TO telemetry_retention_writer;

ALTER TABLE analytics.metric_result_facts MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.metric_result_facts MODIFY TTL toDateTime(period_start) + INTERVAL 1825 DAY DELETE;

ALTER TABLE analytics.energy_interval_facts MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.energy_interval_facts MODIFY TTL toDateTime(period_end) + INTERVAL 1825 DAY DELETE;

ALTER TABLE analytics.energy_hourly_states MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.energy_hourly_states MODIFY TTL toDateTime(hour) + INTERVAL 1825 DAY DELETE;

ALTER TABLE analytics.energy_hourly_facts_v2 MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.energy_hourly_facts_v2 MODIFY TTL hour + INTERVAL 1825 DAY DELETE;

ALTER TABLE analytics.site_condition_anchors_hourly MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.site_condition_anchors_hourly MODIFY TTL hour + INTERVAL 1825 DAY DELETE;
