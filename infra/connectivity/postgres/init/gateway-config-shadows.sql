\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.gateway_concentrator_audit
  DROP CONSTRAINT IF EXISTS gateway_concentrator_audit_outcome_check;

ALTER TABLE connectivity.gateway_concentrator_audit
  ADD CONSTRAINT gateway_concentrator_audit_outcome_check
  CHECK (outcome IN ('APPLIED', 'REJECTED', 'FAILED', 'TIMEOUT', 'STAGED_SHADOW'));

CREATE TABLE IF NOT EXISTS connectivity.gateway_desired_configs (
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)) REFERENCES iam.tenants(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  desired_revision bigint NOT NULL DEFAULT 1 CHECK (desired_revision > 0),
  point_mappings jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(point_mappings) = 'array' AND octet_length(point_mappings::text) <= 262144),
  sampling_policies jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(sampling_policies) = 'array' AND octet_length(sampling_policies::text) <= 131072),
  concentrator_configs jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(concentrator_configs) = 'object' AND octet_length(concentrator_configs::text) <= 262144),
  updated_by uuid NOT NULL CHECK (connectivity.is_uuid_v7(updated_by)),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, gateway_id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS connectivity.gateway_reported_configs (
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)) REFERENCES iam.tenants(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  reported_revision bigint NOT NULL CHECK (reported_revision >= 0),
  applied_status text NOT NULL CHECK (applied_status IN ('APPLIED', 'PARTIAL', 'REJECTED')),
  point_mappings jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(point_mappings) = 'array' AND octet_length(point_mappings::text) <= 262144),
  sampling_policies jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(sampling_policies) = 'array' AND octet_length(sampling_policies::text) <= 131072),
  concentrator_configs jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(concentrator_configs) = 'object' AND octet_length(concentrator_configs::text) <= 262144),
  drift_details jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(drift_details) = 'array' AND octet_length(drift_details::text) <= 65536),
  error_message text CHECK (error_message IS NULL OR length(error_message) <= 1024),
  reported_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, gateway_id)
);

ALTER TABLE connectivity.gateway_desired_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_desired_configs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS gateway_desired_configs_tenant_scope ON connectivity.gateway_desired_configs;
CREATE POLICY gateway_desired_configs_tenant_scope ON connectivity.gateway_desired_configs
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE connectivity.gateway_reported_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_reported_configs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS gateway_reported_configs_tenant_scope ON connectivity.gateway_reported_configs;
CREATE POLICY gateway_reported_configs_tenant_scope ON connectivity.gateway_reported_configs
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON connectivity.gateway_desired_configs FROM PUBLIC;
REVOKE ALL ON connectivity.gateway_reported_configs FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON connectivity.gateway_desired_configs TO connectivity_runtime;
GRANT SELECT, INSERT, UPDATE ON connectivity.gateway_reported_configs TO connectivity_runtime;

COMMENT ON TABLE connectivity.gateway_desired_configs IS '站点网关期望配置影子状态，存放点位映射复制规则、采样发布策略与无线集中器参数/EID 期望快照，由设备接入服务维护。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.gateway_id IS '目标站点网关设备标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.desired_revision IS '单调递增的期望配置修订号，从 1 开始。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.point_mappings IS '源通道到主点位及复制目标点位的映射配置数组（JSON）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.sampling_policies IS '按设备或点位覆盖的采样与发布周期策略数组（JSON）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.concentrator_configs IS '按无线集中器从站地址索引的参数与 EID 槽位期望快照（JSON 对象）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.updated_by IS '最近一次修改期望配置的操作人或服务主体标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.reason IS '变更原因说明。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.created_at IS '期望配置影子首次创建时间（UTC）。';
COMMENT ON COLUMN connectivity.gateway_desired_configs.updated_at IS '期望配置影子最近更新时间（UTC）。';

COMMENT ON TABLE connectivity.gateway_reported_configs IS '站点网关边缘回传的已应用配置影子状态及漂移明细，由设备接入服务维护。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.gateway_id IS '目标站点网关设备标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.reported_revision IS '边缘最近一次上报对应的配置修订号，未接收云端配置时可为 0。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.applied_status IS '边缘应用状态：APPLIED（已完全应用）、PARTIAL（部分应用）或 REJECTED（已拒绝）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.point_mappings IS '边缘当前实际生效的点位映射配置数组（JSON）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.sampling_policies IS '边缘当前实际生效的采样与发布周期策略数组（JSON）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.concentrator_configs IS '边缘当前实际生效的无线集中器参数与 EID 槽位快照（JSON 对象）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.drift_details IS '云端期望配置与边缘上报状态之间的逐项漂移明细（JSON 数组）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.error_message IS '部分成功或应用被拒时的边缘错误信息。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.reported_at IS '边缘生成本次回传报告的时间（UTC）。';
COMMENT ON COLUMN connectivity.gateway_reported_configs.updated_at IS '服务端落库或对账更新本次上报记录的时间（UTC）。';

RESET ROLE;

COMMIT;
