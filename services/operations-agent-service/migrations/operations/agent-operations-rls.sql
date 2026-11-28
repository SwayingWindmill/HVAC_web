\set ON_ERROR_STOP on

-- Enforce strict tenant isolation across all Agent Operations tables and expose a narrow
-- SECURITY DEFINER startup recovery function for interrupted ACTIVE runs.

BEGIN;

SET LOCAL ROLE operations_agent_operations_migrator;

ALTER TABLE agent_operations.agent_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_operations.agent_sessions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_sessions_migrator_all ON agent_operations.agent_sessions;
CREATE POLICY agent_sessions_migrator_all ON agent_operations.agent_sessions
  FOR ALL TO operations_agent_operations_migrator
  USING (true)
  WITH CHECK (true);
DROP POLICY IF EXISTS agent_sessions_tenant_scope ON agent_operations.agent_sessions;
CREATE POLICY agent_sessions_tenant_scope ON agent_operations.agent_sessions
  FOR ALL TO operations_agent_operations_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), ''))
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), ''));

ALTER TABLE agent_operations.agent_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_operations.agent_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_runs_migrator_all ON agent_operations.agent_runs;
CREATE POLICY agent_runs_migrator_all ON agent_operations.agent_runs
  FOR ALL TO operations_agent_operations_migrator
  USING (true)
  WITH CHECK (true);
DROP POLICY IF EXISTS agent_runs_tenant_scope ON agent_operations.agent_runs;
CREATE POLICY agent_runs_tenant_scope ON agent_operations.agent_runs
  FOR ALL TO operations_agent_operations_runtime
  USING (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_runs.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_runs.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  );

ALTER TABLE agent_operations.agent_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_operations.agent_messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_messages_migrator_all ON agent_operations.agent_messages;
CREATE POLICY agent_messages_migrator_all ON agent_operations.agent_messages
  FOR ALL TO operations_agent_operations_migrator
  USING (true)
  WITH CHECK (true);
DROP POLICY IF EXISTS agent_messages_tenant_scope ON agent_operations.agent_messages;
CREATE POLICY agent_messages_tenant_scope ON agent_operations.agent_messages
  FOR ALL TO operations_agent_operations_runtime
  USING (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_messages.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_messages.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  );

ALTER TABLE agent_operations.agent_tool_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_operations.agent_tool_executions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_tool_executions_migrator_all ON agent_operations.agent_tool_executions;
CREATE POLICY agent_tool_executions_migrator_all ON agent_operations.agent_tool_executions
  FOR ALL TO operations_agent_operations_migrator
  USING (true)
  WITH CHECK (true);
DROP POLICY IF EXISTS agent_tool_executions_tenant_scope ON agent_operations.agent_tool_executions;
CREATE POLICY agent_tool_executions_tenant_scope ON agent_operations.agent_tool_executions
  FOR ALL TO operations_agent_operations_runtime
  USING (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_tool_executions.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_tool_executions.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  );

ALTER TABLE agent_operations.agent_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_operations.agent_artifacts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_artifacts_migrator_all ON agent_operations.agent_artifacts;
CREATE POLICY agent_artifacts_migrator_all ON agent_operations.agent_artifacts
  FOR ALL TO operations_agent_operations_migrator
  USING (true)
  WITH CHECK (true);
DROP POLICY IF EXISTS agent_artifacts_tenant_scope ON agent_operations.agent_artifacts;
CREATE POLICY agent_artifacts_tenant_scope ON agent_operations.agent_artifacts
  FOR ALL TO operations_agent_operations_runtime
  USING (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_artifacts.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM agent_operations.agent_sessions s
      WHERE s.session_id = agent_artifacts.session_id
        AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')
    )
  );

CREATE OR REPLACE FUNCTION agent_operations.interrupt_orphaned_active_runs(p_finished_at_ms bigint)
RETURNS integer
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_operations
AS $$
DECLARE
  v_count integer;
BEGIN
  UPDATE agent_operations.agent_runs r
  SET status = 'FAILED',
      finished_at_ms = GREATEST(p_finished_at_ms, r.started_at_ms),
      failure_code = 'RUN_INTERRUPTED'
  FROM agent_operations.agent_sessions s
  WHERE s.session_id = r.session_id
    AND s.status = 'ACTIVE'
    AND s.active_run_id = r.run_id
    AND r.status = 'RUNNING';
  GET DIAGNOSTICS v_count = ROW_COUNT;
  RETURN v_count;
END;
$$;

REVOKE ALL ON FUNCTION agent_operations.interrupt_orphaned_active_runs(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_operations.interrupt_orphaned_active_runs(bigint) TO operations_agent_operations_runtime;

RESET ROLE;

COMMIT;
