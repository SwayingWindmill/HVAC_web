-- WeChat reminders for the mini program (#664, docs/specs/miniprogram-wechat.md): how many more
-- reminders of each kind WeChat lets the platform send a person, one for each time they allowed
-- one in the mini program, per person (issuer + subject) inside the Tenant they were signed in to;
-- and the Alarm reminders on their way.
BEGIN;

SET LOCAL ROLE s16_notification_migrator;

CREATE TABLE notification_runtime.wechat_reminder_allowance (
  tenant_id uuid NOT NULL,
  subject_issuer text NOT NULL CHECK (length(btrim(subject_issuer)) BETWEEN 1 AND 512),
  subject text NOT NULL CHECK (length(btrim(subject)) BETWEEN 1 AND 256),
  kind text NOT NULL CHECK (kind IN ('ALARM')),
  remaining integer NOT NULL CHECK (remaining >= 0),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, subject_issuer, subject, kind)
);

ALTER TABLE notification_runtime.wechat_reminder_allowance ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.wechat_reminder_allowance FORCE ROW LEVEL SECURITY;
CREATE POLICY wechat_reminder_allowance_runtime ON notification_runtime.wechat_reminder_allowance FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- A new urgent or important Alarm to remind people of: who gets it is worked out once (PENDING to
-- EXPANDED), and nobody is reminded after expires_at (30 minutes after the Alarm began).
CREATE TABLE notification_runtime.wechat_alarm_reminder (
  tenant_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  site_id uuid NOT NULL,
  source_event_id uuid NOT NULL,
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 512),
  severity text NOT NULL CHECK (severity IN ('CRITICAL','MAJOR')),
  occurred_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL CHECK (expires_at > occurred_at),
  state text NOT NULL CHECK (state IN ('PENDING','EXPANDED','EXPIRED')),
  next_attempt_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, alarm_id)
);
CREATE INDEX wechat_alarm_reminder_due ON notification_runtime.wechat_alarm_reminder (next_attempt_at) WHERE state = 'PENDING';

-- One person's reminder of one Alarm. A reminder is taken from their allowance before sending
-- (SENDING) and settles as SENT, REFUSED (WeChat: none left), NOT_BOUND, NO_REMINDER_LEFT,
-- EXPIRED or FAILED.
CREATE TABLE notification_runtime.wechat_alarm_delivery (
  tenant_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  subject_issuer text NOT NULL,
  subject text NOT NULL,
  state text NOT NULL CHECK (state IN ('PENDING','SENDING','SENT','REFUSED','NOT_BOUND','NO_REMINDER_LEFT','EXPIRED','FAILED')),
  attempts integer NOT NULL CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  lease_until timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, alarm_id, subject_issuer, subject),
  FOREIGN KEY (tenant_id, alarm_id) REFERENCES notification_runtime.wechat_alarm_reminder (tenant_id, alarm_id)
);
CREATE INDEX wechat_alarm_delivery_due ON notification_runtime.wechat_alarm_delivery (next_attempt_at) WHERE state IN ('PENDING','SENDING');

ALTER TABLE notification_runtime.wechat_alarm_reminder ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.wechat_alarm_reminder FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.wechat_alarm_delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.wechat_alarm_delivery FORCE ROW LEVEL SECURITY;
CREATE POLICY wechat_alarm_reminder_runtime ON notification_runtime.wechat_alarm_reminder FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
-- The reminder worker serves every Tenant, like the notification scheduler.
CREATE POLICY wechat_reminder_allowance_scheduler ON notification_runtime.wechat_reminder_allowance FOR ALL TO s16_notification_scheduler
  USING (true) WITH CHECK (true);
CREATE POLICY wechat_alarm_reminder_scheduler ON notification_runtime.wechat_alarm_reminder FOR ALL TO s16_notification_scheduler
  USING (true) WITH CHECK (true);
CREATE POLICY wechat_alarm_delivery_scheduler ON notification_runtime.wechat_alarm_delivery FOR ALL TO s16_notification_scheduler
  USING (true) WITH CHECK (true);

REVOKE ALL ON notification_runtime.wechat_reminder_allowance, notification_runtime.wechat_alarm_reminder, notification_runtime.wechat_alarm_delivery FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON notification_runtime.wechat_reminder_allowance TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.wechat_alarm_reminder TO s16_notification_runtime;
GRANT SELECT, UPDATE (remaining, updated_at) ON notification_runtime.wechat_reminder_allowance TO s16_notification_scheduler;
GRANT SELECT, UPDATE (state, next_attempt_at, updated_at) ON notification_runtime.wechat_alarm_reminder TO s16_notification_scheduler;
GRANT SELECT, INSERT, UPDATE (state, attempts, next_attempt_at, lease_until, updated_at) ON notification_runtime.wechat_alarm_delivery TO s16_notification_scheduler;

RESET ROLE;

COMMIT;
