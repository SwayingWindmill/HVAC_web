BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- A Site rule published while the Site is already silent has no evaluation yet, and no reading
-- will come to make one (#615). Such a Tenant also has evaluations due: the scheduled pass
-- evaluates those rules with the Site's last readings.
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
  SELECT assignment.tenant_id
  FROM alarm_runtime.alarm_policy_assignment AS assignment
  WHERE assignment.subject_type = 'SITE'
    AND EXISTS (
      SELECT 1 FROM alarm_runtime.telemetry_evaluation_input AS input
      WHERE input.tenant_id = assignment.tenant_id AND input.site_id = assignment.site_id)
    AND NOT EXISTS (
      SELECT 1 FROM alarm_runtime.alarm_evaluation_state AS state
      WHERE state.tenant_id = assignment.tenant_id AND state.site_id = assignment.site_id
        AND state.assignment_id = assignment.assignment_id)
$$;

COMMIT;
