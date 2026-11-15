\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s4_alarm_migrator;

DROP TABLE IF EXISTS alarm_runtime.s13_alarm_identity_map;
DROP TABLE IF EXISTS alarm_runtime.s13_alarm_migration_report;
DROP TABLE IF EXISTS alarm_runtime.alarm_current_pre_s13_backup;
DROP TABLE IF EXISTS alarm_runtime.alarm_idempotency_pre_s13_backup;

COMMENT ON SCHEMA alarm_runtime IS '告警规则评估、当前告警生命周期状态机与通知触发域，由告警服务负责写入。';
COMMENT ON TABLE alarm_runtime.alarm_current IS '当前告警状态主表，按租户与告警主键维护活动或已清除状态、发生次数、首末触发时间与流转状态，由告警服务维护。';
COMMENT ON TABLE alarm_runtime.alarm_evaluation_event IS '告警规则评估器每次输入触发状态变迁时的评估快照与结果事件流水，由告警服务写入。';
COMMENT ON TABLE alarm_runtime.alarm_evaluation_state IS '告警规则评估状态表，按规则分配记录连续触发计数、去抖时间窗与评估调度租约，由告警服务维护。';
COMMENT ON TABLE alarm_runtime.alarm_idempotency IS '告警确认、指派、取消指派、临时静默与取消静默写操作的接口幂等防重表，由告警服务维护。';
COMMENT ON TABLE alarm_runtime.alarm_policy_assignment IS '告警策略修订版本向站点或具体设备的下发分配记录，由告警服务维护。';
COMMENT ON TABLE alarm_runtime.alarm_policy_revision IS '已发布的不可变告警策略定义版本与规则正文摘要，由告警服务维护。';
COMMENT ON TABLE alarm_runtime.alarm_timeline IS '单条告警从触发、确认、临时静默、指派到清除的全流程状态变更时间线，由告警服务写入。';
COMMENT ON TABLE alarm_runtime.events IS '现场设备或上游系统产生的原始异常事件与告警溯源记录，由告警服务写入。';
COMMENT ON TABLE alarm_runtime.notification_outbox IS '待推送给通知服务执行值班提醒的告警事件发件箱及分发租约状态，由告警服务维护。';
COMMENT ON TABLE alarm_runtime.telemetry_evaluation_input IS '从遥测侧同步的最新设备评估输入快照与业务版本号，供告警规则评估器读取。';

RESET ROLE;

COMMIT;
