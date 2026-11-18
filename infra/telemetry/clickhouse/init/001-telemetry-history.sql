CREATE DATABASE IF NOT EXISTS telemetry_history;

CREATE TABLE IF NOT EXISTS telemetry_history.observations (
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
  -- History Sequence of the Telemetry batch that wrote the row; increases in visibility order.
  history_sequence UInt64,
  INDEX history_sequence_minmax history_sequence TYPE minmax GRANULARITY 1,
  INDEX point_id_bf point_id TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX device_telemetry_bf (device_id, telemetry_key) TYPE bloom_filter(0.01) GRANULARITY 1,
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
-- The deduplication window only has to cover retried history batches (about one insert
-- per poll interval); every remembered block stays in server memory.
SETTINGS index_granularity = 8192,
         non_replicated_deduplication_window = 10000;
