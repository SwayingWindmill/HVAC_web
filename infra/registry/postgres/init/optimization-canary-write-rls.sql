/*
  站点级灰度路由写入与负样本查询支撑：
  1. 为 core_registry.optimization_canary_bindings 补充 optimization_runtime 的 INSERT / UPDATE / DELETE RLS 策略与表权限
  2. 为 core_registry.optimization_negative_samples 补充按负样本类别过滤的联合索引，支撑分页检索
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

DROP POLICY IF EXISTS optimization_canary_bindings_optimization_insert_scope ON core_registry.optimization_canary_bindings;
CREATE POLICY optimization_canary_bindings_optimization_insert_scope ON core_registry.optimization_canary_bindings
  FOR INSERT TO optimization_runtime
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_canary_bindings_optimization_update_scope ON core_registry.optimization_canary_bindings;
CREATE POLICY optimization_canary_bindings_optimization_update_scope ON core_registry.optimization_canary_bindings
  FOR UPDATE TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS optimization_canary_bindings_optimization_delete_scope ON core_registry.optimization_canary_bindings;
CREATE POLICY optimization_canary_bindings_optimization_delete_scope ON core_registry.optimization_canary_bindings
  FOR DELETE TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

GRANT INSERT, UPDATE, DELETE ON core_registry.optimization_canary_bindings TO optimization_runtime;

CREATE INDEX IF NOT EXISTS optimization_negative_samples_site_kind_occurred_idx
  ON core_registry.optimization_negative_samples (tenant_id, site_id, sample_kind, occurred_at DESC, id DESC);

RESET ROLE;

COMMIT;
