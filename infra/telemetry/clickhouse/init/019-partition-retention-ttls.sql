-- Partition-level safety-net TTLs behind ClickHouseRetentionPruner (180d raw / 365d 1min / 1825d 15min)
-- so legal holds and verified cold-archive exports always execute before ClickHouse drops expired parts.
-- Set ttl_only_drop_parts via MODIFY SETTING before MODIFY TTL so expired monthly partitions drop whole parts without row rewrites.

ALTER TABLE telemetry_history.observations MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE telemetry_history.observations MODIFY TTL toDateTime(sampled_at) + INTERVAL 730 DAY DELETE;

ALTER TABLE telemetry_history.numeric_1min_states MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE telemetry_history.numeric_1min_states MODIFY TTL bucket + INTERVAL 730 DAY DELETE;

ALTER TABLE telemetry_history.numeric_gauge_1min_states MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE telemetry_history.numeric_gauge_1min_states MODIFY TTL bucket + INTERVAL 730 DAY DELETE;

ALTER TABLE telemetry_history.numeric_15min_states MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE telemetry_history.numeric_15min_states MODIFY TTL bucket + INTERVAL 1825 DAY DELETE;

ALTER TABLE telemetry_history.numeric_gauge_15min_states MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE telemetry_history.numeric_gauge_15min_states MODIFY TTL bucket + INTERVAL 1825 DAY DELETE;

ALTER TABLE analytics.forecast_series MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.forecast_series MODIFY TTL toDateTime(forecast_for) + INTERVAL 730 DAY DELETE;

ALTER TABLE analytics.optimization_evaluations MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.optimization_evaluations MODIFY TTL toDateTime(generated_at) + INTERVAL 730 DAY DELETE;

ALTER TABLE analytics.energy_rebuild_runs MODIFY SETTING ttl_only_drop_parts = 1;
ALTER TABLE analytics.energy_rebuild_runs MODIFY TTL toDateTime(recorded_at) + INTERVAL 730 DAY DELETE;
