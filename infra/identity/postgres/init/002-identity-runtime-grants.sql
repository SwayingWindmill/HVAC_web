BEGIN;

SET LOCAL ROLE identity_migrator;

GRANT EXECUTE ON FUNCTION identity.is_uuid_v7(uuid) TO identity_runtime, identity_admin;

RESET ROLE;

COMMIT;
