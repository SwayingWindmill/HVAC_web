BEGIN;
SET LOCAL ROLE s3_command_migrator;

-- Command Point identity is authoritative Registry data and cannot be inferred
-- safely from a Device/capability pair for historical rows. Require an explicit
-- backfill before tightening the contract on an already-populated database.
DO $point_backfill_guard$
BEGIN
  IF EXISTS (SELECT 1 FROM command_runtime.command_intents LIMIT 1) THEN
    RAISE EXCEPTION 'S3 canonical point identity migration requires an explicit Command point_id backfill for existing Command rows';
  END IF;
END
$point_backfill_guard$;

ALTER TABLE command_runtime.command_intents
  ADD COLUMN point_id uuid NOT NULL;

CREATE INDEX command_intents_tenant_point_sequence_idx
  ON command_runtime.command_intents (
    tenant_id,
    organization_id,
    site_id,
    point_id,
    device_command_sequence
  );

RESET ROLE;
COMMIT;
