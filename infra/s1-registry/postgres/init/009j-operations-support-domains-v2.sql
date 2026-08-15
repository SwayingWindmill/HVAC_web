BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- SE-ARCH-004 V2.1.2 Operations support domains.
-- These are logical domain boundaries inside the Phase 1 modular backend; they
-- are not declarations of mandatory standalone microservices.

-- ---------------------------------------------------------------------------
-- Config Domain
-- Config Release != Direct Device Write. Device application is an orchestration
-- concern and high-risk control parameters must still pass Control/Safety.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS core_registry.config_resources (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  resource_type text NOT NULL CHECK (resource_type IN ('GATEWAY','DEVICE','ASSET','SITE','SYSTEM')),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  config_code text NOT NULL CHECK (config_code ~ '^[a-z][a-z0-9_.-]{0,127}$'),
  risk_level text NOT NULL CHECK (risk_level IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  execution_mode text NOT NULL CHECK (execution_mode IN ('CLOUD_ONLY','EDGE_APPLY','CONTROL_GOVERNED')),
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, resource_type, subject_id, config_code),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK ((risk_level IN ('HIGH','CRITICAL')) = (execution_mode = 'CONTROL_GOVERNED') OR risk_level IN ('LOW','MEDIUM')),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.config_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  config_resource_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(config_resource_id)),
  version_no bigint NOT NULL CHECK (version_no > 0),
  content jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
  content_checksum text NOT NULL CHECK (content_checksum ~ '^[a-f0-9]{64}$'),
  status text NOT NULL CHECK (status IN ('DRAFT','VALIDATING','SUBMITTED','APPROVAL_PENDING','APPROVED','RELEASED','REJECTED','SUPERSEDED','ROLLED_BACK')),
  reason text,
  submitted_at timestamptz,
  approved_at timestamptz,
  released_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, config_resource_id, version_no),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, config_resource_id) REFERENCES core_registry.config_resources(tenant_id, site_id, id),
  CHECK (updated_at >= created_at),
  CHECK (status NOT IN ('SUBMITTED','APPROVAL_PENDING','APPROVED','RELEASED','REJECTED','SUPERSEDED','ROLLED_BACK') OR submitted_at IS NOT NULL),
  CHECK (status NOT IN ('APPROVED','RELEASED','SUPERSEDED','ROLLED_BACK') OR approved_at IS NOT NULL),
  CHECK (status NOT IN ('RELEASED','SUPERSEDED','ROLLED_BACK') OR released_at IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS config_versions_one_released_uidx
  ON core_registry.config_versions (tenant_id, site_id, config_resource_id)
  WHERE status = 'RELEASED';

CREATE TABLE IF NOT EXISTS core_registry.config_desired_states (
  config_resource_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(config_resource_id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  config_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(config_version_id)),
  desired_at timestamptz NOT NULL,
  orchestration_state text NOT NULL CHECK (orchestration_state IN ('PENDING','APPLYING','APPLIED','FAILED','ROLLBACK_PENDING')),
  last_error text,
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, site_id, config_resource_id) REFERENCES core_registry.config_resources(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, config_version_id) REFERENCES core_registry.config_versions(tenant_id, site_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.config_reported_states (
  config_resource_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(config_resource_id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  reported_version_id uuid,
  reported_checksum text CHECK (reported_checksum IS NULL OR reported_checksum ~ '^[a-f0-9]{64}$'),
  reported_content jsonb CHECK (reported_content IS NULL OR jsonb_typeof(reported_content) = 'object'),
  reported_at timestamptz,
  source text NOT NULL CHECK (source IN ('EDGE','DEVICE','CLOUD')),
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, site_id, config_resource_id) REFERENCES core_registry.config_resources(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, reported_version_id) REFERENCES core_registry.config_versions(tenant_id, site_id, id)
);

CREATE OR REPLACE FUNCTION core_registry.reject_released_config_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status IN ('RELEASED','SUPERSEDED','ROLLED_BACK') AND (
       NEW.content IS DISTINCT FROM OLD.content
    OR NEW.content_checksum IS DISTINCT FROM OLD.content_checksum
    OR NEW.config_resource_id IS DISTINCT FROM OLD.config_resource_id
    OR NEW.version_no IS DISTINCT FROM OLD.version_no
  ) THEN
    RAISE EXCEPTION 'Released Config Version content is immutable; create a new version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS config_versions_reject_released_mutation ON core_registry.config_versions;
CREATE TRIGGER config_versions_reject_released_mutation
BEFORE UPDATE ON core_registry.config_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_released_config_mutation();

-- ---------------------------------------------------------------------------
-- Notification Domain
-- Notification ACK/Read is intentionally independent from Alarm lifecycle.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS core_registry.notification_policies (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  policy_code text NOT NULL CHECK (policy_code ~ '^[a-z][a-z0-9_.-]{0,127}$'),
  event_type text NOT NULL CHECK (length(btrim(event_type)) BETWEEN 1 AND 128),
  channel text NOT NULL CHECK (channel IN ('IN_APP','EMAIL','SMS','WEBHOOK')),
  enabled boolean NOT NULL DEFAULT true,
  silence_until timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, policy_code),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.notification_templates (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  template_code text NOT NULL CHECK (template_code ~ '^[a-z][a-z0-9_.-]{0,127}$'),
  locale text NOT NULL CHECK (length(btrim(locale)) BETWEEN 2 AND 32),
  channel text NOT NULL CHECK (channel IN ('IN_APP','EMAIL','SMS','WEBHOOK')),
  subject_template text,
  body_template text NOT NULL,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, template_code, locale, channel),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.notification_messages (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  source_event_id uuid,
  subject_type text NOT NULL CHECK (length(btrim(subject_type)) BETWEEN 1 AND 64),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  template_id uuid,
  title text,
  body text NOT NULL,
  severity text NOT NULL CHECK (severity IN ('INFO','WARNING','CRITICAL')),
  created_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, template_id) REFERENCES core_registry.notification_templates(tenant_id, id),
  FOREIGN KEY (source_event_id) REFERENCES core_registry.domain_outbox_events(id)
);

CREATE TABLE IF NOT EXISTS core_registry.notification_deliveries (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  message_id uuid NOT NULL REFERENCES core_registry.notification_messages(id) ON DELETE CASCADE,
  recipient_type text NOT NULL CHECK (recipient_type IN ('USER','ROLE','WORKLOAD','WEBHOOK')),
  recipient_id text NOT NULL CHECK (length(btrim(recipient_id)) BETWEEN 1 AND 256),
  channel text NOT NULL CHECK (channel IN ('IN_APP','EMAIL','SMS','WEBHOOK')),
  status text NOT NULL CHECK (status IN ('PENDING','SENT','DELIVERED','FAILED','SUPPRESSED')),
  attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  sent_at timestamptz,
  delivered_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (message_id, recipient_type, recipient_id, channel),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.notification_user_states (
  message_id uuid NOT NULL REFERENCES core_registry.notification_messages(id) ON DELETE CASCADE,
  user_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(user_id)),
  read_at timestamptz,
  notification_ack_at timestamptz,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (message_id, user_id),
  CHECK (notification_ack_at IS NULL OR read_at IS NOT NULL)
);

-- Explicitly no FK from Notification state into Alarm status/ack tables. A user
-- reading or acknowledging a notification is not an Alarm ACK business action.

CREATE INDEX IF NOT EXISTS notification_messages_scope_idx
  ON core_registry.notification_messages (tenant_id, site_id, created_at DESC, id);
CREATE INDEX IF NOT EXISTS notification_deliveries_ready_idx
  ON core_registry.notification_deliveries (status, created_at, id)
  WHERE status IN ('PENDING','FAILED');

ALTER TABLE core_registry.config_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.config_resources FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.config_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.config_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.config_desired_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.config_desired_states FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.config_reported_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.config_reported_states FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_templates FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_messages FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_user_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.notification_user_states FORCE ROW LEVEL SECURITY;

CREATE POLICY config_resources_runtime_scope ON core_registry.config_resources
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY config_versions_runtime_scope ON core_registry.config_versions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY config_desired_states_runtime_scope ON core_registry.config_desired_states
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY config_reported_states_runtime_scope ON core_registry.config_reported_states
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY notification_policies_runtime_scope ON core_registry.notification_policies
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY notification_templates_runtime_scope ON core_registry.notification_templates
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY notification_messages_runtime_scope ON core_registry.notification_messages
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY notification_deliveries_runtime_scope ON core_registry.notification_deliveries
  FOR SELECT TO s1_core_runtime
  USING (EXISTS (
    SELECT 1 FROM core_registry.notification_messages m
    WHERE m.id = message_id AND m.tenant_id = core_registry.current_tenant_id()
      AND (m.site_id IS NULL OR core_registry.is_authorized_site(m.site_id))
  ));
CREATE POLICY notification_user_states_runtime_scope ON core_registry.notification_user_states
  FOR SELECT TO s1_core_runtime
  USING (EXISTS (
    SELECT 1 FROM core_registry.notification_messages m
    WHERE m.id = message_id AND m.tenant_id = core_registry.current_tenant_id()
      AND (m.site_id IS NULL OR core_registry.is_authorized_site(m.site_id))
  ));

REVOKE ALL ON core_registry.config_resources, core_registry.config_versions,
  core_registry.config_desired_states, core_registry.config_reported_states,
  core_registry.notification_policies, core_registry.notification_templates,
  core_registry.notification_messages, core_registry.notification_deliveries,
  core_registry.notification_user_states FROM PUBLIC;
GRANT SELECT ON core_registry.config_resources, core_registry.config_versions,
  core_registry.config_desired_states, core_registry.config_reported_states,
  core_registry.notification_policies, core_registry.notification_templates,
  core_registry.notification_messages, core_registry.notification_deliveries,
  core_registry.notification_user_states TO s1_core_runtime;

COMMIT;
