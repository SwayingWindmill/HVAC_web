-- API credentials are gone: the Gateway never served their routes and nothing issued or checked
-- one. Their tables, the api-credential.manage capability and the expiry scan of them go with it.
-- Dropping iam.api_credentials also drops its maintenance_runtime grant and expiry policy (013).
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_iam_migrator;

DROP TABLE iam.api_credentials;
DROP TABLE iam.service_accounts;

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT 7, 'no-api-credentials-v1', array_remove(capabilities, 'api-credential.manage'), 'ACTIVE', '2026-10-21T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 6;

UPDATE iam.role_templates
SET capabilities = array_remove(capabilities, 'api-credential.manage'),
    revision = revision + 1,
    updated_at = '2026-10-21T00:00:00Z'
WHERE capabilities @> ARRAY['api-credential.manage']::text[];

RESET ROLE;
SET LOCAL ROLE s1_core_migrator;

-- The table has no policy for its owner, so lift FORCE for this one update.
ALTER TABLE core_registry.maintenance_work_types NO FORCE ROW LEVEL SECURITY;
UPDATE core_registry.maintenance_work_types
SET description = 'Find expiring gateway mTLS certificates and create actionable maintenance events.'
WHERE job_type = 'CERTIFICATE_EXPIRY_SCAN';
ALTER TABLE core_registry.maintenance_work_types FORCE ROW LEVEL SECURITY;

RESET ROLE;
COMMIT;
