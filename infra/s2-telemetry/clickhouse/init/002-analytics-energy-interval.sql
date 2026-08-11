CREATE DATABASE IF NOT EXISTS analytics;

CREATE TABLE IF NOT EXISTS analytics.energy_interval_facts (
  fact_id UUID,
  tenant_id UUID,
  organization_id UUID,
  site_id UUID,
  device_id UUID,
  point_id UUID,
  sensor_id Nullable(UUID),
  telemetry_key LowCardinality(String),
  energy_type LowCardinality(String),
  period_start DateTime64(3, 'UTC'),
  period_end DateTime64(3, 'UTC'),
  energy_kwh Float64,
  quality LowCardinality(String),
  quality_reasons Array(String),
  observation_count UInt8,
  source_previous_observation_id UUID,
  source_current_observation_id UUID,
  source_offset UInt64,
  dataset_revision UInt64,
  data_watermark DateTime64(3, 'UTC'),
  projected_at DateTime64(3, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(period_end)
ORDER BY (
  tenant_id,
  organization_id,
  site_id,
  point_id,
  device_id,
  energy_type,
  period_end,
  source_current_observation_id
)
TTL period_end + INTERVAL 36 MONTH DELETE
SETTINGS index_granularity = 8192,
         non_replicated_deduplication_window = 100000;

CREATE USER IF NOT EXISTS analytics_projector_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS analytics_projector_writer IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS cube_analytics_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS telemetry_query_history_reader IDENTIFIED WITH no_password;

GRANT SELECT ON telemetry_history.observations TO analytics_projector_reader;
GRANT SELECT ON analytics.energy_interval_facts TO analytics_projector_reader;
GRANT INSERT ON analytics.energy_interval_facts TO analytics_projector_writer;
GRANT SELECT ON analytics.energy_interval_facts TO cube_analytics_reader;
GRANT SELECT ON telemetry_history.observations TO telemetry_query_history_reader;
