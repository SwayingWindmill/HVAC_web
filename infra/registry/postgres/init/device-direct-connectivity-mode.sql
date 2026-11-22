\set ON_ERROR_STOP on

BEGIN;

ALTER VIEW core_registry.gateway_directory_v1 OWNER TO s1_core_migrator;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.devices
  ADD COLUMN IF NOT EXISTS connectivity_mode text NOT NULL DEFAULT 'GATEWAY_PROXIED';

ALTER TABLE core_registry.devices
  DROP CONSTRAINT IF EXISTS devices_connectivity_mode_check;
ALTER TABLE core_registry.devices
  ADD CONSTRAINT devices_connectivity_mode_check
  CHECK (connectivity_mode IN ('GATEWAY_PROXIED', 'DIRECT'));

COMMENT ON COLUMN core_registry.devices.connectivity_mode IS '设备云端连接模式：GATEWAY_PROXIED 表示经网关代理接入，DIRECT 表示单品设备持独立 mTLS 证书直连 MQTT。';

DROP POLICY IF EXISTS devices_read_port ON core_registry.devices;
CREATE POLICY devices_read_port ON core_registry.devices
  FOR SELECT TO core_registry_read_port
  USING (device_type = 'GATEWAY' OR connectivity_mode = 'DIRECT' OR tenant_id = core_registry.current_tenant_id());

CREATE OR REPLACE VIEW core_registry.gateway_directory_v1 WITH (security_barrier) AS
SELECT gateway.id AS gateway_id,
       gateway.tenant_id,
       gateway.site_id,
       gateway.status,
       gateway.revision,
       gateway.device_type,
       gateway.connectivity_mode,
       gateway.code AS device_code
FROM core_registry.devices AS gateway
WHERE gateway.device_type = 'GATEWAY'
   OR gateway.connectivity_mode = 'DIRECT';

RESET ROLE;

ALTER VIEW core_registry.gateway_directory_v1 OWNER TO core_registry_read_port;
REVOKE ALL ON core_registry.gateway_directory_v1 FROM PUBLIC;
GRANT SELECT ON core_registry.gateway_directory_v1 TO connectivity_runtime, core_registry_read_port;

COMMENT ON COLUMN core_registry.gateway_directory_v1.device_type IS '目录中记录的设备类型，例如 GATEWAY 或独立直连设备型号。';
COMMENT ON COLUMN core_registry.gateway_directory_v1.connectivity_mode IS '连接模式：GATEWAY_PROXIED 或 DIRECT。';
COMMENT ON COLUMN core_registry.gateway_directory_v1.device_code IS '设备在站点内的编码标识，直连设备可将其用作自身 sourceKey。';

COMMIT;
