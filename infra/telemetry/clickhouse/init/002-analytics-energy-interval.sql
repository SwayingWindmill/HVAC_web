CREATE DATABASE IF NOT EXISTS analytics;

CREATE TABLE IF NOT EXISTS analytics.energy_interval_facts (
  fact_id UUID,
  tenant_id UUID,
  site_id UUID,
  meter_id UUID,
  meter_binding_id UUID,
  topology_version_id UUID,
  binding_version UInt64,
  energy_type_id UUID,
  meter_role LowCardinality(String),
  direction LowCardinality(String),
  device_id UUID,
  point_id UUID,
  sensor_id Nullable(UUID),
  telemetry_key LowCardinality(String),
  energy_type LowCardinality(String),
  point_revision UInt64,
  unit LowCardinality(String),
  counter_decrease_mode LowCardinality(String),
  counter_rollover_modulus Nullable(Float64),
  period_start DateTime64(3, 'UTC'),
  period_end DateTime64(3, 'UTC'),
  energy_kwh Float64,
  transition_type LowCardinality(String),
  quality LowCardinality(String),
  quality_reasons Array(String),
  source_previous_observation_id UUID,
  source_current_observation_id UUID,
  source_event_id UUID,
  source_partition String,
  source_offset UInt64,
  dataset_revision UInt64,
  data_watermark DateTime64(3, 'UTC'),
  projected_at DateTime64(3, 'UTC'),
  fact_revision UInt64 DEFAULT 0,
  rebuild_run_id Nullable(UUID)
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(period_end)
ORDER BY (
  tenant_id,
  site_id,
  meter_id,
  meter_binding_id,
  point_id,
  device_id,
  energy_type,
  period_end,
  fact_revision,
  source_current_observation_id
)
SETTINGS index_granularity = 8192,
         non_replicated_deduplication_window = 100000;

CREATE USER IF NOT EXISTS analytics_projector_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS analytics_projector_writer IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS cube_analytics_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS telemetry_query_history_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS settlement_reader IDENTIFIED WITH no_password;

GRANT SELECT ON telemetry_history.counter_deltas TO analytics_projector_reader;
GRANT SELECT ON analytics.energy_interval_facts TO analytics_projector_reader;
GRANT INSERT ON analytics.energy_interval_facts TO analytics_projector_writer;
GRANT SELECT ON analytics.energy_interval_facts TO cube_analytics_reader;
-- Settlement reads released Metric results, not energy_interval_facts. Its grant is
-- declared with analytics.metric_result_facts after that dataset is created.
GRANT SELECT ON telemetry_history.observations TO telemetry_query_history_reader;
