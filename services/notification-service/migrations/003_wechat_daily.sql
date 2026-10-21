-- The daily WeChat summary (#664, docs/specs/miniprogram-wechat.md): DAILY reminders, the Site
-- each person gets the summary of (the one they were on when they last allowed one), and one
-- table of deliveries for both kinds of reminder, keyed by the kind and its reference: the Alarm
-- for ALARM, the Site's date for DAILY. The Alarm deliveries so far were test sends and are not
-- carried over.
BEGIN;

SET LOCAL ROLE s16_notification_migrator;

ALTER TABLE notification_runtime.wechat_reminder_allowance DROP CONSTRAINT wechat_reminder_allowance_kind_check;
ALTER TABLE notification_runtime.wechat_reminder_allowance ADD CONSTRAINT wechat_reminder_allowance_kind_check CHECK (kind IN ('ALARM','DAILY'));

CREATE TABLE notification_runtime.wechat_daily_subscription (
  tenant_id uuid NOT NULL,
  subject_issuer text NOT NULL CHECK (length(btrim(subject_issuer)) BETWEEN 1 AND 512),
  subject text NOT NULL CHECK (length(btrim(subject)) BETWEEN 1 AND 256),
  site_id uuid NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, subject_issuer, subject)
);

DROP TABLE notification_runtime.wechat_alarm_delivery;

-- One person's reminder: taken from their allowance before sending (SENDING), settled as SENT,
-- REFUSED (WeChat: none left), NOT_BOUND, NO_REMINDER_LEFT, EXPIRED or FAILED, and never sent after
-- expires_at.
CREATE TABLE notification_runtime.wechat_reminder_delivery (
  tenant_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('ALARM','DAILY')),
  reference text NOT NULL CHECK (length(reference) BETWEEN 1 AND 64),
  subject_issuer text NOT NULL,
  subject text NOT NULL,
  site_id uuid NOT NULL,
  state text NOT NULL CHECK (state IN ('PENDING','SENDING','SENT','REFUSED','NOT_BOUND','NO_REMINDER_LEFT','EXPIRED','FAILED')),
  attempts integer NOT NULL CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  lease_until timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, kind, reference, subject_issuer, subject)
);
CREATE INDEX wechat_reminder_delivery_due ON notification_runtime.wechat_reminder_delivery (next_attempt_at) WHERE state IN ('PENDING','SENDING');

ALTER TABLE notification_runtime.wechat_daily_subscription ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.wechat_daily_subscription FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.wechat_reminder_delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.wechat_reminder_delivery FORCE ROW LEVEL SECURITY;
CREATE POLICY wechat_daily_subscription_runtime ON notification_runtime.wechat_daily_subscription FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY wechat_daily_subscription_scheduler ON notification_runtime.wechat_daily_subscription FOR ALL TO s16_notification_scheduler
  USING (true) WITH CHECK (true);
CREATE POLICY wechat_reminder_delivery_scheduler ON notification_runtime.wechat_reminder_delivery FOR ALL TO s16_notification_scheduler
  USING (true) WITH CHECK (true);

REVOKE ALL ON notification_runtime.wechat_daily_subscription, notification_runtime.wechat_reminder_delivery FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON notification_runtime.wechat_daily_subscription TO s16_notification_runtime;
GRANT SELECT ON notification_runtime.wechat_daily_subscription TO s16_notification_scheduler;
GRANT SELECT, INSERT, UPDATE (state, attempts, next_attempt_at, lease_until, updated_at) ON notification_runtime.wechat_reminder_delivery TO s16_notification_scheduler;

RESET ROLE;

COMMIT;
