/*
  自动优化控制安全护栏（P0）：
  1. 将 site_auto_control_leases 供水温度下限收紧至 5.0°C，新增 deadband_c 与人工干预退避字段
  2. 允许 optimization_execution_monitors 写入无指令意图的影子/拦截/失败记录（command_intent_id 可空），扩展状态枚举并记录护栏决策轨迹
  3. 原地替换 core_registry.site_auto_control_leases_v1 与 core_registry.optimization_execution_monitors_v1 只读视图并在末尾追加新增列
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

UPDATE core_registry.site_auto_control_leases
SET supply_temp_min_c = 5.0,
    supply_temp_max_c = GREATEST(supply_temp_max_c, 5.0)
WHERE supply_temp_min_c < 5.0;

DO $$
DECLARE
  v_constraint text;
BEGIN
  FOR v_constraint IN
    SELECT c.conname
    FROM pg_constraint c
    JOIN pg_class r ON r.oid = c.conrelid
    JOIN pg_namespace n ON n.oid = r.relnamespace
    WHERE n.nspname = 'core_registry'
      AND r.relname = 'site_auto_control_leases'
      AND c.contype = 'c'
      AND pg_get_constraintdef(c.oid) LIKE '%supply_temp_min_c%'
      AND pg_get_constraintdef(c.oid) NOT LIKE '%supply_temp_max_c%'
  LOOP
    EXECUTE format('ALTER TABLE core_registry.site_auto_control_leases DROP CONSTRAINT %I', v_constraint);
  END LOOP;
END
$$;

ALTER TABLE core_registry.site_auto_control_leases
  ADD CONSTRAINT site_auto_control_leases_supply_temp_min_c_check
  CHECK (supply_temp_min_c >= 5.0 AND supply_temp_min_c <= 20.0);

ALTER TABLE core_registry.site_auto_control_leases
  ADD COLUMN IF NOT EXISTS deadband_c double precision NOT NULL DEFAULT 0.2,
  ADD COLUMN IF NOT EXISTS manual_backoff_until timestamptz NULL,
  ADD COLUMN IF NOT EXISTS manual_backoff_reason text NOT NULL DEFAULT '';

ALTER TABLE core_registry.site_auto_control_leases
  DROP CONSTRAINT IF EXISTS site_auto_control_leases_deadband_c_check;
ALTER TABLE core_registry.site_auto_control_leases
  ADD CONSTRAINT site_auto_control_leases_deadband_c_check
  CHECK (deadband_c >= 0.05 AND deadband_c <= 2.0);

COMMENT ON COLUMN core_registry.site_auto_control_leases.deadband_c IS '自动调温控制死区（摄氏度），设定点调整绝对值小于该死区时不触发指令下发。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.manual_backoff_until IS '检测到现场人工介入后的自动控制退避冷却截止时间，未到期前暂停自动下发。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.manual_backoff_reason IS '触发自动控制人工干预退避冷却的原因说明。';

ALTER TABLE core_registry.optimization_execution_monitors
  ALTER COLUMN command_intent_id DROP NOT NULL;

DO $$
DECLARE
  v_constraint text;
BEGIN
  FOR v_constraint IN
    SELECT c.conname
    FROM pg_constraint c
    JOIN pg_class r ON r.oid = c.conrelid
    JOIN pg_namespace n ON n.oid = r.relnamespace
    WHERE n.nspname = 'core_registry'
      AND r.relname = 'optimization_execution_monitors'
      AND c.contype = 'c'
      AND (c.conname = 'optimization_execution_monitors_status_check' OR pg_get_constraintdef(c.oid) LIKE '%OBSERVING%')
  LOOP
    EXECUTE format('ALTER TABLE core_registry.optimization_execution_monitors DROP CONSTRAINT %I', v_constraint);
  END LOOP;
END
$$;

ALTER TABLE core_registry.optimization_execution_monitors
  ADD CONSTRAINT optimization_execution_monitors_status_check
  CHECK (status IN (
    'PENDING_SUBMIT',
    'OBSERVING',
    'VERIFIED',
    'ROLLED_BACK',
    'TRIPPED',
    'TRIPPED_ROLLED_BACK',
    'ROLLBACK_FAILED',
    'SHADOW_RECORDED',
    'SUBMIT_FAILED',
    'MANUAL_OVERRIDE'
  ));

DROP POLICY IF EXISTS site_auto_control_leases_optimization_update_scope ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_optimization_update_scope ON core_registry.site_auto_control_leases
  FOR UPDATE TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (
    tenant_id = core_registry.current_tenant_id()
    AND core_registry.is_authorized_site(site_id)
    AND status IN ('ACTIVE', 'TRIPPED', 'EXPIRED')
  );

GRANT UPDATE (status, trip_reason, manual_backoff_until, manual_backoff_reason, revision, updated_at)
  ON core_registry.site_auto_control_leases TO optimization_runtime;

ALTER TABLE core_registry.optimization_execution_monitors
  ADD COLUMN IF NOT EXISTS supply_temp_min_c double precision NOT NULL DEFAULT 5.0,
  ADD COLUMN IF NOT EXISTS raw_recommended_value double precision NULL,
  ADD COLUMN IF NOT EXISTS clamped_value double precision NULL,
  ADD COLUMN IF NOT EXISTS guardrail_verdict text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS guardrail_trace jsonb NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN core_registry.optimization_execution_monitors.supply_temp_min_c IS '执行观察窗口内允许的冷冻水供水温度下限（摄氏度）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.raw_recommended_value IS '经安全护栏步长与上下限钳位前优化引擎产出的原始推荐设定值。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.clamped_value IS '经安全护栏边界与步长约束钳位后的实际目标设定值。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.guardrail_verdict IS '安全护栏对本次推荐值的判定结论，如放行、钳位、死区跳过或拦截。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.guardrail_trace IS '安全护栏逐项安全规则评估与数值裁剪过程的结构化轨迹数组。';

RESET ROLE;

CREATE OR REPLACE VIEW core_registry.site_auto_control_leases_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, status, armed_by, armed_at, expires_at,
       allowed_capabilities, max_setpoint_step_c, supply_temp_min_c,
       supply_temp_max_c, max_commands_per_hour, revision, updated_at,
       deadband_c, manual_backoff_until, manual_backoff_reason
FROM core_registry.site_auto_control_leases
WHERE tenant_id = core_registry.current_tenant_id();

CREATE OR REPLACE VIEW core_registry.optimization_execution_monitors_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, lease_id, device_id, target_capability,
       restore_supply_temp_c, started_at, window_ends_at, status, updated_at,
       supply_temp_min_c, raw_recommended_value, clamped_value,
       guardrail_verdict, guardrail_trace
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = core_registry.current_tenant_id();

COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.deadband_c IS '自动调温控制死区（摄氏度），设定点调整绝对值小于该死区时不触发指令下发。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.manual_backoff_until IS '检测到现场人工介入后的自动控制退避冷却截止时间，未到期前暂停自动下发。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.manual_backoff_reason IS '触发自动控制人工干预退避冷却的原因说明。';

COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.supply_temp_min_c IS '执行观察窗口内允许的冷冻水供水温度下限（摄氏度）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.raw_recommended_value IS '经安全护栏步长与上下限钳位前优化引擎产出的原始推荐设定值。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.clamped_value IS '经安全护栏边界与步长约束钳位后的实际目标设定值。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.guardrail_verdict IS '安全护栏对本次推荐值的判定结论，如放行、钳位、死区跳过或拦截。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.guardrail_trace IS '安全护栏逐项安全规则评估与数值裁剪过程的结构化轨迹数组。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.status IS '观察状态：PENDING_SUBMIT、OBSERVING、VERIFIED、ROLLED_BACK、TRIPPED、TRIPPED_ROLLED_BACK、ROLLBACK_FAILED、SHADOW_RECORDED、SUBMIT_FAILED 或 MANUAL_OVERRIDE。';

COMMIT;
