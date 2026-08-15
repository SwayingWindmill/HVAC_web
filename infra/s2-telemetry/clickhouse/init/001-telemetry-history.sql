CREATE DATABASE IF NOT EXISTS telemetry_history;

CREATE TABLE IF NOT EXISTS telemetry_history.observations (
  observation_id UUID,
  tenant_id Nullable(UUID),
  site_id Nullable(UUID),
  device_id Nullable(UUID),
  point_id Nullable(UUID),
  sensor_id Nullable(UUID),
  integration_instance_id UUID,
  source_event_id UUID,
  source_partition LowCardinality(String),
  source_offset UInt64,
  source_path LowCardinality(String),
  telemetry_key LowCardinality(String),
  point_type LowCardinality(Nullable(String)),
  point_revision Nullable(UInt64),
  counter_decrease_mode LowCardinality(Nullable(String)),
  counter_rollover_modulus Nullable(Float64),
  value_type Nullable(String),
  unit Nullable(String),
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
  CONSTRAINT accepted_tenant_scope CHECK acceptance_status != 'ACCEPTED' OR (tenant_id IS NOT NULL AND site_id IS NOT NULL AND device_id IS NOT NULL AND point_id IS NOT NULL)
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(sampled_at)
ORDER BY (
  ifNull(tenant_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(site_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(point_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(sensor_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(device_id, toUUID('00000000-0000-0000-0000-000000000000')),
  telemetry_key,
  sampled_at,
  observation_id
)
SETTINGS index_granularity = 8192,
         non_replicated_deduplication_window = 100000;

CREATE TABLE IF NOT EXISTS telemetry_history.numeric_hourly_states (
  hour DateTime('UTC'),
  tenant_id UUID,
  site_id UUID,
  device_id UUID,
  point_id UUID,
  sensor_id UUID,
  telemetry_key LowCardinality(String),
  unit String,
  sample_count AggregateFunction(count),
  average_value AggregateFunction(avg, Float64),
  minimum_value AggregateFunction(min, Float64),
  maximum_value AggregateFunction(max, Float64)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, unit, hour)
SETTINGS non_replicated_deduplication_window = 100000;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_history.observations_to_numeric_hourly
TO telemetry_history.numeric_hourly_states
AS
SELECT
  hour,
  resolved_tenant_id AS tenant_id,
  resolved_site_id AS site_id,
  resolved_device_id AS device_id,
  resolved_point_id AS point_id,
  resolved_sensor_id AS sensor_id,
  telemetry_key,
  resolved_unit AS unit,
  countState() AS sample_count,
  avgState(value_number) AS average_value,
  minState(value_number) AS minimum_value,
  maxState(value_number) AS maximum_value
FROM (
  SELECT
    toStartOfHour(sampled_at) AS hour,
    assumeNotNull(tenant_id) AS resolved_tenant_id,
    assumeNotNull(site_id) AS resolved_site_id,
    assumeNotNull(device_id) AS resolved_device_id,
    assumeNotNull(point_id) AS resolved_point_id,
    ifNull(sensor_id, toUUID('00000000-0000-0000-0000-000000000000')) AS resolved_sensor_id,
    telemetry_key,
    ifNull(unit, '') AS resolved_unit,
    assumeNotNull(value_number) AS value_number
  FROM telemetry_history.observations
  WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND point_type <> 'COUNTER'
    AND value_number IS NOT NULL
    AND tenant_id IS NOT NULL
    AND site_id IS NOT NULL
    AND device_id IS NOT NULL
    AND point_id IS NOT NULL
)
GROUP BY
  hour,
  resolved_tenant_id,
  resolved_site_id,
  resolved_device_id,
  resolved_point_id,
  resolved_sensor_id,
  telemetry_key,
  resolved_unit;

CREATE VIEW IF NOT EXISTS telemetry_history.numeric_hourly AS
SELECT
  hour,
  tenant_id,
  site_id,
  device_id,
  point_id,
  nullIf(sensor_id, toUUID('00000000-0000-0000-0000-000000000000')) AS sensor_id,
  telemetry_key,
  nullIf(unit, '') AS unit,
  countMerge(sample_count) AS sample_count,
  avgMerge(average_value) AS average_value,
  minMerge(minimum_value) AS minimum_value,
  maxMerge(maximum_value) AS maximum_value
FROM telemetry_history.numeric_hourly_states
GROUP BY hour, tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, unit;
