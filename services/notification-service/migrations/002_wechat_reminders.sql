-- WeChat reminders for the mini program (#664, docs/specs/miniprogram-wechat.md): how many more
-- reminders of each kind WeChat lets the platform send a person, one for each time they allowed
-- one in the mini program. Per person (issuer + subject) inside the Tenant they were signed in to.
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

REVOKE ALL ON notification_runtime.wechat_reminder_allowance FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON notification_runtime.wechat_reminder_allowance TO s16_notification_runtime;

RESET ROLE;

COMMIT;
