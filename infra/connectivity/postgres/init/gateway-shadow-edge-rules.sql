\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.gateway_desired_configs
  ADD COLUMN IF NOT EXISTS edge_rule_bundle jsonb;

ALTER TABLE connectivity.gateway_desired_configs
  DROP CONSTRAINT IF EXISTS gateway_desired_configs_edge_rule_bundle_check;

ALTER TABLE connectivity.gateway_desired_configs
  ADD CONSTRAINT gateway_desired_configs_edge_rule_bundle_check
  CHECK (edge_rule_bundle IS NULL OR (jsonb_typeof(edge_rule_bundle) = 'object' AND octet_length(edge_rule_bundle::text) <= 262144));

ALTER TABLE connectivity.gateway_reported_configs
  ADD COLUMN IF NOT EXISTS edge_rule_bundle jsonb;

ALTER TABLE connectivity.gateway_reported_configs
  DROP CONSTRAINT IF EXISTS gateway_reported_configs_edge_rule_bundle_check;

ALTER TABLE connectivity.gateway_reported_configs
  ADD CONSTRAINT gateway_reported_configs_edge_rule_bundle_check
  CHECK (edge_rule_bundle IS NULL OR (jsonb_typeof(edge_rule_bundle) = 'object' AND octet_length(edge_rule_bundle::text) <= 262144));

COMMENT ON COLUMN connectivity.gateway_desired_configs.edge_rule_bundle IS '期望下发至边缘网关本地执行引擎的编译规则包（JSON 对象，未配置时为空）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.edge_rule_bundle IS '边缘网关实际加载并上报的本地执行规则包（JSON 对象，未启用时为空）。';

RESET ROLE;

COMMIT;
