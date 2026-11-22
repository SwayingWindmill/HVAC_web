\set ON_ERROR_STOP on

BEGIN;

GRANT USAGE ON SCHEMA iam, core_registry TO connectivity_migrator;
GRANT REFERENCES ON iam.tenants, core_registry.sites, core_registry.devices TO connectivity_migrator;

SET LOCAL ROLE connectivity_migrator;

CREATE TABLE IF NOT EXISTS connectivity.provisioning_batches (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)) REFERENCES iam.tenants(id),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)) REFERENCES core_registry.sites(id),
  batch_name text NOT NULL CHECK (length(btrim(batch_name)) BETWEEN 1 AND 128),
  product_key text NOT NULL UNIQUE CHECK (product_key ~ '^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$'),
  product_secret_sha256 text NOT NULL CHECK (product_secret_sha256 ~ '^[0-9a-f]{64}$'),
  device_type text NOT NULL CHECK (length(btrim(device_type)) BETWEEN 1 AND 64),
  connectivity_mode text NOT NULL CHECK (connectivity_mode IN ('GATEWAY_PROXIED', 'DIRECT')),
  max_devices integer NOT NULL CHECK (max_devices BETWEEN 1 AND 100000),
  activated_count integer NOT NULL DEFAULT 0 CHECK (activated_count >= 0 AND activated_count <= max_devices),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'PAUSED', 'REVOKED')),
  expires_at timestamptz NOT NULL,
  created_by uuid NOT NULL CHECK (connectivity.is_uuid_v7(created_by)),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (device_type = 'GATEWAY' OR connectivity_mode = 'DIRECT'),
  CHECK (expires_at > created_at),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS provisioning_batches_tenant_created_idx
  ON connectivity.provisioning_batches (tenant_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS connectivity.provisioning_batch_devices (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  batch_id uuid NOT NULL REFERENCES connectivity.provisioning_batches(id),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)) REFERENCES iam.tenants(id),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)) REFERENCES core_registry.sites(id),
  device_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(device_id)),
  hardware_serial text NOT NULL CHECK (hardware_serial ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{1,127}$'),
  status text NOT NULL CHECK (status IN ('PENDING', 'ACTIVATED', 'REVOKED')),
  activated_at timestamptz,
  credential_id uuid CHECK (credential_id IS NULL OR connectivity.is_uuid_v7(credential_id)),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (batch_id, hardware_serial),
  UNIQUE (batch_id, device_id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  CHECK ((status = 'ACTIVATED') = (activated_at IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS provisioning_batch_devices_batch_status_idx
  ON connectivity.provisioning_batch_devices (tenant_id, batch_id, status, id);

ALTER TABLE connectivity.provisioning_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.provisioning_batches FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS provisioning_batches_migrator_all ON connectivity.provisioning_batches;
CREATE POLICY provisioning_batches_migrator_all ON connectivity.provisioning_batches
  FOR ALL TO connectivity_migrator
  USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS provisioning_batches_tenant_scope ON connectivity.provisioning_batches;
CREATE POLICY provisioning_batches_tenant_scope ON connectivity.provisioning_batches
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE connectivity.provisioning_batch_devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.provisioning_batch_devices FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS provisioning_batch_devices_migrator_all ON connectivity.provisioning_batch_devices;
CREATE POLICY provisioning_batch_devices_migrator_all ON connectivity.provisioning_batch_devices
  FOR ALL TO connectivity_migrator
  USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS provisioning_batch_devices_tenant_scope ON connectivity.provisioning_batch_devices;
CREATE POLICY provisioning_batch_devices_tenant_scope ON connectivity.provisioning_batch_devices
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON connectivity.provisioning_batches, connectivity.provisioning_batch_devices TO connectivity_runtime;

ALTER TABLE connectivity.gateway_credential_audit
  DROP CONSTRAINT IF EXISTS gateway_credential_audit_action_check;
ALTER TABLE connectivity.gateway_credential_audit
  ADD CONSTRAINT gateway_credential_audit_action_check
  CHECK (action IN ('CODE_GENERATED','ISSUED','RENEWED','REVOKED','CERTIFICATE_REVOKED','IDENTITY_REENABLED','JITP_ACTIVATED'));

CREATE OR REPLACE FUNCTION connectivity.resolve_provisioning_batch_tenant(p_product_key text)
RETURNS TABLE (batch_id uuid, tenant_id uuid, site_id uuid)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  SELECT pb.id, pb.tenant_id, pb.site_id
  FROM connectivity.provisioning_batches pb
  WHERE pb.product_key = p_product_key
  LIMIT 1;
$$;

REVOKE ALL ON FUNCTION connectivity.resolve_provisioning_batch_tenant(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION connectivity.resolve_provisioning_batch_tenant(text) TO connectivity_runtime;

COMMENT ON TABLE connectivity.provisioning_batches IS '一型一密（JITP）预注册批次表，记录产品级激活凭据摘要与配额状态。';
COMMENT ON COLUMN connectivity.provisioning_batches.id IS '预注册批次主键（UUIDv7）。';
COMMENT ON COLUMN connectivity.provisioning_batches.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.provisioning_batches.site_id IS '所属站点标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.provisioning_batches.batch_name IS '批次显示名称。';
COMMENT ON COLUMN connectivity.provisioning_batches.product_key IS '全局唯一的产品型号凭据标识。';
COMMENT ON COLUMN connectivity.provisioning_batches.product_secret_sha256 IS '一次性下发的产品密钥 SHA-256 十六进制摘要（不存明文）。';
COMMENT ON COLUMN connectivity.provisioning_batches.device_type IS '允许激活的设备类型。';
COMMENT ON COLUMN connectivity.provisioning_batches.connectivity_mode IS '激活设备的连接模式：GATEWAY_PROXIED 或 DIRECT。';
COMMENT ON COLUMN connectivity.provisioning_batches.max_devices IS '本批次允许激活的最大设备数量上限。';
COMMENT ON COLUMN connectivity.provisioning_batches.activated_count IS '本批次已完成 JITP 首次激活的设备计数。';
COMMENT ON COLUMN connectivity.provisioning_batches.status IS '批次状态：ACTIVE、PAUSED 或 REVOKED。';
COMMENT ON COLUMN connectivity.provisioning_batches.expires_at IS '批次一型一密凭证有效期截止时间。';
COMMENT ON COLUMN connectivity.provisioning_batches.created_by IS '创建该批次的操作主体 UUIDv7。';
COMMENT ON COLUMN connectivity.provisioning_batches.created_at IS '批次创建时间。';
COMMENT ON COLUMN connectivity.provisioning_batches.updated_at IS '批次最近更新时间。';

COMMENT ON TABLE connectivity.provisioning_batch_devices IS 'JITP 批次内预先绑定硬件序列号与资产台账设备 ID 的明细表。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.id IS '批次设备预登记记录主键（UUIDv7）。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.batch_id IS '关联的预注册批次 ID。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.site_id IS '所属站点标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.device_id IS '对应的设备台账主键 ID（也是激活后证书 CN）。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.hardware_serial IS '产线烧录的唯一硬件序列号。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.status IS '预登记状态：PENDING、ACTIVATED 或 REVOKED。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.activated_at IS '完成首次 JITP 签发设备证书的时间。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.credential_id IS '首次 JITP 激活签发的客户端证书记录 ID。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.created_at IS '预登记记录创建时间。';
COMMENT ON COLUMN connectivity.provisioning_batch_devices.updated_at IS '预登记记录最近更新时间。';

COMMENT ON FUNCTION connectivity.resolve_provisioning_batch_tenant(text) IS '按产品型号标识安全解析预注册批次所属租户与站点，用于在切入租户 RLS 事务前定位上下文。';

RESET ROLE;

COMMIT;
