/*
  自动控制执行监控记录的分布式抢占租约列与索引
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
CREATE POLICY optimization_execution_monitors_optimization_poll_update ON core_registry.optimization_execution_monitors
  FOR UPDATE TO optimization_runtime
  USING (status = 'OBSERVING')
  WITH CHECK (status = 'OBSERVING');

RESET ROLE;

COMMIT;
