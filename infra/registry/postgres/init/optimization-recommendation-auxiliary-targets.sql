/*
  优化建议多回路协同目标持久化扩展：
  1. 在 core_registry.optimization_recommendations 新增 auxiliary_targets jsonb 列，保存水泵变频、冷却塔风机转速等辅助回路推荐目标数组
  2. 约束 auxiliary_targets 必须为合法 JSON 数组（默认空数组 []）
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.optimization_recommendations
  ADD COLUMN IF NOT EXISTS auxiliary_targets jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE core_registry.optimization_recommendations
  DROP CONSTRAINT IF EXISTS optimization_recommendations_auxiliary_targets_array_chk;
ALTER TABLE core_registry.optimization_recommendations
  ADD CONSTRAINT optimization_recommendations_auxiliary_targets_array_chk
  CHECK (jsonb_typeof(auxiliary_targets) = 'array');

COMMENT ON COLUMN core_registry.optimization_recommendations.auxiliary_targets IS '协同辅助回路（冷冻水泵频率、冷却水泵频率、冷却塔风机转速等）建议目标数组（JSON 数组）。';

RESET ROLE;

COMMIT;
