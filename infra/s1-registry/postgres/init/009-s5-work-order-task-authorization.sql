BEGIN;

SET LOCAL ROLE s1_iam_migrator;

ALTER TABLE iam.work_order_permissions
  DROP CONSTRAINT IF EXISTS work_order_permissions_action_check;
ALTER TABLE iam.work_order_permissions
  ADD CONSTRAINT work_order_permissions_action_check
  CHECK (action IN (
    'work-order:list', 'work-order:read', 'work-order:create', 'work-order:assign',
    'work-order:plan', 'work-order:start', 'work-order:block', 'work-order:resume',
    'work-order:complete', 'work-order:cancel', 'work-order:reopen',
    'work-order:task:list', 'work-order:task:append', 'work-order:task:status', 'work-order:task:reorder'
  ));

ALTER TABLE iam.work_order_authorization_decisions
  ADD COLUMN IF NOT EXISTS task_id uuid;
ALTER TABLE iam.work_order_authorization_decisions
  DROP CONSTRAINT IF EXISTS work_order_authorization_decisions_action_check;
ALTER TABLE iam.work_order_authorization_decisions
  ADD CONSTRAINT work_order_authorization_decisions_action_check
  CHECK (action IN (
    'work-order:list', 'work-order:read', 'work-order:create', 'work-order:assign',
    'work-order:plan', 'work-order:start', 'work-order:block', 'work-order:resume',
    'work-order:complete', 'work-order:cancel', 'work-order:reopen',
    'work-order:task:list', 'work-order:task:append', 'work-order:task:status', 'work-order:task:reorder'
  ));
ALTER TABLE iam.work_order_authorization_decisions
  DROP CONSTRAINT IF EXISTS work_order_authorization_decisions_check;
ALTER TABLE iam.work_order_authorization_decisions
  ADD CONSTRAINT work_order_authorization_decisions_resource_shape_check CHECK (
    (action IN ('work-order:list', 'work-order:create') AND work_order_id IS NULL AND task_id IS NULL)
    OR
    (action IN (
      'work-order:read', 'work-order:assign', 'work-order:plan', 'work-order:start', 'work-order:block',
      'work-order:resume', 'work-order:complete', 'work-order:cancel', 'work-order:reopen',
      'work-order:task:list', 'work-order:task:append', 'work-order:task:reorder'
    ) AND work_order_id IS NOT NULL AND task_id IS NULL)
    OR
    (action = 'work-order:task:status' AND work_order_id IS NOT NULL AND task_id IS NOT NULL)
  );
ALTER TABLE iam.work_order_authorization_decisions
  DROP CONSTRAINT IF EXISTS work_order_authorization_decisions_task_id_check;
ALTER TABLE iam.work_order_authorization_decisions
  ADD CONSTRAINT work_order_authorization_decisions_task_id_check
  CHECK (task_id IS NULL OR iam.is_uuid_v7(task_id));

UPDATE iam.policies
SET document = '{"actions":["work-order:list","work-order:read","work-order:create","work-order:assign","work-order:plan","work-order:start","work-order:block","work-order:resume","work-order:complete","work-order:cancel","work-order:reopen","work-order:task:list","work-order:task:append","work-order:task:status","work-order:task:reorder"],"scope":"site-and-resource","denyWins":true}'::jsonb,
    policy_revision = GREATEST(policy_revision, 2),
    updated_at = GREATEST(updated_at, '2026-08-02T00:00:00Z'::timestamptz)
WHERE policy_key = 'work-order-access';

INSERT INTO iam.work_order_permissions
  (id, principal_id, acting_organization_id, site_id, action, effect, status, valid_from, valid_to, revision, created_at, updated_at)
VALUES
  ('018f1e00-2400-7000-8000-000000000042', '018f1e00-2000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'work-order:task:list', 'ALLOW', 'ACTIVE', '2026-08-02T00:00:00Z', NULL, 1, '2026-08-02T00:00:00Z', '2026-08-02T00:00:00Z'),
  ('018f1e00-2400-7000-8000-000000000043', '018f1e00-2000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'work-order:task:append', 'ALLOW', 'ACTIVE', '2026-08-02T00:00:00Z', NULL, 1, '2026-08-02T00:00:00Z', '2026-08-02T00:00:00Z'),
  ('018f1e00-2400-7000-8000-000000000044', '018f1e00-2000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'work-order:task:status', 'ALLOW', 'ACTIVE', '2026-08-02T00:00:00Z', NULL, 1, '2026-08-02T00:00:00Z', '2026-08-02T00:00:00Z'),
  ('018f1e00-2400-7000-8000-000000000045', '018f1e00-2000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'work-order:task:reorder', 'ALLOW', 'ACTIVE', '2026-08-02T00:00:00Z', NULL, 1, '2026-08-02T00:00:00Z', '2026-08-02T00:00:00Z')
ON CONFLICT DO NOTHING;

COMMIT;
