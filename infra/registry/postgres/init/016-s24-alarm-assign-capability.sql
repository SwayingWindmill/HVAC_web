BEGIN;

SET LOCAL ROLE s1_iam_migrator;

ALTER TABLE iam.alarm_permissions
  DROP CONSTRAINT IF EXISTS alarm_permissions_action_check;
ALTER TABLE iam.alarm_permissions
  ADD CONSTRAINT alarm_permissions_action_check
  CHECK (action IN ('alarm:read', 'alarm:ack', 'alarm:assign'));

ALTER TABLE iam.alarm_authorization_decisions
  DROP CONSTRAINT IF EXISTS alarm_authorization_decisions_action_check;
ALTER TABLE iam.alarm_authorization_decisions
  ADD CONSTRAINT alarm_authorization_decisions_action_check
  CHECK (action IN ('alarm:read', 'alarm:ack', 'alarm:assign'));

ALTER TABLE iam.alarm_authorization_decisions
  DROP CONSTRAINT IF EXISTS alarm_authorization_decisions_check;
ALTER TABLE iam.alarm_authorization_decisions
  ADD CONSTRAINT alarm_authorization_decisions_alarm_scope_check
  CHECK (
    action = 'alarm:read'
    OR (action IN ('alarm:ack', 'alarm:assign') AND alarm_id IS NOT NULL)
  );

UPDATE iam.capability_catalog_revisions
SET status = 'RETIRED'
WHERE status = 'ACTIVE';

INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT
  4,
  's24-alarm-assign-v1',
  array_append(capabilities, 'alarm:assign'),
  'ACTIVE',
  '2026-09-07T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 3;

RESET ROLE;
COMMIT;
