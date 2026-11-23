\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s3_command_migrator;

CREATE TABLE IF NOT EXISTS command_runtime.batches (
  batch_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  idempotency_key text NOT NULL,
  request_hash text NOT NULL,
  capability_name text NOT NULL,
  command_source text NOT NULL DEFAULT 'OPERATOR',
  delivery_mode text NOT NULL DEFAULT 'FAIL_FAST'
    CHECK (delivery_mode IN ('FAIL_FAST', 'QUEUE_IF_OFFLINE')),
  ttl_seconds integer
    CHECK (ttl_seconds IS NULL OR (ttl_seconds >= 1 AND ttl_seconds <= 86400)),
  expires_at timestamptz,
  target_count integer NOT NULL CHECK (target_count > 0 AND target_count <= 100),
  accepted_count integer NOT NULL DEFAULT 0 CHECK (accepted_count >= 0),
  rejected_count integer NOT NULL DEFAULT 0 CHECK (rejected_count >= 0),
  status text NOT NULL
    CHECK (status IN ('PENDING', 'PARTIAL', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'EXPIRED')),
  initiated_by_principal_id text NOT NULL,
  trace_id text NOT NULL DEFAULT '',
  reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, idempotency_key)
);

COMMENT ON TABLE command_runtime.batches IS '站点多设备批量群控广播主表，记录统一能力动作、投递策略、存活有效期、目标数量与整体聚合状态，由控制指令服务维护。';
COMMENT ON COLUMN command_runtime.batches.batch_id IS '批量群控批次唯一标识。';
COMMENT ON COLUMN command_runtime.batches.tenant_id IS '所属租户标识，用于行级安全隔离。';
COMMENT ON COLUMN command_runtime.batches.site_id IS '批量控制目标所属站点标识。';
COMMENT ON COLUMN command_runtime.batches.idempotency_key IS '租户与站点维度下的批量群控请求幂等键。';
COMMENT ON COLUMN command_runtime.batches.request_hash IS '批量请求规范化载荷哈希，用于幂等冲突校验。';
COMMENT ON COLUMN command_runtime.batches.capability_name IS '本次批量群控统一下发的设备可控能力名称。';
COMMENT ON COLUMN command_runtime.batches.command_source IS '批量控制指令来源优先级类别。';
COMMENT ON COLUMN command_runtime.batches.delivery_mode IS '离线投递策略：FAIL_FAST（离线或休眠直接失败）或 QUEUE_IF_OFFLINE（允许离线排队等待唤醒重投）。';
COMMENT ON COLUMN command_runtime.batches.ttl_seconds IS '指令最大存活时长（秒），上限 86400 秒。';
COMMENT ON COLUMN command_runtime.batches.expires_at IS '指令过期截止时间戳，超过该时间尚未执行则自动过期。';
COMMENT ON COLUMN command_runtime.batches.target_count IS '批次请求包含的目标设备条目总数，单批最多 100 台设备。';
COMMENT ON COLUMN command_runtime.batches.accepted_count IS '成功通过门禁并生成子控制指令的条目数量。';
COMMENT ON COLUMN command_runtime.batches.rejected_count IS '受理阶段即被门禁或联锁拒绝的条目数量。';
COMMENT ON COLUMN command_runtime.batches.status IS '批量群控整体聚合状态：PENDING、PARTIAL、SUCCEEDED、FAILED、CANCELLED 或 EXPIRED。';
COMMENT ON COLUMN command_runtime.batches.initiated_by_principal_id IS '发起该批量控制请求的主体标识。';
COMMENT ON COLUMN command_runtime.batches.trace_id IS '分布式链路追踪标识。';
COMMENT ON COLUMN command_runtime.batches.reason IS '批量控制操作原因说明。';
COMMENT ON COLUMN command_runtime.batches.created_at IS '批次受理创建时间。';
COMMENT ON COLUMN command_runtime.batches.updated_at IS '批次最近一次状态更新时间戳。';

CREATE INDEX IF NOT EXISTS batches_tenant_site_created_idx
  ON command_runtime.batches (tenant_id, site_id, created_at DESC, batch_id DESC);

ALTER TABLE command_runtime.command_intents
  ADD COLUMN IF NOT EXISTS delivery_mode text NOT NULL DEFAULT 'FAIL_FAST',
  ADD COLUMN IF NOT EXISTS ttl_seconds integer,
  ADD COLUMN IF NOT EXISTS expires_at timestamptz,
  ADD COLUMN IF NOT EXISTS next_dispatch_after timestamptz,
  ADD COLUMN IF NOT EXISTS batch_id uuid,
  ADD COLUMN IF NOT EXISTS batch_item_index integer;

ALTER TABLE command_runtime.command_intents
  DROP CONSTRAINT IF EXISTS command_intents_delivery_mode_check;
ALTER TABLE command_runtime.command_intents
  ADD CONSTRAINT command_intents_delivery_mode_check
  CHECK (delivery_mode IN ('FAIL_FAST', 'QUEUE_IF_OFFLINE'));

ALTER TABLE command_runtime.command_intents
  DROP CONSTRAINT IF EXISTS command_intents_ttl_seconds_check;
ALTER TABLE command_runtime.command_intents
  ADD CONSTRAINT command_intents_ttl_seconds_check
  CHECK (ttl_seconds IS NULL OR (ttl_seconds >= 1 AND ttl_seconds <= 86400));

ALTER TABLE command_runtime.command_intents
  DROP CONSTRAINT IF EXISTS command_intents_batch_item_index_check;
ALTER TABLE command_runtime.command_intents
  ADD CONSTRAINT command_intents_batch_item_index_check
  CHECK (batch_item_index IS NULL OR batch_item_index >= 0);

ALTER TABLE command_runtime.command_intents
  DROP CONSTRAINT IF EXISTS command_intents_batch_id_fkey;
ALTER TABLE command_runtime.command_intents
  ADD CONSTRAINT command_intents_batch_id_fkey
  FOREIGN KEY (batch_id) REFERENCES command_runtime.batches(batch_id);

COMMENT ON COLUMN command_runtime.command_intents.delivery_mode IS '离线投递策略：FAIL_FAST（实时直发）或 QUEUE_IF_OFFLINE（离线/休眠排队）。';
COMMENT ON COLUMN command_runtime.command_intents.ttl_seconds IS '控制指令存活秒数（1 至 86400 秒）。';
COMMENT ON COLUMN command_runtime.command_intents.expires_at IS '控制指令失效截止时间，超过后不得下发或执行。';
COMMENT ON COLUMN command_runtime.command_intents.next_dispatch_after IS '离线排队指令允许下一次抢占下发的最早时间，设备上线唤醒时可立即清零触发。';
COMMENT ON COLUMN command_runtime.command_intents.batch_id IS '若本指令属于批量群控，则指向所属批次回执主键。';
COMMENT ON COLUMN command_runtime.command_intents.batch_item_index IS '本指令在所属批量群控条目中的从零开始序号。';

CREATE INDEX IF NOT EXISTS command_intents_queued_schedule_idx
  ON command_runtime.command_intents (tenant_id, next_dispatch_after, created_at)
  WHERE status = 'QUEUED';

CREATE INDEX IF NOT EXISTS command_intents_expires_at_idx
  ON command_runtime.command_intents (tenant_id, expires_at)
  WHERE expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS command_intents_batch_id_idx
  ON command_runtime.command_intents (tenant_id, batch_id, batch_item_index)
  WHERE batch_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS command_runtime.batch_items (
  batch_id uuid NOT NULL REFERENCES command_runtime.batches(batch_id) ON DELETE CASCADE,
  item_index integer NOT NULL CHECK (item_index >= 0),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  point_id uuid NOT NULL,
  command_id uuid REFERENCES command_runtime.command_intents(command_id),
  admitted boolean NOT NULL,
  rejection_code text NOT NULL DEFAULT '',
  rejection_reason text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'QUEUED',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (batch_id, item_index)
);

COMMENT ON TABLE command_runtime.batch_items IS '批量群控每个目标设备的子指令关联与受理失败快照明细，由控制指令服务维护。';
COMMENT ON COLUMN command_runtime.batch_items.batch_id IS '所属批量群控主表标识。';
COMMENT ON COLUMN command_runtime.batch_items.item_index IS '目标设备在批次请求中的顺序序号。';
COMMENT ON COLUMN command_runtime.batch_items.tenant_id IS '所属租户标识，用于行级安全隔离。';
COMMENT ON COLUMN command_runtime.batch_items.site_id IS '目标设备所属站点标识。';
COMMENT ON COLUMN command_runtime.batch_items.device_id IS '目标设备标识。';
COMMENT ON COLUMN command_runtime.batch_items.point_id IS '目标控制点位标识。';
COMMENT ON COLUMN command_runtime.batch_items.command_id IS '成功受理后关联生成的控制指令意图标识，被门禁拒绝时为空。';
COMMENT ON COLUMN command_runtime.batch_items.admitted IS '该目标是否通过准入门禁并成功排队。';
COMMENT ON COLUMN command_runtime.batch_items.rejection_code IS '准入被拒时的机器可读错误码。';
COMMENT ON COLUMN command_runtime.batch_items.rejection_reason IS '准入被拒时的人类可读原因描述。';
COMMENT ON COLUMN command_runtime.batch_items.status IS '条目的最新状态映射，便于无联表时快速查看失败明细。';
COMMENT ON COLUMN command_runtime.batch_items.created_at IS '条目记录创建时间。';
COMMENT ON COLUMN command_runtime.batch_items.updated_at IS '条目记录最近更新时间戳。';

CREATE INDEX IF NOT EXISTS batch_items_tenant_command_idx
  ON command_runtime.batch_items (tenant_id, command_id)
  WHERE command_id IS NOT NULL;

ALTER TABLE command_runtime.batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.batches FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.batch_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.batch_items FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS batches_migrator_all ON command_runtime.batches;
CREATE POLICY batches_migrator_all ON command_runtime.batches
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS batches_runtime_tenant ON command_runtime.batches;
CREATE POLICY batches_runtime_tenant ON command_runtime.batches
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS batch_items_migrator_all ON command_runtime.batch_items;
CREATE POLICY batch_items_migrator_all ON command_runtime.batch_items
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS batch_items_runtime_tenant ON command_runtime.batch_items;
CREATE POLICY batch_items_runtime_tenant ON command_runtime.batch_items
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON command_runtime.batches TO s3_command_runtime;
GRANT SELECT, INSERT, UPDATE ON command_runtime.batch_items TO s3_command_runtime;

ALTER TABLE command_runtime.command_intents
  DROP CONSTRAINT IF EXISTS command_intents_precondition_revision_check;
ALTER TABLE command_runtime.command_intents
  ADD CONSTRAINT command_intents_precondition_revision_check
    CHECK (
      (precondition_mode = 'REQUIRE_CURRENT_STATE' AND (snapshot_revision > 0 OR delivery_mode = 'QUEUE_IF_OFFLINE'))
      OR (precondition_mode = 'EMERGENCY_BYPASS' AND snapshot_revision >= 0 AND btrim(bypass_reason) <> '')
    );

CREATE OR REPLACE FUNCTION command_runtime.tenants_with_due_dispatch(due_at timestamptz)
RETURNS SETOF uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, command_runtime
AS $$
  SELECT o.tenant_id
  FROM command_runtime.command_dispatch_outbox o
  WHERE o.delivered_at IS NULL
    AND o.available_at <= due_at
    AND (o.lease_until IS NULL OR o.lease_until <= due_at)
  UNION
  SELECT a.tenant_id
  FROM command_runtime.command_attempts a
  JOIN command_runtime.command_intents i
    ON i.tenant_id = a.tenant_id
   AND i.command_id = a.command_id
  WHERE i.status = 'DISPATCHING'
    AND (
      (a.status = 'PREPARED' AND a.lease_until <= due_at)
      OR (a.status = 'ACKNOWLEDGED' AND a.verification_deadline <= due_at)
    )
  UNION
  SELECT i.tenant_id
  FROM command_runtime.command_intents i
  WHERE i.status = 'AWAITING_APPROVAL'
    AND i.created_at <= (due_at - interval '5 minutes')
  UNION
  SELECT i.tenant_id
  FROM command_runtime.command_intents i
  WHERE i.status = 'QUEUED'
    AND (
      EXISTS (
        SELECT 1
        FROM command_runtime.command_approval_snapshots a
        WHERE a.command_id = i.command_id
          AND (a.expires_at <= due_at OR a.authorization_expires_at <= due_at OR a.authorization_purpose <> 'COMMAND_APPROVE')
      )
      OR (
        NOT EXISTS (SELECT 1 FROM command_runtime.command_approval_snapshots a WHERE a.command_id = i.command_id)
        AND NOT EXISTS (
          SELECT 1
          FROM command_runtime.command_authorization_snapshots s
          WHERE s.command_id = i.command_id
            AND s.authorization_purpose = 'COMMAND_SUBMIT'
            AND (
              s.expires_at > due_at
              OR (i.delivery_mode = 'QUEUE_IF_OFFLINE' AND i.expires_at IS NOT NULL AND i.expires_at > due_at)
            )
        )
      )
      OR EXISTS (
        SELECT 1
        FROM command_runtime.device_priority_locks l
        WHERE l.tenant_id = i.tenant_id
          AND l.device_id = i.device_id
          AND l.locked_until > due_at
          AND (
            CASE i.priority_tier
              WHEN 'SAFETY_INTERLOCK' THEN 500
              WHEN 'MANUAL_OVERRIDE' THEN 400
              WHEN 'PROTECTION_RULE' THEN 300
              WHEN 'AI_OPTIMIZATION' THEN 200
              WHEN 'SCHEDULE' THEN 100
              ELSE 0
            END
          ) < (
            CASE l.locked_priority
              WHEN 'SAFETY_INTERLOCK' THEN 500
              WHEN 'MANUAL_OVERRIDE' THEN 400
              WHEN 'PROTECTION_RULE' THEN 300
              WHEN 'AI_OPTIMIZATION' THEN 200
              WHEN 'SCHEDULE' THEN 100
              ELSE 0
            END
          )
      )
    )
  UNION
  SELECT i.tenant_id
  FROM command_runtime.command_intents i
  WHERE i.status IN ('AWAITING_APPROVAL', 'QUEUED')
    AND i.expires_at IS NOT NULL
    AND i.expires_at <= due_at;
$$;

COMMENT ON FUNCTION command_runtime.tenants_with_due_dispatch(timestamptz) IS '按参考时间筛选存在到期控制指令派发、到期租约清理、超期审批、TTL存活超期或优先级锁拦截任务的租户集合，并跳过仍在离线退避窗口内的队列。';

REVOKE ALL ON FUNCTION command_runtime.tenants_with_due_dispatch(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION command_runtime.tenants_with_due_dispatch(timestamptz) TO s3_command_runtime;

RESET ROLE;

COMMIT;
