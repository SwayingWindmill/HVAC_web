-- 网关/直连设备过户吊销审计扩展与端侧出厂安全清除指令跟踪表：
-- 记录设备过户或人工退役触发的端侧本地证书、配置影子与离线队列清除指令状态及回执。
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.gateway_credential_audit
  DROP CONSTRAINT IF EXISTS gateway_credential_audit_action_check;

ALTER TABLE connectivity.gateway_credential_audit
  ADD CONSTRAINT gateway_credential_audit_action_check
  CHECK (action IN ('CODE_GENERATED','ISSUED','RENEWED','REVOKED','CERTIFICATE_REVOKED','IDENTITY_REENABLED','JITP_ACTIVATED','TRANSFER_REVOKED'));

CREATE TABLE IF NOT EXISTS connectivity.gateway_factory_resets (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  transfer_id uuid CHECK (transfer_id IS NULL OR connectivity.is_uuid_v7(transfer_id)),
  wipe_credentials boolean NOT NULL DEFAULT true,
  wipe_config_shadow boolean NOT NULL DEFAULT true,
  wipe_offline_buffer boolean NOT NULL DEFAULT true,
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 512),
  status text NOT NULL CHECK (status IN ('PENDING', 'DISPATCHED', 'ACKNOWLEDGED', 'FAILED')),
  issued_by uuid NOT NULL CHECK (connectivity.is_uuid_v7(issued_by)),
  dispatched_at timestamptz,
  acknowledged_at timestamptz,
  wiped_artifacts jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(wiped_artifacts) = 'array' AND octet_length(wiped_artifacts::text) <= 8192),
  error_message text CHECK (error_message IS NULL OR length(btrim(error_message)) BETWEEN 1 AND 1024),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (wipe_credentials OR wipe_config_shadow OR wipe_offline_buffer),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS gateway_factory_resets_transfer_uidx
  ON connectivity.gateway_factory_resets (tenant_id, gateway_id, transfer_id)
  WHERE transfer_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS gateway_factory_resets_gateway_idx
  ON connectivity.gateway_factory_resets (tenant_id, gateway_id, created_at DESC, id DESC);

ALTER TABLE connectivity.gateway_factory_resets ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_factory_resets FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS gateway_factory_resets_migrator_all ON connectivity.gateway_factory_resets;
CREATE POLICY gateway_factory_resets_migrator_all ON connectivity.gateway_factory_resets
  FOR ALL TO connectivity_migrator
  USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS gateway_factory_resets_tenant ON connectivity.gateway_factory_resets;
CREATE POLICY gateway_factory_resets_tenant ON connectivity.gateway_factory_resets
  FOR ALL TO connectivity_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON connectivity.gateway_factory_resets FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON connectivity.gateway_factory_resets TO connectivity_runtime;

CREATE OR REPLACE FUNCTION connectivity.resolve_factory_reset_tenant(p_reset_id uuid, p_gateway_id uuid)
RETURNS uuid
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  SELECT tenant_id
  FROM connectivity.gateway_factory_resets
  WHERE id = p_reset_id
    AND gateway_id = p_gateway_id
    AND status IN ('PENDING', 'DISPATCHED')
  LIMIT 1;
$$;

REVOKE ALL ON FUNCTION connectivity.resolve_factory_reset_tenant(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION connectivity.resolve_factory_reset_tenant(uuid, uuid) TO connectivity_runtime;

COMMENT ON TABLE connectivity.gateway_factory_resets IS '下发至边缘网关或直连设备的出厂级本地凭据、配置影子与离线队列清除指令记录表。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.id IS '出厂清除指令主键（UUIDv7，对应端侧消息 resetId）。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.tenant_id IS '发起清理的原所属租户标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.gateway_id IS '目标网关或直连设备主键标识（UUIDv7）。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.transfer_id IS '若由设备过户自动触发，则记录对应的跨站/跨租户过户记录 ID。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.wipe_credentials IS '是否要求擦除端侧本地 mTLS 客户端证书与私钥。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.wipe_config_shadow IS '是否要求清除端侧本地配置影子文件并卸载动态驱动。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.wipe_offline_buffer IS '是否要求清空端侧本地离线缓存、时序重传游标与未发送队列。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.reason IS '下发出厂清除指令的操作原因说明。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.status IS '清除指令执行状态：PENDING、DISPATCHED、ACKNOWLEDGED 或 FAILED。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.issued_by IS '发起清除指令的操作主体 UUIDv7。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.dispatched_at IS '指令通过 MQTT 成功发布给设备的时间。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.acknowledged_at IS '收到端侧回传 FACTORY_RESET_ACK 确认的时间。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.wiped_artifacts IS '端侧确认已实际擦除的本地配置、凭据或缓存项清单。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.error_message IS '若端侧上报擦除失败或异常时记录的错误详情。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.created_at IS '记录创建时间。';
COMMENT ON COLUMN connectivity.gateway_factory_resets.updated_at IS '记录最近更新时间。';
COMMENT ON FUNCTION connectivity.resolve_factory_reset_tenant(uuid, uuid) IS '按指令 ID 与设备 ID 安全解析处于待确认状态指令所属租户，用于在入站生命周期回执时切入租户 RLS 上下文。';

RESET ROLE;

COMMIT;
