CREATE TABLE IF NOT EXISTS analytics.metric_result_facts (
  result_id UUID,
  tenant_id UUID,
  site_id UUID,
  subject_type LowCardinality(String),
  subject_id UUID,
  metric_id UUID,
  metric_version_id UUID,
  metric_code LowCardinality(String),
  metric_version UInt64,
  metric_binding_id UUID,
  binding_version UInt64,
  period_start DateTime64(3, 'UTC'),
  period_end DateTime64(3, 'UTC'),
  calculated_at DateTime64(3, 'UTC'),
  granularity LowCardinality(String),
  value_type LowCardinality(String),
  value_json String,
  value_number Nullable(Float64),
  value_string Nullable(String),
  value_boolean Nullable(UInt8),
  unit LowCardinality(String),
  quality LowCardinality(String),
  completeness Float64,
  calculation_run_id UUID,
  revision UInt64,
  provenance String
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(period_start)
ORDER BY (
  tenant_id,
  site_id,
  metric_id,
  subject_type,
  subject_id,
  granularity,
  period_start,
  period_end,
  revision,
  result_id
)
SETTINGS index_granularity = 8192;

CREATE USER IF NOT EXISTS metric_engine_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS metric_engine_writer IDENTIFIED WITH no_password;

GRANT SELECT ON telemetry_history.observations TO metric_engine_reader;
GRANT SELECT ON telemetry_history.counter_deltas TO metric_engine_reader;
GRANT SELECT ON analytics.energy_interval_facts TO metric_engine_reader;
GRANT SELECT ON analytics.metric_result_facts TO metric_engine_reader;
GRANT INSERT ON analytics.metric_result_facts TO metric_engine_writer;
GRANT ALTER DELETE ON analytics.metric_result_facts TO metric_engine_writer;
GRANT SELECT ON analytics.metric_result_facts TO cube_analytics_reader;
GRANT SELECT ON analytics.metric_result_facts TO settlement_reader;
