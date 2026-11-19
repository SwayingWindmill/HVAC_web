-- P0 energy accounting hardening:
-- 1) Clean bus zero-drops, tiny communication jitter rollbacks, and physical step spikes
--    inside telemetry_history.counter_deltas_from while preserving raw.point_id pruning.
-- 2) Attribute energy intervals using half-open (period_start, period_end] Option A hours:
--    toStartOfHour(addMilliseconds(period_end, -1)) AS hour.

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
      ifNull(raw.unit, '') AS unit_key,
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

DROP VIEW IF EXISTS analytics.energy_facts_to_hourly;
CREATE MATERIALIZED VIEW analytics.energy_facts_to_hourly
TO analytics.energy_hourly_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id,
  site_id,
  point_id,
  toStartOfHour(addMilliseconds(period_end, -1)) AS hour,
  accounting_scope,
  energy_type,
  quality,
  ifNull(load_device_id, toUUID('00000000-0000-0000-0000-000000000000')) AS load_device_id,
  energy_kwh,
  toInt64(1) AS interval_count,
  data_watermark,
  dataset_revision
FROM analytics.energy_interval_facts
WHERE period_end > period_start
  AND dateDiff('millisecond', period_start, period_end) <= 3600000;

DROP VIEW IF EXISTS analytics.energy_fact_corrections_to_hourly;
CREATE MATERIALIZED VIEW analytics.energy_fact_corrections_to_hourly
TO analytics.energy_hourly_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  prior.tenant_id AS tenant_id,
  prior.site_id AS site_id,
  prior.point_id AS point_id,
  toStartOfHour(addMilliseconds(prior.period_end, -1)) AS hour,
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
WHERE correction.fact_revision > 0
  AND prior.period_end > prior.period_start
  AND dateDiff('millisecond', prior.period_start, prior.period_end) <= 3600000;
