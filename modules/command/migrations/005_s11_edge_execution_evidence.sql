BEGIN;
SET LOCAL ROLE s3_command_migrator;

ALTER TABLE command_runtime.connector_evidence
  ADD COLUMN edge_execution_evidence jsonb;

ALTER TABLE command_runtime.connector_evidence
  DROP CONSTRAINT IF EXISTS connector_evidence_connector_phase_check;
ALTER TABLE command_runtime.connector_evidence
  ADD CONSTRAINT connector_evidence_connector_phase_check
  CHECK (connector_phase IN ('PRE_SEND_REJECTED', 'EXECUTION_REJECTED', 'REQUEST_COMMITTED', 'ACKNOWLEDGED'));
ALTER TABLE command_runtime.connector_evidence
  ADD CONSTRAINT connector_evidence_edge_execution_object
  CHECK (edge_execution_evidence IS NULL OR jsonb_typeof(edge_execution_evidence) = 'object');

ALTER POLICY connector_evidence_runtime_org ON command_runtime.connector_evidence
  RENAME TO connector_evidence_runtime_tenant;
ALTER POLICY command_grant_uses_runtime_org ON command_runtime.command_grant_uses
  RENAME TO command_grant_uses_runtime_tenant;

RESET ROLE;
COMMIT;
