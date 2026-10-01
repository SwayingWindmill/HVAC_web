BEGIN;

SET LOCAL ROLE s1_iam_migrator;

CREATE TABLE IF NOT EXISTS iam.reconciliation_state (
  source_system text NOT NULL,
  source_key text NOT NULL,
  source_version bigint NOT NULL CHECK (source_version > 0),
  input_hash text NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (source_system, source_key),
  UNIQUE (principal_id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS iam.reconciliation_events (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  source_system text NOT NULL,
  source_key text NOT NULL,
  source_version bigint NOT NULL CHECK (source_version > 0),
  input_hash text NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
  principal_id uuid NOT NULL,
  status text NOT NULL CHECK (status IN ('APPLIED', 'NO_CHANGE', 'QUARANTINED')),
  reason_code text NOT NULL,
  membership_count integer NOT NULL CHECK (membership_count >= 0),
  role_binding_count integer NOT NULL CHECK (role_binding_count >= 0),
  site_binding_count integer NOT NULL CHECK (site_binding_count >= 0),
  explicit_deny_count integer NOT NULL CHECK (explicit_deny_count >= 0),
  created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS iam.reconciliation_quarantine (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  reconciliation_event_id uuid NOT NULL REFERENCES iam.reconciliation_events(id),
  source_system text NOT NULL,
  source_key text NOT NULL,
  source_version bigint NOT NULL CHECK (source_version > 0),
  input_hash text NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
  current_source_system text,
  current_source_key text,
  current_source_version bigint CHECK (current_source_version > 0),
  current_input_hash text CHECK (current_input_hash IS NULL OR current_input_hash ~ '^[0-9a-f]{64}$'),
  requested_principal_id uuid NOT NULL,
  current_principal_id uuid,
  reason_code text NOT NULL CHECK (reason_code IN (
    'STALE_SOURCE_VERSION',
    'SOURCE_VERSION_CONFLICT',
    'IMMUTABLE_IDENTITY_CONFLICT',
    'PRINCIPAL_IDENTIFIER_CONFLICT',
    'SOURCE_PRINCIPAL_CONFLICT'
  )),
  quarantine_status text NOT NULL CHECK (quarantine_status IN ('OPEN', 'RESOLVED', 'DISMISSED')),
  created_at timestamptz NOT NULL,
  resolved_at timestamptz,
  resolution_note text,
  CHECK ((current_source_system IS NULL) = (current_source_key IS NULL)),
  CHECK ((current_source_system IS NULL) = (current_source_version IS NULL)),
  CHECK ((current_source_system IS NULL) = (current_input_hash IS NULL)),
  CHECK (resolved_at IS NULL OR resolved_at >= created_at)
);

CREATE INDEX IF NOT EXISTS reconciliation_events_source_idx
  ON iam.reconciliation_events (source_system, source_key, source_version, created_at DESC);
CREATE INDEX IF NOT EXISTS reconciliation_quarantine_open_idx
  ON iam.reconciliation_quarantine (source_system, source_key, created_at DESC)
  WHERE quarantine_status = 'OPEN';

ALTER TABLE iam.reconciliation_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.reconciliation_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.reconciliation_quarantine ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.reconciliation_state FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.reconciliation_events FORCE ROW LEVEL SECURITY;
ALTER TABLE iam.reconciliation_quarantine FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS reconciler_principals ON iam.principals;
CREATE POLICY reconciler_principals ON iam.principals
  FOR ALL TO s1_iam_reconciler
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS reconciler_memberships ON iam.tenant_memberships;
CREATE POLICY reconciler_memberships ON iam.tenant_memberships
  FOR ALL TO s1_iam_reconciler
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS reconciler_role_bindings ON iam.role_bindings;
CREATE POLICY reconciler_role_bindings ON iam.role_bindings
  FOR ALL TO s1_iam_reconciler
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS reconciler_site_bindings ON iam.site_bindings;
CREATE POLICY reconciler_site_bindings ON iam.site_bindings
  FOR ALL TO s1_iam_reconciler
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS reconciler_explicit_denies ON iam.explicit_denies;
CREATE POLICY reconciler_explicit_denies ON iam.explicit_denies
  FOR ALL TO s1_iam_reconciler
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS reconciler_state ON iam.reconciliation_state;
CREATE POLICY reconciler_state ON iam.reconciliation_state
  FOR ALL TO s1_iam_reconciler
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS reconciler_events ON iam.reconciliation_events;
CREATE POLICY reconciler_events ON iam.reconciliation_events
  FOR INSERT TO s1_iam_reconciler
  WITH CHECK (true);

DROP POLICY IF EXISTS reconciler_quarantine ON iam.reconciliation_quarantine;
CREATE POLICY reconciler_quarantine ON iam.reconciliation_quarantine
  FOR INSERT TO s1_iam_reconciler
  WITH CHECK (true);

GRANT SELECT, INSERT ON iam.principals TO s1_iam_reconciler;
GRANT UPDATE (display_name, email, status, revision, updated_at)
  ON iam.principals TO s1_iam_reconciler;
GRANT INSERT, DELETE ON iam.tenant_memberships, iam.role_bindings,
  iam.site_bindings, iam.explicit_denies TO s1_iam_reconciler;
GRANT SELECT (tenant_id, principal_id) ON iam.tenant_memberships TO s1_iam_reconciler;
GRANT SELECT (tenant_id, principal_id) ON iam.role_bindings TO s1_iam_reconciler;
GRANT SELECT (tenant_id, principal_id) ON iam.site_bindings TO s1_iam_reconciler;
GRANT SELECT (tenant_id, principal_id) ON iam.explicit_denies TO s1_iam_reconciler;
GRANT SELECT, INSERT, UPDATE ON iam.reconciliation_state TO s1_iam_reconciler;
GRANT INSERT ON iam.reconciliation_events, iam.reconciliation_quarantine TO s1_iam_reconciler;

REVOKE ALL ON iam.reconciliation_state, iam.reconciliation_events,
  iam.reconciliation_quarantine FROM PUBLIC, s1_iam_runtime;

COMMIT;
