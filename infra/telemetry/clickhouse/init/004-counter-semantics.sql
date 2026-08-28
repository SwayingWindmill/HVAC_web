-- Counter deltas are derived from raw historical facts in event-time order.
-- Latest state is not consulted, and a Point revision boundary never produces a delta.
ALTER TABLE telemetry_history.observations
  ADD COLUMN IF NOT EXISTS point_type LowCardinality(Nullable(String));
ALTER TABLE telemetry_history.observations
  ADD COLUMN IF NOT EXISTS point_revision Nullable(UInt64);
ALTER TABLE telemetry_history.observations
  ADD COLUMN IF NOT EXISTS counter_decrease_mode LowCardinality(Nullable(String));
ALTER TABLE telemetry_history.observations
  ADD COLUMN IF NOT EXISTS counter_rollover_modulus Nullable(Float64);

-- The projector receives SELECT only on this canonical view. DEFINER keeps the
-- raw observations table outside the projector identity's read boundary.
CREATE OR REPLACE VIEW telemetry_history.counter_deltas
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
WITH ordered AS (
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
    source_event_id,
    source_partition,
    source_offset,
    quality,
    quality_reasons,
    value_number,
    lagInFrame(toNullable(observation_id)) OVER point_window AS previous_observation_id,
    lagInFrame(value_number) OVER point_window AS previous_value,
    lagInFrame(sampled_at) OVER point_window AS previous_sampled_at,
    lagInFrame(quality, 1, '') OVER point_window AS previous_quality,
    lagInFrame(quality_reasons, 1, []) OVER point_window AS previous_quality_reasons,
    lagInFrame(point_revision) OVER point_window AS previous_point_revision,
    lagInFrame(unit) OVER point_window AS previous_unit,
    max(value_number) OVER revision_window AS previous_max_value
  FROM telemetry_history.observations
  WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND point_type = 'COUNTER'
    AND value_number IS NOT NULL
    AND tenant_id IS NOT NULL
    AND site_id IS NOT NULL
    AND device_id IS NOT NULL
    AND point_id IS NOT NULL
  WINDOW point_window AS (
    PARTITION BY tenant_id, site_id, point_id
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
FROM ordered;

CREATE VIEW IF NOT EXISTS telemetry_history.counter_1min AS
SELECT
  toStartOfMinute(sampled_at) AS bucket,
  tenant_id,
  site_id,
  point_id,
  any(device_id) AS device_id,
  any(sensor_id) AS sensor_id,
  any(telemetry_key) AS telemetry_key,
  any(unit) AS unit,
  sum(ifNull(delta_value, 0.0)) AS delta_sum,
  countIf(delta_value IS NOT NULL) AS delta_count,
  countIf(transition_type = 'RESET') AS reset_count,
  countIf(transition_type = 'ROLLOVER') AS rollover_count,
  countIf(transition_type IN ('INVALID_DECREASE', 'REVISION_BOUNDARY', 'UNIT_BOUNDARY')) AS excluded_transition_count
FROM telemetry_history.counter_deltas
GROUP BY bucket, tenant_id, site_id, point_id;

CREATE VIEW IF NOT EXISTS telemetry_history.counter_15min AS
SELECT
  toStartOfInterval(sampled_at, INTERVAL 15 MINUTE) AS bucket,
  tenant_id,
  site_id,
  point_id,
  any(device_id) AS device_id,
  any(sensor_id) AS sensor_id,
  any(telemetry_key) AS telemetry_key,
  any(unit) AS unit,
  sum(ifNull(delta_value, 0.0)) AS delta_sum,
  countIf(delta_value IS NOT NULL) AS delta_count,
  countIf(transition_type = 'RESET') AS reset_count,
  countIf(transition_type = 'ROLLOVER') AS rollover_count,
  countIf(transition_type IN ('INVALID_DECREASE', 'REVISION_BOUNDARY', 'UNIT_BOUNDARY')) AS excluded_transition_count
FROM telemetry_history.counter_deltas
GROUP BY bucket, tenant_id, site_id, point_id;

CREATE VIEW IF NOT EXISTS telemetry_history.counter_hourly AS
SELECT
  toStartOfHour(sampled_at) AS bucket,
  tenant_id,
  site_id,
  point_id,
  any(device_id) AS device_id,
  any(sensor_id) AS sensor_id,
  any(telemetry_key) AS telemetry_key,
  any(unit) AS unit,
  sum(ifNull(delta_value, 0.0)) AS delta_sum,
  countIf(delta_value IS NOT NULL) AS delta_count,
  countIf(transition_type = 'RESET') AS reset_count,
  countIf(transition_type = 'ROLLOVER') AS rollover_count,
  countIf(transition_type IN ('INVALID_DECREASE', 'REVISION_BOUNDARY', 'UNIT_BOUNDARY')) AS excluded_transition_count
FROM telemetry_history.counter_deltas
GROUP BY bucket, tenant_id, site_id, point_id;
