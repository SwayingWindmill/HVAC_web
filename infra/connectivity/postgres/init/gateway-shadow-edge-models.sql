\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.gateway_desired_configs
  ADD COLUMN IF NOT EXISTS edge_models jsonb;

ALTER TABLE connectivity.gateway_desired_configs
  DROP CONSTRAINT IF EXISTS gateway_desired_configs_edge_models_check;

ALTER TABLE connectivity.gateway_desired_configs
  ADD CONSTRAINT gateway_desired_configs_edge_models_check
  CHECK (edge_models IS NULL OR (jsonb_typeof(edge_models) = 'array' AND octet_length(edge_models::text) <= 262144));

ALTER TABLE connectivity.gateway_reported_configs
  ADD COLUMN IF NOT EXISTS edge_models jsonb,
  ADD COLUMN IF NOT EXISTS edge_model_statuses jsonb;

ALTER TABLE connectivity.gateway_reported_configs
  DROP CONSTRAINT IF EXISTS gateway_reported_configs_edge_models_check;

ALTER TABLE connectivity.gateway_reported_configs
  ADD CONSTRAINT gateway_reported_configs_edge_models_check
  CHECK (edge_models IS NULL OR (jsonb_typeof(edge_models) = 'array' AND octet_length(edge_models::text) <= 262144));

ALTER TABLE connectivity.gateway_reported_configs
  DROP CONSTRAINT IF EXISTS gateway_reported_configs_edge_model_statuses_check;

ALTER TABLE connectivity.gateway_reported_configs
  ADD CONSTRAINT gateway_reported_configs_edge_model_statuses_check
  CHECK (edge_model_statuses IS NULL OR (jsonb_typeof(edge_model_statuses) = 'array' AND octet_length(edge_model_statuses::text) <= 65536));

COMMENT ON COLUMN connectivity.gateway_desired_configs.edge_models IS '期望下发至边缘网关热更新与影子评估的模型及超参包列表（JSON 数组，未配置时为空）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.edge_models IS '边缘网关当前实际生效或影子运行的模型与超参包列表（JSON 数组，未启用时为空）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.edge_model_statuses IS '边缘网关上报的模型影子偏差评估与自动回退运行摘要列表（JSON 数组）。';

RESET ROLE;

COMMIT;
