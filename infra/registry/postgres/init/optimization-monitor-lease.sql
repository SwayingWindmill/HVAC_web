/*
  自动控制执行监控记录的分布式抢占租约列、索引与原子认领函数
  为多副本 optimization-service 提供原子认领与过期接管能力，防止并发重复下发回滚与熔断事件
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.optimization_execution_monitors
  ADD COLUMN IF NOT EXISTS claim_owner text,
  ADD COLUMN IF NOT EXISTS claim_until timestamptz;

ALTER TABLE core_registry.optimization_execution_monitors
  DROP CONSTRAINT IF EXISTS optimization_execution_monitors_claim_pair_check;
ALTER TABLE core_registry.optimization_execution_monitors
  ADD CONSTRAINT optimization_execution_monitors_claim_pair_check
  CHECK (
    (claim_owner IS NULL AND claim_until IS NULL)
    OR (claim_owner IS NOT NULL AND length(btrim(claim_owner)) > 0 AND claim_until IS NOT NULL)
  );

CREATE INDEX IF NOT EXISTS optimization_execution_monitors_claim_idx
  ON core_registry.optimization_execution_monitors (status, claim_until, window_ends_at, started_at, id)
  WHERE status = 'OBSERVING';

COMMENT ON COLUMN core_registry.optimization_execution_monitors.claim_owner IS '当前抢占并持有该执行监控记录租约的优化服务副本标识。';
COMMENT ON COLUMN core_registry.optimization_execution_monitors.claim_until IS '当前副本持有该执行监控记录租约的到期时间，到期后允许其他副本接管。';

DROP POLICY IF EXISTS optimization_execution_monitors_optimization_poll_update ON core_registry.optimization_execution_monitors;

CREATE OR REPLACE FUNCTION core_registry.claim_observing_execution_monitors(
  p_claim_owner text,
  p_claim_until timestamptz,
  p_now timestamptz,
  p_limit integer DEFAULT 100
)
RETURNS SETOF core_registry.optimization_execution_monitors
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, core_registry
AS $$
DECLARE
  v_owner text := btrim(COALESCE(p_claim_owner, ''));
  v_limit integer := LEAST(GREATEST(COALESCE(p_limit, 100), 1), 500);
BEGIN
  IF v_owner = '' OR p_claim_until IS NULL OR p_now IS NULL OR p_claim_until <= p_now THEN
    RAISE EXCEPTION 'execution monitor claim owner and future lease window are required';
  END IF;
  RETURN QUERY
  WITH claimed AS (
    SELECT m.id
    FROM core_registry.optimization_execution_monitors AS m
    WHERE m.status = 'OBSERVING'
      AND (m.claim_until IS NULL OR m.claim_until <= p_now)
    ORDER BY m.window_ends_at ASC, m.started_at ASC, m.id ASC
    LIMIT v_limit
    FOR UPDATE SKIP LOCKED
  )
  UPDATE core_registry.optimization_execution_monitors AS target
  SET claim_owner = v_owner,
      claim_until = p_claim_until,
      updated_at = GREATEST(target.updated_at, p_now)
  FROM claimed
  WHERE target.id = claimed.id
    AND target.status = 'OBSERVING'
  RETURNING target.*;
END;
$$;

COMMENT ON FUNCTION core_registry.claim_observing_execution_monitors(text, timestamptz, timestamptz, integer) IS '以定义者权限原子认领到期的观察中执行监控记录并仅更新租约字段，防止跨租户越权修改其它监控列。';

REVOKE ALL ON FUNCTION core_registry.claim_observing_execution_monitors(text, timestamptz, timestamptz, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core_registry.claim_observing_execution_monitors(text, timestamptz, timestamptz, integer) TO optimization_runtime;

RESET ROLE;

COMMIT;
