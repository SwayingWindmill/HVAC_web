BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- A Site's rules file is the whole set of its rules (#693): publishing it retires each rule the
-- file no longer has. Retiring appends an assignment revision marked retired; the latest revision
-- of an assignment decides, so publishing the rule again brings it back.
ALTER TABLE alarm_runtime.alarm_policy_assignment
  ADD COLUMN retired boolean NOT NULL DEFAULT false;

-- Same as 013, except a Site rule whose latest assignment revision is retired is not awaiting
-- evaluation.
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
    SELECT DISTINCT ON (tenant_id, site_id, assignment_id) tenant_id, site_id, assignment_id, policy_revision_id, retired
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
        AND state.policy_revision_id = current.policy_revision_id)
$$;

COMMIT;
