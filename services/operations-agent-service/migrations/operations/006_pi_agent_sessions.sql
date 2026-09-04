\set ON_ERROR_STOP on

BEGIN;

CREATE TABLE IF NOT EXISTS agent_operations.agent_sessions (
  session_id text PRIMARY KEY CHECK (btrim(session_id) <> ''),
  tenant_id text NOT NULL CHECK (btrim(tenant_id) <> ''),
  site_id text NOT NULL CHECK (btrim(site_id) <> ''),
  agent_definition_id text NOT NULL CHECK (btrim(agent_definition_id) <> ''),
  created_by text NOT NULL CHECK (btrim(created_by) <> ''),
  revision bigint NOT NULL CHECK (revision >= 0),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'WAITING_FOR_INPUT', 'COMPLETED', 'FAILED', 'CANCELLED')),
  active_run_id text,
  created_at_ms bigint NOT NULL CHECK (created_at_ms >= 0),
  updated_at_ms bigint NOT NULL CHECK (updated_at_ms >= 0),
  CHECK ((status = 'ACTIVE') = (active_run_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS agent_operations.agent_runs (
  session_id text NOT NULL REFERENCES agent_operations.agent_sessions(session_id) ON DELETE RESTRICT,
  run_id text NOT NULL CHECK (btrim(run_id) <> ''),
  model_provider text NOT NULL CHECK (btrim(model_provider) <> ''),
  model_id text NOT NULL CHECK (btrim(model_id) <> ''),
  status text NOT NULL CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED')),
  started_at_ms bigint NOT NULL CHECK (started_at_ms >= 0),
  finished_at_ms bigint CHECK (finished_at_ms IS NULL OR finished_at_ms >= started_at_ms),
  input_tokens bigint NOT NULL CHECK (input_tokens >= 0),
  output_tokens bigint NOT NULL CHECK (output_tokens >= 0),
  model_calls bigint NOT NULL CHECK (model_calls >= 0),
  tool_calls bigint NOT NULL CHECK (tool_calls >= 0),
  failure_code text,
  PRIMARY KEY (session_id, run_id),
  CHECK ((status = 'RUNNING') = (finished_at_ms IS NULL)),
  CHECK ((status IN ('RUNNING', 'COMPLETED')) = (failure_code IS NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS agent_runs_one_running_per_session_idx
  ON agent_operations.agent_runs (session_id)
  WHERE status = 'RUNNING';

DO $pi06$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'agent_sessions_active_run_fk'
      AND conrelid = 'agent_operations.agent_sessions'::regclass
  ) THEN
    ALTER TABLE agent_operations.agent_sessions
      ADD CONSTRAINT agent_sessions_active_run_fk
      FOREIGN KEY (session_id, active_run_id)
      REFERENCES agent_operations.agent_runs (session_id, run_id)
      DEFERRABLE INITIALLY DEFERRED;
  END IF;
END
$pi06$;

CREATE TABLE IF NOT EXISTS agent_operations.agent_messages (
  message_id text PRIMARY KEY CHECK (btrim(message_id) <> ''),
  session_id text NOT NULL REFERENCES agent_operations.agent_sessions(session_id) ON DELETE RESTRICT,
  run_id text,
  role text NOT NULL CHECK (role IN ('OPERATOR', 'ASSISTANT')),
  content text NOT NULL CHECK (btrim(content) <> ''),
  created_at_ms bigint NOT NULL CHECK (created_at_ms >= 0),
  FOREIGN KEY (session_id, run_id)
    REFERENCES agent_operations.agent_runs (session_id, run_id)
    DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS agent_messages_session_created_idx
  ON agent_operations.agent_messages (session_id, created_at_ms, message_id);

CREATE TABLE IF NOT EXISTS agent_operations.agent_tool_executions (
  tool_execution_id text PRIMARY KEY CHECK (btrim(tool_execution_id) <> ''),
  session_id text NOT NULL,
  run_id text NOT NULL,
  tool_name text NOT NULL CHECK (btrim(tool_name) <> ''),
  arguments_digest text NOT NULL CHECK (btrim(arguments_digest) <> ''),
  status text NOT NULL CHECK (status IN ('COMPLETED', 'FAILED', 'CANCELLED')),
  started_at_ms bigint NOT NULL CHECK (started_at_ms >= 0),
  finished_at_ms bigint NOT NULL CHECK (finished_at_ms >= started_at_ms),
  result_summary text,
  provenance jsonb NOT NULL CHECK (jsonb_typeof(provenance) = 'array'),
  failure_code text,
  FOREIGN KEY (session_id, run_id)
    REFERENCES agent_operations.agent_runs (session_id, run_id)
    ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS agent_tool_executions_run_idx
  ON agent_operations.agent_tool_executions (session_id, run_id, started_at_ms, tool_execution_id);

CREATE TABLE IF NOT EXISTS agent_operations.agent_artifacts (
  artifact_id text PRIMARY KEY CHECK (btrim(artifact_id) <> ''),
  session_id text NOT NULL,
  run_id text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('EVIDENCE_REF', 'FINDING', 'PROPOSAL', 'INPUT_REQUEST', 'LIMITATION')),
  artifact_payload jsonb NOT NULL CHECK (jsonb_typeof(artifact_payload) = 'object'),
  created_at_ms bigint NOT NULL CHECK (created_at_ms >= 0),
  FOREIGN KEY (session_id, run_id)
    REFERENCES agent_operations.agent_runs (session_id, run_id)
    ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS agent_artifacts_run_idx
  ON agent_operations.agent_artifacts (session_id, run_id, created_at_ms, artifact_id);

ALTER TABLE agent_operations.agent_sessions OWNER TO operations_agent_operations_migrator;
ALTER TABLE agent_operations.agent_runs OWNER TO operations_agent_operations_migrator;
ALTER TABLE agent_operations.agent_messages OWNER TO operations_agent_operations_migrator;
ALTER TABLE agent_operations.agent_tool_executions OWNER TO operations_agent_operations_migrator;
ALTER TABLE agent_operations.agent_artifacts OWNER TO operations_agent_operations_migrator;

GRANT SELECT, INSERT, UPDATE ON
  agent_operations.agent_sessions,
  agent_operations.agent_runs
TO operations_agent_operations_runtime;

GRANT SELECT, INSERT ON
  agent_operations.agent_messages,
  agent_operations.agent_tool_executions,
  agent_operations.agent_artifacts
TO operations_agent_operations_runtime;

COMMIT;
