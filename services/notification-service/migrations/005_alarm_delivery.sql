-- Alarms delivered to people by mail and WeCom (P0-4, docs/specs/alarm-delivery.md). One row per
-- Alarm holds what Notification knows of it (from the Alarm outbox, newest version wins, an
-- acknowledgement or a clear is never undone); one row per message, keyed by what it says to whom
-- over which channel, so the same message is never queued twice.
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s16_notification_migrator;

CREATE TABLE notification_runtime.alarm_delivery (
  tenant_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  site_id uuid NOT NULL,
  alarm_type text NOT NULL CHECK (length(btrim(alarm_type)) BETWEEN 1 AND 256),
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 512),
  summary text NOT NULL CHECK (length(summary) <= 2048),
  severity text NOT NULL CHECK (severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  alarm_version bigint NOT NULL CHECK (alarm_version > 0),
  began_at timestamptz NOT NULL,
  acknowledged_at timestamptz,
  acknowledged_by text CHECK (acknowledged_by IS NULL OR length(acknowledged_by) <= 256),
  cleared_at timestamptz,
  -- When the dispatcher should look at this Alarm again; NULL when there is nothing left to do.
  next_due_at timestamptz,
  lease_until timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, alarm_id)
);
CREATE INDEX alarm_delivery_due ON notification_runtime.alarm_delivery (next_due_at) WHERE next_due_at IS NOT NULL;
CREATE INDEX alarm_delivery_site_open ON notification_runtime.alarm_delivery (tenant_id, site_id, alarm_type) WHERE cleared_at IS NULL;

-- FIRE is the first message, ESCALATE a later step nobody acknowledged in time, ACK tells who was
-- told that someone took it, RECOVERY that it cleared. SUPPRESSED rows record a message that was
-- deliberately not sent (root cause, rate limit) so the decision is visible and not retried.
CREATE TABLE notification_runtime.alarm_delivery_attempt (
  tenant_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  dedup_key text NOT NULL CHECK (length(dedup_key) BETWEEN 1 AND 700),
  site_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('FIRE','ESCALATE','ACK','RECOVERY')),
  step integer NOT NULL CHECK (step >= 0),
  channel text NOT NULL CHECK (channel IN ('smtp','wecom_app','wecom_bot')),
  recipient text NOT NULL CHECK (length(btrim(recipient)) BETWEEN 1 AND 128),
  state text NOT NULL CHECK (state IN ('PENDING','SENDING','SENT','DRY_RUN','SUPPRESSED','FAILED')),
  reason text CHECK (reason IS NULL OR length(reason) <= 128),
  attempts integer NOT NULL CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  lease_until timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, dedup_key),
  FOREIGN KEY (tenant_id, alarm_id) REFERENCES notification_runtime.alarm_delivery (tenant_id, alarm_id)
);
CREATE INDEX alarm_delivery_attempt_due ON notification_runtime.alarm_delivery_attempt (next_attempt_at) WHERE state IN ('PENDING','SENDING');
CREATE INDEX alarm_delivery_attempt_rate ON notification_runtime.alarm_delivery_attempt (tenant_id, site_id, recipient, created_at) WHERE state IN ('PENDING','SENDING','SENT','DRY_RUN');

ALTER TABLE notification_runtime.alarm_delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.alarm_delivery FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.alarm_delivery_attempt ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.alarm_delivery_attempt FORCE ROW LEVEL SECURITY;

-- Recording an Alarm event happens inside its Tenant.
CREATE POLICY alarm_delivery_runtime ON notification_runtime.alarm_delivery FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
-- The dispatcher serves every Tenant, like the WeChat reminder worker.
CREATE POLICY alarm_delivery_scheduler ON notification_runtime.alarm_delivery FOR ALL TO s16_notification_scheduler
  USING (true) WITH CHECK (true);
CREATE POLICY alarm_delivery_attempt_scheduler ON notification_runtime.alarm_delivery_attempt FOR ALL TO s16_notification_scheduler
  USING (true) WITH CHECK (true);

REVOKE ALL ON notification_runtime.alarm_delivery, notification_runtime.alarm_delivery_attempt FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON notification_runtime.alarm_delivery TO s16_notification_runtime;
GRANT SELECT, UPDATE (next_due_at, lease_until, updated_at) ON notification_runtime.alarm_delivery TO s16_notification_scheduler;
GRANT SELECT, INSERT, UPDATE (state, reason, attempts, next_attempt_at, lease_until, updated_at) ON notification_runtime.alarm_delivery_attempt TO s16_notification_scheduler;

RESET ROLE;

COMMIT;
