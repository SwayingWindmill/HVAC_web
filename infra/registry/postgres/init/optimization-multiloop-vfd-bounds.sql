/*
  多回路变频闭环边界与执行监控扩展：
  1. 在 core_registry.site_auto_control_leases 新增水泵变频与冷却塔风机自动调控安全边界（全部可空，默认 NULL 表示沿用安全默认值或未定制）
  2. 在 core_registry.optimization_execution_monitors 新增 loop_key 与 target_unit，区分冷冻水供水温度、冷冻泵/冷却泵频率与冷却塔风机转速回路
  3. 同步更新 IAM 只读视图 site_auto_control_leases_v1 与 optimization_execution_monitors_v1
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.site_auto_control_leases
  ADD COLUMN IF NOT EXISTS pump_freq_min_hz double precision NULL,
  ADD COLUMN IF NOT EXISTS pump_freq_max_hz double precision NULL,
  ADD COLUMN IF NOT EXISTS max_freq_step_hz double precision NULL,
  ADD COLUMN IF NOT EXISTS freq_deadband_hz double precision NULL,
  ADD COLUMN IF NOT EXISTS fan_speed_min_pct double precision NULL,
  ADD COLUMN IF NOT EXISTS fan_speed_max_pct double precision NULL,
  ADD COLUMN IF NOT EXISTS max_fan_step_pct double precision NULL,
  ADD COLUMN IF NOT EXISTS fan_deadband_pct double precision NULL;

ALTER TABLE core_registry.site_auto_control_leases
  DROP CONSTRAINT IF EXISTS site_auto_control_leases_pump_freq_bounds_check;
ALTER TABLE core_registry.site_auto_control_leases
  ADD CONSTRAINT site_auto_control_leases_pump_freq_bounds_check CHECK (
    (pump_freq_min_hz IS NULL OR (pump_freq_min_hz >= 20.0 AND pump_freq_min_hz <= 50.0))
    AND (pump_freq_max_hz IS NULL OR (pump_freq_max_hz >= 25.0 AND pump_freq_max_hz <= 60.0))
    AND (pump_freq_min_hz IS NULL OR pump_freq_max_hz IS NULL OR pump_freq_min_hz <= pump_freq_max_hz)
    AND (max_freq_step_hz IS NULL OR (max_freq_step_hz > 0.0 AND max_freq_step_hz <= 10.0))
    AND (freq_deadband_hz IS NULL OR (freq_deadband_hz >= 0.1 AND freq_deadband_hz <= 5.0))
  );

ALTER TABLE core_registry.site_auto_control_leases
  DROP CONSTRAINT IF EXISTS site_auto_control_leases_fan_speed_bounds_check;
ALTER TABLE core_registry.site_auto_control_leases
  ADD CONSTRAINT site_auto_control_leases_fan_speed_bounds_check CHECK (
    (fan_speed_min_pct IS NULL OR (fan_speed_min_pct >= 15.0 AND fan_speed_min_pct <= 100.0))
    AND (fan_speed_max_pct IS NULL OR (fan_speed_max_pct >= 20.0 AND fan_speed_max_pct <= 100.0))
    AND (fan_speed_min_pct IS NULL OR fan_speed_max_pct IS NULL OR fan_speed_min_pct <= fan_speed_max_pct)
    AND (max_fan_step_pct IS NULL OR (max_fan_step_pct > 0.0 AND max_fan_step_pct <= 25.0))
    AND (fan_deadband_pct IS NULL OR (fan_deadband_pct >= 0.5 AND fan_deadband_pct <= 15.0))
  );

COMMENT ON COLUMN core_registry.site_auto_control_leases.pump_freq_min_hz IS '水泵变频自动调控允许下限频率（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.pump_freq_max_hz IS '水泵变频自动调控允许上限频率（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.max_freq_step_hz IS '水泵单次自动调频最大步长（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.freq_deadband_hz IS '水泵调频防抖死区阈值（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.fan_speed_min_pct IS '冷却塔风机自动调控允许下限转速（%，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.fan_speed_max_pct IS '冷却塔风机自动调控允许上限转速（%，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.max_fan_step_pct IS '冷却塔风机单次自动调速最大步长（%，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases.fan_deadband_pct IS '冷却塔风机调速防抖死区阈值（%，可为空）。';

ALTER TABLE core_registry.optimization_execution_monitors
  ADD COLUMN IF NOT EXISTS loop_key text NOT NULL DEFAULT 'PLANT'
    CHECK (length(btrim(loop_key)) BETWEEN 1 AND 64),
  ADD COLUMN IF NOT EXISTS target_unit text NOT NULL DEFAULT 'C'
    CHECK (target_unit IN ('C', 'Hz', '%'));

COMMENT ON COLUMN core_registry.optimization_execution_monitors.loop_key IS '本次自动控制执行监控所属的控制回路标识键（如 PLANT、CHW_PUMP、CW_PUMP、CT_FAN）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.target_unit IS '本次执行监控目标物理量单位（C 代表摄氏度，Hz 代表赫兹，% 代表百分比）。';

RESET ROLE;

CREATE OR REPLACE VIEW core_registry.site_auto_control_leases_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, status, armed_by, armed_at, expires_at,
       allowed_capabilities, max_setpoint_step_c, supply_temp_min_c,
       supply_temp_max_c, max_commands_per_hour, revision, updated_at,
       deadband_c, manual_backoff_until, manual_backoff_reason,
       execution_mode,
       pump_freq_min_hz, pump_freq_max_hz, max_freq_step_hz, freq_deadband_hz,
       fan_speed_min_pct, fan_speed_max_pct, max_fan_step_pct, fan_deadband_pct
FROM core_registry.site_auto_control_leases
WHERE tenant_id = core_registry.current_tenant_id();

CREATE OR REPLACE VIEW core_registry.optimization_execution_monitors_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, lease_id, device_id, target_capability,
       restore_supply_temp_c, started_at, window_ends_at, status, updated_at,
       supply_temp_min_c, raw_recommended_value, clamped_value,
       guardrail_verdict, guardrail_trace,
       execution_mode, decision_id, trace_id,
       loop_key, target_unit
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = core_registry.current_tenant_id();

COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.pump_freq_min_hz IS '水泵变频自动调控允许下限频率（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.pump_freq_max_hz IS '水泵变频自动调控允许上限频率（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.max_freq_step_hz IS '水泵单次自动调频最大步长（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.freq_deadband_hz IS '水泵调频防抖死区阈值（Hz，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.fan_speed_min_pct IS '冷却塔风机自动调控允许下限转速（%，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.fan_speed_max_pct IS '冷却塔风机自动调控允许上限转速（%，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.max_fan_step_pct IS '冷却塔风机单次自动调速最大步长（%，可为空）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.fan_deadband_pct IS '冷却塔风机调速防抖死区阈值（%，可为空）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.loop_key IS '本次自动控制执行监控所属的控制回路标识键（如 PLANT、CHW_PUMP、CW_PUMP、CT_FAN）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.target_unit IS '本次执行监控目标物理量单位（C 代表摄氏度，Hz 代表赫兹，% 代表百分比）。';

COMMIT;
