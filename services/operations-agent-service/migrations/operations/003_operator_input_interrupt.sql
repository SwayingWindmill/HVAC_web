\set ON_ERROR_STOP on

BEGIN;

ALTER TABLE agent_operations.investigations
  DROP CONSTRAINT IF EXISTS investigations_status_check;

ALTER TABLE agent_operations.investigations
  ADD CONSTRAINT investigations_status_check
  CHECK (status IN (
    'CREATED',
    'RUNNING',
    'PAUSED',
    'WAITING_FOR_OPERATOR_INPUT',
    'CANCELLED',
    'COMPLETED',
    'FAILED'
  ));

ALTER TABLE agent_operations.investigation_business_records
  DROP CONSTRAINT IF EXISTS investigation_business_records_record_type_check;

ALTER TABLE agent_operations.investigation_business_records
  ADD CONSTRAINT investigation_business_records_record_type_check
  CHECK (record_type IN (
    'EVIDENCE',
    'ANALYSIS_REFERENCE',
    'FINDING',
    'TOOL_EXECUTION_RECEIPT',
    'OPERATOR_INPUT_ACCEPTED'
  ));

CREATE UNIQUE INDEX IF NOT EXISTS investigation_operator_input_request_idx
  ON agent_operations.investigation_business_records (
    investigation_id,
    (record_payload ->> 'requestId')
  )
  WHERE record_type = 'OPERATOR_INPUT_ACCEPTED';

CREATE UNIQUE INDEX IF NOT EXISTS investigation_operator_input_idempotency_idx
  ON agent_operations.investigation_business_records (
    investigation_id,
    (record_payload ->> 'idempotencyKey')
  )
  WHERE record_type = 'OPERATOR_INPUT_ACCEPTED';

COMMIT;
