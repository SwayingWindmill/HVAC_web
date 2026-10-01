BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD COLUMN IF NOT EXISTS alarm_delivery_state text,
  ADD COLUMN IF NOT EXISTS alarm_available_at timestamptz,
  ADD COLUMN IF NOT EXISTS alarm_attempts integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS alarm_last_error_code text,
  ADD COLUMN IF NOT EXISTS alarm_published_at timestamptz,
  ADD COLUMN IF NOT EXISTS alarm_claim_owner text,
  ADD COLUMN IF NOT EXISTS alarm_claim_until timestamptz;

UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_delivery_state = 'PENDING',
    alarm_available_at = available_at
WHERE subscription_id IS NULL
  AND alarm_delivery_state IS NULL;

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_alarm_delivery_state_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_alarm_delivery_state_check
  CHECK (alarm_delivery_state IS NULL OR alarm_delivery_state IN ('PENDING', 'PUBLISHED'));

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_alarm_claim_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_alarm_claim_check
  CHECK (
    (alarm_claim_owner IS NULL AND alarm_claim_until IS NULL)
    OR (alarm_claim_owner IS NOT NULL AND alarm_claim_until IS NOT NULL)
  );

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_alarm_published_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_alarm_published_check
  CHECK (
    alarm_delivery_state IS NULL
    OR (alarm_delivery_state = 'PUBLISHED' AND alarm_published_at IS NOT NULL)
    OR (alarm_delivery_state = 'PENDING' AND alarm_published_at IS NULL)
  );

CREATE INDEX IF NOT EXISTS telemetry_publication_outbox_alarm_pending_idx
  ON telemetry_runtime.telemetry_publication_outbox (alarm_available_at, alarm_claim_until, event_id)
  WHERE subscription_id IS NULL AND alarm_delivery_state = 'PENDING';

GRANT UPDATE (
  alarm_delivery_state, alarm_available_at, alarm_attempts, alarm_last_error_code,
  alarm_published_at, alarm_claim_owner, alarm_claim_until
) ON telemetry_runtime.telemetry_publication_outbox TO s2_telemetry_relay;

RESET ROLE;
COMMIT;
