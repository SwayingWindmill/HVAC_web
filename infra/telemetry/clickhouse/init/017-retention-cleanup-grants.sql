CREATE USER IF NOT EXISTS telemetry_retention_writer IDENTIFIED WITH no_password;

GRANT SELECT, ALTER DELETE ON telemetry_history.observations TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_interval_facts TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_hourly_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.forecast_series TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.optimization_evaluations TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_rebuild_runs TO telemetry_retention_writer;
GRANT SELECT ON system.parts TO telemetry_retention_writer;
GRANT SELECT ON system.mutations TO telemetry_retention_writer;
