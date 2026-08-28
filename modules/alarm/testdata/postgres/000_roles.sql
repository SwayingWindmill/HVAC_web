CREATE ROLE s4_alarm_migrator NOLOGIN NOINHERIT NOBYPASSRLS;
CREATE ROLE s4_alarm_runtime NOLOGIN NOINHERIT NOBYPASSRLS;
CREATE ROLE s4_alarm_notification_relay NOLOGIN NOINHERIT NOBYPASSRLS;
CREATE ROLE s4_alarm_service LOGIN PASSWORD 's4-alarm-service-local-only' NOINHERIT NOBYPASSRLS;
GRANT s4_alarm_runtime TO s4_alarm_service;
GRANT s4_alarm_notification_relay TO s4_alarm_service;
CREATE SCHEMA alarm_runtime AUTHORIZATION s4_alarm_migrator;
REVOKE ALL ON SCHEMA alarm_runtime FROM PUBLIC;
