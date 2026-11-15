\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s0_migrator;

COMMENT ON SCHEMA audit_ledger IS '跨服务不可变防篡改审计账本域，由审计账本服务追加写入。';
COMMENT ON TABLE audit_ledger.inbox IS '审计账本按消息标识去重的传输层幂等收件箱，由审计账本服务写入。';
COMMENT ON TABLE audit_ledger.records IS '按租户哈希链首尾相接的只追加审计主记录，由审计账本服务写入。';
COMMENT ON TABLE audit_ledger.tenant_heads IS '各租户审计哈希链当前最新一条记录散列游标，由审计账本服务更新。';

COMMENT ON SCHEMA gateway IS '平台网关会话与路由治理持久化域，由平台网关服务负责写入。';
COMMENT ON TABLE gateway.audit_intents IS '平台网关在提交会话或路由变更前落盘的审计事件待发意图，由平台网关服务写入。';
COMMENT ON TABLE gateway.outbox IS '平台网关事务性发件箱消息及租约投递重试状态，由平台网关服务维护。';
COMMENT ON TABLE gateway.route_audit_records IS '平台网关接口路由归属权切换的只追加审计流水，由平台网关服务写入。';
COMMENT ON TABLE gateway.sessions IS '已登录用户的网关会话及加密保存的 CSRF 与外部令牌密文，由平台网关服务维护。';

RESET ROLE;

COMMIT;
