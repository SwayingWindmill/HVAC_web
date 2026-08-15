BEGIN;

SET LOCAL ROLE s1_iam_migrator;

CREATE TABLE IF NOT EXISTS iam.alarm_permissions (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  tenant_id uuid NOT NULL CHECK (iam.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  action text NOT NULL CHECK (action IN ('alarm:read', 'alarm:ack')),
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

CREATE TABLE IF NOT EXISTS iam.alarm_authorization_decisions (
  sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  principal_id uuid,
  tenant_id uuid NOT NULL CHECK (iam.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  alarm_id uuid,
  action text NOT NULL CHECK (action IN ('alarm:read', 'alarm:ack')),
  allowed boolean NOT NULL,
  policy_revision text NOT NULL CHECK (char_length(policy_revision) BETWEEN 1 AND 128),
  reason_code text NOT NULL CHECK (reason_code IN ('ALLOW_EXACT_SCOPE','DENY_PRINCIPAL','DENY_MEMBERSHIP','DENY_EXPLICIT','DENY_SCOPE')),
  request_id text NOT NULL CHECK (char_length(request_id) BETWEEN 1 AND 256),
  trace_id text NOT NULL CHECK (char_length(trace_id) BETWEEN 1 AND 256),
  occurred_at timestamptz NOT NULL,
  CHECK (principal_id IS NULL OR iam.is_uuid_v7(principal_id)),
  CHECK (alarm_id IS NULL OR iam.is_uuid_v7(alarm_id)),
  CHECK ((action = 'alarm:read') OR (action = 'alarm:ack' AND alarm_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS alarm_permissions_lookup_idx
  ON iam.alarm_permissions (principal_id, tenant_id, site_id, action, effect);
CREATE INDEX IF NOT EXISTS alarm_authorization_decisions_scope_idx
  ON iam.alarm_authorization_decisions (tenant_id, occurred_at DESC, sequence DESC);

ALTER TABLE iam.alarm_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.alarm_permissions FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.alarm_authorization_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.alarm_authorization_decisions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS alarm_permissions_runtime_scope ON iam.alarm_permissions;
CREATE POLICY alarm_permissions_runtime_scope ON iam.alarm_permissions
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id() AND tenant_id = iam.current_tenant_id());

DROP POLICY IF EXISTS alarm_authorization_decisions_runtime_insert ON iam.alarm_authorization_decisions;
CREATE POLICY alarm_authorization_decisions_runtime_insert ON iam.alarm_authorization_decisions
  FOR INSERT TO s1_iam_runtime
  WITH CHECK (
    tenant_id = iam.current_tenant_id()
    AND (principal_id IS NULL OR principal_id = iam.current_principal_id())
  );

DROP POLICY IF EXISTS alarm_permissions_migrator_all ON iam.alarm_permissions;
CREATE POLICY alarm_permissions_migrator_all ON iam.alarm_permissions
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS alarm_authorization_decisions_migrator_all ON iam.alarm_authorization_decisions;
CREATE POLICY alarm_authorization_decisions_migrator_all ON iam.alarm_authorization_decisions
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);

GRANT SELECT ON iam.alarm_permissions TO s1_iam_runtime;
GRANT INSERT ON iam.alarm_authorization_decisions TO s1_iam_runtime;
GRANT USAGE, SELECT ON SEQUENCE iam.alarm_authorization_decisions_sequence_seq TO s1_iam_runtime;
REVOKE ALL ON iam.alarm_permissions, iam.alarm_authorization_decisions FROM PUBLIC;

INSERT INTO iam.policies
  (id, tenant_id, policy_key, policy_revision, status, document, created_at, updated_at)
VALUES
  ('018f1e00-1400-7000-8000-000000000021', '018f1d00-0000-7000-8000-000000000001', 'alarm-access', 1, 'ACTIVE',
   '{"actions":["alarm:read","alarm:ack"],"scope":"site","denyWins":true}'::jsonb,
   '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO iam.alarm_permissions
  (id, principal_id, tenant_id, site_id, action, effect, status, valid_from, valid_to, revision, created_at, updated_at)
VALUES
  ('018f1e00-2400-7000-8000-000000000022', '018f1e00-2000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'alarm:read', 'ALLOW', 'ACTIVE', '2026-08-01T00:00:00Z', NULL, 1, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z'),
  ('018f1e00-2400-7000-8000-000000000023', '018f1e00-2000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'alarm:ack', 'ALLOW', 'ACTIVE', '2026-08-01T00:00:00Z', NULL, 1, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')
ON CONFLICT DO NOTHING;

COMMIT;
