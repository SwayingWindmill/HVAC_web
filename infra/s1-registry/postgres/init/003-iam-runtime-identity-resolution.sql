BEGIN;

SET LOCAL ROLE s1_iam_migrator;

DROP POLICY IF EXISTS principals_migrator_identity_lookup ON iam.principals;
CREATE POLICY principals_migrator_identity_lookup ON iam.principals
  FOR SELECT TO s1_iam_migrator
  USING (true);

CREATE UNIQUE INDEX IF NOT EXISTS policies_one_active_key_uidx
  ON iam.policies (organization_id, policy_key)
  WHERE status = 'ACTIVE';

CREATE OR REPLACE FUNCTION iam.resolve_principal_identity(
  issuer_value text,
  subject_value text
)
RETURNS TABLE (
  principal_id uuid,
  principal_status text,
  principal_revision bigint
)
LANGUAGE sql
STABLE
STRICT
SECURITY DEFINER
SET search_path = pg_catalog, iam
ROWS 1
AS $$
  SELECT principal.id, principal.status, principal.revision
  FROM iam.principals AS principal
  WHERE principal.external_issuer = issuer_value
    AND principal.external_subject = subject_value
$$;

REVOKE ALL ON FUNCTION iam.resolve_principal_identity(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION iam.resolve_principal_identity(text, text) TO s1_iam_runtime;

RESET ROLE;

COMMIT;
