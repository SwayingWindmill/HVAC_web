-- Open reading-gap Alarms for the 08:00 Site-local daily WeChat summary and 24-hour unacknowledged
-- escalation re-push (#800, docs/specs/data-gap-alerting.md). Notification keeps its own bounded
-- copy fed from outbox events rather than reading another owner's database.
BEGIN;

SET LOCAL ROLE s16_notification_migrator;

CREATE TABLE notification_runtime.open_reading_gap_alarm (
  tenant_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  site_id uuid NOT NULL,
  alarm_type text NOT NULL CHECK (length(btrim(alarm_type)) BETWEEN 1 AND 128),
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 512),
  current_severity text NOT NULL CHECK (current_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  condition text NOT NULL CHECK (condition IN ('ACTIVE','CLEARED')),
  first_occurred_at timestamptz NOT NULL,
  last_event_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, alarm_id)
);

CREATE INDEX open_reading_gap_alarm_site_idx
  ON notification_runtime.open_reading_gap_alarm (tenant_id, site_id, condition, first_occurred_at);

ALTER TABLE notification_runtime.open_reading_gap_alarm ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.open_reading_gap_alarm FORCE ROW LEVEL SECURITY;

CREATE POLICY open_reading_gap_alarm_runtime ON notification_runtime.open_reading_gap_alarm
  FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY open_reading_gap_alarm_scheduler ON notification_runtime.open_reading_gap_alarm
  FOR SELECT TO s16_notification_scheduler
  USING (true);

REVOKE ALL ON notification_runtime.open_reading_gap_alarm FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON notification_runtime.open_reading_gap_alarm TO s16_notification_runtime;
GRANT SELECT ON notification_runtime.open_reading_gap_alarm TO s16_notification_scheduler;

GRANT UPDATE (source_event_id, title, severity, occurred_at, expires_at, state, next_attempt_at, updated_at)
  ON notification_runtime.wechat_alarm_reminder TO s16_notification_runtime;

RESET ROLE;

COMMIT;
