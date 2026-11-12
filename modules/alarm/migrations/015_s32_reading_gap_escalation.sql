BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- Same as 014, plus Tenants that have an active, unacknowledged reading-gap Alarm older than 24
-- hours not yet escalated (#800). Once a device stops reporting, its silence rule has no further
-- next_evaluation_at, so the scheduled pass needs this union branch to wake the Tenant at 24h.
CREATE OR REPLACE FUNCTION alarm_runtime.tenants_with_due_evaluations(due_at timestamptz)
RETURNS SETOF uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, alarm_runtime
AS $$
  SELECT tenant_id
  FROM alarm_runtime.alarm_evaluation_state
  WHERE next_evaluation_at IS NOT NULL
    AND next_evaluation_at <= due_at
    AND (lease_until IS NULL OR lease_until <= due_at)
  UNION
  SELECT current.tenant_id
  FROM (
    SELECT DISTINCT ON (tenant_id, site_id, assignment_id) tenant_id, site_id, assignment_id, assignment_revision, policy_revision_id, retired
    FROM alarm_runtime.alarm_policy_assignment
    WHERE subject_type = 'SITE'
    ORDER BY tenant_id, site_id, assignment_id, assignment_revision DESC) AS current
  WHERE NOT current.retired
    AND EXISTS (
      SELECT 1 FROM alarm_runtime.telemetry_evaluation_input AS input
      WHERE input.tenant_id = current.tenant_id AND input.site_id = current.site_id)
    AND NOT EXISTS (
      SELECT 1 FROM alarm_runtime.alarm_evaluation_state AS state
      WHERE state.tenant_id = current.tenant_id AND state.site_id = current.site_id
        AND state.assignment_id = current.assignment_id
        AND state.policy_revision_id = current.policy_revision_id
        AND state.assignment_revision = current.assignment_revision)
  UNION
  SELECT alarm.tenant_id
  FROM alarm_runtime.alarm_current AS alarm
  WHERE alarm.condition = 'ACTIVE'
    AND alarm.acknowledged_at IS NULL
    AND alarm.current_severity IN ('WARNING', 'MAJOR')
    AND alarm.first_occurred_at <= due_at - interval '24 hours'
    AND (
      alarm.alarm_type LIKE 'reading-gap/%'
      OR alarm.alarm_type LIKE 'readings-not-updated/%'
    )
    AND NOT EXISTS (
      SELECT 1
      FROM alarm_runtime.alarm_timeline AS tl
      WHERE tl.tenant_id = alarm.tenant_id
        AND tl.site_id = alarm.site_id
        AND tl.alarm_id = alarm.alarm_id
        AND tl.reason = 'READING_GAP_UNACKNOWLEDGED_24H'
    )
$$;

COMMIT;
