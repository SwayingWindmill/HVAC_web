BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.telemetry_subscriptions
  ADD COLUMN IF NOT EXISTS subject text,
  ADD COLUMN IF NOT EXISTS subject_issuer text,
  ADD COLUMN IF NOT EXISTS session_id text,
  ADD COLUMN IF NOT EXISTS channel text,
  ADD COLUMN IF NOT EXISTS policy_revision_ref text;

UPDATE telemetry_runtime.recovery_cursors cursor_record
SET revoked_at = COALESCE(cursor_record.revoked_at, GREATEST(cursor_record.created_at, CURRENT_TIMESTAMP))
WHERE EXISTS (
  SELECT 1
  FROM telemetry_runtime.telemetry_subscriptions subscription
  WHERE subscription.subscription_id = cursor_record.subscription_id
    AND (
      subscription.subject IS NULL OR subscription.subject_issuer IS NULL
      OR subscription.session_id IS NULL OR subscription.channel IS NULL
      OR subscription.policy_revision_ref IS NULL
    )
);

UPDATE telemetry_runtime.telemetry_subscriptions
SET status = 'REVOKED',
    revoked_at = COALESCE(revoked_at, GREATEST(created_at, CURRENT_TIMESTAMP)),
    subject = COALESCE(subject, 'migration-invalidated:' || subscription_id),
    subject_issuer = COALESCE(subject_issuer, 'urn:hvac:s2-ticket-06:migration'),
    session_id = COALESCE(session_id, 'migration:' || md5(subscription_id)),
    channel = COALESCE(channel, 's2:migrated_' || md5(subscription_id)),
    policy_revision_ref = COALESCE(policy_revision_ref, policy_revision::text),
    updated_at = GREATEST(updated_at, CURRENT_TIMESTAMP)
WHERE subject IS NULL OR subject_issuer IS NULL OR session_id IS NULL OR channel IS NULL OR policy_revision_ref IS NULL;

ALTER TABLE telemetry_runtime.telemetry_subscriptions
  ALTER COLUMN subject SET NOT NULL,
  ALTER COLUMN subject_issuer SET NOT NULL,
  ALTER COLUMN session_id SET NOT NULL,
  ALTER COLUMN channel SET NOT NULL,
  ALTER COLUMN policy_revision_ref SET NOT NULL;

ALTER TABLE telemetry_runtime.telemetry_subscriptions
  DROP CONSTRAINT IF EXISTS telemetry_subscriptions_identity_check;
ALTER TABLE telemetry_runtime.telemetry_subscriptions
  ADD CONSTRAINT telemetry_subscriptions_identity_check
  CHECK (
    char_length(subject) BETWEEN 1 AND 1024
    AND char_length(subject_issuer) BETWEEN 1 AND 1024
    AND char_length(session_id) BETWEEN 1 AND 512
  );

ALTER TABLE telemetry_runtime.telemetry_subscriptions
  DROP CONSTRAINT IF EXISTS telemetry_subscriptions_channel_check;
ALTER TABLE telemetry_runtime.telemetry_subscriptions
  ADD CONSTRAINT telemetry_subscriptions_channel_check
  CHECK (char_length(channel) BETWEEN 16 AND 256 AND channel ~ '^s2:[A-Za-z0-9_-]+$');

CREATE UNIQUE INDEX IF NOT EXISTS telemetry_subscriptions_channel_uq
  ON telemetry_runtime.telemetry_subscriptions(channel);
CREATE INDEX IF NOT EXISTS telemetry_subscriptions_principal_channel_idx
  ON telemetry_runtime.telemetry_subscriptions(principal_id, channel, expires_at)
  WHERE status = 'ACTIVE';

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD COLUMN IF NOT EXISTS claim_owner text,
  ADD COLUMN IF NOT EXISTS claim_until timestamptz;

ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  DROP CONSTRAINT IF EXISTS telemetry_publication_outbox_claim_check;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox
  ADD CONSTRAINT telemetry_publication_outbox_claim_check
  CHECK (
    (claim_owner IS NULL AND claim_until IS NULL)
    OR (claim_owner IS NOT NULL AND claim_until IS NOT NULL)
  );

CREATE INDEX IF NOT EXISTS telemetry_publication_outbox_relay_idx
  ON telemetry_runtime.telemetry_publication_outbox
  (available_at, claim_until, event_id)
  WHERE delivery_state = 'PENDING';

GRANT UPDATE (
  delivery_state, available_at, attempts, last_error_code, published_at,
  claim_owner, claim_until
) ON telemetry_runtime.telemetry_publication_outbox TO s2_telemetry_relay;

RESET ROLE;
COMMIT;
