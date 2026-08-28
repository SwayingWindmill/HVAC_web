BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE alarm_runtime.alarm_policy_revision (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  policy_revision_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  schema_version integer NOT NULL CHECK (schema_version = 1),
  digest text NOT NULL CHECK (digest ~ '^[a-f0-9]{64}$'),
  policy jsonb NOT NULL CHECK (jsonb_typeof(policy) = 'object'),
  released_at timestamptz NOT NULL,
  released_by text NOT NULL CHECK (length(btrim(released_by)) BETWEEN 1 AND 256),
  PRIMARY KEY (tenant_id, site_id, policy_id, revision),
  UNIQUE (tenant_id, site_id, policy_revision_id)
);

CREATE TABLE alarm_runtime.alarm_policy_assignment (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  assignment_id uuid NOT NULL,
  assignment_revision bigint NOT NULL CHECK (assignment_revision > 0),
  policy_revision_id uuid NOT NULL,
  subject_type text NOT NULL CHECK (subject_type IN ('SITE','DEVICE','POINT','METRIC','EXTERNAL')),
  subject_id uuid NOT NULL,
  assigned_at timestamptz NOT NULL,
  assigned_by text NOT NULL CHECK (length(btrim(assigned_by)) BETWEEN 1 AND 256),
  PRIMARY KEY (tenant_id, site_id, assignment_id, assignment_revision),
  FOREIGN KEY (tenant_id, site_id, policy_revision_id)
    REFERENCES alarm_runtime.alarm_policy_revision (tenant_id, site_id, policy_revision_id)
);
CREATE INDEX alarm_policy_assignment_latest_idx
  ON alarm_runtime.alarm_policy_assignment (tenant_id, site_id, assignment_id, assignment_revision DESC);

CREATE TABLE alarm_runtime.alarm_evaluation_state (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  assignment_id uuid NOT NULL,
  assignment_revision bigint NOT NULL CHECK (assignment_revision > 0),
  policy_revision_id uuid NOT NULL,
  subject_type text NOT NULL CHECK (subject_type IN ('SITE','DEVICE','POINT','METRIC','EXTERNAL')),
  subject_id uuid NOT NULL,
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  status text NOT NULL CHECK (status IN ('MATCHED','NOT_MATCHED','INDETERMINATE')),
  candidate_since timestamptz,
  repeat_count bigint NOT NULL DEFAULT 0 CHECK (repeat_count >= 0),
  last_input_revision text NOT NULL CHECK (length(btrim(last_input_revision)) BETWEEN 1 AND 256),
  quality_blocker text,
  next_evaluation_at timestamptz,
  active_alarm_id uuid,
  active_incident_correlation_id uuid,
  last_snapshot jsonb NOT NULL CHECK (jsonb_typeof(last_snapshot) = 'object'),
  last_evaluated_at timestamptz NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  lease_owner text,
  lease_until timestamptz,
  lease_fence bigint NOT NULL DEFAULT 0 CHECK (lease_fence >= 0),
  PRIMARY KEY (tenant_id, site_id, assignment_id),
  FOREIGN KEY (tenant_id, site_id, assignment_id, assignment_revision)
    REFERENCES alarm_runtime.alarm_policy_assignment (tenant_id, site_id, assignment_id, assignment_revision),
  FOREIGN KEY (tenant_id, site_id, active_alarm_id)
    REFERENCES alarm_runtime.alarm_current (tenant_id, site_id, alarm_id),
  CHECK ((active_alarm_id IS NULL) = (active_incident_correlation_id IS NULL)),
  CHECK ((lease_owner IS NULL) = (lease_until IS NULL)),
  CHECK (lease_owner IS NULL OR length(btrim(lease_owner)) BETWEEN 1 AND 256)
);
CREATE INDEX alarm_evaluation_due_idx
  ON alarm_runtime.alarm_evaluation_state (tenant_id, next_evaluation_at, assignment_id)
  WHERE next_evaluation_at IS NOT NULL;

CREATE TABLE alarm_runtime.alarm_evaluation_event (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  assignment_id uuid NOT NULL,
  state_version bigint NOT NULL CHECK (state_version > 0),
  assignment_revision bigint NOT NULL CHECK (assignment_revision > 0),
  policy_revision_id uuid NOT NULL,
  input_revision text NOT NULL CHECK (length(btrim(input_revision)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('MATCHED','NOT_MATCHED','INDETERMINATE')),
  effect text NOT NULL CHECK (effect IN ('NONE','PUBLISH','CLEAR')),
  quality_blocker text,
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  active_alarm_id uuid,
  active_incident_correlation_id uuid,
  snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
  evaluated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, assignment_id, state_version),
  FOREIGN KEY (tenant_id, site_id, assignment_id, assignment_revision)
    REFERENCES alarm_runtime.alarm_policy_assignment (tenant_id, site_id, assignment_id, assignment_revision),
  CHECK ((active_alarm_id IS NULL) = (active_incident_correlation_id IS NULL))
);
CREATE INDEX alarm_evaluation_event_time_idx
  ON alarm_runtime.alarm_evaluation_event (tenant_id, site_id, evaluated_at DESC, assignment_id, state_version DESC);

CREATE OR REPLACE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'alarm evaluator released/history rows are immutable';
END $$;

CREATE TRIGGER alarm_policy_revision_immutable
BEFORE UPDATE OR DELETE ON alarm_runtime.alarm_policy_revision
FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();
CREATE TRIGGER alarm_policy_assignment_immutable
BEFORE UPDATE OR DELETE ON alarm_runtime.alarm_policy_assignment
FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();
CREATE TRIGGER alarm_evaluation_event_immutable
BEFORE UPDATE OR DELETE ON alarm_runtime.alarm_evaluation_event
FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluator_history_mutation();

ALTER TABLE alarm_runtime.alarm_policy_revision ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_policy_revision FORCE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_policy_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_policy_assignment FORCE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_evaluation_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_evaluation_state FORCE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_evaluation_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_evaluation_event FORCE ROW LEVEL SECURITY;

CREATE POLICY alarm_policy_revision_migrator_all ON alarm_runtime.alarm_policy_revision
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY alarm_policy_assignment_migrator_all ON alarm_runtime.alarm_policy_assignment
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY alarm_evaluation_state_migrator_all ON alarm_runtime.alarm_evaluation_state
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY alarm_evaluation_event_migrator_all ON alarm_runtime.alarm_evaluation_event
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

CREATE POLICY alarm_policy_revision_runtime_select ON alarm_runtime.alarm_policy_revision
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_policy_revision_runtime_insert ON alarm_runtime.alarm_policy_revision
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_policy_assignment_runtime_select ON alarm_runtime.alarm_policy_assignment
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_policy_assignment_runtime_insert ON alarm_runtime.alarm_policy_assignment
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_evaluation_state_runtime_all ON alarm_runtime.alarm_evaluation_state
  FOR ALL TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_evaluation_event_runtime_select ON alarm_runtime.alarm_evaluation_event
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_evaluation_event_runtime_insert ON alarm_runtime.alarm_evaluation_event
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON alarm_runtime.alarm_policy_revision FROM PUBLIC, s4_alarm_runtime;
REVOKE ALL ON alarm_runtime.alarm_policy_assignment FROM PUBLIC, s4_alarm_runtime;
REVOKE ALL ON alarm_runtime.alarm_evaluation_state FROM PUBLIC, s4_alarm_runtime;
REVOKE ALL ON alarm_runtime.alarm_evaluation_event FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT ON alarm_runtime.alarm_policy_revision TO s4_alarm_runtime;
GRANT SELECT, INSERT ON alarm_runtime.alarm_policy_assignment TO s4_alarm_runtime;
GRANT SELECT, INSERT, UPDATE ON alarm_runtime.alarm_evaluation_state TO s4_alarm_runtime;
GRANT SELECT, INSERT ON alarm_runtime.alarm_evaluation_event TO s4_alarm_runtime;

RESET ROLE;
COMMIT;
