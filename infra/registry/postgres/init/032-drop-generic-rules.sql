-- Generic rules are gone (ADR 0026): a rule could be released but no process ever ran one; alarms
-- and the low ΔT diagnosis keep their own rules. The rule_runtime schema goes with every table
-- 001_s20 and 002_s21 made in it, and rule.manage leaves the capability catalog and role templates
-- (014 added it; 014 stays registered because later catalog revisions build on its revision 3).
-- Where the old roles exist they lose what 001_s20 and the bootstrap granted them and can no longer
-- sign in; the roles stay, because a server's role-credentials file still names them.
\set ON_ERROR_STOP on

BEGIN;

DROP SCHEMA IF EXISTS rule_runtime CASCADE;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rule_runtime_migrator') THEN
    REVOKE USAGE ON SCHEMA iam, core_registry FROM rule_runtime_migrator;
    REVOKE REFERENCES (id) ON iam.tenants FROM rule_runtime_migrator;
    REVOKE REFERENCES (tenant_id, id) ON core_registry.sites FROM rule_runtime_migrator;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rule_runtime_runtime') THEN
    REVOKE CONNECT ON DATABASE hvac_s1 FROM rule_runtime_runtime;
    ALTER ROLE rule_runtime_runtime NOLOGIN;
  END IF;
END
$$;

SET LOCAL ROLE s1_iam_migrator;

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT 8, 'no-generic-rules-v1', array_remove(capabilities, 'rule.manage'), 'ACTIVE', '2026-10-22T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 7;

UPDATE iam.role_templates
SET capabilities = array_remove(capabilities, 'rule.manage'),
    revision = revision + 1,
    updated_at = '2026-10-22T00:00:00Z'
WHERE capabilities @> ARRAY['rule.manage']::text[];

RESET ROLE;
COMMIT;
