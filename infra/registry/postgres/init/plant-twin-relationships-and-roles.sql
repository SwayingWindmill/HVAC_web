-- 语义设备群孪生模型扩展：资产显式液压关系（asset_relationships）、点位语义角色（semantic_role）
-- 与 ADR-0016 只读端口孪生视图（plant_twin_*_v1）。
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.asset_relationships (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  from_asset_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(from_asset_id)),
  to_asset_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(to_asset_id)),
  relationship_role text NOT NULL CHECK (relationship_role IN ('SERVES')),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL DEFAULT clock_timestamp(),
  valid_to timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  CHECK (from_asset_id <> to_asset_id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, from_asset_id) REFERENCES core_registry.assets(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, to_asset_id) REFERENCES core_registry.assets(tenant_id, site_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS asset_relationships_active_edge_uidx
  ON core_registry.asset_relationships (tenant_id, site_id, from_asset_id, to_asset_id, relationship_role)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

CREATE INDEX IF NOT EXISTS asset_relationships_site_scope_idx
  ON core_registry.asset_relationships (tenant_id, site_id, status, relationship_role, from_asset_id, to_asset_id);

ALTER TABLE core_registry.asset_relationships ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.asset_relationships FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS asset_relationships_migrator_all ON core_registry.asset_relationships;
CREATE POLICY asset_relationships_migrator_all ON core_registry.asset_relationships
  FOR ALL TO s1_core_migrator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS asset_relationships_runtime_scope ON core_registry.asset_relationships;
CREATE POLICY asset_relationships_runtime_scope ON core_registry.asset_relationships
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS asset_relationships_writer_scope ON core_registry.asset_relationships;
CREATE POLICY asset_relationships_writer_scope ON core_registry.asset_relationships
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS asset_relationships_optimization_scope ON core_registry.asset_relationships;
CREATE POLICY asset_relationships_optimization_scope ON core_registry.asset_relationships
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS asset_relationships_fdd_scope ON core_registry.asset_relationships;
CREATE POLICY asset_relationships_fdd_scope ON core_registry.asset_relationships
  FOR SELECT TO fdd_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS asset_relationships_read_port ON core_registry.asset_relationships;
CREATE POLICY asset_relationships_read_port ON core_registry.asset_relationships
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.asset_relationships FROM PUBLIC;
GRANT SELECT ON core_registry.asset_relationships TO s1_core_runtime, optimization_runtime, fdd_runtime, core_registry_read_port;
GRANT SELECT, INSERT, UPDATE ON core_registry.asset_relationships TO s1_core_writer;

ALTER TABLE core_registry.point_subject_bindings
  ADD COLUMN IF NOT EXISTS semantic_role text NULL;

ALTER TABLE core_registry.point_subject_bindings
  DROP CONSTRAINT IF EXISTS point_subject_bindings_semantic_role_check;
ALTER TABLE core_registry.point_subject_bindings
  ADD CONSTRAINT point_subject_bindings_semantic_role_check
  CHECK (semantic_role IS NULL OR semantic_role IN (
    'ACTIVE_POWER',
    'RUN_STATE',
    'CHW_LEAVING_TEMP',
    'CHW_ENTERING_TEMP',
    'CW_LEAVING_TEMP',
    'CW_ENTERING_TEMP',
    'SUPPLY_WATER_TEMP',
    'RETURN_WATER_TEMP',
    'COOLING_LOAD',
    'WATER_FLOW',
    'ZONE_TEMP'
  ));

CREATE INDEX IF NOT EXISTS point_subject_bindings_semantic_role_idx
  ON core_registry.point_subject_bindings (tenant_id, site_id, semantic_role)
  WHERE semantic_role IS NOT NULL AND status = 'ACTIVE' AND valid_to IS NULL;

GRANT SELECT (semantic_role) ON core_registry.point_subject_bindings TO optimization_runtime;

-- 只读端口角色对底层基表的最小读权限与租户站点隔离策略（避免与 017 / 037 的既有策略重名）。
DROP POLICY IF EXISTS sites_read_port ON core_registry.sites;
CREATE POLICY sites_read_port ON core_registry.sites
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(id));
GRANT SELECT (tenant_id, id, timezone, status) ON core_registry.sites TO core_registry_read_port;

DROP POLICY IF EXISTS assets_read_port ON core_registry.assets;
CREATE POLICY assets_read_port ON core_registry.assets
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT (tenant_id, site_id, id, code, display_name, asset_type, status, rated_power, rated_capacity, nameplate)
  ON core_registry.assets TO core_registry_read_port;

GRANT SELECT (binding_role) ON core_registry.device_bindings TO core_registry_read_port;

DROP POLICY IF EXISTS point_subject_bindings_read_port ON core_registry.point_subject_bindings;
CREATE POLICY point_subject_bindings_read_port ON core_registry.point_subject_bindings
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT (tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, semantic_role, status, valid_from, valid_to)
  ON core_registry.point_subject_bindings TO core_registry_read_port;

DROP POLICY IF EXISTS operating_schedules_read_port ON core_registry.operating_schedules;
CREATE POLICY operating_schedules_read_port ON core_registry.operating_schedules
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT (tenant_id, site_id, id, pre_cool_lead_minutes, coast_down_lag_minutes, revision)
  ON core_registry.operating_schedules TO core_registry_read_port;

DROP POLICY IF EXISTS operating_schedule_slots_read_port ON core_registry.operating_schedule_slots;
CREATE POLICY operating_schedule_slots_read_port ON core_registry.operating_schedule_slots
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT (tenant_id, site_id, schedule_id, schedule_revision, day_of_week, start_minute, end_minute, target_supply_temp_c)
  ON core_registry.operating_schedule_slots TO core_registry_read_port;

DROP POLICY IF EXISTS operating_exceptions_read_port ON core_registry.operating_exceptions;
CREATE POLICY operating_exceptions_read_port ON core_registry.operating_exceptions
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT (tenant_id, site_id, id, title, kind, start_date, end_date, replacement_slots, reason, status)
  ON core_registry.operating_exceptions TO core_registry_read_port;

-- ADR-0016 语义设备群孪生只读视图（每张视图显式绑定租户与授权站点条件）。
CREATE VIEW core_registry.plant_twin_sites_v1 WITH (security_barrier) AS
SELECT s.tenant_id,
       s.id AS site_id,
       s.timezone,
       COALESCE(scs.mode, 'REMOTE_AUTO') AS site_control_mode,
       COALESCE(scs.reason, '') AS site_control_reason
FROM core_registry.sites AS s
LEFT JOIN core_registry.site_control_states AS scs
  ON scs.tenant_id = s.tenant_id
 AND scs.site_id = s.id
WHERE s.status <> 'RETIRED'
  AND s.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(s.id);

CREATE VIEW core_registry.plant_twin_units_v1 WITH (security_barrier) AS
SELECT a.tenant_id,
       a.site_id,
       a.id AS asset_id,
       a.code AS asset_code,
       a.display_name AS asset_name,
       a.asset_type,
       CASE
         WHEN EXISTS (
           SELECT 1
           FROM jsonb_array_elements_text(COALESCE(a.nameplate->'unconfirmed', '[]'::jsonb)) u(f)
           WHERE u.f = 'ratedPowerKw'
         ) THEN NULL::double precision
         ELSE a.rated_power
       END AS rated_power_kw,
       CASE
         WHEN EXISTS (
           SELECT 1
           FROM jsonb_array_elements_text(COALESCE(a.nameplate->'unconfirmed', '[]'::jsonb)) u(f)
           WHERE u.f = 'ratedCapacityKw'
         ) THEN NULL::double precision
         ELSE a.rated_capacity
       END AS rated_capacity_kw,
       CASE
         WHEN EXISTS (
           SELECT 1
           FROM jsonb_array_elements_text(COALESCE(a.nameplate->'unconfirmed', '[]'::jsonb)) u(f)
           WHERE u.f = 'feederAuxiliaryPowerKw'
         ) THEN 0::double precision
         ELSE COALESCE((a.nameplate->>'feederAuxiliaryPowerKw')::double precision, 0::double precision)
       END AS feeder_auxiliary_power_kw
FROM core_registry.assets AS a
WHERE a.status = 'ACTIVE'
  AND a.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(a.site_id);

CREATE VIEW core_registry.plant_twin_asset_locks_v1 WITH (security_barrier) AS
SELECT acl.tenant_id,
       acl.site_id,
       acl.asset_id,
       acl.lock_mode,
       COALESCE(acl.reason, '') AS reason
FROM core_registry.asset_control_locks AS acl
WHERE acl.lock_mode <> 'UNLOCKED'
  AND acl.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(acl.site_id);

CREATE VIEW core_registry.plant_twin_devices_v1 WITH (security_barrier) AS
SELECT d.tenant_id,
       d.site_id,
       d.id AS device_id,
       d.code AS device_code,
       d.device_type,
       COALESCE(gdsk.source_key, d.code) AS source_key
FROM core_registry.devices AS d
LEFT JOIN core_registry.gateway_device_source_keys AS gdsk
  ON gdsk.tenant_id = d.tenant_id
 AND gdsk.site_id = d.site_id
 AND gdsk.device_id = d.id
 AND gdsk.status = 'ACTIVE'
WHERE d.status = 'ACTIVE'
  AND d.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(d.site_id);

CREATE VIEW core_registry.plant_twin_device_bindings_v1 WITH (security_barrier) AS
SELECT db.tenant_id,
       db.site_id,
       db.asset_id,
       db.device_id,
       db.binding_role,
       db.valid_from,
       db.valid_to
FROM core_registry.device_bindings AS db
WHERE db.status = 'ACTIVE'
  AND db.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(db.site_id);

CREATE VIEW core_registry.plant_twin_point_roles_v1 WITH (security_barrier) AS
SELECT psb.tenant_id,
       psb.site_id,
       psb.point_id,
       tp.reporting_device_id,
       d.code AS device_code,
       COALESCE(gdsk.source_key, d.code) AS device_source_key,
       tp.point_code,
       tp.source_key AS wire_source_key,
       tp.point_type,
       COALESCE(tp.unit, '') AS unit,
       tp.writable,
       COALESCE(tp.source_metadata, '{}'::jsonb) AS source_metadata,
       psb.subject_type,
       psb.space_id,
       psb.asset_id,
       psb.binding_role,
       COALESCE(psb.semantic_role, '') AS semantic_role,
       psb.valid_from,
       psb.valid_to
FROM core_registry.point_subject_bindings AS psb
JOIN core_registry.telemetry_points AS tp
  ON tp.tenant_id = psb.tenant_id
 AND tp.site_id = psb.site_id
 AND tp.id = psb.point_id
JOIN core_registry.devices AS d
  ON d.tenant_id = tp.tenant_id
 AND d.site_id = tp.site_id
 AND d.id = tp.reporting_device_id
LEFT JOIN core_registry.gateway_device_source_keys AS gdsk
  ON gdsk.tenant_id = d.tenant_id
 AND gdsk.site_id = d.site_id
 AND gdsk.device_id = d.id
 AND gdsk.status = 'ACTIVE'
WHERE psb.status = 'ACTIVE'
  AND tp.status = 'ACTIVE'
  AND d.status = 'ACTIVE'
  AND psb.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(psb.site_id);

CREATE VIEW core_registry.plant_twin_hydraulic_edges_v1 WITH (security_barrier) AS
SELECT ar.tenant_id,
       ar.site_id,
       ar.from_asset_id,
       ar.to_asset_id,
       ar.relationship_role,
       ar.valid_from,
       ar.valid_to
FROM core_registry.asset_relationships AS ar
WHERE ar.status = 'ACTIVE'
  AND ar.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(ar.site_id);

CREATE VIEW core_registry.plant_twin_schedules_v1 WITH (security_barrier) AS
SELECT os.tenant_id,
       os.site_id,
       os.id AS schedule_id,
       os.pre_cool_lead_minutes,
       os.coast_down_lag_minutes,
       os.revision
FROM core_registry.operating_schedules AS os
WHERE os.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(os.site_id);

CREATE VIEW core_registry.plant_twin_schedule_slots_v1 WITH (security_barrier) AS
SELECT oss.tenant_id,
       oss.site_id,
       oss.schedule_id,
       oss.schedule_revision,
       oss.day_of_week,
       oss.start_minute,
       oss.end_minute,
       oss.target_supply_temp_c
FROM core_registry.operating_schedule_slots AS oss
WHERE oss.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(oss.site_id);

CREATE VIEW core_registry.plant_twin_schedule_exceptions_v1 WITH (security_barrier) AS
SELECT oe.tenant_id,
       oe.site_id,
       oe.id AS exception_id,
       oe.title,
       oe.kind,
       oe.start_date,
       oe.end_date,
       oe.replacement_slots,
       oe.reason
FROM core_registry.operating_exceptions AS oe
WHERE oe.status = 'APPROVED'
  AND oe.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(oe.site_id);

COMMENT ON TABLE core_registry.asset_relationships IS '资产之间的有向连接关系事实表（例如水泵或冷却塔服务于某台主机，或主机汇入水路总管），用于构建设备群孪生水力拓扑。';
COMMENT ON COLUMN core_registry.asset_relationships.id IS '资产关系记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.asset_relationships.tenant_id IS '所属租户标识，用于多租户行级隔离。';
COMMENT ON COLUMN core_registry.asset_relationships.site_id IS '所属站点标识，用于站点级访问范围控制。';
COMMENT ON COLUMN core_registry.asset_relationships.from_asset_id IS '关系的发起方资产标识（例如提供供水或冷却服务的水泵、冷却塔或主机）。';
COMMENT ON COLUMN core_registry.asset_relationships.to_asset_id IS '关系的接收方资产标识（例如被服务的主机或冷冻水总管）。';
COMMENT ON COLUMN core_registry.asset_relationships.relationship_role IS '关系角色类型，当前仅允许 SERVES（水力服务关系）。';
COMMENT ON COLUMN core_registry.asset_relationships.status IS '登记生命周期状态：ACTIVE 为生效中，RETIRED 为已退役。';
COMMENT ON COLUMN core_registry.asset_relationships.valid_from IS '该关系生效的起始时间。';
COMMENT ON COLUMN core_registry.asset_relationships.valid_to IS '该关系失效的结束时间；为空表示当前仍然有效。';
COMMENT ON COLUMN core_registry.asset_relationships.created_at IS '记录创建时间。';
COMMENT ON COLUMN core_registry.asset_relationships.updated_at IS '记录最后修改时间。';
COMMENT ON COLUMN core_registry.asset_relationships.revision IS '乐观锁与变更版本号，从 1 起递增。';

COMMENT ON COLUMN core_registry.point_subject_bindings.semantic_role IS '点位对测量对象承担的物理语义角色（如 ACTIVE_POWER、CHW_LEAVING_TEMP、SUPPLY_WATER_TEMP），用于替代跨站点硬编码点位代码。';

RESET ROLE;

ALTER VIEW core_registry.plant_twin_sites_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_units_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_asset_locks_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_devices_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_device_bindings_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_point_roles_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_hydraulic_edges_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_schedules_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_schedule_slots_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.plant_twin_schedule_exceptions_v1 OWNER TO core_registry_read_port;

GRANT USAGE ON SCHEMA core_registry TO core_registry_read_port;
GRANT SELECT ON
  core_registry.plant_twin_sites_v1,
  core_registry.plant_twin_units_v1,
  core_registry.plant_twin_asset_locks_v1,
  core_registry.plant_twin_devices_v1,
  core_registry.plant_twin_device_bindings_v1,
  core_registry.plant_twin_point_roles_v1,
  core_registry.plant_twin_hydraulic_edges_v1,
  core_registry.plant_twin_schedules_v1,
  core_registry.plant_twin_schedule_slots_v1,
  core_registry.plant_twin_schedule_exceptions_v1
  TO s1_core_runtime, optimization_runtime, fdd_runtime, connectivity_runtime, core_registry_read_port;

COMMENT ON VIEW core_registry.plant_twin_sites_v1 IS '设备群孪生只读视图：站点时区与集中控制全局模式（默认 REMOTE_AUTO）。';
COMMENT ON COLUMN core_registry.plant_twin_sites_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_sites_v1.site_id IS '站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_sites_v1.timezone IS '站点本地 IANA 时区名称。';
COMMENT ON COLUMN core_registry.plant_twin_sites_v1.site_control_mode IS '站点集中控制模式：REMOTE_AUTO、MANUAL_OVERRIDE 或 SAFE_SHUTDOWN_EXIT。';
COMMENT ON COLUMN core_registry.plant_twin_sites_v1.site_control_reason IS '切换到非远程自动模式时填写的现场说明。';

COMMENT ON VIEW core_registry.plant_twin_units_v1 IS '设备群孪生只读视图：活动资产台账与经 unconfirmed 过滤后的铭牌额定参数。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.asset_id IS '资产主键标识。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.asset_code IS '资产在站点内的短代码。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.asset_name IS '资产中文展示名称。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.asset_type IS '资产分类（如 CHILLER、CHILLED_WATER_PUMP、COOLING_WATER_PUMP、COOLING_TOWER、CHILLED_WATER_LOOP）。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.rated_power_kw IS '铭牌额定输入功率（千瓦）；若标记在 nameplate.unconfirmed 中则返回空。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.rated_capacity_kw IS '铭牌额定制冷量（千瓦）；若标记在 nameplate.unconfirmed 中则返回空。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.feeder_auxiliary_power_kw IS '同馈线共用电表下配套辅机估算功率（千瓦）；未配置或未确认时返回 0。';

COMMENT ON VIEW core_registry.plant_twin_asset_locks_v1 IS '设备群孪生只读视图：当前处于锁定状态的资产及其锁定原因。';
COMMENT ON COLUMN core_registry.plant_twin_asset_locks_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_asset_locks_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_asset_locks_v1.asset_id IS '被锁定的资产标识。';
COMMENT ON COLUMN core_registry.plant_twin_asset_locks_v1.lock_mode IS '锁定模式（如 MANUAL_LOCK 或 MAINTENANCE_LOCK）。';
COMMENT ON COLUMN core_registry.plant_twin_asset_locks_v1.reason IS '现场人员设置锁定的原因说明。';

COMMENT ON VIEW core_registry.plant_twin_devices_v1 IS '设备群孪生只读视图：活动设备及其面向网关消息解析的设备名（source_key）。';
COMMENT ON COLUMN core_registry.plant_twin_devices_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_devices_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_devices_v1.device_id IS '设备主键标识。';
COMMENT ON COLUMN core_registry.plant_twin_devices_v1.device_code IS '设备在站点内的短代码。';
COMMENT ON COLUMN core_registry.plant_twin_devices_v1.device_type IS '设备类型（如 HVAC_POWER_METER、CHILLER_CONTROLLER、BTU_METER）。';
COMMENT ON COLUMN core_registry.plant_twin_devices_v1.source_key IS '网关报文中的设备标识名，优先取活动的 gateway_device_source_keys，否则回退为设备代码。';

COMMENT ON VIEW core_registry.plant_twin_device_bindings_v1 IS '设备群孪生只读视图：设备与资产之间的活动绑定关系。';
COMMENT ON COLUMN core_registry.plant_twin_device_bindings_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_device_bindings_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_device_bindings_v1.asset_id IS '被绑定的资产标识。';
COMMENT ON COLUMN core_registry.plant_twin_device_bindings_v1.device_id IS '执行计量、传感或控制的设备标识。';
COMMENT ON COLUMN core_registry.plant_twin_device_bindings_v1.binding_role IS '设备对资产承担的职责角色（如 METER、CONTROLLER、SENSOR）。';
COMMENT ON COLUMN core_registry.plant_twin_device_bindings_v1.valid_from IS '该绑定生效的起始时间。';
COMMENT ON COLUMN core_registry.plant_twin_device_bindings_v1.valid_to IS '该绑定失效的结束时间；为空表示当前仍然有效。';

COMMENT ON VIEW core_registry.plant_twin_point_roles_v1 IS '设备群孪生只读视图：活动点位、上报设备、测量对象与语义角色的统一索引。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.point_id IS '点位主键标识。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.reporting_device_id IS '上报该点位的设备标识。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.device_code IS '上报设备的短代码。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.device_source_key IS '上报设备在网关报文中的设备名。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.point_code IS '点位在平台内的标准代码。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.wire_source_key IS '点位在网关遥测报文中的原始键名（telemetry_points.source_key）。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.point_type IS '点位类型（TELEMETRY、COUNTER、STATE、SETTING 或 COMMAND）。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.unit IS '工程单位代码；未配置单位时返回空字符串。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.writable IS '点位是否允许平台写入控制指令。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.source_metadata IS '控制能力、反馈点与联锁规则等源端元数据。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.subject_type IS '测量或控制对象层级（SITE、SPACE 或 ASSET）。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.space_id IS '当对象为 SPACE 时对应的空间标识。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.asset_id IS '当对象为 ASSET 时对应的资产标识。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.binding_role IS '点位绑定角色（DESCRIBES 或 CONTROLS）。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.semantic_role IS '物理语义角色枚举名；尚未标注时返回空字符串。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.valid_from IS '该点位绑定生效的起始时间。';
COMMENT ON COLUMN core_registry.plant_twin_point_roles_v1.valid_to IS '该点位绑定失效的结束时间；为空表示当前仍然有效。';

COMMENT ON VIEW core_registry.plant_twin_hydraulic_edges_v1 IS '设备群孪生只读视图：活动的资产间水力服务有向边。';
COMMENT ON COLUMN core_registry.plant_twin_hydraulic_edges_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_hydraulic_edges_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_hydraulic_edges_v1.from_asset_id IS '服务提供方资产标识（例如水泵、冷却塔或主机）。';
COMMENT ON COLUMN core_registry.plant_twin_hydraulic_edges_v1.to_asset_id IS '服务接收方资产标识（例如主机或水路总管）。';
COMMENT ON COLUMN core_registry.plant_twin_hydraulic_edges_v1.relationship_role IS '关系角色类型（SERVES）。';
COMMENT ON COLUMN core_registry.plant_twin_hydraulic_edges_v1.valid_from IS '该连接生效的起始时间。';
COMMENT ON COLUMN core_registry.plant_twin_hydraulic_edges_v1.valid_to IS '该连接失效的结束时间；为空表示当前仍然有效。';

COMMENT ON VIEW core_registry.plant_twin_schedules_v1 IS '设备群孪生只读视图：站点周运行计划头表与预冷/延停宽限窗配置。';
COMMENT ON COLUMN core_registry.plant_twin_schedules_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedules_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedules_v1.schedule_id IS '运行计划主键标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedules_v1.pre_cool_lead_minutes IS '开机前允许提前预冷的宽限分钟数。';
COMMENT ON COLUMN core_registry.plant_twin_schedules_v1.coast_down_lag_minutes IS '停机后允许延后收尾的宽限分钟数。';
COMMENT ON COLUMN core_registry.plant_twin_schedules_v1.revision IS '当前生效的计划修订版本号。';

COMMENT ON VIEW core_registry.plant_twin_schedule_slots_v1 IS '设备群孪生只读视图：按周几、分钟区间与目标供水温度拆分的计划时段明细。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.schedule_id IS '所属运行计划标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.schedule_revision IS '时段对应的运行计划修订版本号。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.day_of_week IS '星期序号（1 为周一，7 为周日）。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.start_minute IS '当天起始分钟偏移（0 到 1439）。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.end_minute IS '当天结束分钟偏移（1 到 1440，大于 start_minute）。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_slots_v1.target_supply_temp_c IS '时段内设定的目标冷冻水供水温度（摄氏度，可为空）。';

COMMENT ON VIEW core_registry.plant_twin_schedule_exceptions_v1 IS '设备群孪生只读视图：已批准生效的假期停机、加班供冷与检修窗口例外安排。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.exception_id IS '运行例外记录主键标识。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.title IS '例外事件名称。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.kind IS '例外类型：HOLIDAY_SHUTDOWN、OVERTIME_COOLING 或 MAINTENANCE_WINDOW。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.start_date IS '例外生效起始日期（含当天，站点本地日期）。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.end_date IS '例外生效结束日期（含当天，站点本地日期）。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.replacement_slots IS '加班供冷时的替代运行分钟时段列表（JSONB 数组）。';
COMMENT ON COLUMN core_registry.plant_twin_schedule_exceptions_v1.reason IS '申请或批准该例外的原因说明。';

COMMIT;
