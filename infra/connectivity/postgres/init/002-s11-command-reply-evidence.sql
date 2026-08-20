\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.command_reply_correlations
  ADD COLUMN reply_execution_evidence jsonb;

ALTER TABLE connectivity.command_reply_correlations
  ADD CONSTRAINT connectivity_command_reply_execution_evidence_object
  CHECK (reply_execution_evidence IS NULL OR jsonb_typeof(reply_execution_evidence) = 'object');

COMMIT;
