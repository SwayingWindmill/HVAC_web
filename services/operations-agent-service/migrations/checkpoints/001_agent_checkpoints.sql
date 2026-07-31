\set ON_ERROR_STOP on

BEGIN;

CREATE SCHEMA IF NOT EXISTS agent_checkpoints
  AUTHORIZATION operations_agent_checkpoints_migrator;

REVOKE ALL ON SCHEMA agent_checkpoints FROM PUBLIC;
GRANT USAGE ON SCHEMA agent_checkpoints TO operations_agent_checkpoints_runtime;

CREATE TABLE IF NOT EXISTS agent_checkpoints.runtime_checkpoints (
  checkpoint_id text PRIMARY KEY CHECK (btrim(checkpoint_id) <> ''),
  investigation_id text NOT NULL CHECK (btrim(investigation_id) <> ''),
  run_id text NOT NULL CHECK (btrim(run_id) <> ''),
  runtime_revision text NOT NULL CHECK (btrim(runtime_revision) <> ''),
  position text NOT NULL CHECK (btrim(position) <> ''),
  opaque_state text NOT NULL,
  saved_at_ms bigint NOT NULL,
  expires_at_ms bigint NOT NULL CHECK (expires_at_ms > saved_at_ms)
);

CREATE INDEX IF NOT EXISTS runtime_checkpoints_lookup_idx
  ON agent_checkpoints.runtime_checkpoints (
    investigation_id,
    run_id,
    saved_at_ms DESC,
    checkpoint_id DESC
  );

CREATE INDEX IF NOT EXISTS runtime_checkpoints_expiry_idx
  ON agent_checkpoints.runtime_checkpoints (expires_at_ms);

ALTER TABLE agent_checkpoints.runtime_checkpoints
  OWNER TO operations_agent_checkpoints_migrator;

GRANT SELECT, INSERT, DELETE ON agent_checkpoints.runtime_checkpoints
TO operations_agent_checkpoints_runtime;

ALTER DEFAULT PRIVILEGES FOR ROLE operations_agent_checkpoints_migrator IN SCHEMA agent_checkpoints
  GRANT SELECT, INSERT, DELETE ON TABLES TO operations_agent_checkpoints_runtime;

COMMIT;
