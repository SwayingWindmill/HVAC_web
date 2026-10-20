BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- Readings evaluate only the rules reading their device (#659), so a new version of a rule whose
-- device is silent has no evaluation with that version, and no reading will come to make one.
-- Such a Tenant also has evaluations due: the scheduled pass evaluates each Site rule not yet
-- evaluated with the version assigned to it, with the Site's last readings.
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
    SELECT DISTINCT ON (tenant_id, site_id, assignment_id) tenant_id, site_id, assignment_id, policy_revision_id
    FROM alarm_runtime.alarm_policy_assignment
    WHERE subject_type = 'SITE'
    ORDER BY tenant_id, site_id, assignment_id, assignment_revision DESC) AS current
  WHERE EXISTS (
      SELECT 1 FROM alarm_runtime.telemetry_evaluation_input AS input
      WHERE input.tenant_id = current.tenant_id AND input.site_id = current.site_id)
    AND NOT EXISTS (
      SELECT 1 FROM alarm_runtime.alarm_evaluation_state AS state
      WHERE state.tenant_id = current.tenant_id AND state.site_id = current.site_id
        AND state.assignment_id = current.assignment_id
        AND state.policy_revision_id = current.policy_revision_id)
$$;

COMMIT;
