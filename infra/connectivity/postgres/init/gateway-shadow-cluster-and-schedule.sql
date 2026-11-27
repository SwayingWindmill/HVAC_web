\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.gateway_desired_configs
  ADD COLUMN IF NOT EXISTS cluster_policy_json jsonb,
  ADD COLUMN IF NOT EXISTS energy_schedule_json jsonb;

ALTER TABLE connectivity.gateway_desired_configs
  DROP CONSTRAINT IF EXISTS gateway_desired_configs_cluster_policy_json_check;

ALTER TABLE connectivity.gateway_desired_configs
  ADD CONSTRAINT gateway_desired_configs_cluster_policy_json_check
  CHECK (cluster_policy_json IS NULL OR (jsonb_typeof(cluster_policy_json) = 'object' AND octet_length(cluster_policy_json::text) <= 65536));

ALTER TABLE connectivity.gateway_desired_configs
  DROP CONSTRAINT IF EXISTS gateway_desired_configs_energy_schedule_json_check;

ALTER TABLE connectivity.gateway_desired_configs
  ADD CONSTRAINT gateway_desired_configs_energy_schedule_json_check
  CHECK (energy_schedule_json IS NULL OR (jsonb_typeof(energy_schedule_json) = 'object' AND octet_length(energy_schedule_json::text) <= 262144));

ALTER TABLE connectivity.gateway_reported_configs
  ADD COLUMN IF NOT EXISTS cluster_policy_json jsonb,
  ADD COLUMN IF NOT EXISTS energy_schedule_json jsonb;

ALTER TABLE connectivity.gateway_reported_configs
  DROP CONSTRAINT IF EXISTS gateway_reported_configs_cluster_policy_json_check;

ALTER TABLE connectivity.gateway_reported_configs
  ADD CONSTRAINT gateway_reported_configs_cluster_policy_json_check
  CHECK (cluster_policy_json IS NULL OR (jsonb_typeof(cluster_policy_json) = 'object' AND octet_length(cluster_policy_json::text) <= 65536));

ALTER TABLE connectivity.gateway_reported_configs
  DROP CONSTRAINT IF EXISTS gateway_reported_configs_energy_schedule_json_check;

ALTER TABLE connectivity.gateway_reported_configs
  ADD CONSTRAINT gateway_reported_configs_energy_schedule_json_check
  CHECK (energy_schedule_json IS NULL OR (jsonb_typeof(energy_schedule_json) = 'object' AND octet_length(energy_schedule_json::text) <= 262144));

COMMENT ON COLUMN connectivity.gateway_desired_configs.cluster_policy_json IS '期望下发至边缘网关的多冷机群控与水泵冷却塔协同策略（JSON 对象，未配置时为空）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.energy_schedule_json IS '期望下发至边缘网关的版本化分时用能调度计划（JSON 对象，未配置时为空）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.cluster_policy_json IS '边缘网关当前实际应用并上报的多冷机群控与水泵冷却塔协同策略（JSON 对象，未启用时为空）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.energy_schedule_json IS '边缘网关当前实际应用并上报的版本化分时用能调度计划（JSON 对象，未启用时为空）。';

RESET ROLE;

COMMIT;
