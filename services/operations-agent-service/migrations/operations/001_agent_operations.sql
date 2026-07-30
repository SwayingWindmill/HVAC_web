\set ON_ERROR_STOP on

BEGIN;

CREATE SCHEMA IF NOT EXISTS agent_operations
  AUTHORIZATION operations_agent_operations_migrator;

REVOKE ALL ON SCHEMA agent_operations FROM PUBLIC;
GRANT USAGE ON SCHEMA agent_operations TO operations_agent_operations_runtime;

CREATE TABLE IF NOT EXISTS agent_operations.investigations (
  investigation_id text PRIMARY KEY CHECK (btrim(investigation_id) <> ''),
  revision bigint NOT NULL CHECK (revision >= 0),
  status text NOT NULL CHECK (status IN ('CREATED', 'RUNNING', 'PAUSED', 'CANCELLED', 'COMPLETED', 'FAILED')),
  active_run_id text,
  active_lease_id text,
  active_lease_expires_at_ms bigint,
  snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
  created_at_ms bigint NOT NULL,
  updated_at_ms bigint NOT NULL,
  CHECK ((active_lease_id IS NULL) = (active_lease_expires_at_ms IS NULL))
);

CREATE TABLE IF NOT EXISTS agent_operations.investigation_effects (
  investigation_id text NOT NULL REFERENCES agent_operations.investigations(investigation_id) ON DELETE RESTRICT,
  idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
  run_id text NOT NULL CHECK (btrim(run_id) <> ''),
  step_id text NOT NULL CHECK (btrim(step_id) <> ''),
  effect_kind text NOT NULL CHECK (effect_kind IN ('EVIDENCE', 'FINDING', 'PROPOSED_ACTION')),
  record_id text NOT NULL CHECK (btrim(record_id) <> ''),
  committed_at_ms bigint NOT NULL,
  PRIMARY KEY (investigation_id, idempotency_key),
  UNIQUE (investigation_id, effect_kind, record_id)
);

CREATE TABLE IF NOT EXISTS agent_operations.application_outbox (
  outbox_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  investigation_id text NOT NULL REFERENCES agent_operations.investigations(investigation_id) ON DELETE RESTRICT,
  event_type text NOT NULL CHECK (btrim(event_type) <> ''),
  investigation_revision bigint NOT NULL CHECK (investigation_revision >= 0),
  event_payload jsonb NOT NULL CHECK (jsonb_typeof(event_payload) = 'object'),
  occurred_at_ms bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS agent_operations.audit_records (
  audit_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  investigation_id text NOT NULL REFERENCES agent_operations.investigations(investigation_id) ON DELETE RESTRICT,
  action text NOT NULL CHECK (btrim(action) <> ''),
  investigation_revision bigint NOT NULL CHECK (investigation_revision >= 0),
  audit_payload jsonb NOT NULL CHECK (jsonb_typeof(audit_payload) = 'object'),
  occurred_at_ms bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX IF NOT EXISTS application_outbox_investigation_revision_idx
  ON agent_operations.application_outbox (investigation_id, investigation_revision, outbox_id);

CREATE INDEX IF NOT EXISTS audit_records_investigation_revision_idx
  ON agent_operations.audit_records (investigation_id, investigation_revision, audit_id);

ALTER TABLE agent_operations.investigations OWNER TO operations_agent_operations_migrator;
ALTER TABLE agent_operations.investigation_effects OWNER TO operations_agent_operations_migrator;
ALTER TABLE agent_operations.application_outbox OWNER TO operations_agent_operations_migrator;
ALTER TABLE agent_operations.audit_records OWNER TO operations_agent_operations_migrator;
ALTER SEQUENCE agent_operations.application_outbox_outbox_id_seq OWNER TO operations_agent_operations_migrator;
ALTER SEQUENCE agent_operations.audit_records_audit_id_seq OWNER TO operations_agent_operations_migrator;

GRANT SELECT, INSERT, UPDATE ON agent_operations.investigations
TO operations_agent_operations_runtime;

GRANT SELECT, INSERT ON
  agent_operations.investigation_effects,
  agent_operations.application_outbox,
  agent_operations.audit_records
TO operations_agent_operations_runtime;

GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA agent_operations
TO operations_agent_operations_runtime;

COMMIT;
