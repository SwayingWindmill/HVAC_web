\set ON_ERROR_STOP on

BEGIN;

ALTER TABLE agent_operations.audit_records
  ALTER COLUMN investigation_id DROP NOT NULL,
  ALTER COLUMN investigation_revision DROP NOT NULL;

ALTER TABLE agent_operations.audit_records
  ADD COLUMN IF NOT EXISTS event_id text,
  ADD COLUMN IF NOT EXISTS organization_id text,
  ADD COLUMN IF NOT EXISTS site_id text,
  ADD COLUMN IF NOT EXISTS run_id text,
  ADD COLUMN IF NOT EXISTS delivery_status text NOT NULL DEFAULT 'PENDING',
  ADD COLUMN IF NOT EXISTS attempt_count integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS next_attempt_at_ms bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS lease_until_ms bigint,
  ADD COLUMN IF NOT EXISTS delivered_at_ms bigint,
  ADD COLUMN IF NOT EXISTS last_failure_class text;

UPDATE agent_operations.audit_records
SET event_id = COALESCE(event_id, 'legacy-audit:' || audit_id::text),
    organization_id = COALESCE(organization_id, audit_payload->>'organizationId', 'legacy'),
    site_id = COALESCE(site_id, audit_payload->>'siteId', 'legacy'),
    run_id = COALESCE(run_id, audit_payload->>'runId'),
    next_attempt_at_ms = CASE
      WHEN next_attempt_at_ms = 0 THEN occurred_at_ms
      ELSE next_attempt_at_ms
    END
WHERE event_id IS NULL
   OR organization_id IS NULL
   OR site_id IS NULL
   OR next_attempt_at_ms = 0;

ALTER TABLE agent_operations.audit_records
  ALTER COLUMN event_id SET NOT NULL,
  ALTER COLUMN organization_id SET NOT NULL,
  ALTER COLUMN site_id SET NOT NULL,
  ALTER COLUMN delivery_status DROP DEFAULT;

ALTER TABLE agent_operations.audit_records
  DROP CONSTRAINT IF EXISTS audit_records_event_id_key,
  DROP CONSTRAINT IF EXISTS audit_records_delivery_status_check,
  DROP CONSTRAINT IF EXISTS audit_records_attempt_count_check,
  DROP CONSTRAINT IF EXISTS audit_records_last_failure_class_check,
  ADD CONSTRAINT audit_records_event_id_key UNIQUE (event_id),
  ADD CONSTRAINT audit_records_delivery_status_check
    CHECK (delivery_status IN ('PENDING', 'IN_FLIGHT', 'FAILED', 'DELIVERED')),
  ADD CONSTRAINT audit_records_attempt_count_check CHECK (attempt_count >= 0),
  ADD CONSTRAINT audit_records_last_failure_class_check CHECK (
    last_failure_class IS NULL
    OR last_failure_class IN ('TIMEOUT', 'UNAVAILABLE', 'REJECTED', 'INVALID_RESPONSE')
  );

CREATE INDEX IF NOT EXISTS audit_records_delivery_idx
  ON agent_operations.audit_records (
    delivery_status,
    next_attempt_at_ms,
    lease_until_ms,
    audit_id
  );

GRANT UPDATE (
  delivery_status,
  attempt_count,
  next_attempt_at_ms,
  lease_until_ms,
  delivered_at_ms,
  last_failure_class
) ON agent_operations.audit_records
TO operations_agent_operations_runtime;

COMMIT;
