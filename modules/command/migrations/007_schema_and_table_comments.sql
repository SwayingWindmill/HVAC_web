\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s3_command_migrator;

COMMENT ON SCHEMA command_runtime IS '设备控制指令授权校验、执行围栏、状态流转与回执核验域，由控制指令服务负责写入。';
COMMENT ON TABLE command_runtime.capability_profiles IS '设备可控能力档位、参数取值上下限、单步幅度限制、风险等级与审批策略配置，由控制指令服务维护。';
COMMENT ON TABLE command_runtime.command_approval_snapshots IS '高风险控制指令执行前固化的审批人、风险策略版本与授权范围快照，由控制指令服务写入。';
COMMENT ON TABLE command_runtime.command_attempts IS '控制指令每次下发尝试的执行围栏、下发租约、回执超时截止时间与核验证据引用，由控制指令服务维护。';
COMMENT ON TABLE command_runtime.command_audit_intents IS '控制指令核心生命周期变更的脱敏审计意图与投递位点，由控制指令服务写入。';
COMMENT ON TABLE command_runtime.command_authorization_snapshots IS '控制指令受理时刻固化下来的发起主体、能力范围与紧急吊销版本快照，由控制指令服务写入。';
COMMENT ON TABLE command_runtime.command_dispatch_outbox IS '等待站点网关下行通道消费下发的控制指令发件箱队列及租约状态，由控制指令服务维护。';
COMMENT ON TABLE command_runtime.command_grant_uses IS '已使用的一次性控制授权令牌消费防重记录，由控制指令服务写入。';
COMMENT ON TABLE command_runtime.command_idempotency IS '按租户、设备与幂等键登记的控制指令请求去重映射，由控制指令服务维护。';
COMMENT ON TABLE command_runtime.command_intents IS '设备控制指令意图主表，记录规范化参数、读回核验点位、活动执行围栏与当前状态，由控制指令服务维护。';
COMMENT ON TABLE command_runtime.command_risk_snapshots IS '控制指令受理时按风控规则计算得出的风险定级与判定依据快照，由控制指令服务写入。';
COMMENT ON TABLE command_runtime.command_transitions IS '控制指令从受理到完成或失败的只追加状态流转时间线，由控制指令服务写入。';
COMMENT ON TABLE command_runtime.connector_evidence IS '站点网关下行通道发布控制指令到现场设备前后的请求摘要、写入标记与响应结果证据，由控制指令服务写入。';
COMMENT ON TABLE command_runtime.device_control_state IS '单台设备的控制序号游标、当前活动执行围栏与冻结控制组状态，由控制指令服务维护。';

RESET ROLE;

COMMIT;
