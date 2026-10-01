BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

ALTER TABLE alarm_runtime.alarm_current
  ADD COLUMN IF NOT EXISTS assignee_id text,
  ADD COLUMN IF NOT EXISTS suppressed_until timestamptz;

ALTER TABLE alarm_runtime.alarm_current
  DROP CONSTRAINT IF EXISTS alarm_current_assignee_nonempty,
  ADD CONSTRAINT alarm_current_assignee_nonempty
    CHECK (assignee_id IS NULL OR length(btrim(assignee_id)) > 0),
  DROP CONSTRAINT IF EXISTS alarm_current_suppression_consistent,
  ADD CONSTRAINT alarm_current_suppression_consistent
    CHECK ((status = 'SUPPRESSED') = (suppressed_until IS NOT NULL));

CREATE TABLE IF NOT EXISTS alarm_runtime.alarm_idempotency (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
  request_digest text NOT NULL CHECK (request_digest ~ '^[a-f0-9]{64}$'),
  response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, alarm_id, idempotency_key),
  FOREIGN KEY (tenant_id, site_id, alarm_id)
    REFERENCES alarm_runtime.alarm_current (tenant_id, site_id, alarm_id)
    ON DELETE CASCADE
);

ALTER TABLE alarm_runtime.alarm_current ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_current FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS alarm_current_runtime_tenant ON alarm_runtime.alarm_current;
CREATE POLICY alarm_current_runtime_tenant ON alarm_runtime.alarm_current
  FOR ALL TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE alarm_runtime.alarm_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_idempotency FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS alarm_idempotency_runtime_tenant ON alarm_runtime.alarm_idempotency;
CREATE POLICY alarm_idempotency_runtime_tenant ON alarm_runtime.alarm_idempotency
  FOR ALL TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON alarm_runtime.alarm_current FROM s4_alarm_runtime;
GRANT SELECT, UPDATE (status, assignee_id, suppressed_until, evidence, transitions, version, updated_at)
  ON alarm_runtime.alarm_current TO s4_alarm_runtime;
GRANT SELECT, INSERT ON alarm_runtime.alarm_idempotency TO s4_alarm_runtime;

COMMIT;
