/*
  边缘规则告警事件回流云端投影与去重追踪账本：
  1. 扩展 alarm_runtime.events（metadata 字段、租户写入/更新 RLS 与授权）；
  2. 新增不可变收据账本 alarm_runtime.edge_alarm_event_receipt 保证回流事件幂等去重与可审计；
  3. 新增规则投影游标表 alarm_runtime.edge_rule_projection_cursor 拦截离网补传乱序覆盖与跨通道重复状态反转。
*/
BEGIN;

SET LOCAL ROLE s4_alarm_migrator;

ALTER TABLE alarm_runtime.events
  ADD COLUMN IF NOT EXISTS metadata jsonb NOT NULL DEFAULT '{}'::jsonb
  CHECK (jsonb_typeof(metadata) = 'object');

COMMENT ON COLUMN alarm_runtime.events.metadata IS '告警触发/恢复事件扩展元数据（JSON 对象），例如边缘评估来源、规则版本、离网标志及边缘已执行联动动作标识。';

DROP POLICY IF EXISTS events_runtime_insert ON alarm_runtime.events;
CREATE POLICY events_runtime_insert ON alarm_runtime.events
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS events_runtime_update ON alarm_runtime.events;
CREATE POLICY events_runtime_update ON alarm_runtime.events
  FOR UPDATE TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT INSERT, UPDATE (end_time, status, metadata) ON alarm_runtime.events TO s4_alarm_runtime;

CREATE TABLE IF NOT EXISTS alarm_runtime.edge_alarm_event_receipt (
  receipt_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  gateway_id text NOT NULL CHECK (length(btrim(gateway_id)) BETWEEN 1 AND 128),
  edge_event_id text NOT NULL CHECK (length(btrim(edge_event_id)) BETWEEN 1 AND 256),
  mqtt_message_id uuid,
  rule_id uuid NOT NULL,
  event_type text NOT NULL CHECK (event_type IN ('EDGE_RULE_ALARM_RAISED', 'EDGE_RULE_ALARM_CLEARED')),
  occurred_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL,
  offline boolean NOT NULL DEFAULT false,
  bundle_revision bigint NOT NULL DEFAULT 0 CHECK (bundle_revision >= 0),
  edge_bundle_digest text NOT NULL DEFAULT '' CHECK (edge_bundle_digest = '' OR edge_bundle_digest ~ '^[a-f0-9]{64}$'),
  transition_seq bigint CHECK (transition_seq IS NULL OR transition_seq >= 0),
  outcome text NOT NULL CHECK (outcome IN ('RAISED', 'CLEARED', 'UPDATED', 'DUPLICATE', 'IGNORED_OUT_OF_ORDER', 'IGNORED_UNMAPPED_RULE')),
  alarm_id uuid,
  event_id uuid,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS edge_alarm_event_receipt_dedup_uidx
  ON alarm_runtime.edge_alarm_event_receipt (tenant_id, site_id, gateway_id, edge_event_id, occurred_at)
  WHERE outcome <> 'DUPLICATE';

CREATE INDEX IF NOT EXISTS edge_alarm_event_receipt_rule_idx
  ON alarm_runtime.edge_alarm_event_receipt (tenant_id, site_id, rule_id, occurred_at DESC, created_at DESC);

DROP TRIGGER IF EXISTS edge_alarm_event_receipt_immutable ON alarm_runtime.edge_alarm_event_receipt;
CREATE TRIGGER edge_alarm_event_receipt_immutable
  BEFORE UPDATE OR DELETE ON alarm_runtime.edge_alarm_event_receipt
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();

COMMENT ON TABLE alarm_runtime.edge_alarm_event_receipt IS '网关边缘规则告警事件回流云端处理的不可变收据账本，用于幂等去重、乱序过滤与边缘来源审计。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.receipt_id IS '边缘告警收据记录主键（UUIDv7）。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.tenant_id IS '所属租户标识，用于行级租户隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.site_id IS '所属站点标识。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.gateway_id IS '上报该边缘规则事件的网关设备标识。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.edge_event_id IS '边缘规则引擎生成的事件标识（如 edge-rule:<ruleId>:<seq>），回退时为 mqtt:<messageId>。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.mqtt_message_id IS 'MQTT 入站证据表关联的传输消息标识（UUIDv7）。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.rule_id IS '触发该边缘事件的站点规则分配标识（assignmentId / ruleId）。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.event_type IS '规范化后的边缘事件类型：EDGE_RULE_ALARM_RAISED 或 EDGE_RULE_ALARM_CLEARED。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.occurred_at IS '边缘规则判定状态发生跃迁的原始事件时间戳（UTC）。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.received_at IS '云端接入层首次接收该 MQTT 事件的 UTC 时间戳。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.offline IS '该事件是否在网关离网缓存期间触发。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.bundle_revision IS '边缘判定该事件时运行的站点规则包版本号。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.edge_bundle_digest IS '边缘判定时运行的规则包摘要（64 位十六进制 SHA-256，缺省为空字符串）。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.transition_seq IS '解析自 edgeEventId 尾部的边缘状态机单调序号，用于同一时刻事件的排序与去重。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.outcome IS '云端投影结果：RAISED、CLEARED、UPDATED、DUPLICATE、IGNORED_OUT_OF_ORDER 或 IGNORED_UNMAPPED_RULE。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.alarm_id IS '关联投影或更新的云端告警当前状态主键标识。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.event_id IS '关联写入或关闭的 alarm_runtime.events 事件记录标识。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.metadata IS '边缘回流事件附带的原始原因、触发动作列表与投影审计元数据 JSON。';
COMMENT ON COLUMN alarm_runtime.edge_alarm_event_receipt.created_at IS '云端记录本次收据的 UTC 时间戳。';

ALTER TABLE alarm_runtime.edge_alarm_event_receipt ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.edge_alarm_event_receipt FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS edge_alarm_event_receipt_migrator_all ON alarm_runtime.edge_alarm_event_receipt;
CREATE POLICY edge_alarm_event_receipt_migrator_all ON alarm_runtime.edge_alarm_event_receipt
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS edge_alarm_event_receipt_runtime_select ON alarm_runtime.edge_alarm_event_receipt;
CREATE POLICY edge_alarm_event_receipt_runtime_select ON alarm_runtime.edge_alarm_event_receipt
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS edge_alarm_event_receipt_runtime_insert ON alarm_runtime.edge_alarm_event_receipt;
CREATE POLICY edge_alarm_event_receipt_runtime_insert ON alarm_runtime.edge_alarm_event_receipt
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.edge_alarm_event_receipt FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT ON alarm_runtime.edge_alarm_event_receipt TO s4_alarm_runtime;

CREATE TABLE IF NOT EXISTS alarm_runtime.edge_rule_projection_cursor (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  rule_id uuid NOT NULL,
  gateway_id text NOT NULL CHECK (length(btrim(gateway_id)) BETWEEN 1 AND 128),
  last_occurred_at timestamptz NOT NULL,
  last_transition_seq bigint CHECK (last_transition_seq IS NULL OR last_transition_seq >= 0),
  last_event_type text NOT NULL CHECK (last_event_type IN ('EDGE_RULE_ALARM_RAISED', 'EDGE_RULE_ALARM_CLEARED')),
  last_edge_event_id text NOT NULL CHECK (length(btrim(last_edge_event_id)) BETWEEN 1 AND 256),
  last_bundle_revision bigint NOT NULL DEFAULT 0 CHECK (last_bundle_revision >= 0),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, rule_id)
);

COMMENT ON TABLE alarm_runtime.edge_rule_projection_cursor IS '按（租户、站点、规则）维度记录已应用的最新边缘规则事件水位线游标，防止断网重连补传时的乱序事件回退云端状态。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.tenant_id IS '所属租户标识，用于行级租户隔离（RLS）。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.site_id IS '所属站点标识，与租户标识和规则标识共同构成主键。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.rule_id IS '站点告警规则分配标识（assignmentId / ruleId）。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.gateway_id IS '最近一次推进该规则边缘游标的网关标识。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.last_occurred_at IS '已应用到云端状态机的最新边缘事件发生时间（UTC）。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.last_transition_seq IS '已应用的最近一次边缘状态机单调序号（可选）。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.last_event_type IS '最近一次应用的边缘事件类型：EDGE_RULE_ALARM_RAISED 或 EDGE_RULE_ALARM_CLEARED。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.last_edge_event_id IS '最近一次应用的边缘事件标识。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.last_bundle_revision IS '最近一次应用的边缘事件所属规则包版本号。';
COMMENT ON COLUMN alarm_runtime.edge_rule_projection_cursor.updated_at IS '游标最后更新的 UTC 时间戳。';

ALTER TABLE alarm_runtime.edge_rule_projection_cursor ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.edge_rule_projection_cursor FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS edge_rule_projection_cursor_migrator_all ON alarm_runtime.edge_rule_projection_cursor;
CREATE POLICY edge_rule_projection_cursor_migrator_all ON alarm_runtime.edge_rule_projection_cursor
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS edge_rule_projection_cursor_runtime_tenant ON alarm_runtime.edge_rule_projection_cursor;
CREATE POLICY edge_rule_projection_cursor_runtime_tenant ON alarm_runtime.edge_rule_projection_cursor
  FOR ALL TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE alarm_runtime.edge_rule_projection_cursor FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT, UPDATE ON alarm_runtime.edge_rule_projection_cursor TO s4_alarm_runtime;

RESET ROLE;

COMMIT;
