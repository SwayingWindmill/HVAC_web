DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_migrator') THEN
    CREATE ROLE s3_command_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_runtime') THEN
    CREATE ROLE s3_command_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_dispatcher') THEN
    CREATE ROLE s3_command_dispatcher NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_migrator_service') THEN
    CREATE ROLE s3_command_migrator_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_service') THEN
    CREATE ROLE s3_command_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_dispatcher_service') THEN
    CREATE ROLE s3_command_dispatcher_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

ALTER ROLE s3_command_migrator_service PASSWORD 's3-command-migrator-local-only';
ALTER ROLE s3_command_service PASSWORD 's3-command-service-local-only';
ALTER ROLE s3_command_dispatcher_service PASSWORD 's3-command-dispatcher-local-only';

GRANT s3_command_migrator TO s3_command_migrator_service;
GRANT s3_command_runtime TO s3_command_service;
GRANT s3_command_dispatcher TO s3_command_dispatcher_service;

CREATE SCHEMA IF NOT EXISTS command_runtime AUTHORIZATION s3_command_migrator;
ALTER SCHEMA command_runtime OWNER TO s3_command_migrator;
REVOKE ALL ON SCHEMA command_runtime FROM PUBLIC;

ALTER DEFAULT PRIVILEGES FOR ROLE s3_command_migrator IN SCHEMA command_runtime REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE s3_command_migrator IN SCHEMA command_runtime REVOKE ALL ON FUNCTIONS FROM PUBLIC;
