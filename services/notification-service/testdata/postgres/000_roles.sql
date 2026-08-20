DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's16_notification_migrator') THEN
    CREATE ROLE s16_notification_migrator NOLOGIN NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's16_notification_runtime') THEN
    CREATE ROLE s16_notification_runtime NOLOGIN NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's16_notification_scheduler') THEN
    CREATE ROLE s16_notification_scheduler NOLOGIN NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's16_notification_service') THEN
    CREATE ROLE s16_notification_service LOGIN PASSWORD 's16-notification-local-only' NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;
GRANT s16_notification_runtime TO s16_notification_service;
GRANT s16_notification_scheduler TO s16_notification_service;
