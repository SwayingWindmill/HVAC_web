-- 现场设备/电表/边缘网关硬件更换记录与设备外部标识字段：
-- 保证换机换表后原有逻辑设备（devices.id）、资产（assets.id）与点位（telemetry_points.id）不变，
-- 历史账单、时序曲线、告警规则与小程序绑定均无缝延续，仅更新硬件序列号/通信地址/网关映射并记录审计履历。
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.devices
  ADD COLUMN IF NOT EXISTS external_id text;

ALTER TABLE core_registry.devices
  ADD COLUMN IF NOT EXISTS protocol_address jsonb;

ALTER TABLE core_registry.devices
  DROP CONSTRAINT IF EXISTS devices_external_id_length_check;
ALTER TABLE core_registry.devices
  ADD CONSTRAINT devices_external_id_length_check
  CHECK (external_id IS NULL OR length(btrim(external_id)) BETWEEN 1 AND 256);

ALTER TABLE core_registry.devices
  DROP CONSTRAINT IF EXISTS devices_protocol_address_document_check;
ALTER TABLE core_registry.devices
  ADD CONSTRAINT devices_protocol_address_document_check
  CHECK (
    protocol_address IS NULL
    OR (jsonb_typeof(protocol_address) = 'object' AND octet_length(protocol_address::text) <= 8192)
  );

CREATE UNIQUE INDEX IF NOT EXISTS devices_site_external_id_active_uidx
  ON core_registry.devices (tenant_id, site_id, external_id)
  WHERE external_id IS NOT NULL AND status <> 'RETIRED';

CREATE TABLE IF NOT EXISTS core_registry.device_hardware_replacements (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  previous_external_id text CHECK (previous_external_id IS NULL OR length(btrim(previous_external_id)) BETWEEN 1 AND 256),
  new_external_id text NOT NULL CHECK (length(btrim(new_external_id)) BETWEEN 1 AND 256),
  previous_gateway_device_id uuid CHECK (previous_gateway_device_id IS NULL OR core_registry.is_uuid_v7(previous_gateway_device_id)),
  new_gateway_device_id uuid CHECK (new_gateway_device_id IS NULL OR core_registry.is_uuid_v7(new_gateway_device_id)),
  previous_protocol_address jsonb CHECK (
    previous_protocol_address IS NULL
    OR (jsonb_typeof(previous_protocol_address) = 'object' AND octet_length(previous_protocol_address::text) <= 8192)
  ),
  new_protocol_address jsonb CHECK (
    new_protocol_address IS NULL
    OR (jsonb_typeof(new_protocol_address) = 'object' AND octet_length(new_protocol_address::text) <= 8192)
  ),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
  replaced_by text NOT NULL CHECK (length(btrim(replaced_by)) BETWEEN 1 AND 256),
  device_revision bigint NOT NULL CHECK (device_revision > 0),
  replaced_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, previous_gateway_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, new_gateway_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id)
);

CREATE INDEX IF NOT EXISTS device_hardware_replacements_device_idx
  ON core_registry.device_hardware_replacements (tenant_id, site_id, device_id, replaced_at DESC, id DESC);

ALTER TABLE core_registry.device_hardware_replacements ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_hardware_replacements FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS device_hardware_replacements_migrator_all ON core_registry.device_hardware_replacements;
CREATE POLICY device_hardware_replacements_migrator_all ON core_registry.device_hardware_replacements
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS device_hardware_replacements_runtime_scope ON core_registry.device_hardware_replacements;
CREATE POLICY device_hardware_replacements_runtime_scope ON core_registry.device_hardware_replacements
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS device_hardware_replacements_writer_scope ON core_registry.device_hardware_replacements;
CREATE POLICY device_hardware_replacements_writer_scope ON core_registry.device_hardware_replacements
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.device_hardware_replacements FROM PUBLIC;
GRANT SELECT ON core_registry.device_hardware_replacements TO s1_core_runtime;
GRANT SELECT, INSERT ON core_registry.device_hardware_replacements TO s1_core_writer;

COMMENT ON COLUMN core_registry.devices.external_id IS '现场硬件外部序列号或通信标识（更换硬件时更新，保持逻辑设备主键不变）。';
COMMENT ON COLUMN core_registry.devices.protocol_address IS '现场设备协议通信地址配置（如总线站号、端口与通道参数）。';
COMMENT ON TABLE core_registry.device_hardware_replacements IS '逻辑设备更换现场硬件（网关、电表或传感器）的不可变变更履历表，确保历史数据按原逻辑设备连续记录。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.id IS '硬件更换记录主键。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.tenant_id IS '所属租户标识。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.site_id IS '所属站点标识。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.device_id IS '保持连续不变的逻辑设备主键。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.previous_external_id IS '更换前原硬件外部序列号或标识。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.new_external_id IS '更换后新硬件外部序列号或标识。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.previous_gateway_device_id IS '更换前设备接入的旧网关设备主键。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.new_gateway_device_id IS '更换后设备接入的新网关设备主键。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.previous_protocol_address IS '更换前协议地址或硬件参数快照。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.new_protocol_address IS '更换后协议地址或硬件参数快照。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.reason IS '现场更换硬件的原因说明。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.replaced_by IS '执行硬件更换的操作人身份标识。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.device_revision IS '更换完成后逻辑设备的最新修订版本号。';
COMMENT ON COLUMN core_registry.device_hardware_replacements.replaced_at IS '现场硬件更换完成并生效的时间戳。';

RESET ROLE;
COMMIT;
