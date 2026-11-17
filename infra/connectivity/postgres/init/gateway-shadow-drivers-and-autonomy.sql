\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.uplink_quarantine
  DROP CONSTRAINT IF EXISTS uplink_quarantine_reason_code_check;

ALTER TABLE connectivity.uplink_quarantine
  ADD CONSTRAINT uplink_quarantine_reason_code_check
  CHECK (reason_code IN ('GATEWAY_UNKNOWN', 'GATEWAY_CREDENTIAL_INACTIVE', 'MESSAGE_INVALID', 'RATE_LIMITED'));

ALTER TABLE connectivity.gateway_desired_configs
  ADD COLUMN IF NOT EXISTS driver_configs jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE connectivity.gateway_desired_configs
  DROP CONSTRAINT IF EXISTS gateway_desired_configs_driver_configs_check;

ALTER TABLE connectivity.gateway_desired_configs
  ADD CONSTRAINT gateway_desired_configs_driver_configs_check
  CHECK (jsonb_typeof(driver_configs) = 'array' AND octet_length(driver_configs::text) <= 262144);

ALTER TABLE connectivity.gateway_desired_configs
  ADD COLUMN IF NOT EXISTS autonomy_policy jsonb;

ALTER TABLE connectivity.gateway_desired_configs
  DROP CONSTRAINT IF EXISTS gateway_desired_configs_autonomy_policy_check;

ALTER TABLE connectivity.gateway_desired_configs
  ADD CONSTRAINT gateway_desired_configs_autonomy_policy_check
  CHECK (autonomy_policy IS NULL OR (jsonb_typeof(autonomy_policy) = 'object' AND octet_length(autonomy_policy::text) <= 65536));

ALTER TABLE connectivity.gateway_reported_configs
  ADD COLUMN IF NOT EXISTS driver_configs jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE connectivity.gateway_reported_configs
  DROP CONSTRAINT IF EXISTS gateway_reported_configs_driver_configs_check;

ALTER TABLE connectivity.gateway_reported_configs
  ADD CONSTRAINT gateway_reported_configs_driver_configs_check
  CHECK (jsonb_typeof(driver_configs) = 'array' AND octet_length(driver_configs::text) <= 262144);

ALTER TABLE connectivity.gateway_reported_configs
  ADD COLUMN IF NOT EXISTS autonomy_policy jsonb;

ALTER TABLE connectivity.gateway_reported_configs
  DROP CONSTRAINT IF EXISTS gateway_reported_configs_autonomy_policy_check;

ALTER TABLE connectivity.gateway_reported_configs
  ADD CONSTRAINT gateway_reported_configs_autonomy_policy_check
  CHECK (autonomy_policy IS NULL OR (jsonb_typeof(autonomy_policy) = 'object' AND octet_length(autonomy_policy::text) <= 65536));

COMMENT ON COLUMN connectivity.gateway_desired_configs.driver_configs IS '期望生效的网关声明式协议驱动与寄存器映射配置数组（JSON）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.autonomy_policy IS '期望生效的网关断网自治与本地保护策略配置（JSON 对象，未配置时为空）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.driver_configs IS '边缘网关实际应用的协议驱动与寄存器映射配置数组（JSON）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.autonomy_policy IS '边缘网关实际应用的断网自治与本地保护策略配置（JSON 对象，未启用时为空）。';

RESET ROLE;

COMMIT;
