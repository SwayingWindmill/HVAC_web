\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_iam_migrator;

DROP POLICY IF EXISTS memberships_migrator_name_lookup ON iam.tenant_memberships;
CREATE POLICY memberships_migrator_name_lookup ON iam.tenant_memberships
  FOR SELECT TO s1_iam_migrator
  USING (true);

CREATE OR REPLACE FUNCTION iam.resolve_tenant_principal_names(
  tenant_value uuid,
  principal_ids uuid[],
  at_value timestamptz DEFAULT now()
)
RETURNS TABLE (
  principal_id uuid,
  display_name text
)
LANGUAGE sql
STABLE
STRICT
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $$
  SELECT principal.id, principal.display_name
  FROM iam.tenant_memberships AS membership
  JOIN iam.principals AS principal ON principal.id = membership.principal_id
  WHERE membership.tenant_id = tenant_value
    AND membership.principal_id = ANY(principal_ids)
    AND membership.status = 'ACTIVE'
    AND membership.valid_from <= at_value
    AND (membership.valid_to IS NULL OR at_value < membership.valid_to)
    AND principal.status = 'ACTIVE'
$$;

REVOKE ALL ON FUNCTION iam.resolve_tenant_principal_names(uuid, uuid[], timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION iam.resolve_tenant_principal_names(uuid, uuid[], timestamptz) TO s1_iam_runtime;

RESET ROLE;
COMMIT;
