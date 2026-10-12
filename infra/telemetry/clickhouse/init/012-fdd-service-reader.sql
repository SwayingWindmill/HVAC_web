-- FDD reads a loop's supply and return readings from the Telemetry history owner table with its
-- own account (ADR 0020); it can read observations and nothing else, and cannot write.
CREATE USER IF NOT EXISTS fdd_service_reader IDENTIFIED WITH no_password;

GRANT SELECT ON telemetry_history.observations TO fdd_service_reader;
