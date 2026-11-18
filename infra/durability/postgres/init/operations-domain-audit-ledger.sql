BEGIN;
SET LOCAL ROLE s0_migrator;

ALTER TABLE audit_ledger.records
  DROP CONSTRAINT IF EXISTS records_aggregate_type_check,
  ADD CONSTRAINT records_aggregate_type_check
    CHECK (aggregate_type IN (
      'bff-session',
      'operations-investigation',
      'remote-command',
      'control-lock',
      'operating-schedule',
      'registry-mutation',
      'iam-admin',
      'gateway-credential'
    )),
  ADD COLUMN IF NOT EXISTS client_ip text,
  ADD COLUMN IF NOT EXISTS client_kind text,
  ADD COLUMN IF NOT EXISTS actor_display text,
  ADD COLUMN IF NOT EXISTS target_label text,
  ADD COLUMN IF NOT EXISTS change_summary jsonb;

ALTER TABLE audit_ledger.records
  DROP CONSTRAINT IF EXISTS records_client_kind_check,
  ADD CONSTRAINT records_client_kind_check
    CHECK (client_kind IS NULL OR client_kind IN ('BROWSER', 'MINI_PROGRAM', 'SYSTEM'));

CREATE INDEX IF NOT EXISTS audit_records_tenant_category_occurred_idx
  ON audit_ledger.records (tenant_id, aggregate_type, occurred_at DESC, ledger_sequence DESC);

COMMENT ON TABLE audit_ledger.records IS '跨服务与网关统一业务操作不可变防篡改哈希链审计主表，覆盖登录会话、AI运维以及控制、登记、权限与接入凭证变更，由审计账本服务追加写入。';
COMMENT ON COLUMN audit_ledger.records.client_ip IS '发起本次操作的客户端源 IP（经边界清洗，仅用于审计追溯）。';
COMMENT ON COLUMN audit_ledger.records.client_kind IS '发起本次操作的终端类型：BROWSER（网页端）、MINI_PROGRAM（小程序端）或 SYSTEM（平台系统调用）。';
COMMENT ON COLUMN audit_ledger.records.actor_display IS '执行本次操作时的操作者展示名称快照，便于脱离账号标识回溯。';
COMMENT ON COLUMN audit_ledger.records.target_label IS '本次操作目标对象的可读业务名称快照，例如设备名称、点位名称或成员姓名。';
COMMENT ON COLUMN audit_ledger.records.change_summary IS '本次变更前后关键字段对比及结果摘要的结构化 JSON 文档，敏感凭据字段已脱敏。';

RESET ROLE;
COMMIT;
