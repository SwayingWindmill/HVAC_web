/*
  告警抑制与取消指派权限扩展（P1）：
  扩展告警权限与决策审计约束以支持 alarm:unassign、alarm:suppress、alarm:unsuppress，
  发布权限能力目录第 11 版并同步至携带 alarm:assign 的角色模板。
*/
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_iam_migrator;

ALTER TABLE IF EXISTS iam.alarm_permissions
  DROP CONSTRAINT IF EXISTS alarm_permissions_action_check;
ALTER TABLE IF EXISTS iam.alarm_permissions
  ADD CONSTRAINT alarm_permissions_action_check
  CHECK (action IN ('alarm:read', 'alarm:ack', 'alarm:assign', 'alarm:unassign', 'alarm:suppress', 'alarm:unsuppress'));

ALTER TABLE iam.alarm_authorization_decisions
  DROP CONSTRAINT IF EXISTS alarm_authorization_decisions_action_check;
ALTER TABLE iam.alarm_authorization_decisions
  ADD CONSTRAINT alarm_authorization_decisions_action_check
  CHECK (action IN ('alarm:read', 'alarm:ack', 'alarm:assign', 'alarm:unassign', 'alarm:suppress', 'alarm:unsuppress'));

ALTER TABLE iam.alarm_authorization_decisions
  DROP CONSTRAINT IF EXISTS alarm_authorization_decisions_alarm_scope_check;
ALTER TABLE iam.alarm_authorization_decisions
  DROP CONSTRAINT IF EXISTS alarm_authorization_decisions_check;
ALTER TABLE iam.alarm_authorization_decisions
  ADD CONSTRAINT alarm_authorization_decisions_alarm_scope_check
  CHECK (
    action = 'alarm:read'
    OR (action IN ('alarm:ack', 'alarm:assign', 'alarm:unassign', 'alarm:suppress', 'alarm:unsuppress') AND alarm_id IS NOT NULL)
  );

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT 11, 'alarm-lifecycle-p1-v1', capabilities || ARRAY['alarm:unassign', 'alarm:suppress', 'alarm:unsuppress']::text[], 'ACTIVE', '2026-11-17T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 10;

UPDATE iam.role_templates
SET capabilities = capabilities
      || CASE WHEN capabilities @> ARRAY['alarm:unassign']::text[] THEN ARRAY[]::text[] ELSE ARRAY['alarm:unassign']::text[] END
      || CASE WHEN capabilities @> ARRAY['alarm:suppress']::text[] THEN ARRAY[]::text[] ELSE ARRAY['alarm:suppress']::text[] END
      || CASE WHEN capabilities @> ARRAY['alarm:unsuppress']::text[] THEN ARRAY[]::text[] ELSE ARRAY['alarm:unsuppress']::text[] END,
    revision = revision + 1,
    updated_at = '2026-11-17T00:00:00Z'
WHERE capabilities @> ARRAY['alarm:assign']::text[]
  AND NOT (capabilities @> ARRAY['alarm:unassign', 'alarm:suppress', 'alarm:unsuppress']::text[]);

RESET ROLE;

COMMIT;

