/*
  优化治理闭环：模型灰度路由、控制周期采样台账与负样本飞轮（P2）：
  1. 在 core_registry.ai_model_definitions 新增 serving_ref 列，定义模型服务路由引用
  2. 新增 core_registry.optimization_canary_bindings 表，记录站点级控制回路的灰度求解路由绑定
  3. 新增 core_registry.optimization_control_ticks 表，记录冷站闭环/影子/基准/人工控制周期的工况与 COP 实测快照
  4. 新增 core_registry.optimization_negative_samples 表，记录护栏裁剪、拦截、人工接管、监控熔断与设定值偏离负样本
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.ai_model_definitions
  ADD COLUMN IF NOT EXISTS serving_ref text NOT NULL DEFAULT 'builtin://hvac-recommendation-v1';

ALTER TABLE core_registry.ai_model_definitions
  DROP CONSTRAINT IF EXISTS ai_model_definitions_serving_ref_check;
ALTER TABLE core_registry.ai_model_definitions
  ADD CONSTRAINT ai_model_definitions_serving_ref_check
  CHECK (length(btrim(serving_ref)) BETWEEN 1 AND 512);

COMMENT ON COLUMN core_registry.ai_model_definitions.serving_ref IS '模型推理或求解服务的路由标识（默认 builtin://hvac-recommendation-v1），用于部署版本关联与站点灰度选择。';

CREATE TABLE IF NOT EXISTS core_registry.optimization_canary_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  loop_key text NOT NULL DEFAULT 'PLANT' CHECK (length(btrim(loop_key)) BETWEEN 1 AND 64),
  serving_ref text NOT NULL CHECK (length(btrim(serving_ref)) BETWEEN 1 AND 512),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'DISABLED')),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, loop_key),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

COMMENT ON TABLE core_registry.optimization_canary_bindings IS '站点与控制回路维度的优化求解模型灰度路由绑定表，决定当前启用的模型服务引用及状态。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.id IS '灰度绑定记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.loop_key IS '控制回路标识键，默认 PLANT 代表全冷站主回路。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.serving_ref IS '为该站点控制回路指定的求解服务路由引用。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.status IS '灰度绑定状态：ACTIVE（启用中）或 DISABLED（已停用）。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.created_at IS '灰度绑定创建时间（UTC）。';
COMMENT ON COLUMN core_registry.optimization_canary_bindings.updated_at IS '灰度绑定最近更新时间（UTC）。';

CREATE TABLE IF NOT EXISTS core_registry.optimization_control_ticks (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  optimization_run_id uuid NULL,
  recommendation_id uuid NULL,
  decision_id uuid NULL,
  control_mode text NOT NULL CHECK (control_mode IN ('AI_CLOSED_LOOP', 'AI_SHADOW', 'RULE_BASELINE', 'MANUAL_OVERRIDE')),
  loop_key text NOT NULL DEFAULT 'PLANT' CHECK (length(btrim(loop_key)) BETWEEN 1 AND 64),
  solver_serving_ref text NOT NULL DEFAULT 'builtin://hvac-recommendation-v1' CHECK (length(btrim(solver_serving_ref)) BETWEEN 1 AND 512),
  deployment_revision_id uuid NULL,
  outdoor_wet_bulb_c double precision NULL,
  cooling_load_kw double precision NULL,
  chiller_power_kw double precision NULL,
  observed_cop double precision NULL,
  supply_temp_c double precision NULL,
  recommended_supply_temp_c double precision NULL,
  applied_supply_temp_c double precision NULL,
  recorded_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE INDEX IF NOT EXISTS optimization_control_ticks_site_recorded_idx
  ON core_registry.optimization_control_ticks (tenant_id, site_id, recorded_at DESC);

CREATE INDEX IF NOT EXISTS optimization_control_ticks_site_mode_recorded_idx
  ON core_registry.optimization_control_ticks (tenant_id, site_id, control_mode, recorded_at DESC);

COMMENT ON TABLE core_registry.optimization_control_ticks IS '冷站优化控制周期采样台账，记录各控制模式下的气象、冷负荷、冷机功率、实测 COP 与设定值执行对比数据。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.id IS '控制周期采样主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.optimization_run_id IS '关联的优化求解运行批次标识（可为空）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.recommendation_id IS '关联的优化建议记录标识（可为空）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.decision_id IS '关联的优化决策周期标识（可为空）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.control_mode IS '本周期实际控制模式：AI_CLOSED_LOOP（AI闭环）、AI_SHADOW（AI影子）、RULE_BASELINE（规则基线）或 MANUAL_OVERRIDE（人工接管）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.loop_key IS '控制回路标识键，默认 PLANT 代表冷站主回路。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.solver_serving_ref IS '本周期执行推荐的求解服务路由引用。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.deployment_revision_id IS '本周期求解所使用的 AI 部署版本标识（可为空）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.outdoor_wet_bulb_c IS '采样时刻室外湿球温度（摄氏度，用于双变量工况分箱）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.cooling_load_kw IS '采样时刻冷站制冷负荷（千瓦，用于双变量工况分箱）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.chiller_power_kw IS '采样时刻冷机实测总电功率（千瓦）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.observed_cop IS '采样时刻实测冷机系统能效比 COP。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.supply_temp_c IS '采样时刻冷冻水实测供水温度（摄氏度）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.recommended_supply_temp_c IS '优化引擎推荐的冷冻水供水温度设定值（摄氏度）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.applied_supply_temp_c IS '经安全护栏裁剪或现场实际下发执行的供水温度设定值（摄氏度）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.recorded_at IS '采样观测业务时间戳（UTC）。';
COMMENT ON COLUMN core_registry.optimization_control_ticks.created_at IS '采样记录落库创建时间（UTC）。';

CREATE TABLE IF NOT EXISTS core_registry.optimization_negative_samples (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  recommendation_id uuid NULL,
  monitor_id uuid NULL,
  decision_id uuid NULL,
  trace_id text NOT NULL DEFAULT '' CHECK (trace_id = '' OR trace_id ~ '^[0-9a-f]{32}$'),
  sample_kind text NOT NULL CHECK (sample_kind IN ('GUARDRAIL_CLAMPED', 'GUARDRAIL_VETOED', 'MANUAL_OVERRIDE', 'MONITOR_TRIPPED', 'TRACKING_DEVIATION')),
  reason_code text NOT NULL DEFAULT '',
  solver_serving_ref text NOT NULL DEFAULT '',
  raw_recommended_value double precision NULL,
  applied_or_clamped_value double precision NULL,
  feature_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(feature_snapshot) = 'object'),
  guardrail_trace jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(guardrail_trace) = 'array'),
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE INDEX IF NOT EXISTS optimization_negative_samples_site_occurred_idx
  ON core_registry.optimization_negative_samples (tenant_id, site_id, occurred_at DESC);

COMMENT ON TABLE core_registry.optimization_negative_samples IS '优化控制负样本数据飞轮台账，沉淀护栏裁剪/拦截、人工接管、监控熔断与设定值偏离案例以供离线复盘与模型迭代。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.id IS '负样本记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.recommendation_id IS '关联的优化建议标识（可为空）。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.monitor_id IS '关联的执行监控记录标识（可为空）。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.decision_id IS '关联的优化决策周期标识（可为空）。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.trace_id IS 'W3C 32 位十六进制分布式追踪编号（无上下文时为空字符串）。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.sample_kind IS '负样本类别：GUARDRAIL_CLAMPED（护栏钳位）、GUARDRAIL_VETOED（护栏拦截）、MANUAL_OVERRIDE（人工接管）、MONITOR_TRIPPED（监控熔断）或 TRACKING_DEVIATION（设定值跟踪偏离）。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.reason_code IS '触发该负样本沉淀的规范化原因编码。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.solver_serving_ref IS '产出原始推荐值的求解服务路由引用。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.raw_recommended_value IS '护栏或人工介入前优化引擎输出的原始推荐设定值。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.applied_or_clamped_value IS '经护栏钳位或现场实际生效的设定值。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.feature_snapshot IS '负样本发生时刻的工况特征快照 JSON 对象。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.guardrail_trace IS '安全护栏逐项规则评估与裁剪轨迹 JSON 数组。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.occurred_at IS '负样本事件发生业务时间（UTC）。';
COMMENT ON COLUMN core_registry.optimization_negative_samples.created_at IS '负样本记录落库创建时间（UTC）。';

ALTER TABLE core_registry.optimization_canary_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_canary_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_control_ticks ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_control_ticks FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_negative_samples ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_negative_samples FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS optimization_canary_bindings_migrator_all ON core_registry.optimization_canary_bindings;
CREATE POLICY optimization_canary_bindings_migrator_all ON core_registry.optimization_canary_bindings
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS optimization_control_ticks_migrator_all ON core_registry.optimization_control_ticks;
CREATE POLICY optimization_control_ticks_migrator_all ON core_registry.optimization_control_ticks
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS optimization_negative_samples_migrator_all ON core_registry.optimization_negative_samples;
CREATE POLICY optimization_negative_samples_migrator_all ON core_registry.optimization_negative_samples
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS optimization_canary_bindings_runtime_scope ON core_registry.optimization_canary_bindings;
CREATE POLICY optimization_canary_bindings_runtime_scope ON core_registry.optimization_canary_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_control_ticks_runtime_scope ON core_registry.optimization_control_ticks;
CREATE POLICY optimization_control_ticks_runtime_scope ON core_registry.optimization_control_ticks
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_negative_samples_runtime_scope ON core_registry.optimization_negative_samples;
CREATE POLICY optimization_negative_samples_runtime_scope ON core_registry.optimization_negative_samples
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_canary_bindings_optimization_select_scope ON core_registry.optimization_canary_bindings;
CREATE POLICY optimization_canary_bindings_optimization_select_scope ON core_registry.optimization_canary_bindings
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_control_ticks_optimization_select_scope ON core_registry.optimization_control_ticks;
CREATE POLICY optimization_control_ticks_optimization_select_scope ON core_registry.optimization_control_ticks
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_control_ticks_optimization_insert_scope ON core_registry.optimization_control_ticks;
CREATE POLICY optimization_control_ticks_optimization_insert_scope ON core_registry.optimization_control_ticks
  FOR INSERT TO optimization_runtime
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_negative_samples_optimization_select_scope ON core_registry.optimization_negative_samples;
CREATE POLICY optimization_negative_samples_optimization_select_scope ON core_registry.optimization_negative_samples
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_negative_samples_optimization_insert_scope ON core_registry.optimization_negative_samples;
CREATE POLICY optimization_negative_samples_optimization_insert_scope ON core_registry.optimization_negative_samples
  FOR INSERT TO optimization_runtime
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_control_ticks_mv_select_scope ON core_registry.optimization_control_ticks;
CREATE POLICY optimization_control_ticks_mv_select_scope ON core_registry.optimization_control_ticks
  FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.optimization_canary_bindings,
  core_registry.optimization_control_ticks,
  core_registry.optimization_negative_samples FROM PUBLIC;

GRANT SELECT ON core_registry.optimization_canary_bindings,
  core_registry.optimization_control_ticks,
  core_registry.optimization_negative_samples TO s1_core_runtime;

GRANT SELECT ON core_registry.optimization_canary_bindings TO optimization_runtime;
GRANT SELECT, INSERT ON core_registry.optimization_control_ticks,
  core_registry.optimization_negative_samples TO optimization_runtime;

GRANT SELECT ON core_registry.optimization_control_ticks TO mv_runtime;

RESET ROLE;

COMMIT;
