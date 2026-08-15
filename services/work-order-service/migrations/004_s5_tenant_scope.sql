BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

-- Data Architecture V2 uses Tenant as the only Work Order partition key.
-- The former dual Organization/Tenant compatibility migration is intentionally retired.

RESET ROLE;
COMMIT;
