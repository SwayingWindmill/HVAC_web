BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's0_migrator') THEN
    CREATE ROLE s0_migrator LOGIN PASSWORD 's0-migrator-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gateway_runtime') THEN
    CREATE ROLE gateway_runtime LOGIN PASSWORD 'gateway-runtime-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gateway_relay_runtime') THEN
    CREATE ROLE gateway_relay_runtime LOGIN PASSWORD 'gateway-relay-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'audit_consumer_runtime') THEN
    CREATE ROLE audit_consumer_runtime LOGIN PASSWORD 'audit-consumer-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'audit_query_runtime') THEN
    CREATE ROLE audit_query_runtime LOGIN PASSWORD 'audit-query-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

CREATE SCHEMA IF NOT EXISTS gateway AUTHORIZATION s0_migrator;
CREATE SCHEMA IF NOT EXISTS audit_ledger AUTHORIZATION s0_migrator;
ALTER SCHEMA gateway OWNER TO s0_migrator;
ALTER SCHEMA audit_ledger OWNER TO s0_migrator;
REVOKE ALL ON SCHEMA gateway FROM PUBLIC;
REVOKE ALL ON SCHEMA audit_ledger FROM PUBLIC;

GRANT CONNECT ON DATABASE hvac_s0 TO s0_migrator, gateway_runtime, gateway_relay_runtime, audit_consumer_runtime, audit_query_runtime;

COMMIT;
