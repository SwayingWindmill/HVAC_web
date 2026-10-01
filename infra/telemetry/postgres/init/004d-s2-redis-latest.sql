BEGIN;

SET LOCAL ROLE s2_telemetry_migrator;

-- Redis Latest is a rebuildable, non-authoritative projection. Its delivery
-- lifecycle is independent from Centrifugo publication even though both use
-- the same committed device-snapshot outbox row.
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD COLUMN IF NOT EXISTS latest_cache_state text NOT NULL DEFAULT 'NOT_APPLICABLE',
  ADD COLUMN IF NOT EXISTS latest_cache_available_at timestamptz,
  ADD COLUMN IF NOT EXISTS latest_cache_attempts integer NOT NULL DEFAULT 0
    CHECK (latest_cache_attempts >= 0),
  ADD COLUMN IF NOT EXISTS latest_cache_last_error_code text,
  ADD COLUMN IF NOT EXISTS latest_cache_materialized_at timestamptz;

UPDATE telemetry_runtime.telemetry_publication_outbox
SET latest_cache_state = CASE WHEN subscription_id IS NULL THEN 'PENDING' ELSE 'NOT_APPLICABLE' END,
    latest_cache_available_at = available_at
WHERE latest_cache_available_at IS NULL;

CREATE OR REPLACE FUNCTION telemetry_runtime.initialize_latest_cache_outbox()
RETURNS trigger
LANGUAGE plpgsql
AS $latest_cache_outbox$
BEGIN
  NEW.latest_cache_available_at := coalesce(NEW.latest_cache_available_at, NEW.available_at);
  IF NEW.subscription_id IS NULL THEN
    NEW.latest_cache_state := 'PENDING';
    NEW.latest_cache_materialized_at := NULL;
  ELSE
    NEW.latest_cache_state := 'NOT_APPLICABLE';
    NEW.latest_cache_materialized_at := NULL;
  END IF;
  RETURN NEW;
END
$latest_cache_outbox$;

DROP TRIGGER IF EXISTS telemetry_publication_outbox_initialize_latest_cache ON telemetry_runtime.telemetry_publication_outbox;
CREATE TRIGGER telemetry_publication_outbox_initialize_latest_cache
BEFORE INSERT ON telemetry_runtime.telemetry_publication_outbox
FOR EACH ROW EXECUTE FUNCTION telemetry_runtime.initialize_latest_cache_outbox();

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ALTER COLUMN latest_cache_available_at SET NOT NULL;

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_latest_cache_value_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_latest_cache_value_check
  CHECK (latest_cache_state IN ('NOT_APPLICABLE', 'PENDING', 'MATERIALIZED'));

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_latest_cache_materialized_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_latest_cache_materialized_check
  CHECK (
    (latest_cache_state = 'MATERIALIZED' AND latest_cache_materialized_at IS NOT NULL)
    OR (latest_cache_state IN ('NOT_APPLICABLE', 'PENDING') AND latest_cache_materialized_at IS NULL)
  );

CREATE INDEX IF NOT EXISTS telemetry_publication_outbox_latest_cache_pending_idx
  ON telemetry_runtime.telemetry_publication_outbox (latest_cache_available_at, event_id)
  WHERE latest_cache_state = 'PENDING';

GRANT UPDATE (
  latest_cache_state,
  latest_cache_available_at,
  latest_cache_attempts,
  latest_cache_last_error_code,
  latest_cache_materialized_at
) ON telemetry_runtime.telemetry_publication_outbox TO s2_telemetry_relay;

RESET ROLE;
COMMIT;
