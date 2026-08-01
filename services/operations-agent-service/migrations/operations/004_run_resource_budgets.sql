\set ON_ERROR_STOP on

BEGIN;

CREATE TABLE IF NOT EXISTS agent_operations.run_resource_budgets (
  investigation_id TEXT NOT NULL REFERENCES agent_operations.investigations(investigation_id) ON DELETE CASCADE,
  run_id TEXT NOT NULL,
  policy_revision TEXT NOT NULL,
  started_at_ms BIGINT NOT NULL CHECK (started_at_ms >= 0),
  limit_model_invocations BIGINT NOT NULL CHECK (limit_model_invocations > 0),
  limit_tool_requests BIGINT NOT NULL CHECK (limit_tool_requests > 0),
  limit_wall_clock_ms BIGINT NOT NULL CHECK (limit_wall_clock_ms > 0),
  limit_query_range_ms BIGINT NOT NULL CHECK (limit_query_range_ms > 0),
  limit_query_buckets BIGINT NOT NULL CHECK (limit_query_buckets > 0),
  limit_owner_records BIGINT NOT NULL CHECK (limit_owner_records > 0),
  limit_payload_bytes BIGINT NOT NULL CHECK (limit_payload_bytes > 0),
  model_invocations BIGINT NOT NULL DEFAULT 0 CHECK (model_invocations >= 0),
  tool_requests BIGINT NOT NULL DEFAULT 0 CHECK (tool_requests >= 0),
  maximum_query_range_ms BIGINT NOT NULL DEFAULT 0 CHECK (maximum_query_range_ms >= 0),
  query_buckets BIGINT NOT NULL DEFAULT 0 CHECK (query_buckets >= 0),
  owner_records BIGINT NOT NULL DEFAULT 0 CHECK (owner_records >= 0),
  payload_bytes BIGINT NOT NULL DEFAULT 0 CHECK (payload_bytes >= 0),
  exhausted_dimension TEXT NULL CHECK (exhausted_dimension IS NULL OR exhausted_dimension IN (
    'MODEL_INVOCATIONS',
    'TOOL_REQUESTS',
    'WALL_CLOCK_MS',
    'QUERY_RANGE_MS',
    'QUERY_BUCKETS',
    'OWNER_RECORDS',
    'PAYLOAD_BYTES'
  )),
  exhausted_at_ms BIGINT NULL CHECK (exhausted_at_ms IS NULL OR exhausted_at_ms >= 0),
  exhausted_consumed BIGINT NULL CHECK (exhausted_consumed IS NULL OR exhausted_consumed >= 0),
  exhausted_limit BIGINT NULL CHECK (exhausted_limit IS NULL OR exhausted_limit > 0),
  exhausted_outcome TEXT NULL CHECK (exhausted_outcome IS NULL OR exhausted_outcome IN (
    'PARTIAL',
    'UNABLE_TO_CONCLUDE'
  )),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (investigation_id, run_id),
  CONSTRAINT run_resource_budget_exhaustion_shape CHECK (
    (exhausted_dimension IS NULL
      AND exhausted_at_ms IS NULL
      AND exhausted_consumed IS NULL
      AND exhausted_limit IS NULL
      AND exhausted_outcome IS NULL)
    OR
    (exhausted_dimension IS NOT NULL
      AND exhausted_at_ms IS NOT NULL
      AND exhausted_consumed IS NOT NULL
      AND exhausted_limit IS NOT NULL
      AND exhausted_outcome IS NOT NULL)
  )
);

CREATE TABLE IF NOT EXISTS agent_operations.run_resource_budget_operations (
  investigation_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  operation_id TEXT NOT NULL CHECK (char_length(operation_id) BETWEEN 1 AND 256),
  accepted_at_ms BIGINT NOT NULL CHECK (accepted_at_ms >= 0),
  PRIMARY KEY (investigation_id, run_id, operation_id),
  FOREIGN KEY (investigation_id, run_id)
    REFERENCES agent_operations.run_resource_budgets(investigation_id, run_id)
    ON DELETE CASCADE
);

GRANT SELECT, INSERT, UPDATE ON agent_operations.run_resource_budgets
  TO operations_agent_operations_runtime;
GRANT SELECT, INSERT ON agent_operations.run_resource_budget_operations
  TO operations_agent_operations_runtime;

COMMIT;
