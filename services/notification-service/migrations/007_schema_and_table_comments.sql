\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s16_notification_migrator;

COMMENT ON SCHEMA notification_runtime IS '告警分派、值班邮件与微信小程序提醒运行时域，由通知服务负责写入。';
COMMENT ON TABLE notification_runtime.alarm_delivery IS '通知服务视角下的告警投递状态跟踪主表，记录告警最新已知版本与确认清除时间。';
COMMENT ON TABLE notification_runtime.alarm_delivery_attempt IS '按通知渠道与收件人去重的告警单步发送尝试、重试计划与租约状态，由通知服务维护。';
COMMENT ON TABLE notification_runtime.open_reading_gap_alarm IS '当前仍处于未关闭状态的读数中断告警跟踪记录，用于中断升级提醒，由通知服务维护。';
COMMENT ON TABLE notification_runtime.wechat_alarm_reminder IS '等待发送的微信小程序告警提醒任务队列与过期时间，由通知服务维护。';
COMMENT ON TABLE notification_runtime.wechat_daily_subscription IS '值班人员在站点的微信小程序每日汇总提醒订阅记录，由通知服务维护。';
COMMENT ON TABLE notification_runtime.wechat_reminder_allowance IS '用户在不同微信订阅模板下当前剩余可用的一次性推送配额，由通知服务维护。';
COMMENT ON TABLE notification_runtime.wechat_reminder_delivery IS '微信小程序一次性订阅消息实际发送状态、重试次数与租约游标，由通知服务维护。';

RESET ROLE;

COMMIT;
