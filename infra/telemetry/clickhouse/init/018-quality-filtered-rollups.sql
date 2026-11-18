-- Quality-filtered numeric gauge rollups (1-minute and 15-minute UTC states) for
-- history aggregate queries. Raw authority remains telemetry_history.observations.

CREATE TABLE IF NOT EXISTS telemetry_history.numeric_gauge_1min_states (
  bucket DateTime('UTC'),
  tenant_id UUID,
  site_id UUID,
  device_id UUID,
  point_id UUID,
  point_revision UInt64,
  telemetry_key LowCardinality(String),
  point_type LowCardinality(String),
  unit_state AggregateFunction(any, Nullable(String)),
  max_projected_at AggregateFunction(max, DateTime64(3, 'UTC')),
  good_count AggregateFunction(countIf, UInt8),
  partial_count AggregateFunction(countIf, UInt8),
  estimated_count AggregateFunction(countIf, UInt8),
  manual_count AggregateFunction(countIf, UInt8),
  stale_count AggregateFunction(countIf, UInt8),
  invalid_count AggregateFunction(countIf, UInt8),
  total_count AggregateFunction(count),
  valid_count AggregateFunction(countIf, UInt8),
  valid_avg AggregateFunction(avgIf, Float64, UInt8),
  valid_min AggregateFunction(minIf, Float64, UInt8),
  valid_max AggregateFunction(maxIf, Float64, UInt8),
  valid_first AggregateFunction(argMinIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8),
  valid_last AggregateFunction(argMaxIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8),
  usable_count AggregateFunction(countIf, UInt8),
  usable_avg AggregateFunction(avgIf, Float64, UInt8),
  usable_min AggregateFunction(minIf, Float64, UInt8),
  usable_max AggregateFunction(maxIf, Float64, UInt8),
  usable_first AggregateFunction(argMinIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8),
  usable_last AggregateFunction(argMaxIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, device_id, point_id, point_revision, telemetry_key, point_type, bucket)
SETTINGS non_replicated_deduplication_window = 10000;

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
  anyState(obs.unit) AS unit_state,
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

CREATE TABLE IF NOT EXISTS telemetry_history.numeric_gauge_15min_states (
  bucket DateTime('UTC'),
  tenant_id UUID,
  site_id UUID,
  device_id UUID,
  point_id UUID,
  point_revision UInt64,
  telemetry_key LowCardinality(String),
  point_type LowCardinality(String),
  unit_state AggregateFunction(any, Nullable(String)),
  max_projected_at AggregateFunction(max, DateTime64(3, 'UTC')),
  good_count AggregateFunction(countIf, UInt8),
  partial_count AggregateFunction(countIf, UInt8),
  estimated_count AggregateFunction(countIf, UInt8),
  manual_count AggregateFunction(countIf, UInt8),
  stale_count AggregateFunction(countIf, UInt8),
  invalid_count AggregateFunction(countIf, UInt8),
  total_count AggregateFunction(count),
  valid_count AggregateFunction(countIf, UInt8),
  valid_avg AggregateFunction(avgIf, Float64, UInt8),
  valid_min AggregateFunction(minIf, Float64, UInt8),
  valid_max AggregateFunction(maxIf, Float64, UInt8),
  valid_first AggregateFunction(argMinIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8),
  valid_last AggregateFunction(argMaxIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8),
  usable_count AggregateFunction(countIf, UInt8),
  usable_avg AggregateFunction(avgIf, Float64, UInt8),
  usable_min AggregateFunction(minIf, Float64, UInt8),
  usable_max AggregateFunction(maxIf, Float64, UInt8),
  usable_first AggregateFunction(argMinIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8),
  usable_last AggregateFunction(argMaxIf, Float64, Tuple(DateTime64(3, 'UTC'), String), UInt8)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, device_id, point_id, point_revision, telemetry_key, point_type, bucket)
SETTINGS non_replicated_deduplication_window = 10000;

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
  anyState(obs.unit) AS unit_state,
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

GRANT SELECT ON telemetry_history.numeric_gauge_1min_states TO telemetry_query_history_reader;
GRANT SELECT ON telemetry_history.numeric_gauge_15min_states TO telemetry_query_history_reader;

CREATE OR REPLACE VIEW telemetry_history.numeric_1min
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  bucket,
  tenant_id,
  site_id,
  device_id,
  point_id,
  CAST(NULL, 'Nullable(UUID)') AS sensor_id,
  telemetry_key,
  nullIf(anyMerge(unit_state), '') AS unit,
  countIfMerge(usable_count) AS sample_count,
  avgIfMerge(usable_avg) AS average_value,
  minIfMerge(usable_min) AS minimum_value,
  maxIfMerge(usable_max) AS maximum_value
FROM telemetry_history.numeric_gauge_1min_states
GROUP BY bucket, tenant_id, site_id, device_id, point_id, telemetry_key
HAVING sample_count > 0;

CREATE OR REPLACE VIEW telemetry_history.numeric_15min
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  bucket,
  tenant_id,
  site_id,
  device_id,
  point_id,
  CAST(NULL, 'Nullable(UUID)') AS sensor_id,
  telemetry_key,
  nullIf(anyMerge(unit_state), '') AS unit,
  countIfMerge(usable_count) AS sample_count,
  avgIfMerge(usable_avg) AS average_value,
  minIfMerge(usable_min) AS minimum_value,
  maxIfMerge(usable_max) AS maximum_value
FROM telemetry_history.numeric_gauge_15min_states
GROUP BY bucket, tenant_id, site_id, device_id, point_id, telemetry_key
HAVING sample_count > 0;

CREATE OR REPLACE VIEW telemetry_history.numeric_hourly
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  toStartOfHour(bucket) AS hour,
  tenant_id,
  site_id,
  device_id,
  point_id,
  CAST(NULL, 'Nullable(UUID)') AS sensor_id,
  telemetry_key,
  nullIf(anyMerge(unit_state), '') AS unit,
  countIfMerge(usable_count) AS sample_count,
  avgIfMerge(usable_avg) AS average_value,
  minIfMerge(usable_min) AS minimum_value,
  maxIfMerge(usable_max) AS maximum_value
FROM telemetry_history.numeric_gauge_15min_states
GROUP BY hour, tenant_id, site_id, device_id, point_id, telemetry_key
HAVING sample_count > 0;

GRANT SELECT ON telemetry_history.numeric_1min TO telemetry_query_history_reader;
GRANT SELECT ON telemetry_history.numeric_15min TO telemetry_query_history_reader;
GRANT SELECT ON telemetry_history.numeric_hourly TO telemetry_query_history_reader;

GRANT SELECT, ALTER DELETE ON telemetry_history.numeric_gauge_1min_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON telemetry_history.numeric_gauge_15min_states TO telemetry_retention_writer;
