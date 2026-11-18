CREATE USER IF NOT EXISTS telemetry_retention_writer IDENTIFIED WITH no_password;

GRANT SELECT, ALTER DELETE ON telemetry_history.observations TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON telemetry_history.numeric_1min_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON telemetry_history.numeric_15min_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON telemetry_history.numeric_hourly_states TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_interval_facts TO telemetry_retention_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_hourly_states TO telemetry_retention_writer;
