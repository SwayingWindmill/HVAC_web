\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s16_notification_migrator;

ALTER TABLE notification_runtime.alarm_delivery
  ADD COLUMN IF NOT EXISTS device_id text,
  ADD COLUMN IF NOT EXISTS suppressed_until timestamptz;

CREATE INDEX IF NOT EXISTS alarm_delivery_attempt_cooldown
  ON notification_runtime.alarm_delivery_attempt (tenant_id, site_id, created_at)
  WHERE kind = 'FIRE' AND state IN ('PENDING','SENDING','SENT','DRY_RUN');

COMMENT ON COLUMN notification_runtime.alarm_delivery.device_id IS '触发告警的关联设备标识或设备编码，用于按设备及上级拓扑进行根因抑制。';
COMMENT ON COLUMN notification_runtime.alarm_delivery.suppressed_until IS '告警维护静默窗口截止时间；在此时间之前调度器暂缓向值班人员发送通知。';

GRANT SELECT, INSERT, UPDATE ON notification_runtime.alarm_delivery TO s16_notification_runtime;
GRANT SELECT ON notification_runtime.alarm_delivery TO s16_notification_scheduler;

RESET ROLE;

COMMIT;
