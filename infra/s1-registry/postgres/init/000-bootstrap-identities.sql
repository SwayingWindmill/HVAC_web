BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_migrator') THEN
    CREATE ROLE s1_iam_migrator LOGIN PASSWORD 's1-iam-migrator-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_runtime') THEN
    CREATE ROLE s1_iam_runtime LOGIN PASSWORD 's1-iam-runtime-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_reconciler') THEN
    CREATE ROLE s1_iam_reconciler LOGIN PASSWORD 's1-iam-reconciler-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_core_migrator') THEN
    CREATE ROLE s1_core_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_core_runtime') THEN
    CREATE ROLE s1_core_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_migration_operator') THEN
    CREATE ROLE s1_migration_operator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metric_engine_runtime') THEN
    CREATE ROLE metric_engine_runtime LOGIN PASSWORD 'metric-engine-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'scheduler_runtime') THEN
    CREATE ROLE scheduler_runtime LOGIN PASSWORD 'scheduler-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maintenance_runtime') THEN
    CREATE ROLE maintenance_runtime LOGIN PASSWORD 'maintenance-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'settlement_runtime') THEN
    CREATE ROLE settlement_runtime LOGIN PASSWORD 'settlement-runtime-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'forecast_runtime') THEN
    CREATE ROLE forecast_runtime LOGIN PASSWORD 'forecast-runtime-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'optimization_runtime') THEN
    CREATE ROLE optimization_runtime LOGIN PASSWORD 'optimization-runtime-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fdd_runtime') THEN
    CREATE ROLE fdd_runtime LOGIN PASSWORD 'fdd-runtime-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

CREATE SCHEMA IF NOT EXISTS iam AUTHORIZATION s1_iam_migrator;
CREATE SCHEMA IF NOT EXISTS core_registry AUTHORIZATION s1_core_migrator;
ALTER SCHEMA iam OWNER TO s1_iam_migrator;
ALTER SCHEMA core_registry OWNER TO s1_core_migrator;
REVOKE ALL ON SCHEMA iam FROM PUBLIC;
REVOKE ALL ON SCHEMA core_registry FROM PUBLIC;

GRANT CONNECT ON DATABASE hvac_s1 TO s1_iam_migrator, s1_iam_runtime, s1_iam_reconciler,
  s1_core_migrator, s1_core_runtime, s1_migration_operator, metric_engine_runtime, scheduler_runtime, maintenance_runtime, settlement_runtime,
  forecast_runtime, optimization_runtime, fdd_runtime;
GRANT USAGE ON SCHEMA iam TO s1_iam_runtime, s1_iam_reconciler, maintenance_runtime;
GRANT USAGE ON SCHEMA core_registry TO s1_core_runtime, s1_migration_operator, metric_engine_runtime, scheduler_runtime, maintenance_runtime, settlement_runtime,
  forecast_runtime, optimization_runtime, fdd_runtime;

COMMIT;
