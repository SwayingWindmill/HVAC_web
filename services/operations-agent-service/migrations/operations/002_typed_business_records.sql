\set ON_ERROR_STOP on

BEGIN;

ALTER TABLE agent_operations.investigation_effects
  DROP CONSTRAINT IF EXISTS investigation_effects_effect_kind_check;

ALTER TABLE agent_operations.investigation_effects
  ADD CONSTRAINT investigation_effects_effect_kind_check
  CHECK (effect_kind IN (
    'EVIDENCE',
    'ANALYSIS_REFERENCE',
    'FINDING',
    'TOOL_EXECUTION_RECEIPT',
    'PROPOSED_ACTION'
  ));

ALTER TABLE agent_operations.investigation_effects
  DROP CONSTRAINT IF EXISTS investigation_effects_investigation_id_effect_kind_record_id_key;

ALTER TABLE agent_operations.investigation_effects
  DROP CONSTRAINT IF EXISTS investigation_effects_investigation_record_key;

ALTER TABLE agent_operations.investigation_effects
  ADD CONSTRAINT investigation_effects_investigation_record_key
  UNIQUE (investigation_id, record_id);

CREATE TABLE IF NOT EXISTS agent_operations.investigation_business_records (
  investigation_id text NOT NULL
    REFERENCES agent_operations.investigations(investigation_id) ON DELETE RESTRICT,
  record_id text NOT NULL CHECK (btrim(record_id) <> ''),
  record_type text NOT NULL CHECK (record_type IN (
    'EVIDENCE',
    'ANALYSIS_REFERENCE',
    'FINDING',
    'TOOL_EXECUTION_RECEIPT'
  )),
  schema_version integer NOT NULL CHECK (schema_version = 1),
  record_payload jsonb NOT NULL CHECK (jsonb_typeof(record_payload) = 'object'),
  recorded_at_ms bigint NOT NULL CHECK (recorded_at_ms >= 0),
  tool_owner text,
  tool_request_id text,
  tool_attempt_id text,
  PRIMARY KEY (investigation_id, record_id),
  CHECK (octet_length(record_payload::text) <= 65536),
  CHECK (record_payload ->> 'investigationId' = investigation_id),
  CHECK (record_payload ->> 'id' = record_id),
  CHECK (record_payload ->> 'recordType' = record_type),
  CHECK ((record_payload ->> 'schemaVersion')::integer = schema_version),
  CHECK (
    (record_type = 'TOOL_EXECUTION_RECEIPT')
    = (tool_owner IS NOT NULL AND tool_request_id IS NOT NULL AND tool_attempt_id IS NOT NULL)
  ),
  CHECK (tool_owner IS NULL OR tool_owner IN (
    'registry',
    'telemetry-query-service',
    'command-service'
  )),
  UNIQUE (investigation_id, tool_owner, tool_request_id, tool_attempt_id),
  UNIQUE (investigation_id, tool_owner, tool_attempt_id)
);

CREATE INDEX IF NOT EXISTS investigation_business_records_type_idx
  ON agent_operations.investigation_business_records (
    investigation_id,
    record_type,
    recorded_at_ms,
    record_id
  );

ALTER TABLE agent_operations.investigation_business_records
  OWNER TO operations_agent_operations_migrator;

GRANT SELECT, INSERT ON agent_operations.investigation_business_records
TO operations_agent_operations_runtime;

COMMIT;
