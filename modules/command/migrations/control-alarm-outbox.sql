\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s3_command_migrator;

CREATE TABLE IF NOT EXISTS command_runtime.control_alarm_outbox (
  outbox_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  event_kind text NOT NULL CHECK (btrim(event_kind) <> ''),
  reason_code text NOT NULL DEFAULT '',
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'DELIVERED', 'DEAD_LETTER')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at timestamptz NOT NULL,
  lease_owner text,
  lease_until timestamptz,
  delivered_at timestamptz,
  last_error_code text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (command_id, event_kind)
);

CREATE INDEX IF NOT EXISTS control_alarm_outbox_pending_idx
  ON command_runtime.control_alarm_outbox (tenant_id, available_at, lease_until, created_at, outbox_id)
  WHERE status = 'PENDING';

COMMENT ON TABLE command_runtime.control_alarm_outbox IS '控制失败与核验未知状态告警持久化发件箱表，保证进程重启或下游告警通道阻塞时不丢失安全告警。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.outbox_id IS '控制告警发件箱条目主键（UUIDv7）。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.tenant_id IS '所属租户标识，用于行级租户隔离（RLS）。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.command_id IS '关联的控制命令意图标识，引用 command_intents 主键。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.site_id IS '关联站点标识。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.device_id IS '关联目标设备标识。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.event_kind IS '控制安全事件类型，例如 OUTCOME_UNKNOWN、PULSE_DELIVERED_POWER_UNCONFIRMED 或 EMERGENCY_DO_RELEASE。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.reason_code IS '触发告警的结构化原因码。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.payload IS '待投递到告警服务的标准控制事件 JSON 载荷。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.status IS '发件箱投递状态：PENDING 待投递、DELIVERED 已投递、DEAD_LETTER 超过最大重试次数转入死信。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.attempts IS '累计抢占投递尝试次数。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.available_at IS '下一次允许被中继器抢占投递的时间戳。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.lease_owner IS '当前持有投递租约的工作负载实例标识。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.lease_until IS '当前投递租约过期时间戳，过期后允许其他实例接管重试。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.delivered_at IS '成功投递到告警服务的完成时间戳。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.last_error_code IS '最近一次投递失败记录的脱敏错误码。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.created_at IS '发件箱条目创建时间戳。';
COMMENT ON COLUMN command_runtime.control_alarm_outbox.updated_at IS '发件箱条目最近一次状态变更时间戳。';

ALTER TABLE command_runtime.control_alarm_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.control_alarm_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY control_alarm_outbox_migrator_all ON command_runtime.control_alarm_outbox
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);

CREATE POLICY control_alarm_outbox_runtime_tenant ON command_runtime.control_alarm_outbox
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE command_runtime.control_alarm_outbox FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON command_runtime.control_alarm_outbox TO s3_command_runtime;

CREATE OR REPLACE FUNCTION command_runtime.tenants_with_pending_control_alarms(ready_at timestamptz)
RETURNS SETOF uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, command_runtime
AS $$
  SELECT DISTINCT tenant_id
  FROM command_runtime.control_alarm_outbox
  WHERE status = 'PENDING'
    AND available_at <= ready_at
    AND (lease_until IS NULL OR lease_until <= ready_at)
  ORDER BY tenant_id;
$$;

COMMENT ON FUNCTION command_runtime.tenants_with_pending_control_alarms(timestamptz) IS '以定义者权限发现当前存在待投递控制安全告警的租户列表，供运行时按租户激活 RLS 后逐租户抢占投递。';

REVOKE ALL ON FUNCTION command_runtime.tenants_with_pending_control_alarms(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION command_runtime.tenants_with_pending_control_alarms(timestamptz) TO s3_command_runtime;

RESET ROLE;
COMMIT;
