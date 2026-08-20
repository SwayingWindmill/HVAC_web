BEGIN;

CREATE SCHEMA IF NOT EXISTS notification_runtime AUTHORIZATION s16_notification_migrator;
REVOKE ALL ON SCHEMA notification_runtime FROM PUBLIC;
SET LOCAL ROLE s16_notification_migrator;

CREATE TABLE notification_runtime.template_revision (
  tenant_id uuid NOT NULL,
  template_id uuid NOT NULL,
  template_revision_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  schema_version integer NOT NULL CHECK (schema_version = 1),
  digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  channel text NOT NULL CHECK (channel IN ('IN_APP','EMAIL','REST')),
  template jsonb NOT NULL CHECK (jsonb_typeof(template) = 'object'),
  released_at timestamptz NOT NULL,
  released_by text NOT NULL CHECK (length(btrim(released_by)) BETWEEN 1 AND 256),
  PRIMARY KEY (tenant_id, template_revision_id),
  UNIQUE (tenant_id, template_id, revision),
  UNIQUE (tenant_id, template_id, digest)
);

CREATE TABLE notification_runtime.audience_revision (
  tenant_id uuid NOT NULL,
  audience_id uuid NOT NULL,
  audience_revision_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  schema_version integer NOT NULL CHECK (schema_version = 1),
  digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  audience jsonb NOT NULL CHECK (jsonb_typeof(audience) = 'object'),
  released_at timestamptz NOT NULL,
  released_by text NOT NULL CHECK (length(btrim(released_by)) BETWEEN 1 AND 256),
  PRIMARY KEY (tenant_id, audience_revision_id),
  UNIQUE (tenant_id, audience_id, revision),
  UNIQUE (tenant_id, audience_id, digest)
);

CREATE TABLE notification_runtime.policy_revision (
  tenant_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  policy_revision_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  schema_version integer NOT NULL CHECK (schema_version = 1),
  digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  mandatory_safety boolean NOT NULL,
  policy jsonb NOT NULL CHECK (jsonb_typeof(policy) = 'object'),
  released_at timestamptz NOT NULL,
  released_by text NOT NULL CHECK (length(btrim(released_by)) BETWEEN 1 AND 256),
  PRIMARY KEY (tenant_id, policy_revision_id),
  UNIQUE (tenant_id, policy_id, revision),
  UNIQUE (tenant_id, policy_id, digest)
);

CREATE TABLE notification_runtime.policy_assignment (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  assignment_id uuid NOT NULL,
  assignment_revision bigint NOT NULL CHECK (assignment_revision > 0),
  policy_id uuid NOT NULL,
  policy_revision_id uuid NOT NULL,
  enabled boolean NOT NULL,
  assigned_at timestamptz NOT NULL,
  assigned_by text NOT NULL CHECK (length(btrim(assigned_by)) BETWEEN 1 AND 256),
  PRIMARY KEY (tenant_id, site_id, assignment_id, assignment_revision),
  FOREIGN KEY (tenant_id, policy_revision_id) REFERENCES notification_runtime.policy_revision (tenant_id, policy_revision_id)
);
CREATE INDEX notification_policy_assignment_latest_idx
  ON notification_runtime.policy_assignment (tenant_id, site_id, assignment_id, assignment_revision DESC);

CREATE TABLE notification_runtime.source_alarm_event (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  source_event_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  incident_correlation_id uuid NOT NULL,
  action text NOT NULL CHECK (action IN ('CREATED','SEVERITY_CHANGED','ACKNOWLEDGED','CLEARED')),
  current_severity text NOT NULL CHECK (current_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  peak_severity text NOT NULL CHECK (peak_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  condition text NOT NULL CHECK (condition IN ('ACTIVE','CLEARED')),
  event jsonb NOT NULL CHECK (jsonb_typeof(event) = 'object'),
  occurred_at timestamptz NOT NULL,
  recorded_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, source_event_id)
);
CREATE INDEX notification_source_alarm_incident_idx
  ON notification_runtime.source_alarm_event (tenant_id, site_id, incident_correlation_id, occurred_at, source_event_id);

CREATE TABLE notification_runtime.user_preference (
  tenant_id uuid NOT NULL,
  principal_id text NOT NULL CHECK (length(btrim(principal_id)) BETWEEN 1 AND 256),
  channel text NOT NULL CHECK (channel IN ('IN_APP','EMAIL','REST')),
  advisory_enabled boolean NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, principal_id, channel)
);

CREATE TABLE notification_runtime.notification_intent (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  intent_id uuid NOT NULL,
  source_event_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  incident_correlation_id uuid NOT NULL,
  source_action text NOT NULL CHECK (source_action IN ('CREATED','SEVERITY_CHANGED','ACKNOWLEDGED','CLEARED')),
  current_severity text NOT NULL CHECK (current_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  policy_revision_id uuid NOT NULL,
  assignment_id uuid NOT NULL,
  assignment_revision bigint NOT NULL CHECK (assignment_revision > 0),
  stage integer NOT NULL CHECK (stage >= 0 AND stage <= 15),
  channel text NOT NULL CHECK (channel IN ('IN_APP','EMAIL','REST')),
  integration_id uuid,
  mandatory_safety boolean NOT NULL,
  recipients jsonb NOT NULL CHECK (jsonb_typeof(recipients) = 'array'),
  template_revision_id uuid NOT NULL,
  rendered_subject text NOT NULL CHECK (length(rendered_subject) BETWEEN 1 AND 512),
  rendered_body text NOT NULL CHECK (length(rendered_body) BETWEEN 1 AND 16384),
  due_at timestamptz NOT NULL,
  status text NOT NULL CHECK (status IN ('SCHEDULED','CLAIMED','MATERIALIZED','EXTERNAL_SUBMITTED','DELIVERED','FAILED','CANCELLED')),
  disposition text NOT NULL CHECK (disposition IN ('PENDING','DELIVERED','OUTCOME_UNKNOWN','FAILED','CANCELLED')),
  external_delivery_intent_id uuid,
  lease_owner text,
  lease_until timestamptz,
  lease_fence bigint NOT NULL DEFAULT 0 CHECK (lease_fence >= 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
  PRIMARY KEY (tenant_id, intent_id),
  UNIQUE (tenant_id, source_event_id, assignment_id, assignment_revision, stage),
  FOREIGN KEY (tenant_id, source_event_id) REFERENCES notification_runtime.source_alarm_event (tenant_id, source_event_id),
  FOREIGN KEY (tenant_id, policy_revision_id) REFERENCES notification_runtime.policy_revision (tenant_id, policy_revision_id),
  CHECK ((channel = 'IN_APP' AND integration_id IS NULL) OR (channel IN ('EMAIL','REST') AND integration_id IS NOT NULL)),
  CHECK ((status = 'CLAIMED' AND lease_owner IS NOT NULL AND lease_until IS NOT NULL) OR status <> 'CLAIMED')
);
CREATE INDEX notification_intent_due_idx
  ON notification_runtime.notification_intent (tenant_id, due_at, intent_id)
  WHERE status = 'SCHEDULED';
CREATE INDEX notification_intent_incident_idx
  ON notification_runtime.notification_intent (tenant_id, site_id, incident_correlation_id, status, due_at);

CREATE TABLE notification_runtime.inbox_item (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  inbox_item_id uuid NOT NULL,
  intent_id uuid NOT NULL,
  principal_id text NOT NULL CHECK (length(btrim(principal_id)) BETWEEN 1 AND 256),
  alarm_id uuid NOT NULL,
  incident_correlation_id uuid NOT NULL,
  source_action text NOT NULL CHECK (source_action IN ('CREATED','SEVERITY_CHANGED','ACKNOWLEDGED','CLEARED')),
  severity text NOT NULL CHECK (severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 512),
  body text NOT NULL CHECK (length(body) BETWEEN 1 AND 16384),
  status text NOT NULL CHECK (status IN ('UNREAD','READ','ACKED')),
  created_at timestamptz NOT NULL,
  read_at timestamptz,
  acked_at timestamptz,
  PRIMARY KEY (tenant_id, inbox_item_id),
  UNIQUE (tenant_id, intent_id, principal_id),
  FOREIGN KEY (tenant_id, intent_id) REFERENCES notification_runtime.notification_intent (tenant_id, intent_id)
);
CREATE INDEX notification_inbox_principal_idx
  ON notification_runtime.inbox_item (tenant_id, principal_id, created_at DESC, inbox_item_id DESC);
CREATE INDEX notification_inbox_unread_idx
  ON notification_runtime.inbox_item (tenant_id, principal_id, created_at DESC)
  WHERE status = 'UNREAD';

CREATE TABLE notification_runtime.intent_event (
  tenant_id uuid NOT NULL,
  event_id uuid NOT NULL,
  intent_id uuid NOT NULL,
  event_type text NOT NULL CHECK (event_type IN ('CREATED','CLAIMED','MATERIALIZED','EXTERNAL_SUBMITTED','DELIVERY_UPDATED','CANCELLED')),
  detail jsonb NOT NULL CHECK (jsonb_typeof(detail) = 'object'),
  occurred_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, event_id),
  FOREIGN KEY (tenant_id, intent_id) REFERENCES notification_runtime.notification_intent (tenant_id, intent_id)
);
CREATE INDEX notification_intent_event_idx ON notification_runtime.intent_event (tenant_id, intent_id, occurred_at, event_id);

CREATE FUNCTION notification_runtime.reject_notification_history_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'notification released/history rows are immutable';
END;
$$;

CREATE TRIGGER notification_template_revision_immutable BEFORE UPDATE OR DELETE ON notification_runtime.template_revision
FOR EACH ROW EXECUTE FUNCTION notification_runtime.reject_notification_history_mutation();
CREATE TRIGGER notification_audience_revision_immutable BEFORE UPDATE OR DELETE ON notification_runtime.audience_revision
FOR EACH ROW EXECUTE FUNCTION notification_runtime.reject_notification_history_mutation();
CREATE TRIGGER notification_policy_revision_immutable BEFORE UPDATE OR DELETE ON notification_runtime.policy_revision
FOR EACH ROW EXECUTE FUNCTION notification_runtime.reject_notification_history_mutation();
CREATE TRIGGER notification_policy_assignment_immutable BEFORE UPDATE OR DELETE ON notification_runtime.policy_assignment
FOR EACH ROW EXECUTE FUNCTION notification_runtime.reject_notification_history_mutation();
CREATE TRIGGER notification_source_alarm_event_immutable BEFORE UPDATE OR DELETE ON notification_runtime.source_alarm_event
FOR EACH ROW EXECUTE FUNCTION notification_runtime.reject_notification_history_mutation();
CREATE TRIGGER notification_intent_event_immutable BEFORE UPDATE OR DELETE ON notification_runtime.intent_event
FOR EACH ROW EXECUTE FUNCTION notification_runtime.reject_notification_history_mutation();

CREATE FUNCTION notification_runtime.enforce_notification_intent_frozen_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $func$
BEGIN
  IF (to_jsonb(NEW) - ARRAY['status','disposition','external_delivery_intent_id','lease_owner','lease_until','lease_fence','updated_at'])
     IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','disposition','external_delivery_intent_id','lease_owner','lease_until','lease_fence','updated_at']) THEN
    RAISE EXCEPTION 'notification intent frozen snapshot is immutable';
  END IF;
  RETURN NEW;
END;
$func$;
CREATE TRIGGER notification_intent_frozen_snapshot BEFORE UPDATE ON notification_runtime.notification_intent
FOR EACH ROW EXECUTE FUNCTION notification_runtime.enforce_notification_intent_frozen_snapshot();

ALTER TABLE notification_runtime.template_revision ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.template_revision FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.audience_revision ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.audience_revision FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.policy_revision ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.policy_revision FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.policy_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.policy_assignment FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.source_alarm_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.source_alarm_event FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.user_preference ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.user_preference FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.notification_intent ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.notification_intent FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.inbox_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.inbox_item FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.intent_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_runtime.intent_event FORCE ROW LEVEL SECURITY;

CREATE POLICY notification_template_runtime ON notification_runtime.template_revision FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_audience_runtime ON notification_runtime.audience_revision FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_policy_runtime ON notification_runtime.policy_revision FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_assignment_runtime ON notification_runtime.policy_assignment FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_source_event_runtime ON notification_runtime.source_alarm_event FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_preference_runtime ON notification_runtime.user_preference FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_intent_runtime ON notification_runtime.notification_intent FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_intent_scheduler ON notification_runtime.notification_intent FOR ALL TO s16_notification_scheduler
  USING (true)
  WITH CHECK (true);
CREATE POLICY notification_inbox_runtime ON notification_runtime.inbox_item FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY notification_intent_event_runtime ON notification_runtime.intent_event FOR ALL TO s16_notification_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON ALL TABLES IN SCHEMA notification_runtime FROM PUBLIC, s16_notification_runtime;
GRANT USAGE ON SCHEMA notification_runtime TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.template_revision TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.audience_revision TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.policy_revision TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.policy_assignment TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.source_alarm_event TO s16_notification_runtime;
GRANT SELECT, INSERT, UPDATE ON notification_runtime.user_preference TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.notification_intent TO s16_notification_runtime;
GRANT UPDATE (status, disposition, external_delivery_intent_id, lease_owner, lease_until, lease_fence, updated_at)
  ON notification_runtime.notification_intent TO s16_notification_runtime;
GRANT USAGE ON SCHEMA notification_runtime TO s16_notification_scheduler;
GRANT SELECT ON notification_runtime.notification_intent TO s16_notification_scheduler;
GRANT UPDATE (status, lease_owner, lease_until, lease_fence, updated_at)
  ON notification_runtime.notification_intent TO s16_notification_scheduler;
GRANT SELECT, INSERT, UPDATE ON notification_runtime.inbox_item TO s16_notification_runtime;
GRANT SELECT, INSERT ON notification_runtime.intent_event TO s16_notification_runtime;

RESET ROLE;
COMMIT;
