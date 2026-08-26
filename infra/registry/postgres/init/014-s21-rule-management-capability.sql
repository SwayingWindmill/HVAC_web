BEGIN;

SET LOCAL ROLE s1_iam_migrator;

UPDATE iam.capability_catalog_revisions
SET status = 'RETIRED'
WHERE status = 'ACTIVE';

INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT
  3,
  's21-v1',
  array_append(capabilities, 'rule.manage'),
  'ACTIVE',
  '2026-08-20T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 2;

UPDATE iam.role_templates
SET capabilities = array_append(capabilities, 'rule.manage'),
    revision = revision + 1,
    updated_at = '2026-08-20T00:00:00Z'
WHERE status = 'ACTIVE'
  AND capabilities @> ARRAY['iam.admin']::text[]
  AND NOT capabilities @> ARRAY['rule.manage']::text[];

RESET ROLE;
COMMIT;
