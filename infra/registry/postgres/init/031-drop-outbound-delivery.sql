-- The unified outbound delivery service is gone (ADR 0026): it never ran anywhere. Device alarms
-- reach people through Notification's own WeChat sender, platform faults through Alertmanager.
-- Its schema goes with every table 001_s15_outbound_delivery.sql made in it.
\set ON_ERROR_STOP on

BEGIN;

DROP SCHEMA IF EXISTS outbound_delivery CASCADE;

COMMIT;
