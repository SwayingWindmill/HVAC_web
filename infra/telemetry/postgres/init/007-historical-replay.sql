BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.source_observations
  DROP CONSTRAINT IF EXISTS source_observations_source_path_check;
ALTER TABLE telemetry_runtime.source_observations
  ADD CONSTRAINT source_observations_source_path_check
  CHECK (source_path IN ('WEBHOOK', 'PUSH', 'POLL', 'RECONCILIATION', 'HISTORY_REPLAY'));

ALTER TABLE telemetry_runtime.source_delivery_evidence
  DROP CONSTRAINT IF EXISTS source_delivery_evidence_source_path_check;
ALTER TABLE telemetry_runtime.source_delivery_evidence
  ADD CONSTRAINT source_delivery_evidence_source_path_check
  CHECK (source_path IN ('WEBHOOK', 'PUSH', 'POLL', 'RECONCILIATION', 'HISTORY_REPLAY'));

RESET ROLE;
COMMIT;
