BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- Offline rollback for a successfully applied S13 migration. Alarm Service must be
-- stopped before this script runs; runtime dual-model operation is not supported.
DO $$
BEGIN
  IF to_regclass('alarm_runtime.alarm_current_pre_s13_backup') IS NULL
     OR to_regclass('alarm_runtime.alarm_idempotency_pre_s13_backup') IS NULL THEN
    RAISE EXCEPTION 'S13 pre-migration backups are missing';
  END IF;
END $$;

DROP TABLE alarm_runtime.alarm_timeline;
DROP FUNCTION alarm_runtime.reject_alarm_timeline_mutation();

DELETE FROM alarm_runtime.alarm_idempotency;
DELETE FROM alarm_runtime.alarm_current;

DROP INDEX IF EXISTS alarm_runtime.alarm_current_one_active_fingerprint_uidx;
DROP INDEX IF EXISTS alarm_runtime.alarm_current_condition_time_idx;
DROP INDEX IF EXISTS alarm_runtime.alarm_current_severity_time_idx;

ALTER TABLE alarm_runtime.alarm_current
  DROP COLUMN fingerprint,
  DROP COLUMN incident_correlation_id,
  DROP COLUMN rule_revision,
  DROP COLUMN condition,
  DROP COLUMN current_severity,
  DROP COLUMN peak_severity,
  DROP COLUMN acknowledged_at,
  DROP COLUMN acknowledged_by,
  DROP COLUMN acknowledgement_comment,
  DROP COLUMN suppression,
  DROP COLUMN cleared_at,
  DROP COLUMN links,
  ADD COLUMN severity text NOT NULL CHECK (severity IN ('INFO', 'WARNING', 'MAJOR', 'CRITICAL')),
  ADD COLUMN status text NOT NULL CHECK (status IN ('OPEN', 'ACKNOWLEDGED', 'SUPPRESSED', 'CLOSED')),
  ADD COLUMN transitions jsonb NOT NULL CHECK (jsonb_typeof(transitions) = 'array' AND jsonb_array_length(transitions) > 0),
  ADD COLUMN suppressed_until timestamptz;

ALTER TABLE alarm_runtime.alarm_current
  ADD CONSTRAINT alarm_current_suppression_consistent
    CHECK ((status = 'SUPPRESSED') = (suppressed_until IS NOT NULL));

INSERT INTO alarm_runtime.alarm_current (
  alarm_id, tenant_id, site_id, device_id, source_type, source_reference, title, summary,
  occurrence_count, first_occurred_at, last_occurred_at, evidence, version, created_at, updated_at,
  assignee_id, event_id, point_id, alarm_type, severity, status, transitions, suppressed_until
)
SELECT
  alarm_id, tenant_id, site_id, device_id, source_type, source_reference, title, summary,
  occurrence_count, first_occurred_at, last_occurred_at, evidence, version, created_at, updated_at,
  assignee_id, event_id, point_id, alarm_type, severity, status, transitions, suppressed_until
FROM alarm_runtime.alarm_current_pre_s13_backup;

INSERT INTO alarm_runtime.alarm_idempotency (
  tenant_id, site_id, alarm_id, idempotency_key, request_digest, response, created_at
)
SELECT tenant_id, site_id, alarm_id, idempotency_key, request_digest, response, created_at
FROM alarm_runtime.alarm_idempotency_pre_s13_backup;

CREATE INDEX alarm_current_site_status_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, status, last_occurred_at DESC);
CREATE INDEX alarm_current_site_severity_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, severity, last_occurred_at DESC);

REVOKE ALL ON alarm_runtime.alarm_current FROM s4_alarm_runtime;
GRANT SELECT, UPDATE (status, assignee_id, suppressed_until, evidence, transitions, version, updated_at)
  ON alarm_runtime.alarm_current TO s4_alarm_runtime;

DROP POLICY alarm_current_migrator_all ON alarm_runtime.alarm_current;
DROP POLICY alarm_idempotency_migrator_all ON alarm_runtime.alarm_idempotency;
DROP TABLE alarm_runtime.s13_alarm_identity_map;
DROP TABLE alarm_runtime.s13_alarm_migration_report;
DROP TABLE alarm_runtime.alarm_current_pre_s13_backup;
DROP TABLE alarm_runtime.alarm_idempotency_pre_s13_backup;

RESET ROLE;
COMMIT;
