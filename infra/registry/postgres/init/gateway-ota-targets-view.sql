\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

GRANT USAGE ON SCHEMA core_registry TO core_registry_read_port, connectivity_runtime;

-- core_registry.devices already grants SELECT to core_registry_read_port via 017-registry-read-port.sql;
-- grant column-level SELECT on core_registry.assets.tags so gateway_ota_targets_v1 can merge asset tags.
GRANT SELECT (tags) ON core_registry.assets TO core_registry_read_port;

CREATE OR REPLACE VIEW core_registry.gateway_ota_targets_v1 WITH (security_barrier) AS
SELECT gateway.id AS gateway_id,
       gateway.tenant_id,
       gateway.site_id,
       gateway.code AS gateway_code,
       gateway.display_name AS gateway_name,
       gateway.status,
       COALESCE(gateway.tags, '{}'::jsonb) AS device_tags,
       COALESCE(bound_assets.asset_tags, '{}'::jsonb) || COALESCE(gateway.tags, '{}'::jsonb) AS effective_tags,
       gateway.revision
FROM core_registry.devices AS gateway
LEFT JOIN LATERAL (
  SELECT COALESCE(jsonb_object_agg(kv.key, kv.value ORDER BY a.id, kv.key), '{}'::jsonb) AS asset_tags
  FROM core_registry.device_bindings AS db
  JOIN core_registry.assets AS a
    ON a.tenant_id = db.tenant_id
   AND a.site_id = db.site_id
   AND a.id = db.asset_id
   AND a.status = 'ACTIVE'
  CROSS JOIN LATERAL jsonb_each(a.tags) AS kv(key, value)
  WHERE db.tenant_id = gateway.tenant_id
    AND db.site_id = gateway.site_id
    AND db.device_id = gateway.id
    AND db.status = 'ACTIVE'
    AND db.valid_to IS NULL
) AS bound_assets ON true
WHERE gateway.device_type = 'GATEWAY'
  AND gateway.tenant_id = core_registry.current_tenant_id()
  AND core_registry.is_authorized_site(gateway.site_id);

RESET ROLE;

ALTER VIEW core_registry.gateway_ota_targets_v1 OWNER TO core_registry_read_port;

REVOKE ALL ON core_registry.gateway_ota_targets_v1 FROM PUBLIC;
GRANT SELECT ON core_registry.gateway_ota_targets_v1 TO connectivity_runtime, core_registry_read_port;

COMMENT ON VIEW core_registry.gateway_ota_targets_v1 IS '设备接入层 OTA 目标网关只读视图：提供当前租户及已授权站点下网关设备的自身标签与绑定资产合并标签，用于灰度发布筛选与任务详情展示。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.gateway_id IS '网关设备主键标识（UUIDv7）。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.tenant_id IS '所属租户标识（UUIDv7）。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.site_id IS '所属站点标识（UUIDv7）。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.gateway_code IS '网关在站点内的编码。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.gateway_name IS '网关展示名称。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.status IS '网关生命周期状态：ACTIVE、INACTIVE 或 RETIRED。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.device_tags IS '网关设备自身登记的多维标签键值映射。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.effective_tags IS '合并网关绑定活动资产标签与设备自身标签后的有效标签（设备标签优先覆盖同名键）。';
COMMENT ON COLUMN core_registry.gateway_ota_targets_v1.revision IS '网关设备记录版本号。';

COMMIT;
