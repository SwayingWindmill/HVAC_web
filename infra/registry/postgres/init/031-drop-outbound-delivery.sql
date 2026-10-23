-- The unified outbound delivery service is gone (ADR 0026): it never ran anywhere. Device alarms
-- reach people through Notification's own WeChat sender, platform faults through Alertmanager.
-- Its schema goes with every table 001_s15_outbound_delivery.sql made in it. Where the old roles
-- exist (a database that ran 001_s15), they lose what 001_s15 and the bootstrap granted them and
-- can no longer sign in; the roles stay, because a server's role-credentials file still names them.
\set ON_ERROR_STOP on

BEGIN;

DROP SCHEMA IF EXISTS outbound_delivery CASCADE;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'outbound_delivery_migrator') THEN
    REVOKE USAGE ON SCHEMA iam, core_registry FROM outbound_delivery_migrator;
    REVOKE REFERENCES (id) ON iam.tenants FROM outbound_delivery_migrator;
    REVOKE REFERENCES (tenant_id, id) ON core_registry.sites FROM outbound_delivery_migrator;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'outbound_delivery_runtime') THEN
    REVOKE CONNECT ON DATABASE hvac_s1 FROM outbound_delivery_runtime;
    ALTER ROLE outbound_delivery_runtime NOLOGIN;
  END IF;
END
$$;

COMMIT;
