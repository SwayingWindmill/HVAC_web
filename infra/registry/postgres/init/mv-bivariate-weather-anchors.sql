\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.savings_measures
  ADD COLUMN IF NOT EXISTS baseline_model_kind TEXT NOT NULL DEFAULT 'UNIVARIATE_COOLING'
    CHECK (baseline_model_kind IN ('UNIVARIATE_COOLING', 'BIVARIATE_COOLING_WET_BULB')),
  ADD COLUMN IF NOT EXISTS baseline_wet_bulb_slope DOUBLE PRECISION,
  ADD COLUMN IF NOT EXISTS baseline_r_squared DOUBLE PRECISION,
  ADD COLUMN IF NOT EXISTS baseline_mean_wet_bulb_c DOUBLE PRECISION;

COMMENT ON COLUMN core_registry.savings_measures.baseline_model_kind IS '节能对比基线回归模型类型：UNIVARIATE_COOLING（仅冷量单变量回归）或 BIVARIATE_COOLING_WET_BULB（冷量与室外湿球温度双变量回归）。';
COMMENT ON COLUMN core_registry.savings_measures.baseline_wet_bulb_slope IS '双变量基线模型中室外湿球温度的回归斜率（kWh/°C）；单变量模型或未拟合时为 NULL。';
COMMENT ON COLUMN core_registry.savings_measures.baseline_r_squared IS '基线回归模型的决定系数 R²；未拟合或总平方和为零时为 NULL。';
COMMENT ON COLUMN core_registry.savings_measures.baseline_mean_wet_bulb_c IS '双变量基线期样本的平均室外湿球温度（°C），用于在报告期小时缺测湿球温度时保持能量平衡回退。';

ALTER TABLE core_registry.savings_daily_results
  ADD COLUMN IF NOT EXISTS mean_wet_bulb_c DOUBLE PRECISION,
  ADD COLUMN IF NOT EXISTS mean_dry_bulb_c DOUBLE PRECISION,
  ADD COLUMN IF NOT EXISTS mean_load_ratio DOUBLE PRECISION,
  ADD COLUMN IF NOT EXISTS ai_closed_loop_share DOUBLE PRECISION;

COMMENT ON COLUMN core_registry.savings_daily_results.mean_wet_bulb_c IS '该核证自然日内具备工况锚点小时的室外湿球温度算术均值（°C）；无数据时为 NULL。';
COMMENT ON COLUMN core_registry.savings_daily_results.mean_dry_bulb_c IS '该核证自然日内具备工况锚点小时的室外干球温度算术均值（°C）；无数据时为 NULL。';
COMMENT ON COLUMN core_registry.savings_daily_results.mean_load_ratio IS '该核证自然日内具备工况锚点小时的冷站平均负载率；无数据时为 NULL。';
COMMENT ON COLUMN core_registry.savings_daily_results.ai_closed_loop_share IS '该核证自然日内具备工况锚点小时的 AI 闭环控制时长占比；无数据时为 NULL。';

RESET ROLE;

COMMIT;
