/*
  自动优化控制影子执行模式与决策/追踪链路字段（P1）：
  1. 在 site_auto_control_leases 新增 execution_mode（SHADOW | CLOSED_LOOP，默认 CLOSED_LOOP），支持先以影子租约验证再切闭环
  2. 在 optimization_execution_monitors 新增 execution_mode、decision_id（优化决策周期 UUID）与 trace_id（32 位 W3C 追踪编号）及决策部分索引
  3. 原地替换 core_registry.site_auto_control_leases_v1 与 core_registry.optimization_execution_monitors_v1 只读视图并在末尾追加新增列
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.site_auto_control_leases
  ADD COLUMN IF NOT EXISTS execution_mode text NOT NULL DEFAULT 'CLOSED_LOOP';

ALTER TABLE core_registry.site_auto_control_leases
  DROP CONSTRAINT IF EXISTS site_auto_control_leases_execution_mode_check;
ALTER TABLE core_registry.site_auto_control_leases
  ADD CONSTRAINT site_auto_control_leases_execution_mode_check
  CHECK (execution_mode IN ('SHADOW', 'CLOSED_LOOP'));

COMMENT ON COLUMN core_registry.site_auto_control_leases.execution_mode IS '自动控制租约执行模式：SHADOW（影子推演，仅走安全护栏与监控记录而不真实下发指令）或 CLOSED_LOOP（闭环控制，真实下发指令并进入执行观察）。';

ALTER TABLE core_registry.optimization_execution_monitors
  ADD COLUMN IF NOT EXISTS execution_mode text NOT NULL DEFAULT 'CLOSED_LOOP',
  ADD COLUMN IF NOT EXISTS decision_id uuid NULL,
  ADD COLUMN IF NOT EXISTS trace_id text NOT NULL DEFAULT '';

ALTER TABLE core_registry.optimization_execution_monitors
  DROP CONSTRAINT IF EXISTS optimization_execution_monitors_execution_mode_check;
ALTER TABLE core_registry.optimization_execution_monitors
  ADD CONSTRAINT optimization_execution_monitors_execution_mode_check
  CHECK (execution_mode IN ('SHADOW', 'CLOSED_LOOP'));

ALTER TABLE core_registry.optimization_execution_monitors
  DROP CONSTRAINT IF EXISTS optimization_execution_monitors_trace_id_check;
ALTER TABLE core_registry.optimization_execution_monitors
  ADD CONSTRAINT optimization_execution_monitors_trace_id_check
  CHECK (trace_id = '' OR trace_id ~ '^[0-9a-f]{32}$');

CREATE INDEX IF NOT EXISTS optimization_execution_monitors_decision_idx
  ON core_registry.optimization_execution_monitors (tenant_id, site_id, decision_id)
  WHERE decision_id IS NOT NULL;

COMMENT ON COLUMN core_registry.optimization_execution_monitors.execution_mode IS '本次执行监控关联的租约执行模式：SHADOW（影子推演）或 CLOSED_LOOP（闭环控制）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.decision_id IS '关联的优化决策周期标识，用于串联同一轮寻优建议、护栏裁剪记录与执行监控。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.trace_id IS '提交或记录本次优化执行时携带的 W3C 32 位十六进制分布式追踪编号（未携带追踪上下文时为空字符串）。';

RESET ROLE;

CREATE OR REPLACE VIEW core_registry.site_auto_control_leases_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, status, armed_by, armed_at, expires_at,
       allowed_capabilities, max_setpoint_step_c, supply_temp_min_c,
       supply_temp_max_c, max_commands_per_hour, revision, updated_at,
       deadband_c, manual_backoff_until, manual_backoff_reason,
       execution_mode
FROM core_registry.site_auto_control_leases
WHERE tenant_id = core_registry.current_tenant_id();

CREATE OR REPLACE VIEW core_registry.optimization_execution_monitors_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, lease_id, device_id, target_capability,
       restore_supply_temp_c, started_at, window_ends_at, status, updated_at,
       supply_temp_min_c, raw_recommended_value, clamped_value,
       guardrail_verdict, guardrail_trace,
       execution_mode, decision_id, trace_id
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = core_registry.current_tenant_id();

COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.execution_mode IS '自动控制租约执行模式：SHADOW（影子推演）或 CLOSED_LOOP（闭环控制）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.execution_mode IS '本次执行监控关联的租约执行模式：SHADOW（影子推演）或 CLOSED_LOOP（闭环控制）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.decision_id IS '关联的优化决策周期标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.trace_id IS '提交或记录本次优化执行时携带的 W3C 32 位十六进制分布式追踪编号。';

COMMIT;
