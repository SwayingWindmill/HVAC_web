BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.presence_policies
  ADD COLUMN IF NOT EXISTS max_future_clock_skew_seconds integer NOT NULL DEFAULT 30
  CHECK (max_future_clock_skew_seconds BETWEEN 0 AND 3600);
ALTER TABLE telemetry_runtime.presence_policies
  ADD COLUMN IF NOT EXISTS max_source_lag_seconds integer NOT NULL DEFAULT 600
  CHECK (max_source_lag_seconds BETWEEN 1 AND 604800);

ALTER TABLE telemetry_runtime.freshness_policies
  ADD COLUMN IF NOT EXISTS expected_sample_interval_seconds integer
  CHECK (expected_sample_interval_seconds IS NULL OR expected_sample_interval_seconds BETWEEN 1 AND 604800);
ALTER TABLE telemetry_runtime.freshness_policies
  ADD COLUMN IF NOT EXISTS value_type text
  CHECK (value_type IS NULL OR value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON'));
ALTER TABLE telemetry_runtime.freshness_policies
  ADD COLUMN IF NOT EXISTS expected_unit text
  CHECK (expected_unit IS NULL OR char_length(expected_unit) BETWEEN 1 AND 64);
ALTER TABLE telemetry_runtime.freshness_policies
  ADD COLUMN IF NOT EXISTS minimum_number double precision;
ALTER TABLE telemetry_runtime.freshness_policies
  ADD COLUMN IF NOT EXISTS maximum_number double precision;
ALTER TABLE telemetry_runtime.freshness_policies
  ADD CONSTRAINT freshness_policies_numeric_range_check
  CHECK (minimum_number IS NULL OR maximum_number IS NULL OR minimum_number <= maximum_number) NOT VALID;
ALTER TABLE telemetry_runtime.freshness_policies
  VALIDATE CONSTRAINT freshness_policies_numeric_range_check;
ALTER TABLE telemetry_runtime.freshness_policies
  ADD CONSTRAINT freshness_policies_configured_contract_check
  CHECK (
    NOT configured
    OR (
      expected_sample_interval_seconds IS NOT NULL
      AND fresh_within_seconds >= expected_sample_interval_seconds
      AND value_type IS NOT NULL
      AND ((value_type = 'NUMBER') OR (minimum_number IS NULL AND maximum_number IS NULL))
    )
  ) NOT VALID;
-- Leave the configured contract NOT VALID during the expand phase. Existing rows
-- are interpreted fail closed by the runtime until Core reconciliation supplies
-- the typed policy fields; a later contract phase may validate the constraint.

ALTER TABLE telemetry_runtime.source_observations
  ADD COLUMN IF NOT EXISTS source_path text;
UPDATE telemetry_runtime.source_observations
SET source_path = 'WEBHOOK'
WHERE source_path IS NULL;
ALTER TABLE telemetry_runtime.source_observations
  ALTER COLUMN source_path SET NOT NULL;
ALTER TABLE telemetry_runtime.source_observations
  ADD CONSTRAINT source_observations_source_path_check
  CHECK (source_path IN ('WEBHOOK', 'PUSH', 'POLL', 'RECONCILIATION')) NOT VALID;
ALTER TABLE telemetry_runtime.source_observations
  VALIDATE CONSTRAINT source_observations_source_path_check;

ALTER TABLE telemetry_runtime.source_observations
  ADD COLUMN IF NOT EXISTS quality text;
UPDATE telemetry_runtime.source_observations
SET quality = CASE WHEN acceptance_status = 'ACCEPTED' THEN 'GOOD' ELSE 'INVALID' END
WHERE quality IS NULL;
ALTER TABLE telemetry_runtime.source_observations
  ALTER COLUMN quality SET NOT NULL;
ALTER TABLE telemetry_runtime.source_observations
  ADD CONSTRAINT source_observations_quality_check
  CHECK (quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL', 'STALE', 'INVALID')) NOT VALID;
ALTER TABLE telemetry_runtime.source_observations
  VALIDATE CONSTRAINT source_observations_quality_check;
-- Ingest acceptance and telemetry quality are independent V2 dimensions.
-- A duplicate/out-of-order delivery decision must not redefine the quality
-- classification of an otherwise valid historical fact.

-- Ticket 01's coarse clock guard predates acceptance evidence. Future-clock
-- candidates must now be persisted as bounded REJECTED evidence regardless of
-- how far the source clock is ahead, so remove that legacy write-time limit.
DO $drop_future_clock_guard$
DECLARE
  constraint_name text;
BEGIN
  FOR constraint_name IN
    SELECT conname
    FROM pg_constraint
    WHERE conrelid = 'telemetry_runtime.source_observations'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) ILIKE '%received_at%sampled_at%24%'
  LOOP
    EXECUTE format(
      'ALTER TABLE telemetry_runtime.source_observations DROP CONSTRAINT %I',
      constraint_name
    );
  END LOOP;
END $drop_future_clock_guard$;

ALTER TABLE telemetry_runtime.ingest_quarantine
  DROP CONSTRAINT IF EXISTS ingest_quarantine_reason_code_check;
ALTER TABLE telemetry_runtime.ingest_quarantine
  ADD CONSTRAINT ingest_quarantine_reason_code_check
  CHECK (reason_code IN (
    'MAPPING_NOT_FOUND', 'MAPPING_CONFLICT', 'MAPPING_QUARANTINED', 'MAPPING_RETIRED',
    'POLICY_NOT_CONFIGURED', 'SOURCE_UNTRUSTED', 'TYPE_MISMATCH', 'UNIT_MISMATCH',
    'OUT_OF_RANGE', 'CLOCK_AHEAD', 'CLOCK_BEHIND'
  ));

CREATE INDEX IF NOT EXISTS source_observations_receipt_idx
  ON telemetry_runtime.source_observations
  (integration_instance_id, source_partition, source_offset, source_event_id, acceptance_status);
CREATE INDEX IF NOT EXISTS source_observations_rejected_device_key_idx
  ON telemetry_runtime.source_observations (device_id, telemetry_key, created_at DESC)
  WHERE acceptance_status = 'REJECTED' AND device_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS telemetry_runtime.source_delivery_evidence (
  evidence_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(evidence_id)),
  integration_instance_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(integration_instance_id)),
  source_event_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(source_event_id)),
  source_partition text NOT NULL CHECK (char_length(source_partition) BETWEEN 1 AND 256),
  source_offset bigint NOT NULL CHECK (source_offset >= 0),
  source_path text NOT NULL CHECK (source_path IN ('WEBHOOK', 'PUSH', 'POLL', 'RECONCILIATION')),
  delivery_status text NOT NULL CHECK (delivery_status IN ('DUPLICATE', 'OUT_OF_ORDER')),
  quality_reason text NOT NULL CHECK (quality_reason IN ('DUPLICATE', 'OUT_OF_ORDER', 'REPLAYED')),
  payload_sha256 text NOT NULL CHECK (char_length(payload_sha256) = 64 AND payload_sha256 !~ '[^a-f0-9]'),
  detected_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS source_delivery_evidence_position_idx
  ON telemetry_runtime.source_delivery_evidence
  (integration_instance_id, source_partition, source_offset, detected_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS source_delivery_evidence_identity_uidx
  ON telemetry_runtime.source_delivery_evidence (
    integration_instance_id, source_event_id, source_partition, source_offset,
    delivery_status, quality_reason, payload_sha256
  );
CREATE UNIQUE INDEX IF NOT EXISTS ingest_quarantine_open_coverage_identity_uidx
  ON telemetry_runtime.ingest_quarantine (
    integration_instance_id, external_entity_type, external_id, reason_code,
    (evidence ->> 'sourceRevision')
  )
  WHERE resolved_at IS NULL AND evidence ->> 'kind' = 'OBSERVATION_COVERAGE_REPORT';

ALTER TABLE telemetry_runtime.source_delivery_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.source_delivery_evidence FORCE ROW LEVEL SECURITY;
CREATE POLICY source_delivery_evidence_migrator_all
  ON telemetry_runtime.source_delivery_evidence FOR ALL TO s2_telemetry_migrator
  USING (true) WITH CHECK (true);
CREATE POLICY source_delivery_evidence_runtime_all
  ON telemetry_runtime.source_delivery_evidence FOR ALL TO s2_telemetry_runtime
  USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON telemetry_runtime.source_delivery_evidence TO s2_telemetry_runtime;
REVOKE ALL ON telemetry_runtime.source_delivery_evidence FROM PUBLIC;

RESET ROLE;
COMMIT;
