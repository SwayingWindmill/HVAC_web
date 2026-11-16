/*
  自动控制授权租约与执行监控窗口
  1. site_auto_control_leases 记录站点自动优化控制的限时授权、能力白名单、步长与温度边界及熔断状态
  2. optimization_execution_monitors 记录自动下发控制指令后的 30 分钟执行观察窗口与自动回滚结果
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.site_auto_control_leases (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'TRIPPED', 'REVOKED', 'EXPIRED')),
  armed_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(armed_by)),
  armed_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  allowed_capabilities text[] NOT NULL CHECK (cardinality(allowed_capabilities) > 0),
  max_setpoint_step_c double precision NOT NULL DEFAULT 1.0 CHECK (max_setpoint_step_c > 0 AND max_setpoint_step_c <= 2.0),
  supply_temp_min_c double precision NOT NULL CHECK (supply_temp_min_c >= 4.0 AND supply_temp_min_c <= 20.0),
  supply_temp_max_c double precision NOT NULL CHECK (supply_temp_max_c >= supply_temp_min_c AND supply_temp_max_c <= 25.0),
  zone_temp_max_c double precision NOT NULL DEFAULT 26.0 CHECK (zone_temp_max_c >= 18.0 AND zone_temp_max_c <= 32.0),
  min_running_chillers integer NOT NULL DEFAULT 1 CHECK (min_running_chillers >= 0),
  max_commands_per_hour integer NOT NULL DEFAULT 2 CHECK (max_commands_per_hour >= 1 AND max_commands_per_hour <= 6),
  trip_reason text,
  revision bigint NOT NULL CHECK (revision >= 1),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (expires_at > armed_at AND expires_at <= armed_at + interval '24 hours'),
  CHECK (updated_at >= created_at)
);

COMMENT ON TABLE core_registry.site_auto_control_leases IS '站级自动优化控制授权租约，记录有效期、权限白名单、设定点步长和温控边界以及熔断状态。';

CREATE UNIQUE INDEX IF NOT EXISTS site_auto_control_leases_one_active_idx
  ON core_registry.site_auto_control_leases (tenant_id, site_id)
  WHERE status = 'ACTIVE';

CREATE INDEX IF NOT EXISTS site_auto_control_leases_site_updated_idx
  ON core_registry.site_auto_control_leases (tenant_id, site_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS core_registry.optimization_execution_monitors (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  lease_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(lease_id)),
  recommendation_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(recommendation_id)),
  command_intent_id uuid NOT NULL UNIQUE,
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  target_capability text NOT NULL CHECK (length(btrim(target_capability)) > 0),
  baseline_supply_temp_c double precision,
  applied_supply_temp_c double precision,
  restore_supply_temp_c double precision,
  zone_temp_max_c double precision NOT NULL,
  supply_temp_max_c double precision NOT NULL,
  started_at timestamptz NOT NULL,
  window_ends_at timestamptz NOT NULL,
  last_checked_at timestamptz,
  telemetry_unhealthy_since timestamptz,
  status text NOT NULL CHECK (status IN ('OBSERVING', 'VERIFIED', 'ROLLED_BACK', 'TRIPPED')),
  trip_reason text,
  rollback_command_intent_id uuid,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, lease_id) REFERENCES core_registry.site_auto_control_leases(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, recommendation_id) REFERENCES core_registry.optimization_recommendations(tenant_id, site_id, id),
  CHECK (window_ends_at > started_at),
  CHECK (updated_at >= created_at)
);

COMMENT ON TABLE core_registry.optimization_execution_monitors IS '自动优化控制指令下发后的执行观察窗口记录，追踪温度边界、主机功率与异常自动回滚状态。';

CREATE INDEX IF NOT EXISTS optimization_execution_monitors_site_status_idx
  ON core_registry.optimization_execution_monitors (tenant_id, site_id, status, started_at DESC);

CREATE INDEX IF NOT EXISTS optimization_execution_monitors_observing_idx
  ON core_registry.optimization_execution_monitors (status, window_ends_at, started_at)
  WHERE status = 'OBSERVING';

ALTER TABLE core_registry.registry_audit_facts
  DROP CONSTRAINT IF EXISTS registry_audit_facts_resource_type_check;
ALTER TABLE core_registry.registry_audit_facts
  ADD CONSTRAINT registry_audit_facts_resource_type_check
  CHECK (resource_type IN (
    'SITE', 'SPACE', 'ASSET', 'DEVICE', 'SENSOR', 'POINT', 'BINDING',
    'TEMPLATE_REVISION', 'TEMPLATE_ASSIGNMENT', 'IMPORT', 'RETIREMENT',
    'OPERATING_SCHEDULE', 'OPERATING_EXCEPTION',
    'MONTHLY_ELECTRICITY_BUDGET', 'UTILITY_BILL',
    'SITE_CONTROL_STATE', 'ASSET_CONTROL_LOCK', 'AUTO_CONTROL_LEASE'
  ));

ALTER TABLE core_registry.site_auto_control_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.site_auto_control_leases FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_execution_monitors ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_execution_monitors FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS site_auto_control_leases_migrator_all ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_migrator_all ON core_registry.site_auto_control_leases
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS optimization_execution_monitors_migrator_all ON core_registry.optimization_execution_monitors;
CREATE POLICY optimization_execution_monitors_migrator_all ON core_registry.optimization_execution_monitors
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS site_auto_control_leases_runtime_scope ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_runtime_scope ON core_registry.site_auto_control_leases
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_execution_monitors_runtime_scope ON core_registry.optimization_execution_monitors;
CREATE POLICY optimization_execution_monitors_runtime_scope ON core_registry.optimization_execution_monitors
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS site_auto_control_leases_writer_scope ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_writer_scope ON core_registry.site_auto_control_leases
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_execution_monitors_writer_scope ON core_registry.optimization_execution_monitors;
CREATE POLICY optimization_execution_monitors_writer_scope ON core_registry.optimization_execution_monitors
  FOR SELECT TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS site_auto_control_leases_optimization_select_scope ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_optimization_select_scope ON core_registry.site_auto_control_leases
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS site_auto_control_leases_optimization_update_scope ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_optimization_update_scope ON core_registry.site_auto_control_leases
  FOR UPDATE TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (
    tenant_id = core_registry.current_tenant_id()
    AND core_registry.is_authorized_site(site_id)
    AND status IN ('TRIPPED', 'EXPIRED')
  );

DROP POLICY IF EXISTS optimization_execution_monitors_optimization_scope ON core_registry.optimization_execution_monitors;
CREATE POLICY optimization_execution_monitors_optimization_scope ON core_registry.optimization_execution_monitors
  FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_execution_monitors_optimization_poll ON core_registry.optimization_execution_monitors;
CREATE POLICY optimization_execution_monitors_optimization_poll ON core_registry.optimization_execution_monitors
  FOR SELECT TO optimization_runtime
  USING (status = 'OBSERVING');

DROP POLICY IF EXISTS site_auto_control_leases_read_port ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_read_port ON core_registry.site_auto_control_leases
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS optimization_execution_monitors_read_port ON core_registry.optimization_execution_monitors;
CREATE POLICY optimization_execution_monitors_read_port ON core_registry.optimization_execution_monitors
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS site_auto_control_leases_iam_scope ON core_registry.site_auto_control_leases;
CREATE POLICY site_auto_control_leases_iam_scope ON core_registry.site_auto_control_leases
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS optimization_execution_monitors_iam_scope ON core_registry.optimization_execution_monitors;
CREATE POLICY optimization_execution_monitors_iam_scope ON core_registry.optimization_execution_monitors
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS site_control_states_iam_scope ON core_registry.site_control_states;
CREATE POLICY site_control_states_iam_scope ON core_registry.site_control_states
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS asset_control_locks_iam_scope ON core_registry.asset_control_locks;
CREATE POLICY asset_control_locks_iam_scope ON core_registry.asset_control_locks
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS device_bindings_iam_scope ON core_registry.device_bindings;
CREATE POLICY device_bindings_iam_scope ON core_registry.device_bindings
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id = core_registry.current_tenant_id());

REVOKE ALL ON core_registry.site_auto_control_leases, core_registry.optimization_execution_monitors FROM PUBLIC;
GRANT SELECT ON core_registry.site_auto_control_leases, core_registry.optimization_execution_monitors TO s1_core_runtime, core_registry_read_port, s1_iam_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.site_auto_control_leases TO s1_core_writer;
GRANT SELECT ON core_registry.optimization_execution_monitors TO s1_core_writer;
GRANT SELECT ON core_registry.site_auto_control_leases TO optimization_runtime;
GRANT UPDATE (status, trip_reason, revision, updated_at) ON core_registry.site_auto_control_leases TO optimization_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.optimization_execution_monitors TO optimization_runtime;
GRANT SELECT ON core_registry.site_control_states, core_registry.asset_control_locks TO s1_iam_runtime;
GRANT SELECT (tenant_id, site_id, device_id, asset_id, status, valid_from, valid_to)
  ON core_registry.device_bindings TO s1_iam_runtime;

RESET ROLE;

COMMIT;
