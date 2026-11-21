BEGIN;

SET LOCAL ROLE s3_command_migrator;

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
            AND s.expires_at > due_at
            AND s.authorization_purpose = 'COMMAND_SUBMIT'
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
    );
$$;

REVOKE ALL ON FUNCTION command_runtime.tenants_with_due_dispatch(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION command_runtime.tenants_with_due_dispatch(timestamptz) TO s3_command_runtime;

COMMENT ON FUNCTION command_runtime.tenants_with_due_dispatch(timestamptz) IS '按参考时间筛选存在到期控制指令派发、到期租约清理、超期审批或优先级锁拦截任务的租户集合，用于跨租户唤醒而免除空载轮询。';

CREATE OR REPLACE FUNCTION command_runtime.tenants_with_due_verification(due_at timestamptz)
RETURNS SETOF uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, command_runtime
AS $$
  SELECT DISTINCT a.tenant_id
  FROM command_runtime.command_attempts a
  JOIN command_runtime.command_intents i
    ON i.tenant_id = a.tenant_id
   AND i.command_id = a.command_id
  JOIN command_runtime.device_control_state d
    ON d.tenant_id = i.tenant_id
   AND d.device_id = i.device_id
  WHERE a.status = 'ACKNOWLEDGED'
    AND i.status = 'DISPATCHING'
    AND (
      a.verification_deadline <= due_at
      OR (
        a.execution_fence = i.active_execution_fence
        AND NOT (d.frozen_control_groups ? 'SETPOINT')
        AND (a.verification_lease_until IS NULL OR a.verification_lease_until <= due_at)
      )
    );
$$;

REVOKE ALL ON FUNCTION command_runtime.tenants_with_due_verification(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION command_runtime.tenants_with_due_verification(timestamptz) TO s3_command_runtime;

COMMENT ON FUNCTION command_runtime.tenants_with_due_verification(timestamptz) IS '按参考时间筛选存在待状态回读核验或已超期控制指令尝试的租户集合，避免对空闲或冻结设备发起无谓事务轮询。';

RESET ROLE;

COMMIT;
