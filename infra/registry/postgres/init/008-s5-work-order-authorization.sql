BEGIN;

SET LOCAL ROLE s1_iam_migrator;

CREATE TABLE IF NOT EXISTS iam.work_order_permissions (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  tenant_id uuid NOT NULL CHECK (iam.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  action text NOT NULL CHECK (action IN ('work-order:list', 'work-order:read', 'work-order:create', 'work-order:assign', 'work-order:plan', 'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete', 'work-order:cancel', 'work-order:reopen')),
  effect text NOT NULL CHECK (effect IN ('ALLOW', 'DENY')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REVOKED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (principal_id, tenant_id, site_id, action, effect),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.work_order_ownership_targets (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (iam.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  target_type text NOT NULL CHECK (target_type IN ('PRINCIPAL', 'TEAM')),
  target_id text NOT NULL CHECK (char_length(btrim(target_id)) BETWEEN 1 AND 256),
  effect text NOT NULL CHECK (effect IN ('ALLOW', 'DENY')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REVOKED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, target_type, target_id, effect),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.work_order_authorization_decisions (
  sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  principal_id uuid,
  tenant_id uuid NOT NULL CHECK (iam.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  work_order_id uuid,
  assignee_id text CHECK (assignee_id IS NULL OR char_length(btrim(assignee_id)) BETWEEN 1 AND 256),
  team_id text CHECK (team_id IS NULL OR char_length(btrim(team_id)) BETWEEN 1 AND 256),
  action text NOT NULL CHECK (action IN ('work-order:list', 'work-order:read', 'work-order:create', 'work-order:assign', 'work-order:plan', 'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete', 'work-order:cancel', 'work-order:reopen')),
  allowed boolean NOT NULL,
  policy_revision text NOT NULL CHECK (char_length(policy_revision) BETWEEN 1 AND 128),
  reason_code text NOT NULL CHECK (reason_code IN ('ALLOW_EXACT_SCOPE','DENY_PRINCIPAL','DENY_MEMBERSHIP','DENY_EXPLICIT','DENY_SCOPE')),
  request_id text NOT NULL CHECK (char_length(request_id) BETWEEN 1 AND 256),
  trace_id text NOT NULL CHECK (char_length(trace_id) BETWEEN 1 AND 256),
  occurred_at timestamptz NOT NULL,
  CHECK (principal_id IS NULL OR iam.is_uuid_v7(principal_id)),
  CHECK (work_order_id IS NULL OR iam.is_uuid_v7(work_order_id)),
  CHECK ((action IN ('work-order:list', 'work-order:create') AND work_order_id IS NULL) OR (action IN ('work-order:read', 'work-order:assign', 'work-order:plan', 'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete', 'work-order:cancel', 'work-order:reopen') AND work_order_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS work_order_permissions_lookup_idx
  ON iam.work_order_permissions (principal_id, tenant_id, site_id, action, effect);
CREATE INDEX IF NOT EXISTS work_order_ownership_targets_lookup_idx
  ON iam.work_order_ownership_targets (tenant_id, site_id, target_type, target_id, effect);
CREATE INDEX IF NOT EXISTS work_order_authorization_decisions_scope_idx
  ON iam.work_order_authorization_decisions (tenant_id, occurred_at DESC, sequence DESC);

ALTER TABLE iam.work_order_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.work_order_permissions FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.work_order_ownership_targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.work_order_ownership_targets FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.work_order_authorization_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.work_order_authorization_decisions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS work_order_permissions_runtime_scope ON iam.work_order_permissions;
CREATE POLICY work_order_permissions_runtime_scope ON iam.work_order_permissions
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id() AND tenant_id = iam.current_tenant_id());

DROP POLICY IF EXISTS work_order_ownership_targets_runtime_scope ON iam.work_order_ownership_targets;
CREATE POLICY work_order_ownership_targets_runtime_scope ON iam.work_order_ownership_targets
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id = iam.current_tenant_id());

DROP POLICY IF EXISTS work_order_authorization_decisions_runtime_insert ON iam.work_order_authorization_decisions;
CREATE POLICY work_order_authorization_decisions_runtime_insert ON iam.work_order_authorization_decisions
  FOR INSERT TO s1_iam_runtime
  WITH CHECK (
    tenant_id = iam.current_tenant_id()
    AND (principal_id IS NULL OR principal_id = iam.current_principal_id())
  );

DROP POLICY IF EXISTS work_order_permissions_migrator_all ON iam.work_order_permissions;
CREATE POLICY work_order_permissions_migrator_all ON iam.work_order_permissions
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS work_order_ownership_targets_migrator_all ON iam.work_order_ownership_targets;
CREATE POLICY work_order_ownership_targets_migrator_all ON iam.work_order_ownership_targets
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS work_order_authorization_decisions_migrator_all ON iam.work_order_authorization_decisions;
CREATE POLICY work_order_authorization_decisions_migrator_all ON iam.work_order_authorization_decisions
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);

GRANT SELECT ON iam.work_order_permissions, iam.work_order_ownership_targets TO s1_iam_runtime;
GRANT INSERT ON iam.work_order_authorization_decisions TO s1_iam_runtime;
GRANT USAGE, SELECT ON SEQUENCE iam.work_order_authorization_decisions_sequence_seq TO s1_iam_runtime;
REVOKE ALL ON iam.work_order_permissions, iam.work_order_ownership_targets, iam.work_order_authorization_decisions FROM PUBLIC;

COMMIT;
