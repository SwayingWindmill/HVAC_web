CREATE TABLE IF NOT EXISTS analytics.optimization_evaluations (
  evaluation_id UUID,
  tenant_id UUID,
  site_id UUID,
  optimization_run_id UUID,
  recommendation_id UUID,
  subject_type LowCardinality(String),
  subject_id UUID,
  objective LowCardinality(String),
  solver_outcome LowCardinality(String),
  quality LowCardinality(String),
  constraint_count UInt32,
  input_snapshot_id UUID,
  policy_version_id UUID,
  topology_version_id UUID,
  load_forecast_snapshot_id UUID,
  pv_forecast_snapshot_id Nullable(UUID),
  tariff_version_id UUID,
  evaluation_json String,
  generated_at DateTime64(3, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(generated_at)
ORDER BY (tenant_id, site_id, optimization_run_id, evaluation_id)
SETTINGS index_granularity = 8192;

CREATE USER IF NOT EXISTS optimization_service_reader IDENTIFIED WITH no_password;
CREATE USER IF NOT EXISTS optimization_service_writer IDENTIFIED WITH no_password;

GRANT SELECT ON analytics.optimization_evaluations TO optimization_service_reader;
GRANT INSERT ON analytics.optimization_evaluations TO optimization_service_writer;
GRANT SELECT ON analytics.optimization_evaluations TO cube_analytics_reader;
