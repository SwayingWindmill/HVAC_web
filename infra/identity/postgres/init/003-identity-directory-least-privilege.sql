BEGIN;

SET LOCAL ROLE identity_migrator;

REVOKE SELECT ON identity.users FROM identity_directory_reader;
GRANT SELECT (id, display_name, email, status) ON identity.users TO identity_directory_reader;

RESET ROLE;

COMMIT;
