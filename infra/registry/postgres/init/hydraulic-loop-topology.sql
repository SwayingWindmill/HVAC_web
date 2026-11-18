/*
  暖通水力环路与并联支路拓扑孪生扩展：
  1. 在 core_registry.assets 增加 water_loop（水力环路）与 branch_code（并联支路编码）可选列、约束、部分索引与只读权限
  2. 将 core_registry.asset_relationships.relationship_role 约束扩展为 SERVES、ISOLATES 与 FEEDS_HEADER
  3. 扩展 core_registry.point_subject_bindings.semantic_role 约束，支持阀门启闭反馈/指令、变频频率反馈/指令、总管压差与支路流量
  4. 更新 core_registry.plant_twin_units_v1（末尾追加 water_loop 与 branch_code）并新增 core_registry.plant_twin_asset_relationships_v1 视图
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.assets
  ADD COLUMN IF NOT EXISTS water_loop text NULL,
  ADD COLUMN IF NOT EXISTS branch_code text NULL;

ALTER TABLE core_registry.assets
  DROP CONSTRAINT IF EXISTS assets_water_loop_check;
ALTER TABLE core_registry.assets
  ADD CONSTRAINT assets_water_loop_check
  CHECK (water_loop IS NULL OR water_loop IN ('CHILLED_WATER', 'CONDENSER_WATER', 'COOLING_TOWER_LOOP'));

ALTER TABLE core_registry.assets
  DROP CONSTRAINT IF EXISTS assets_branch_code_check;
ALTER TABLE core_registry.assets
  ADD CONSTRAINT assets_branch_code_check
  CHECK (branch_code IS NULL OR (btrim(branch_code) <> '' AND char_length(branch_code) <= 64 AND branch_code = btrim(branch_code)));

CREATE INDEX IF NOT EXISTS assets_site_hydraulic_branch_idx
  ON core_registry.assets (tenant_id, site_id, water_loop, branch_code)
  WHERE status = 'ACTIVE' AND (water_loop IS NOT NULL OR branch_code IS NOT NULL);

GRANT SELECT (water_loop, branch_code)
  ON core_registry.assets TO core_registry_read_port, optimization_runtime, fdd_runtime, connectivity_runtime;

COMMENT ON COLUMN core_registry.assets.water_loop IS '资产所属水力环路标识：CHILLED_WATER（冷冻水环路）、CONDENSER_WATER（冷却水环路）或 COOLING_TOWER_LOOP（冷却塔外循环）；旧站点未配置时为 NULL。';
COMMENT ON COLUMN core_registry.assets.branch_code IS '资产所属并联支路编码，用于标识同一水力环路内成对串联运行的冷机、水泵、阀门及流量测点；未配置时为 NULL。';

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
      AND r.relname = 'asset_relationships'
      AND c.contype = 'c'
      AND pg_get_constraintdef(c.oid) LIKE '%relationship_role%'
  LOOP
    EXECUTE format('ALTER TABLE core_registry.asset_relationships DROP CONSTRAINT %I', v_constraint);
  END LOOP;
END
$$;

ALTER TABLE core_registry.asset_relationships
  ADD CONSTRAINT asset_relationships_relationship_role_check
  CHECK (relationship_role IN ('SERVES', 'ISOLATES', 'FEEDS_HEADER'));

COMMENT ON COLUMN core_registry.asset_relationships.relationship_role IS '资产水力拓扑关系角色：SERVES（服务下游资产）、ISOLATES（阀门隔离对应支路或设备）或 FEEDS_HEADER（支路或机组汇入水系统总管）。';

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
      AND r.relname = 'point_subject_bindings'
      AND c.contype = 'c'
      AND (c.conname = 'point_subject_bindings_semantic_role_check' OR pg_get_constraintdef(c.oid) LIKE '%semantic_role%')
  LOOP
    EXECUTE format('ALTER TABLE core_registry.point_subject_bindings DROP CONSTRAINT %I', v_constraint);
  END LOOP;
END
$$;

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
    'ZONE_TEMP',
    'VALVE_FEEDBACK',
    'VALVE_COMMAND',
    'VFD_FREQUENCY',
    'VFD_FREQUENCY_COMMAND',
    'HEADER_DIFF_PRESSURE',
    'BRANCH_FLOW'
  ));

COMMENT ON COLUMN core_registry.point_subject_bindings.semantic_role IS '点位在冷站水力与能耗物理模型中的语义角色，涵盖电功率、启停、冷/冷却水温度、流量、区域温度、阀门反馈与指令、变频频率反馈与指令、总管压差及支路流量。';

RESET ROLE;

CREATE OR REPLACE VIEW core_registry.plant_twin_units_v1 WITH (security_barrier) AS
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
       END AS feeder_auxiliary_power_kw,
       a.water_loop,
       a.branch_code
FROM core_registry.assets AS a
WHERE a.status = 'ACTIVE'
  AND a.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(a.site_id);

COMMENT ON COLUMN core_registry.plant_twin_units_v1.water_loop IS '资产所属水力环路标识（CHILLED_WATER、CONDENSER_WATER 或 COOLING_TOWER_LOOP），未配置时为 NULL。';
COMMENT ON COLUMN core_registry.plant_twin_units_v1.branch_code IS '资产所属并联支路编码，未配置时为 NULL。';

CREATE OR REPLACE VIEW core_registry.plant_twin_asset_relationships_v1 WITH (security_barrier) AS
SELECT ar.tenant_id,
       ar.site_id,
       ar.id AS relationship_id,
       ar.from_asset_id,
       from_asset.code AS from_asset_code,
       from_asset.display_name AS from_asset_name,
       from_asset.asset_type AS from_asset_type,
       from_asset.water_loop AS from_water_loop,
       from_asset.branch_code AS from_branch_code,
       ar.to_asset_id,
       to_asset.code AS to_asset_code,
       to_asset.display_name AS to_asset_name,
       to_asset.asset_type AS to_asset_type,
       to_asset.water_loop AS to_water_loop,
       to_asset.branch_code AS to_branch_code,
       ar.relationship_role,
       ar.valid_from,
       ar.valid_to
FROM core_registry.asset_relationships AS ar
JOIN core_registry.assets AS from_asset
  ON from_asset.tenant_id = ar.tenant_id
 AND from_asset.site_id = ar.site_id
 AND from_asset.id = ar.from_asset_id
JOIN core_registry.assets AS to_asset
  ON to_asset.tenant_id = ar.tenant_id
 AND to_asset.site_id = ar.site_id
 AND to_asset.id = ar.to_asset_id
WHERE ar.status = 'ACTIVE'
  AND from_asset.status = 'ACTIVE'
  AND to_asset.status = 'ACTIVE'
  AND ar.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(ar.site_id);

ALTER VIEW core_registry.plant_twin_asset_relationships_v1 OWNER TO core_registry_read_port;

GRANT SELECT ON core_registry.plant_twin_asset_relationships_v1
  TO s1_core_runtime, optimization_runtime, fdd_runtime, connectivity_runtime, core_registry_read_port;

COMMENT ON VIEW core_registry.plant_twin_asset_relationships_v1 IS '设备群数字孪生只读视图：站点在役资产间的显式水力拓扑关系及两端资产的水力环路与并联支路归属。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.tenant_id IS '所属租户 UUID。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.site_id IS '所属站点 UUID。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.relationship_id IS '资产关系主键 UUID。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.from_asset_id IS '关系起点资产 UUID。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.from_asset_code IS '关系起点资产编码。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.from_asset_name IS '关系起点资产显示名称。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.from_asset_type IS '关系起点资产类型。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.from_water_loop IS '关系起点资产所属水力环路，未配置时为 NULL。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.from_branch_code IS '关系起点资产所属并联支路编码，未配置时为 NULL。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.to_asset_id IS '关系终点资产 UUID。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.to_asset_code IS '关系终点资产编码。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.to_asset_name IS '关系终点资产显示名称。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.to_asset_type IS '关系终点资产类型。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.to_water_loop IS '关系终点资产所属水力环路，未配置时为 NULL。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.to_branch_code IS '关系终点资产所属并联支路编码，未配置时为 NULL。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.relationship_role IS '水力拓扑关系角色：SERVES、ISOLATES 或 FEEDS_HEADER。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.valid_from IS '关系生效时间戳。';
COMMENT ON COLUMN core_registry.plant_twin_asset_relationships_v1.valid_to IS '关系失效时间戳，NULL 表示当前有效。';

COMMIT;
