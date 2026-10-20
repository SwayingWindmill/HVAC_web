BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- The scheduled pass looks at evaluations due without new telemetry, which is how a Site that
-- stopped reporting is noticed (#615). Rows stay Tenant-scoped: this tells the runtime only which
-- Tenants have one due, and it then claims them inside each Tenant's own scope.
CREATE FUNCTION alarm_runtime.tenants_with_due_evaluations(due_at timestamptz)
RETURNS SETOF uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, alarm_runtime
AS $$
  SELECT DISTINCT tenant_id
  FROM alarm_runtime.alarm_evaluation_state
  WHERE next_evaluation_at IS NOT NULL
    AND next_evaluation_at <= due_at
    AND (lease_until IS NULL OR lease_until <= due_at)
$$;

REVOKE ALL ON FUNCTION alarm_runtime.tenants_with_due_evaluations(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION alarm_runtime.tenants_with_due_evaluations(timestamptz) TO s4_alarm_runtime;

COMMIT;
