-- 1) Rewrite telemetry_history.counter_deltas_from into a single-pass linear CTE pipeline (4 base
--    scans total: pre_anchors, in_range, post_next_pool, prefix_max; zero self-joins and zero
--    UNION ALL duplication) while keeping exact output parity with 025.
-- 2) Grant SELECT ON analytics.energy_hourly_states to analytics_projector_reader and
--    telemetry_query_history_reader so invoker queries on analytics.energy_hourly succeed without Code: 497.

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
      tenant_id, site_id, point_id, device_id, sensor_id, telemetry_key, unit, unit_key, point_revision,
      counter_decrease_mode, counter_rollover_modulus, sampled_at, received_at, observation_id,
      source_event_id, source_partition, source_offset, quality, quality_reasons, value_number,
      unit_max_rate, unit_jitter_tol
    FROM post_next_pool
  ),
  prefix_max AS (
    SELECT
      tenant_id,
      site_id,
      point_id,
      point_revision,
      unit_key,
      toNullable(max(value_number)) AS prefix_max_value
    FROM scoped
    WHERE sampled_at < {since:DateTime64(3, 'UTC')}
    GROUP BY tenant_id, site_id, point_id, point_revision, unit_key
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
      if(
        countIf(is_zero_drop = 0) OVER seg_prev > 0,
        argMaxIf(toNullable(value_number), tuple(sampled_at, observation_id), is_zero_drop = 0) OVER seg_prev,
        CAST(NULL, 'Nullable(Float64)')
      ) AS p1_val,
      if(
        countIf(is_zero_drop = 0) OVER seg_prev > 0,
        argMaxIf(toNullable(sampled_at), tuple(sampled_at, observation_id), is_zero_drop = 0) OVER seg_prev,
        CAST(NULL, 'Nullable(DateTime64(3, \'UTC\'))')
      ) AS p1_at
    FROM zero_flagged
    WINDOW seg_prev AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
  ),
  spike1_flagged AS (
    SELECT
      *,
      if(
        is_zero_drop = 0 AND p1_val IS NOT NULL AND p1_at IS NOT NULL AND (
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
      if(
        countIf(is_zero_drop = 0 AND is_spike_1 = 0) OVER seg_prev > 0,
        argMaxIf(toNullable(value_number), tuple(sampled_at, observation_id), is_zero_drop = 0 AND is_spike_1 = 0) OVER seg_prev,
        CAST(NULL, 'Nullable(Float64)')
      ) AS p2_val,
      if(
        countIf(is_zero_drop = 0 AND is_spike_1 = 0) OVER seg_prev > 0,
        argMaxIf(toNullable(sampled_at), tuple(sampled_at, observation_id), is_zero_drop = 0 AND is_spike_1 = 0) OVER seg_prev,
        CAST(NULL, 'Nullable(DateTime64(3, \'UTC\'))')
      ) AS p2_at
    FROM spike1_flagged
    WINDOW seg_prev AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
  ),
  spike2_flagged AS (
    SELECT
      *,
      if(
        is_zero_drop = 0 AND is_spike_1 = 0 AND p2_val IS NOT NULL AND p2_at IS NOT NULL AND (
          (value_number > ifNull(p2_val, 0.0) AND (value_number - ifNull(p2_val, 0.0)) > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p2_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0))
          OR (value_number < ifNull(p2_val, 0.0) - unit_jitter_tol AND ifNull(counter_decrease_mode, '') IN ('RESET_TO_ZERO', 'RESET', 'METER_REPLACEMENT') AND value_number > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p2_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0))
          OR (value_number < ifNull(p2_val, 0.0) - unit_jitter_tol AND counter_decrease_mode = 'ROLLOVER' AND counter_rollover_modulus IS NOT NULL AND counter_rollover_modulus > 0 AND (ifNull(p2_val, 0.0) > counter_rollover_modulus OR value_number >= counter_rollover_modulus OR ((counter_rollover_modulus - ifNull(p2_val, 0.0)) + value_number) > unit_max_rate * greatest(dateDiff('millisecond', ifNull(p2_at, sampled_at), sampled_at) / 3600000.0, 1.0 / 60.0)))
        ),
        toUInt8(1),
        toUInt8(0)
      ) AS is_spike_2
    FROM spike2_lag
  ),
  epoch_tagged AS (
    SELECT
      *,
      sum(
        if(
          is_zero_drop = 0 AND is_spike_1 = 0 AND is_spike_2 = 0 AND p2_val IS NOT NULL AND value_number < ifNull(p2_val, 0.0) - unit_jitter_tol,
          toUInt64(1),
          toUInt64(0)
        )
      ) OVER (
        PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key
        ORDER BY sampled_at ASC, observation_id ASC
        ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
      ) AS reset_epoch
    FROM spike2_flagged
  ),
  jitter_bounds AS (
    SELECT
      *,
      if(
        countIf(is_zero_drop = 0 AND is_spike_1 = 0 AND is_spike_2 = 0) OVER epoch_prev > 0,
        maxIf(toNullable(value_number), is_zero_drop = 0 AND is_spike_1 = 0 AND is_spike_2 = 0) OVER epoch_prev,
        CAST(NULL, 'Nullable(Float64)')
      ) AS epoch_max_before
    FROM epoch_tagged
    WINDOW epoch_prev AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key, reset_epoch ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
  ),
  seq_annotated AS (
    SELECT
      *,
      if(
        is_zero_drop = 0
        AND is_spike_1 = 0
        AND is_spike_2 = 0
        AND epoch_max_before IS NOT NULL
        AND value_number > 0
        AND value_number < ifNull(epoch_max_before, 0.0)
        AND (ifNull(epoch_max_before, 0.0) - value_number) <= unit_jitter_tol,
        toUInt8(1),
        toUInt8(0)
      ) AS is_jitter_rollback,
      if(
        is_zero_drop = 0
        AND is_spike_1 = 0
        AND is_spike_2 = 0
        AND NOT (
          epoch_max_before IS NOT NULL
          AND value_number > 0
          AND value_number < ifNull(epoch_max_before, 0.0)
          AND (ifNull(epoch_max_before, 0.0) - value_number) <= unit_jitter_tol
        ),
        toUInt8(1),
        toUInt8(0)
      ) AS is_valid_anchor
    FROM jitter_bounds
  ),
  seq_windowed AS (
    SELECT
      *,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(toNullable(observation_id), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST(NULL, 'Nullable(UUID)')) AS previous_observation_id,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(toNullable(value_number), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST(NULL, 'Nullable(Float64)')) AS previous_value,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(toNullable(sampled_at), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST(NULL, 'Nullable(DateTime64(3, \'UTC\'))')) AS previous_sampled_at,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(quality, tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, '') AS previous_quality,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(quality_reasons, tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST([], 'Array(String)')) AS previous_quality_reasons,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(toNullable(point_revision), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, CAST(NULL, 'Nullable(UInt64)')) AS previous_point_revision,
      if(countIf(is_valid_anchor = 1) OVER pt_prev > 0, argMaxIf(tuple(unit), tuple(sampled_at, observation_id), is_valid_anchor = 1) OVER pt_prev, tuple(CAST(NULL, 'Nullable(String)'))).1 AS previous_unit,
      if(countIf(is_valid_anchor = 1) OVER rev_prev > 0, maxIf(toNullable(value_number), is_valid_anchor = 1) OVER rev_prev, CAST(NULL, 'Nullable(Float64)')) AS window_prev_max_value
    FROM seq_annotated
    WINDOW
      pt_prev AS (PARTITION BY tenant_id, site_id, point_id ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),
      rev_prev AS (PARTITION BY tenant_id, site_id, point_id, point_revision, unit_key ORDER BY sampled_at ASC, observation_id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
  ),
  resolved_window AS (
    SELECT
      w.*,
      multiIf(
        pm.prefix_max_value IS NOT NULL AND w.window_prev_max_value IS NOT NULL,
        toNullable(greatest(assumeNotNull(pm.prefix_max_value), assumeNotNull(w.window_prev_max_value))),
        pm.prefix_max_value IS NOT NULL, pm.prefix_max_value,
        w.window_prev_max_value
      ) AS previous_max_value
    FROM seq_windowed AS w
    LEFT JOIN prefix_max AS pm
      ON w.tenant_id = pm.tenant_id
     AND w.site_id = pm.site_id
     AND w.point_id = pm.point_id
     AND w.point_revision = pm.point_revision
     AND w.unit_key = pm.unit_key
    WHERE w.row_kind IN ('RANGE', 'NEXT')
  )
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
  if(
    is_valid_anchor = 0,
    multiIf(
      is_jitter_rollback = 1, 'UNCHANGED',
      is_zero_drop = 1, 'INVALID_ZERO_DROP',
      'INVALID_SPIKE'
    ),
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
    )
  ) AS transition_type,
  if(
    is_valid_anchor = 0,
    if(is_jitter_rollback = 1, toNullable(0.0), CAST(NULL, 'Nullable(Float64)')),
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
    )
  ) AS delta_value
FROM resolved_window;

GRANT SELECT ON analytics.energy_hourly_states TO analytics_projector_reader, telemetry_query_history_reader;
