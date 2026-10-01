CREATE USER IF NOT EXISTS telemetry_history_writer IDENTIFIED WITH no_password;
GRANT INSERT ON telemetry_history.observations TO telemetry_history_writer;
GRANT SELECT(sampled_at, tenant_id, site_id, device_id, point_id, sensor_id, telemetry_key, unit, value_number, acceptance_status, point_type)
  ON telemetry_history.observations TO telemetry_history_writer;
