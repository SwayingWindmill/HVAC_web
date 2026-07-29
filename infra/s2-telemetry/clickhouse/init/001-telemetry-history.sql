CREATE DATABASE IF NOT EXISTS telemetry_history;

CREATE TABLE IF NOT EXISTS telemetry_history.observations (
  observation_id UUID,
  owning_organization_id Nullable(UUID),
  site_id Nullable(UUID),
  device_id Nullable(UUID),
  integration_instance_id UUID,
  source_event_id UUID,
  source_partition LowCardinality(String),
  source_offset UInt64,
  source_path LowCardinality(String),
  telemetry_key LowCardinality(String),
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
  projected_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(sampled_at)
ORDER BY (
  ifNull(owning_organization_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(site_id, toUUID('00000000-0000-0000-0000-000000000000')),
  ifNull(device_id, toUUID('00000000-0000-0000-0000-000000000000')),
  telemetry_key,
  sampled_at,
  observation_id
)
TTL sampled_at + INTERVAL 36 MONTH DELETE
SETTINGS index_granularity = 8192,
         non_replicated_deduplication_window = 100000;

CREATE TABLE IF NOT EXISTS telemetry_history.numeric_hourly_states (
  hour DateTime('UTC'),
  owning_organization_id UUID,
  site_id UUID,
  device_id UUID,
  telemetry_key LowCardinality(String),
  unit String,
  sample_count AggregateFunction(count),
  average_value AggregateFunction(avg, Float64),
  minimum_value AggregateFunction(min, Float64),
  maximum_value AggregateFunction(max, Float64)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (owning_organization_id, site_id, device_id, telemetry_key, unit, hour)
SETTINGS non_replicated_deduplication_window = 100000;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_history.observations_to_numeric_hourly
TO telemetry_history.numeric_hourly_states
AS
SELECT
  hour,
  organization_id AS owning_organization_id,
  resolved_site_id AS site_id,
  resolved_device_id AS device_id,
  telemetry_key,
  resolved_unit AS unit,
  countState() AS sample_count,
  avgState(value_number) AS average_value,
  minState(value_number) AS minimum_value,
  maxState(value_number) AS maximum_value
FROM (
  SELECT
    toStartOfHour(sampled_at) AS hour,
    assumeNotNull(owning_organization_id) AS organization_id,
    assumeNotNull(site_id) AS resolved_site_id,
    assumeNotNull(device_id) AS resolved_device_id,
    telemetry_key,
    ifNull(unit, '') AS resolved_unit,
    assumeNotNull(value_number) AS value_number
  FROM telemetry_history.observations
  WHERE acceptance_status = 'ACCEPTED'
    AND value_number IS NOT NULL
    AND owning_organization_id IS NOT NULL
    AND site_id IS NOT NULL
    AND device_id IS NOT NULL
)
GROUP BY
  hour,
  organization_id,
  resolved_site_id,
  resolved_device_id,
  telemetry_key,
  resolved_unit;

CREATE VIEW IF NOT EXISTS telemetry_history.numeric_hourly AS
SELECT
  hour,
  owning_organization_id,
  site_id,
  device_id,
  telemetry_key,
  nullIf(unit, '') AS unit,
  countMerge(sample_count) AS sample_count,
  avgMerge(average_value) AS average_value,
  minMerge(minimum_value) AS minimum_value,
  maxMerge(maximum_value) AS maximum_value
FROM telemetry_history.numeric_hourly_states
GROUP BY hour, owning_organization_id, site_id, device_id, telemetry_key, unit;
