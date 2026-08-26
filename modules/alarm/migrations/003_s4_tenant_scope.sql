BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- Data Architecture V2 uses Tenant as the only Alarm partition key.
-- The former dual Organization/Tenant compatibility migration is intentionally retired.

RESET ROLE;
COMMIT;
