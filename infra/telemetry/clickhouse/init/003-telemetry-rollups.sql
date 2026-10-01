-- Phase 1 layered telemetry rollups required by the canonical data architecture.
-- Raw authority remains telemetry_history.observations. These layers are rebuildable.

CREATE TABLE IF NOT EXISTS telemetry_history.numeric_1min_states (
  bucket DateTime('UTC'),
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
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, unit, bucket)
SETTINGS non_replicated_deduplication_window = 100000;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_history.observations_to_numeric_1min
TO telemetry_history.numeric_1min_states
AS
SELECT
  toStartOfMinute(sampled_at) AS bucket,
  assumeNotNull(tenant_id) AS tenant_id,
  assumeNotNull(site_id) AS site_id,
  assumeNotNull(device_id) AS device_id,
  assumeNotNull(point_id) AS point_id,
  ifNull(sensor_id, toUUID('00000000-0000-0000-0000-000000000000')) AS sensor_id,
  telemetry_key,
  ifNull(unit, '') AS unit,
  countState() AS sample_count,
  avgState(assumeNotNull(value_number)) AS average_value,
  minState(assumeNotNull(value_number)) AS minimum_value,
  maxState(assumeNotNull(value_number)) AS maximum_value
FROM telemetry_history.observations
WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_type <> 'COUNTER'
  AND value_number IS NOT NULL
  AND tenant_id IS NOT NULL
  AND site_id IS NOT NULL
  AND device_id IS NOT NULL
  AND point_id IS NOT NULL
GROUP BY bucket, tenant_id, site_id, device_id, point_id, sensor_id, telemetry_key, unit;

CREATE VIEW IF NOT EXISTS telemetry_history.numeric_1min AS
SELECT
  bucket,
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
FROM telemetry_history.numeric_1min_states
GROUP BY bucket, tenant_id, site_id, device_id, point_id, sensor_id, telemetry_key, unit;

CREATE TABLE IF NOT EXISTS telemetry_history.numeric_15min_states (
  bucket DateTime('UTC'),
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
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, unit, bucket)
SETTINGS non_replicated_deduplication_window = 100000;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_history.observations_to_numeric_15min
TO telemetry_history.numeric_15min_states
AS
SELECT
  toStartOfInterval(sampled_at, INTERVAL 15 MINUTE) AS bucket,
  assumeNotNull(tenant_id) AS tenant_id,
  assumeNotNull(site_id) AS site_id,
  assumeNotNull(device_id) AS device_id,
  assumeNotNull(point_id) AS point_id,
  ifNull(sensor_id, toUUID('00000000-0000-0000-0000-000000000000')) AS sensor_id,
  telemetry_key,
  ifNull(unit, '') AS unit,
  countState() AS sample_count,
  avgState(assumeNotNull(value_number)) AS average_value,
  minState(assumeNotNull(value_number)) AS minimum_value,
  maxState(assumeNotNull(value_number)) AS maximum_value
FROM telemetry_history.observations
WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_type <> 'COUNTER'
  AND value_number IS NOT NULL
  AND tenant_id IS NOT NULL
  AND site_id IS NOT NULL
  AND device_id IS NOT NULL
  AND point_id IS NOT NULL
GROUP BY bucket, tenant_id, site_id, device_id, point_id, sensor_id, telemetry_key, unit;

CREATE VIEW IF NOT EXISTS telemetry_history.numeric_15min AS
SELECT
  bucket,
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
FROM telemetry_history.numeric_15min_states
GROUP BY bucket, tenant_id, site_id, device_id, point_id, sensor_id, telemetry_key, unit;

-- Deliberately no canonical daily rollup here. Business-day aggregation must use
-- the Site timezone from Registry; a UTC toStartOfDay bucket is not a V2 business day.
