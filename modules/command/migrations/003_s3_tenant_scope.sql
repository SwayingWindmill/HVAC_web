BEGIN;
SET LOCAL ROLE s3_command_migrator;

-- Data Architecture V2 uses Tenant as the only Command partition key.
-- The former dual Organization/Tenant compatibility migration is intentionally retired.

RESET ROLE;
COMMIT;
