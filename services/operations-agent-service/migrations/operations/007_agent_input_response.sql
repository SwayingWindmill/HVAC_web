\set ON_ERROR_STOP on

BEGIN;

ALTER TABLE agent_operations.agent_artifacts
  DROP CONSTRAINT IF EXISTS agent_artifacts_kind_check;

ALTER TABLE agent_operations.agent_artifacts
  ADD CONSTRAINT agent_artifacts_kind_check
  CHECK (kind IN ('EVIDENCE_REF', 'FINDING', 'PROPOSAL', 'INPUT_REQUEST', 'INPUT_RESPONSE', 'LIMITATION'));

COMMIT;
