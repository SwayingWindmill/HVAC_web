\set ON_ERROR_STOP on

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's0_migrator') THEN CREATE ROLE s0_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gateway_runtime') THEN CREATE ROLE gateway_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gateway_relay_runtime') THEN CREATE ROLE gateway_relay_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'audit_consumer_runtime') THEN CREATE ROLE audit_consumer_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'audit_query_runtime') THEN CREATE ROLE audit_query_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_migrator') THEN CREATE ROLE identity_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_runtime') THEN CREATE ROLE identity_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_admin') THEN CREATE ROLE identity_admin LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_directory_reader') THEN CREATE ROLE identity_directory_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_migrator') THEN CREATE ROLE s1_iam_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_runtime') THEN CREATE ROLE s1_iam_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_admin') THEN CREATE ROLE s1_iam_admin LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_iam_reconciler') THEN CREATE ROLE s1_iam_reconciler LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_core_migrator') THEN CREATE ROLE s1_core_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_core_runtime') THEN CREATE ROLE s1_core_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_migration_operator') THEN CREATE ROLE s1_migration_operator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_core_service') THEN CREATE ROLE s1_core_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'outbound_delivery_migrator') THEN CREATE ROLE outbound_delivery_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'outbound_delivery_runtime') THEN CREATE ROLE outbound_delivery_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metric_engine_runtime') THEN CREATE ROLE metric_engine_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'scheduler_runtime') THEN CREATE ROLE scheduler_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'connectivity_migrator') THEN CREATE ROLE connectivity_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'connectivity_runtime') THEN CREATE ROLE connectivity_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'settlement_runtime') THEN CREATE ROLE settlement_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'forecast_runtime') THEN CREATE ROLE forecast_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'optimization_runtime') THEN CREATE ROLE optimization_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_iam_grant_runtime') THEN CREATE ROLE s2_iam_grant_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_migrator') THEN CREATE ROLE s2_telemetry_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_runtime') THEN CREATE ROLE s2_telemetry_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_relay') THEN CREATE ROLE s2_telemetry_relay NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_history') THEN CREATE ROLE s2_telemetry_history NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_migrator_service') THEN CREATE ROLE s2_telemetry_migrator_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_service') THEN CREATE ROLE s2_telemetry_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_gateway') THEN CREATE ROLE s2_telemetry_gateway LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_iam') THEN CREATE ROLE s2_telemetry_iam LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_relay_service') THEN CREATE ROLE s2_telemetry_relay_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's2_telemetry_history_service') THEN CREATE ROLE s2_telemetry_history_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_migrator') THEN CREATE ROLE s3_command_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_runtime') THEN CREATE ROLE s3_command_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_migrator_service') THEN CREATE ROLE s3_command_migrator_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's3_command_service') THEN CREATE ROLE s3_command_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's4_alarm_migrator') THEN CREATE ROLE s4_alarm_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's4_alarm_runtime') THEN CREATE ROLE s4_alarm_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's4_alarm_service') THEN CREATE ROLE s4_alarm_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's5_work_order_migrator') THEN CREATE ROLE s5_work_order_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's5_work_order_runtime') THEN CREATE ROLE s5_work_order_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's5_work_order_writer') THEN CREATE ROLE s5_work_order_writer NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's5_work_order_service') THEN CREATE ROLE s5_work_order_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's5_work_order_mutation_service') THEN CREATE ROLE s5_work_order_mutation_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; END IF;
END
$$;

\connect hvac_identity
REVOKE CONNECT ON DATABASE hvac_identity FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS identity AUTHORIZATION identity_migrator;
ALTER SCHEMA identity OWNER TO identity_migrator;
REVOKE ALL ON SCHEMA identity FROM PUBLIC;
GRANT CONNECT ON DATABASE hvac_identity TO identity_runtime, identity_admin, identity_directory_reader;
GRANT USAGE ON SCHEMA identity TO identity_runtime, identity_admin, identity_directory_reader;

\connect hvac_s0
REVOKE CONNECT ON DATABASE hvac_s0 FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS gateway AUTHORIZATION s0_migrator;
CREATE SCHEMA IF NOT EXISTS audit_ledger AUTHORIZATION s0_migrator;
ALTER SCHEMA gateway OWNER TO s0_migrator;
ALTER SCHEMA audit_ledger OWNER TO s0_migrator;
REVOKE ALL ON SCHEMA gateway, audit_ledger FROM PUBLIC;
GRANT CONNECT ON DATABASE hvac_s0 TO s0_migrator, gateway_runtime, gateway_relay_runtime, audit_consumer_runtime, audit_query_runtime;

\connect hvac_s1
ALTER ROLE settlement_runtime LOGIN;
REVOKE CONNECT ON DATABASE hvac_s1 FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS iam AUTHORIZATION s1_iam_migrator;
CREATE SCHEMA IF NOT EXISTS core_registry AUTHORIZATION s1_core_migrator;
CREATE SCHEMA IF NOT EXISTS outbound_delivery AUTHORIZATION outbound_delivery_migrator;
CREATE SCHEMA IF NOT EXISTS connectivity AUTHORIZATION connectivity_migrator;
ALTER SCHEMA iam OWNER TO s1_iam_migrator;
ALTER SCHEMA core_registry OWNER TO s1_core_migrator;
ALTER SCHEMA outbound_delivery OWNER TO outbound_delivery_migrator;
ALTER SCHEMA connectivity OWNER TO connectivity_migrator;
REVOKE ALL ON SCHEMA iam, core_registry, outbound_delivery, connectivity FROM PUBLIC;
GRANT CONNECT ON DATABASE hvac_s1 TO s1_iam_migrator, s1_iam_runtime, s1_iam_admin, s1_iam_reconciler, s1_core_service, outbound_delivery_runtime, metric_engine_runtime, scheduler_runtime, settlement_runtime, s2_iam_grant_runtime, connectivity_runtime;
GRANT USAGE ON SCHEMA iam TO s1_iam_runtime, s1_iam_admin, s1_iam_reconciler;
GRANT USAGE ON SCHEMA core_registry TO s1_core_runtime, metric_engine_runtime, scheduler_runtime, settlement_runtime, forecast_runtime, optimization_runtime;
GRANT USAGE ON SCHEMA outbound_delivery TO outbound_delivery_runtime;
GRANT USAGE ON SCHEMA connectivity TO connectivity_runtime;
GRANT s1_core_runtime TO s1_core_service;
ALTER DEFAULT PRIVILEGES FOR ROLE outbound_delivery_migrator IN SCHEMA outbound_delivery REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE outbound_delivery_migrator IN SCHEMA outbound_delivery REVOKE ALL ON FUNCTIONS FROM PUBLIC;

\connect hvac_s2
REVOKE CONNECT ON DATABASE hvac_s2 FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS telemetry_runtime AUTHORIZATION s2_telemetry_migrator;
ALTER SCHEMA telemetry_runtime OWNER TO s2_telemetry_migrator;
REVOKE ALL ON SCHEMA telemetry_runtime FROM PUBLIC;
GRANT USAGE ON SCHEMA telemetry_runtime TO s2_telemetry_runtime, s2_telemetry_relay, s2_telemetry_history;
GRANT s2_telemetry_migrator TO s2_telemetry_migrator_service;
GRANT s2_telemetry_runtime TO s2_telemetry_service;
GRANT s2_telemetry_relay TO s2_telemetry_relay_service;
GRANT s2_telemetry_history TO s2_telemetry_history_service;
GRANT CONNECT ON DATABASE hvac_s2 TO s2_telemetry_migrator_service, s2_telemetry_service, s2_telemetry_gateway, s2_telemetry_iam, s2_telemetry_relay_service, s2_telemetry_history_service;
ALTER DEFAULT PRIVILEGES FOR ROLE s2_telemetry_migrator IN SCHEMA telemetry_runtime REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE s2_telemetry_migrator IN SCHEMA telemetry_runtime REVOKE ALL ON FUNCTIONS FROM PUBLIC;

\connect hvac_s3
REVOKE CONNECT ON DATABASE hvac_s3 FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS command_runtime AUTHORIZATION s3_command_migrator;
ALTER SCHEMA command_runtime OWNER TO s3_command_migrator;
REVOKE ALL ON SCHEMA command_runtime FROM PUBLIC;
GRANT s3_command_migrator TO s3_command_migrator_service;
GRANT s3_command_runtime TO s3_command_service;
GRANT CONNECT ON DATABASE hvac_s3 TO s3_command_migrator_service, s3_command_service;
ALTER DEFAULT PRIVILEGES FOR ROLE s3_command_migrator IN SCHEMA command_runtime REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE s3_command_migrator IN SCHEMA command_runtime REVOKE ALL ON FUNCTIONS FROM PUBLIC;

\connect hvac_s4
REVOKE CONNECT ON DATABASE hvac_s4 FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS alarm_runtime AUTHORIZATION s4_alarm_migrator;
ALTER SCHEMA alarm_runtime OWNER TO s4_alarm_migrator;
REVOKE ALL ON SCHEMA alarm_runtime FROM PUBLIC;
GRANT s4_alarm_runtime TO s4_alarm_service;
GRANT CONNECT ON DATABASE hvac_s4 TO s4_alarm_service;

\connect hvac_s5
REVOKE CONNECT ON DATABASE hvac_s5 FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS work_order_runtime AUTHORIZATION s5_work_order_migrator;
ALTER SCHEMA work_order_runtime OWNER TO s5_work_order_migrator;
REVOKE ALL ON SCHEMA work_order_runtime FROM PUBLIC;
GRANT s5_work_order_runtime TO s5_work_order_service;
GRANT s5_work_order_writer TO s5_work_order_mutation_service;
GRANT CONNECT ON DATABASE hvac_s5 TO s5_work_order_service, s5_work_order_mutation_service;
