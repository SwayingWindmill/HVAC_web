-- 将 IAM 运行时（s1_iam_runtime）对核心注册表基表的直接读取权限收回，
-- 改为通过 core_registry_read_port 持有的 *_v1 契约视图读取。
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

GRANT USAGE ON SCHEMA core_registry TO core_registry_read_port, s1_iam_runtime;

DROP POLICY IF EXISTS sites_read_port_tenant_catalog ON core_registry.sites;
CREATE POLICY sites_read_port_tenant_catalog ON core_registry.sites
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());

CREATE VIEW core_registry.sites_v1 WITH (security_barrier) AS
SELECT id, tenant_id, status
FROM core_registry.sites
WHERE tenant_id = core_registry.current_tenant_id();

CREATE VIEW core_registry.iam_requested_devices_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, device_type, status
FROM core_registry.devices
WHERE tenant_id = core_registry.current_tenant_id()
  AND id = ANY (core_registry.current_iam_requested_device_ids());

CREATE VIEW core_registry.site_auto_control_leases_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, status, armed_by, armed_at, expires_at,
       allowed_capabilities, max_setpoint_step_c, supply_temp_min_c,
       supply_temp_max_c, max_commands_per_hour, revision, updated_at
FROM core_registry.site_auto_control_leases
WHERE tenant_id = core_registry.current_tenant_id();

CREATE VIEW core_registry.optimization_execution_monitors_v1 WITH (security_barrier) AS
SELECT id, tenant_id, site_id, lease_id, device_id, target_capability,
       restore_supply_temp_c, started_at, window_ends_at, status, updated_at
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = core_registry.current_tenant_id();

DROP POLICY IF EXISTS sites_iam_tenant_catalog_scope ON core_registry.sites;
DROP POLICY IF EXISTS devices_iam_exact_request_scope ON core_registry.devices;
DROP POLICY IF EXISTS site_auto_control_leases_iam_scope ON core_registry.site_auto_control_leases;
DROP POLICY IF EXISTS optimization_execution_monitors_iam_scope ON core_registry.optimization_execution_monitors;
DROP POLICY IF EXISTS site_control_states_iam_scope ON core_registry.site_control_states;
DROP POLICY IF EXISTS asset_control_locks_iam_scope ON core_registry.asset_control_locks;
DROP POLICY IF EXISTS device_bindings_iam_scope ON core_registry.device_bindings;

REVOKE ALL ON
  core_registry.sites,
  core_registry.devices,
  core_registry.device_bindings,
  core_registry.site_control_states,
  core_registry.asset_control_locks,
  core_registry.site_auto_control_leases,
  core_registry.optimization_execution_monitors
FROM s1_iam_runtime;

RESET ROLE;

ALTER VIEW core_registry.sites_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.iam_requested_devices_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.site_auto_control_leases_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.optimization_execution_monitors_v1 OWNER TO core_registry_read_port;

REVOKE ALL ON
  core_registry.sites_v1,
  core_registry.iam_requested_devices_v1,
  core_registry.site_auto_control_leases_v1,
  core_registry.optimization_execution_monitors_v1
FROM PUBLIC;

GRANT SELECT ON
  core_registry.sites_v1,
  core_registry.iam_requested_devices_v1,
  core_registry.site_control_states_v1,
  core_registry.device_control_locks_v1,
  core_registry.site_auto_control_leases_v1,
  core_registry.optimization_execution_monitors_v1
TO s1_iam_runtime;

COMMENT ON VIEW core_registry.sites_v1 IS 'IAM 授权只读视图：当前租户下的站点标识与生命周期状态目录。';
COMMENT ON COLUMN core_registry.sites_v1.id IS '站点主键标识。';
COMMENT ON COLUMN core_registry.sites_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.sites_v1.status IS '站点状态：ACTIVE、INACTIVE 或 RETIRED。';

COMMENT ON VIEW core_registry.iam_requested_devices_v1 IS 'IAM 授权只读视图：严格限定在当前租户与 app.requested_device_ids 列表内的请求设备范围。';
COMMENT ON COLUMN core_registry.iam_requested_devices_v1.id IS '设备主键标识。';
COMMENT ON COLUMN core_registry.iam_requested_devices_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.iam_requested_devices_v1.site_id IS '设备所属站点标识。';
COMMENT ON COLUMN core_registry.iam_requested_devices_v1.device_type IS '设备类型。';
COMMENT ON COLUMN core_registry.iam_requested_devices_v1.status IS '设备状态：ACTIVE、INACTIVE 或 RETIRED。';

COMMENT ON VIEW core_registry.site_auto_control_leases_v1 IS 'IAM 自动控制负载授权只读视图：当前租户的站点自动优化控制租约与安全边界。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.id IS '租约主键标识。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.status IS '租约状态：ACTIVE、TRIPPED、REVOKED 或 EXPIRED。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.armed_by IS '签署启用该自动控制租约的操作员主体标识。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.armed_at IS '租约启用时间。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.expires_at IS '租约到期时间。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.allowed_capabilities IS '租约允许自动下发的控制能力白名单。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.max_setpoint_step_c IS '单次设定点允许调整的最大步长（摄氏度）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.supply_temp_min_c IS '允许下发的供水温度下限（摄氏度）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.supply_temp_max_c IS '允许下发的供水温度上限（摄氏度）。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.max_commands_per_hour IS '每小时允许自动下发指令的最大次数预算。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.revision IS '乐观锁版本号。';
COMMENT ON COLUMN core_registry.site_auto_control_leases_v1.updated_at IS '记录最后修改时间。';

COMMENT ON VIEW core_registry.optimization_execution_monitors_v1 IS 'IAM 自动控制负载授权只读视图：当前租户的自动控制指令观察窗口与回滚目标记录。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.id IS '观察记录主键标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.lease_id IS '关联的站点自动控制租约标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.device_id IS '被控设备标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.target_capability IS '目标控制能力标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.restore_supply_temp_c IS '异常时用于自动回滚恢复的供水温度设定值（摄氏度）。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.started_at IS '执行观察窗口开始时间。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.window_ends_at IS '执行观察窗口结束时间。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.status IS '观察状态：OBSERVING、VERIFIED、ROLLED_BACK 或 TRIPPED。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors_v1.updated_at IS '记录最后修改时间。';

COMMIT;
