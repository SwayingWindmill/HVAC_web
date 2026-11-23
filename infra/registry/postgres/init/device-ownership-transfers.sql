-- 跨站点/跨租户物理设备过户台账（核心注册表域）：
-- 每次过户必须在源站点退役原逻辑设备并在目标站点/租户生成全新的逻辑设备 UUIDv7，
-- 彻底隔离新旧租户历史时序与操作审计边界，同时记录可追溯的不可变过户流水。
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.device_transfers (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  source_tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(source_tenant_id)),
  source_site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(source_site_id)),
  source_device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(source_device_id)),
  target_tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(target_tenant_id)),
  target_site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(target_site_id)),
  target_device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(target_device_id)),
  transfer_scope text NOT NULL CHECK (transfer_scope IN ('CROSS_SITE', 'CROSS_TENANT')),
  hardware_external_id text CHECK (hardware_external_id IS NULL OR length(btrim(hardware_external_id)) BETWEEN 1 AND 256),
  wipe_required boolean NOT NULL DEFAULT true,
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
  transferred_by text NOT NULL CHECK (length(btrim(transferred_by)) BETWEEN 1 AND 256),
  source_device_revision bigint NOT NULL CHECK (source_device_revision > 0),
  transferred_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source_device_id),
  UNIQUE (target_device_id),
  CHECK (source_device_id <> target_device_id),
  CHECK (
    (transfer_scope = 'CROSS_SITE' AND source_tenant_id = target_tenant_id AND source_site_id <> target_site_id)
    OR (transfer_scope = 'CROSS_TENANT' AND source_tenant_id <> target_tenant_id)
  ),
  FOREIGN KEY (source_tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (target_tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (source_tenant_id, source_site_id, source_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (target_tenant_id, target_site_id, target_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id)
);

CREATE INDEX IF NOT EXISTS device_transfers_source_idx
  ON core_registry.device_transfers (source_tenant_id, source_site_id, source_device_id, transferred_at DESC);

CREATE INDEX IF NOT EXISTS device_transfers_target_idx
  ON core_registry.device_transfers (target_tenant_id, target_site_id, target_device_id, transferred_at DESC);

ALTER TABLE core_registry.device_transfers ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_transfers FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS device_transfers_migrator_all ON core_registry.device_transfers;
CREATE POLICY device_transfers_migrator_all ON core_registry.device_transfers
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS device_transfers_runtime_scope ON core_registry.device_transfers;
CREATE POLICY device_transfers_runtime_scope ON core_registry.device_transfers
  FOR SELECT TO s1_core_runtime
  USING (
    (source_tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(source_site_id))
    OR (target_tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(target_site_id))
  );

DROP POLICY IF EXISTS device_transfers_writer_scope ON core_registry.device_transfers;
CREATE POLICY device_transfers_writer_scope ON core_registry.device_transfers
  FOR ALL TO s1_core_writer
  USING (source_tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(source_site_id))
  WITH CHECK (source_tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(source_site_id));

REVOKE ALL ON core_registry.device_transfers FROM PUBLIC;
GRANT SELECT ON core_registry.device_transfers TO s1_core_runtime;
GRANT SELECT, INSERT ON core_registry.device_transfers TO s1_core_writer;

COMMENT ON TABLE core_registry.device_transfers IS '物理设备跨站点或跨租户过户的不可变变更记录，保证新老归属历史数据严格隔离并生成全新目标设备主键。';
COMMENT ON COLUMN core_registry.device_transfers.id IS '设备过户记录主键（UUIDv7）。';
COMMENT ON COLUMN core_registry.device_transfers.source_tenant_id IS '过户前原所属租户标识。';
COMMENT ON COLUMN core_registry.device_transfers.source_site_id IS '过户前原所属站点标识。';
COMMENT ON COLUMN core_registry.device_transfers.source_device_id IS '过户前被退役的原逻辑设备主键。';
COMMENT ON COLUMN core_registry.device_transfers.target_tenant_id IS '过户后新接收租户标识。';
COMMENT ON COLUMN core_registry.device_transfers.target_site_id IS '过户后新接收站点标识。';
COMMENT ON COLUMN core_registry.device_transfers.target_device_id IS '在目标站点新创建的独立逻辑设备主键（严格区别于原设备主键）。';
COMMENT ON COLUMN core_registry.device_transfers.transfer_scope IS '过户范围：CROSS_SITE（同租户跨站点）或 CROSS_TENANT（跨租户）。';
COMMENT ON COLUMN core_registry.device_transfers.hardware_external_id IS '跟随实体硬件迁移的外部序列号或标识快照。';
COMMENT ON COLUMN core_registry.device_transfers.wipe_required IS '过户后是否要求触发端侧本地凭据与缓存出厂清除指令。';
COMMENT ON COLUMN core_registry.device_transfers.reason IS '发起设备跨站或跨租户过户的业务原因说明。';
COMMENT ON COLUMN core_registry.device_transfers.transferred_by IS '执行设备过户的操作主体标识。';
COMMENT ON COLUMN core_registry.device_transfers.source_device_revision IS '原逻辑设备退役前的最后修订版本号。';
COMMENT ON COLUMN core_registry.device_transfers.transferred_at IS '设备过户事务提交生效时间戳。';

RESET ROLE;

COMMIT;
