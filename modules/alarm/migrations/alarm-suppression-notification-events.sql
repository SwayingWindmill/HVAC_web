/*
  P1 告警生命周期增强：扩展告警通知发件箱（notification_outbox）的 action 约束，
  新增 SUPPRESSED 与 UNSUPPRESSED 状态事件，使值班提醒与通知下游感知静默及解除静默变更。
*/
SET LOCAL ROLE s4_alarm_migrator;

ALTER TABLE alarm_runtime.notification_outbox
  DROP CONSTRAINT IF EXISTS notification_outbox_action_check;

ALTER TABLE alarm_runtime.notification_outbox
  ADD CONSTRAINT notification_outbox_action_check
  CHECK (action IN (
    'CREATED',
    'SEVERITY_CHANGED',
    'ACKNOWLEDGED',
    'CLEARED',
    'SUPPRESSED',
    'UNSUPPRESSED'
  ));

RESET ROLE;
