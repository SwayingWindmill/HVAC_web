/*
  站点告警规则自动化动作持久化发件箱：在告警触发或恢复落库同一事务内原子写入待投递动作（Webhook、工单与保护性控制命令），
  并通过租户 RLS 隔离、按租户抢占租约重试以及死信观测重放保证动作不丢失、不阻塞评估主链路。
*/
BEGIN;

SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE IF NOT EXISTS alarm_runtime.rule_action_outbox (
  outbox_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  alarm_version bigint NOT NULL CHECK (alarm_version > 0),
  assignment_id uuid NOT NULL,
  action_index integer NOT NULL CHECK (action_index >= 0 AND action_index < 4),
  trigger_kind text NOT NULL CHECK (trigger_kind IN ('RAISE', 'CLEAR')),
  action_kind text NOT NULL CHECK (action_kind IN ('WEBHOOK', 'WORK_ORDER', 'PROTECTION_COMMAND')),
  target_ref text NOT NULL CHECK (length(btrim(target_ref)) BETWEEN 1 AND 256),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'DELIVERED', 'DEAD_LETTER')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at timestamptz NOT NULL,
  lease_owner text,
  lease_until timestamptz,
  delivered_at timestamptz,
  dead_lettered_at timestamptz,
  last_error_code text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, alarm_id, alarm_version, action_index),
  FOREIGN KEY (tenant_id, site_id, alarm_id) REFERENCES alarm_runtime.alarm_current (tenant_id, site_id, alarm_id),
  CHECK ((status = 'DELIVERED') = (delivered_at IS NOT NULL)),
  CHECK ((status = 'DEAD_LETTER') = (dead_lettered_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS alarm_rule_action_outbox_pending_idx
  ON alarm_runtime.rule_action_outbox (tenant_id, available_at, lease_until, created_at, outbox_id)
  WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS alarm_rule_action_outbox_dead_letter_idx
  ON alarm_runtime.rule_action_outbox (tenant_id, site_id, dead_lettered_at DESC, outbox_id)
  WHERE status = 'DEAD_LETTER';

COMMENT ON TABLE alarm_runtime.rule_action_outbox IS '站点告警规则联动动作持久化发件箱表，用于可靠投递 Webhook、自动工单和保护性控制命令，并记录重试与死信状态。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.outbox_id IS '规则动作发件箱条目主键（UUIDv7）。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.tenant_id IS '所属租户标识，用于行级租户隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.site_id IS '所属站点标识。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.alarm_id IS '触发本次动作的告警当前状态主键标识。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.alarm_version IS '触发本次动作时对应的告警版本号，与动作序号共同保证幂等去重。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.assignment_id IS '关联的站点告警规则分配标识。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.action_index IS '同一条规则内动作列表的零基序号（0 到 3）。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.trigger_kind IS '动作触发阶段：RAISE 表示告警触发，CLEAR 表示告警恢复。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.action_kind IS '动作类型：WEBHOOK、WORK_ORDER 或 PROTECTION_COMMAND。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.target_ref IS '脱敏后的目标摘要（不含密钥引用或查询口令参数），用于安全检索与死信展示。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.payload IS '动作投递执行所需的完整结构化 JSON 载荷。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.status IS '发件箱投递状态：PENDING 待投递、DELIVERED 已投递、DEAD_LETTER 超过重试上限或永久失败进入死信。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.attempts IS '累计抢占投递尝试次数。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.available_at IS '下一次允许被中继器抢占投递的时间戳。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.lease_owner IS '当前持有投递租约的工作负载实例标识。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.lease_until IS '当前投递租约过期时间戳，过期后允许其他实例接管。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.delivered_at IS '成功投递完成的 UTC 时间戳。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.dead_lettered_at IS '转入死信队列的 UTC 时间戳。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.last_error_code IS '最近一次投递失败记录的脱敏错误分类码。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.created_at IS '发件箱条目创建时间戳。';
COMMENT ON COLUMN alarm_runtime.rule_action_outbox.updated_at IS '发件箱条目最近一次状态更新时间戳。';

ALTER TABLE alarm_runtime.rule_action_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.rule_action_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY alarm_rule_action_outbox_migrator_all ON alarm_runtime.rule_action_outbox
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

CREATE POLICY alarm_rule_action_outbox_runtime_tenant ON alarm_runtime.rule_action_outbox
  FOR ALL TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.rule_action_outbox FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON alarm_runtime.rule_action_outbox TO s4_alarm_runtime;

CREATE OR REPLACE FUNCTION alarm_runtime.tenants_with_pending_rule_actions(ready_at timestamptz)
RETURNS SETOF uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, alarm_runtime
AS $$
  SELECT DISTINCT tenant_id
  FROM alarm_runtime.rule_action_outbox
  WHERE status = 'PENDING'
    AND available_at <= ready_at
    AND (lease_until IS NULL OR lease_until <= ready_at)
  ORDER BY tenant_id;
$$;

COMMENT ON FUNCTION alarm_runtime.tenants_with_pending_rule_actions(timestamptz) IS '以定义者权限发现当前存在到期可投递规则动作的租户列表，供中继器在租户 RLS 作用域内逐租户抢占投递。';

REVOKE ALL ON FUNCTION alarm_runtime.tenants_with_pending_rule_actions(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION alarm_runtime.tenants_with_pending_rule_actions(timestamptz) TO s4_alarm_runtime;

CREATE OR REPLACE FUNCTION alarm_runtime.rule_action_outbox_state_counts()
RETURNS TABLE (pending_count bigint, dead_letter_count bigint)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, alarm_runtime
AS $$
  SELECT
    COUNT(*) FILTER (WHERE status = 'PENDING')::bigint AS pending_count,
    COUNT(*) FILTER (WHERE status = 'DEAD_LETTER')::bigint AS dead_letter_count
  FROM alarm_runtime.rule_action_outbox;
$$;

COMMENT ON FUNCTION alarm_runtime.rule_action_outbox_state_counts() IS '以定义者权限统计跨租户规则动作发件箱的待投递数量与死信数量，用于暴露全局堆积监控指标而不泄漏租户行数据。';

REVOKE ALL ON FUNCTION alarm_runtime.rule_action_outbox_state_counts() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION alarm_runtime.rule_action_outbox_state_counts() TO s4_alarm_runtime;

RESET ROLE;

COMMIT;
