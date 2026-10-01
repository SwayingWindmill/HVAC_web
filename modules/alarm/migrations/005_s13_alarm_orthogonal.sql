BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- S13 is a one-shot aggregate migration. The migrator is intentionally NOBYPASSRLS,
-- so it needs explicit offline migration policies before it can back up or transform
-- FORCE-RLS runtime rows.
CREATE POLICY alarm_current_migrator_all ON alarm_runtime.alarm_current
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY alarm_idempotency_migrator_all ON alarm_runtime.alarm_idempotency
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

-- Preserve exact pre-migration data copies so rollback restores the old aggregate
-- and idempotency responses instead of running both models.
CREATE TABLE alarm_runtime.alarm_current_pre_s13_backup AS
TABLE alarm_runtime.alarm_current WITH DATA;
CREATE TABLE alarm_runtime.alarm_idempotency_pre_s13_backup AS
TABLE alarm_runtime.alarm_idempotency WITH DATA;

CREATE TABLE alarm_runtime.s13_alarm_migration_report (
  migration_id text PRIMARY KEY,
  migrated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  source_incident_count bigint NOT NULL,
  target_incident_count bigint NOT NULL,
  active_incident_count bigint NOT NULL,
  cleared_incident_count bigint NOT NULL,
  legacy_reopen_count bigint NOT NULL,
  identity_preserved boolean NOT NULL
);
CREATE TABLE alarm_runtime.s13_alarm_identity_map (
  legacy_alarm_id uuid PRIMARY KEY,
  incident_alarm_id uuid NOT NULL UNIQUE,
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  incident_correlation_id uuid NOT NULL,
  condition text NOT NULL CHECK (condition IN ('ACTIVE', 'CLEARED'))
);

DO $$
DECLARE
  reopen_count bigint;
BEGIN
  SELECT count(*) INTO reopen_count
  FROM alarm_runtime.alarm_current alarm,
       LATERAL jsonb_array_elements(alarm.transitions) transition
  WHERE transition->>'operation' = 'REOPEN';
  IF reopen_count <> 0 THEN
    RAISE EXCEPTION 'S13 migration refuses % legacy REOPEN facts; split them into distinct incidents before migration', reopen_count;
  END IF;
END $$;

ALTER TABLE alarm_runtime.alarm_current
  ADD COLUMN fingerprint text,
  ADD COLUMN incident_correlation_id uuid,
  ADD COLUMN rule_revision text,
  ADD COLUMN condition text,
  ADD COLUMN current_severity text,
  ADD COLUMN peak_severity text,
  ADD COLUMN acknowledged_at timestamptz,
  ADD COLUMN acknowledged_by text,
  ADD COLUMN acknowledgement_comment text,
  ADD COLUMN suppression jsonb,
  ADD COLUMN cleared_at timestamptz,
  ADD COLUMN links jsonb;

UPDATE alarm_runtime.alarm_current alarm
SET alarm_type = COALESCE(NULLIF(btrim(alarm.alarm_type), ''), 'LEGACY_MIGRATED'),
    incident_correlation_id = alarm.alarm_id,
    rule_revision = COALESCE(
      (
        SELECT NULLIF(btrim(transition->>'policyRevision'), '')
        FROM jsonb_array_elements(alarm.transitions) transition
        WHERE NULLIF(btrim(transition->>'policyRevision'), '') IS NOT NULL
        ORDER BY (transition->>'version')::bigint DESC
        LIMIT 1
      ),
      left(alarm.source_reference, 128)
    ),
    condition = CASE WHEN alarm.status = 'CLOSED' THEN 'CLEARED' ELSE 'ACTIVE' END,
    current_severity = alarm.severity,
    peak_severity = alarm.severity,
    acknowledged_at = (
      SELECT (transition->>'occurredAt')::timestamptz
      FROM jsonb_array_elements(alarm.transitions) transition
      WHERE transition->>'operation' = 'ACKNOWLEDGE'
      ORDER BY (transition->>'version')::bigint
      LIMIT 1
    ),
    acknowledged_by = (
      SELECT COALESCE(NULLIF(btrim(transition->>'actorId'), ''), left(alarm.source_reference, 256))
      FROM jsonb_array_elements(alarm.transitions) transition
      WHERE transition->>'operation' = 'ACKNOWLEDGE'
      ORDER BY (transition->>'version')::bigint
      LIMIT 1
    ),
    acknowledgement_comment = (
      SELECT NULLIF(transition->>'reason', '')
      FROM jsonb_array_elements(alarm.transitions) transition
      WHERE transition->>'operation' = 'ACKNOWLEDGE'
      ORDER BY (transition->>'version')::bigint
      LIMIT 1
    ),
    suppression = CASE WHEN alarm.status = 'SUPPRESSED' THEN (
      SELECT jsonb_build_object(
        'startsAt', transition->>'occurredAt',
        'expiresAt', transition->>'suppressedUntil',
        'reason', COALESCE(NULLIF(btrim(transition->>'reason'), ''), 'LEGACY_MIGRATED'),
        'actorId', COALESCE(NULLIF(btrim(transition->>'actorId'), ''), left(alarm.source_reference, 256)),
        'policyRevision', COALESCE(NULLIF(btrim(transition->>'policyRevision'), ''), left(alarm.source_reference, 128))
      )
      FROM jsonb_array_elements(alarm.transitions) transition
      WHERE transition->>'operation' = 'SUPPRESS'
      ORDER BY (transition->>'version')::bigint DESC
      LIMIT 1
    ) ELSE NULL END,
    cleared_at = CASE WHEN alarm.status = 'CLOSED' THEN alarm.updated_at ELSE NULL END,
    links =
      (CASE WHEN alarm.device_id IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(jsonb_build_object('kind','DEVICE','targetId',alarm.device_id::text)) END) ||
      (CASE WHEN alarm.event_id IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(jsonb_build_object('kind','EVENT','targetId',alarm.event_id::text)) END) ||
      (CASE WHEN alarm.point_id IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(jsonb_build_object('kind','POINT','targetId',alarm.point_id::text)) END);

UPDATE alarm_runtime.alarm_current
SET fingerprint = encode(sha256(convert_to(concat_ws(chr(31),
  tenant_id::text,
  site_id::text,
  source_type,
  btrim(source_reference),
  btrim(alarm_type),
  COALESCE(device_id::text, ''),
  COALESCE(point_id::text, '')
), 'UTF8')), 'hex');

DO $$
DECLARE
  duplicate_group_count bigint;
BEGIN
  SELECT count(*) INTO duplicate_group_count
  FROM (
    SELECT tenant_id, site_id, fingerprint
    FROM alarm_runtime.alarm_current
    WHERE condition = 'ACTIVE'
    GROUP BY tenant_id, site_id, fingerprint
    HAVING count(*) > 1
  ) duplicates;
  IF duplicate_group_count <> 0 THEN
    RAISE EXCEPTION 'S13 migration refuses % duplicate active fingerprint groups; reconcile them into one incident before migration', duplicate_group_count;
  END IF;
END $$;

INSERT INTO alarm_runtime.s13_alarm_identity_map (
  legacy_alarm_id, incident_alarm_id, fingerprint, incident_correlation_id, condition
)
SELECT alarm_id, alarm_id, fingerprint, incident_correlation_id, condition
FROM alarm_runtime.alarm_current;

ALTER TABLE alarm_runtime.alarm_current
  ALTER COLUMN alarm_type SET NOT NULL,
  ALTER COLUMN fingerprint SET NOT NULL,
  ALTER COLUMN incident_correlation_id SET NOT NULL,
  ALTER COLUMN rule_revision SET NOT NULL,
  ALTER COLUMN condition SET NOT NULL,
  ALTER COLUMN current_severity SET NOT NULL,
  ALTER COLUMN peak_severity SET NOT NULL,
  ALTER COLUMN links SET NOT NULL,
  ADD CONSTRAINT alarm_current_fingerprint_format CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  ADD CONSTRAINT alarm_current_rule_revision_check CHECK (length(btrim(rule_revision)) BETWEEN 1 AND 128),
  ADD CONSTRAINT alarm_current_condition_check CHECK (condition IN ('ACTIVE','CLEARED')),
  ADD CONSTRAINT alarm_current_current_severity_check CHECK (current_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  ADD CONSTRAINT alarm_current_peak_severity_check CHECK (peak_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  ADD CONSTRAINT alarm_current_clear_fact_check CHECK ((condition = 'ACTIVE' AND cleared_at IS NULL) OR (condition = 'CLEARED' AND cleared_at IS NOT NULL)),
  ADD CONSTRAINT alarm_current_ack_fact_check CHECK ((acknowledged_at IS NULL AND acknowledged_by IS NULL AND acknowledgement_comment IS NULL) OR (acknowledged_at IS NOT NULL AND length(btrim(acknowledged_by)) BETWEEN 1 AND 256)),
  ADD CONSTRAINT alarm_current_suppression_shape CHECK (suppression IS NULL OR (condition = 'ACTIVE' AND jsonb_typeof(suppression) = 'object')),
  ADD CONSTRAINT alarm_current_links_shape CHECK (jsonb_typeof(links) = 'array');

CREATE UNIQUE INDEX alarm_current_one_active_fingerprint_uidx
  ON alarm_runtime.alarm_current (tenant_id, site_id, fingerprint)
  WHERE condition = 'ACTIVE';
CREATE INDEX alarm_current_condition_time_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, condition, first_occurred_at DESC, alarm_id DESC);
CREATE INDEX alarm_current_severity_time_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, current_severity, first_occurred_at DESC, alarm_id DESC);

CREATE TABLE alarm_runtime.alarm_timeline (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  operation text NOT NULL CHECK (operation IN ('PUBLISH','ACKNOWLEDGE','ASSIGN','UNASSIGN','SUPPRESS','UNSUPPRESS','CLEAR')),
  condition text NOT NULL CHECK (condition IN ('ACTIVE','CLEARED')),
  reason text NOT NULL,
  actor_type text NOT NULL CHECK (length(btrim(actor_type)) BETWEEN 1 AND 64),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  assignee_id text,
  suppression jsonb,
  current_severity text NOT NULL CHECK (current_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  policy_revision text NOT NULL CHECK (length(btrim(policy_revision)) BETWEEN 1 AND 128),
  correlation_id text NOT NULL CHECK (length(btrim(correlation_id)) BETWEEN 1 AND 256),
  occurred_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, alarm_id, version),
  FOREIGN KEY (tenant_id, site_id, alarm_id)
    REFERENCES alarm_runtime.alarm_current (tenant_id, site_id, alarm_id)
);

INSERT INTO alarm_runtime.alarm_timeline (
  tenant_id, site_id, alarm_id, version, operation, condition, reason, actor_type, actor_id,
  assignee_id, suppression, current_severity, policy_revision, correlation_id, occurred_at
)
SELECT
  alarm.tenant_id,
  alarm.site_id,
  alarm.alarm_id,
  (transition.entry->>'version')::bigint,
  CASE transition.entry->>'operation' WHEN 'CLOSE' THEN 'CLEAR' ELSE COALESCE(NULLIF(transition.entry->>'operation',''),'PUBLISH') END,
  CASE WHEN transition.entry->>'toStatus' = 'CLOSED' THEN 'CLEARED' ELSE 'ACTIVE' END,
  COALESCE(transition.entry->>'reason', 'LEGACY_MIGRATED'),
  COALESCE(NULLIF(btrim(transition.entry->>'actorType'), ''), 'SYSTEM'),
  COALESCE(NULLIF(btrim(transition.entry->>'actorId'), ''), left(alarm.source_reference, 256)),
  transition.entry->>'assigneeId',
  CASE WHEN transition.entry->>'operation' = 'SUPPRESS' THEN jsonb_build_object(
    'startsAt', transition.entry->>'occurredAt',
    'expiresAt', transition.entry->>'suppressedUntil',
    'reason', COALESCE(NULLIF(btrim(transition.entry->>'reason'), ''), 'LEGACY_MIGRATED'),
    'actorId', COALESCE(NULLIF(btrim(transition.entry->>'actorId'), ''), left(alarm.source_reference, 256)),
    'policyRevision', COALESCE(NULLIF(btrim(transition.entry->>'policyRevision'), ''), alarm.rule_revision)
  ) ELSE NULL END,
  alarm.current_severity,
  COALESCE(NULLIF(btrim(transition.entry->>'policyRevision'), ''), alarm.rule_revision),
  COALESCE(NULLIF(btrim(transition.entry->>'correlationId'),''), alarm.alarm_id::text),
  (transition.entry->>'occurredAt')::timestamptz
FROM alarm_runtime.alarm_current alarm
CROSS JOIN LATERAL jsonb_array_elements(alarm.transitions) WITH ORDINALITY transition(entry, ordinal)
ORDER BY alarm.alarm_id, transition.ordinal;

CREATE OR REPLACE FUNCTION alarm_runtime.reject_alarm_timeline_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'alarm timeline is append-only';
END $$;

CREATE TRIGGER alarm_timeline_immutable
BEFORE UPDATE OR DELETE ON alarm_runtime.alarm_timeline
FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_timeline_mutation();

ALTER TABLE alarm_runtime.alarm_timeline ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_timeline FORCE ROW LEVEL SECURITY;
CREATE POLICY alarm_timeline_migrator_all ON alarm_runtime.alarm_timeline
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY alarm_timeline_runtime_select ON alarm_runtime.alarm_timeline
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_timeline_runtime_insert ON alarm_runtime.alarm_timeline
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON alarm_runtime.alarm_timeline FROM PUBLIC;
GRANT SELECT, INSERT ON alarm_runtime.alarm_timeline TO s4_alarm_runtime;

INSERT INTO alarm_runtime.s13_alarm_migration_report (
  migration_id, source_incident_count, target_incident_count, active_incident_count,
  cleared_incident_count, legacy_reopen_count, identity_preserved
)
SELECT
  'S13-2026-08-19',
  (SELECT count(*) FROM alarm_runtime.alarm_current_pre_s13_backup),
  count(*),
  count(*) FILTER (WHERE condition = 'ACTIVE'),
  count(*) FILTER (WHERE condition = 'CLEARED'),
  0,
  (SELECT count(*) FROM alarm_runtime.s13_alarm_identity_map) = (SELECT count(*) FROM alarm_runtime.alarm_current_pre_s13_backup)
    AND NOT EXISTS (
      SELECT legacy_alarm_id FROM alarm_runtime.s13_alarm_identity_map WHERE legacy_alarm_id <> incident_alarm_id
    )
FROM alarm_runtime.alarm_current;

DROP INDEX IF EXISTS alarm_runtime.alarm_current_status_time_idx;
ALTER TABLE alarm_runtime.alarm_current
  DROP COLUMN status,
  DROP COLUMN severity,
  DROP COLUMN transitions,
  DROP COLUMN suppressed_until;

REVOKE ALL ON alarm_runtime.alarm_current FROM s4_alarm_runtime;
GRANT SELECT ON alarm_runtime.alarm_current TO s4_alarm_runtime;
GRANT INSERT (
  alarm_id, tenant_id, site_id, device_id, event_id, point_id, alarm_type, fingerprint, incident_correlation_id,
  source_type, source_reference, rule_revision, title, summary, condition, current_severity, peak_severity,
  occurrence_count, first_occurred_at, last_occurred_at, evidence, links, version, created_at, updated_at
) ON alarm_runtime.alarm_current TO s4_alarm_runtime;
GRANT UPDATE (
  rule_revision, condition, current_severity, peak_severity, acknowledged_at, acknowledged_by,
  acknowledgement_comment, assignee_id, suppression, occurrence_count, last_occurred_at, cleared_at,
  evidence, links, version, updated_at
) ON alarm_runtime.alarm_current TO s4_alarm_runtime;

REVOKE ALL ON alarm_runtime.alarm_current_pre_s13_backup FROM PUBLIC, s4_alarm_runtime;
REVOKE ALL ON alarm_runtime.alarm_idempotency_pre_s13_backup FROM PUBLIC, s4_alarm_runtime;
REVOKE ALL ON alarm_runtime.s13_alarm_migration_report FROM PUBLIC, s4_alarm_runtime;
REVOKE ALL ON alarm_runtime.s13_alarm_identity_map FROM PUBLIC, s4_alarm_runtime;

RESET ROLE;
COMMIT;
