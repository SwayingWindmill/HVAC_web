-- Retire duplicate legacy numeric rollup materialized views and state tables, expose
-- read-only compatibility views backed by quality-filtered gauge rollup states, add
-- skip indexes and FINAL sync reads for point queries, and grant whole-partition
-- retention privileges.

DROP VIEW IF EXISTS telemetry_history.observations_to_numeric_1min;
DROP VIEW IF EXISTS telemetry_history.observations_to_numeric_15min;
DROP VIEW IF EXISTS telemetry_history.observations_to_numeric_hourly;

DROP TABLE IF EXISTS telemetry_history.numeric_1min_states;
DROP TABLE IF EXISTS telemetry_history.numeric_15min_states;
DROP TABLE IF EXISTS telemetry_history.numeric_hourly_states;

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

ALTER TABLE telemetry_history.observations
  ADD INDEX IF NOT EXISTS point_id_bf point_id TYPE bloom_filter(0.01) GRANULARITY 1;
ALTER TABLE telemetry_history.observations
  ADD INDEX IF NOT EXISTS device_telemetry_bf (device_id, telemetry_key) TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE analytics.metric_result_facts
  ADD INDEX IF NOT EXISTS result_id_bf result_id TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE analytics.forecast_series
  ADD INDEX IF NOT EXISTS forecast_id_bf forecast_id TYPE bloom_filter(0.01) GRANULARITY 1;
ALTER TABLE analytics.forecast_series
  ADD INDEX IF NOT EXISTS forecast_job_id_bf forecast_job_id TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE analytics.optimization_evaluations
  ADD INDEX IF NOT EXISTS evaluation_id_bf evaluation_id TYPE bloom_filter(0.01) GRANULARITY 1;

CREATE OR REPLACE VIEW analytics.tariff_price_windows_visible AS
SELECT tenant_id, site_id, window_start, window_end, period_code, energy_rate_per_kwh, tariff_version_id
FROM analytics.tariff_price_windows FINAL
WHERE (tenant_id, site_id, generation) IN (
  SELECT tenant_id, site_id, max(generation) FROM analytics.tariff_price_window_syncs FINAL GROUP BY tenant_id, site_id
);

CREATE OR REPLACE VIEW analytics.site_total_submeters_visible AS
SELECT tenant_id, site_id, point_id, device_id, meter_name, ordinal
FROM analytics.site_total_submeters FINAL
WHERE (tenant_id, site_id, generation) IN (
  SELECT tenant_id, site_id, max(generation)
  FROM analytics.site_total_submeter_syncs FINAL
  GROUP BY tenant_id, site_id
  HAVING argMax(synthesized, generation) = 1
);

GRANT SELECT, ALTER DELETE ON telemetry_history.observations TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON telemetry_history.numeric_gauge_1min_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON telemetry_history.numeric_gauge_15min_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_interval_facts TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_hourly_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.forecast_series TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.optimization_evaluations TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_rebuild_runs TO telemetry_retention_writer;
GRANT SELECT ON system.parts TO telemetry_retention_writer;
GRANT SELECT ON system.mutations TO telemetry_retention_writer;

ALTER TABLE telemetry_history.observations
  MODIFY COLUMN value_type LowCardinality(Nullable(String));
ALTER TABLE analytics.energy_interval_facts
  MODIFY COLUMN source_partition LowCardinality(String);
ALTER TABLE analytics.forecast_series
  MODIFY COLUMN unit LowCardinality(String);

ALTER TABLE analytics.forecast_series
  MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE analytics.optimization_evaluations
  MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE analytics.energy_rebuild_runs
  MODIFY SETTING non_replicated_deduplication_window = 10000;
