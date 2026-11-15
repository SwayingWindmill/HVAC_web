\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE operations_agent_operations_migrator;

COMMENT ON SCHEMA agent_operations IS 'AI 助手会话、回答运行轮次、只读工具调用与产出记录域，由 AI 助手服务负责写入。';
COMMENT ON TABLE agent_operations.agent_artifacts IS 'AI 助手单次回答留下的依据、结论卡片、提问回答、建议与局限说明等助手产出，由 AI 助手服务写入。';
COMMENT ON TABLE agent_operations.agent_messages IS 'AI 助手会话内值班人员提问与助手回复的消息正文及角色流水，由 AI 助手服务写入。';
COMMENT ON TABLE agent_operations.agent_runs IS 'AI 助手回答一个问题的单次尝试，记录模型标识、起止时间、Token 用量及运行状态，由 AI 助手服务维护。';
COMMENT ON TABLE agent_operations.agent_sessions IS '按租户与站点隔离的 AI 助手会话主表，记录发起人、会话状态与当前运行指针，由 AI 助手服务维护。';
COMMENT ON TABLE agent_operations.agent_tool_executions IS 'AI 助手调用平台只读助手工具的参数摘要、结果摘要与数据来源，由 AI 助手服务写入。';

RESET ROLE;

COMMIT;
