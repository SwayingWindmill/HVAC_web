CREATE TABLE IF NOT EXISTS analytics.forecast_series (
  forecast_id UUID,
  tenant_id UUID,
  site_id UUID,
  subject_type LowCardinality(String),
  subject_id UUID,
  target LowCardinality(String),
  forecast_job_id UUID,
  forecast_snapshot_id UUID,
  deployment_id UUID,
  model_id UUID,
  model_version_id UUID,
  model_version UInt64,
  feature_set_version_id UUID,
  feature_set_version UInt64,
  input_snapshot_id UUID,
  topology_version_id UUID,
  forecast_origin DateTime64(3, 'UTC'),
  forecast_for DateTime64(3, 'UTC'),
  horizon_minutes UInt32,
  value Float64,
  unit String,
  lower_bound Nullable(Float64),
  upper_bound Nullable(Float64),
  quantile Nullable(Float64),
  quality LowCardinality(String),
  generated_at DateTime64(3, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(forecast_for)
ORDER BY (
  tenant_id,
  site_id,
  target,
  subject_type,
  subject_id,
  forecast_origin,
  forecast_for,
  forecast_id
)
SETTINGS index_granularity = 8192;

CREATE USER IF NOT EXISTS forecast_service_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS forecast_service_writer IDENTIFIED WITH no_password;

GRANT SELECT ON telemetry_history.observations TO forecast_service_reader;
GRANT SELECT ON telemetry_history.counter_deltas TO forecast_service_reader;
GRANT SELECT ON analytics.energy_interval_facts TO forecast_service_reader;
GRANT SELECT ON analytics.metric_result_facts TO forecast_service_reader;
GRANT SELECT ON analytics.forecast_series TO forecast_service_reader;
GRANT INSERT ON analytics.forecast_series TO forecast_service_writer;
GRANT SELECT ON analytics.forecast_series TO cube_analytics_reader;
