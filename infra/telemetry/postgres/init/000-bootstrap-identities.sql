BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_migrator') THEN
    CREATE ROLE s2_telemetry_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_runtime') THEN
    CREATE ROLE s2_telemetry_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_relay') THEN
    CREATE ROLE s2_telemetry_relay NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_history') THEN
    CREATE ROLE s2_telemetry_history NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_migrator_service') THEN
    CREATE ROLE s2_telemetry_migrator_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_service') THEN
    CREATE ROLE s2_telemetry_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_gateway') THEN
    CREATE ROLE s2_telemetry_gateway LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_iam') THEN
    CREATE ROLE s2_telemetry_iam LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_relay_service') THEN
    CREATE ROLE s2_telemetry_relay_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_history_service') THEN
    CREATE ROLE s2_telemetry_history_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

ALTER ROLE s2_telemetry_migrator_service PASSWORD 's2-telemetry-migrator-local-only';
ALTER ROLE s2_telemetry_service PASSWORD 's2-telemetry-runtime-local-only';
ALTER ROLE s2_telemetry_gateway PASSWORD 's2-telemetry-gateway-local-only';
ALTER ROLE s2_telemetry_iam PASSWORD 's2-telemetry-iam-local-only';
ALTER ROLE s2_telemetry_relay_service PASSWORD 's2-telemetry-relay-local-only';
ALTER ROLE s2_telemetry_history_service PASSWORD 's2-telemetry-history-local-only';

GRANT s2_telemetry_migrator TO s2_telemetry_migrator_service;
GRANT s2_telemetry_runtime TO s2_telemetry_service;
GRANT s2_telemetry_relay TO s2_telemetry_relay_service;
GRANT s2_telemetry_history TO s2_telemetry_history_service;

CREATE SCHEMA IF NOT EXISTS telemetry_runtime AUTHORIZATION s2_telemetry_migrator;
ALTER SCHEMA telemetry_runtime OWNER TO s2_telemetry_migrator;
REVOKE ALL ON SCHEMA telemetry_runtime FROM PUBLIC;
GRANT USAGE ON SCHEMA telemetry_runtime TO s2_telemetry_runtime, s2_telemetry_relay, s2_telemetry_history;

GRANT CONNECT ON DATABASE hvac_s2 TO
  s2_telemetry_migrator_service,
  s2_telemetry_service,
  s2_telemetry_gateway,
  s2_telemetry_iam,
  s2_telemetry_relay_service,
  s2_telemetry_history_service;

ALTER DEFAULT PRIVILEGES FOR ROLE s2_telemetry_migrator IN SCHEMA telemetry_runtime REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE s2_telemetry_migrator IN SCHEMA telemetry_runtime REVOKE ALL ON FUNCTIONS FROM PUBLIC;

COMMIT;
