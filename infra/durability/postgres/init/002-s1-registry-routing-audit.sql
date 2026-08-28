BEGIN;

ALTER TABLE gateway.route_audit_records
  DROP CONSTRAINT IF EXISTS route_audit_records_event_type_check;

ALTER TABLE gateway.route_audit_records
  ADD CONSTRAINT route_audit_records_event_type_check
  CHECK (event_type IN (
    'ROUTE_DECIDED',
    'ROUTE_POLICY_CHANGED',
    'ROUTE_SHADOW_COMPARED',
    'ROUTE_FALLBACK_EXECUTED'
  ));

ALTER TABLE gateway.route_audit_records
  ADD COLUMN IF NOT EXISTS outcome_code text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS primary_status integer CHECK (primary_status BETWEEN 100 AND 599),
  ADD COLUMN IF NOT EXISTS secondary_status integer CHECK (secondary_status BETWEEN 100 AND 599),
  ADD COLUMN IF NOT EXISTS primary_body_sha256 text CHECK (primary_body_sha256 ~ '^[a-f0-9]{64}$'),
  ADD COLUMN IF NOT EXISTS secondary_body_sha256 text CHECK (secondary_body_sha256 ~ '^[a-f0-9]{64}$'),
  ADD COLUMN IF NOT EXISTS semantic_equal boolean;

ALTER TABLE gateway.route_audit_records
  ADD CONSTRAINT route_audit_comparison_evidence_check
  CHECK (
    event_type NOT IN ('ROUTE_SHADOW_COMPARED', 'ROUTE_FALLBACK_EXECUTED')
    OR (
      previous_owner <> ''
      AND outcome_code <> ''
      AND primary_status IS NOT NULL
      AND secondary_status IS NOT NULL
      AND primary_body_sha256 IS NOT NULL
      AND secondary_body_sha256 IS NOT NULL
      AND (event_type <> 'ROUTE_SHADOW_COMPARED' OR semantic_equal IS NOT NULL)
    )
  );

COMMIT;
