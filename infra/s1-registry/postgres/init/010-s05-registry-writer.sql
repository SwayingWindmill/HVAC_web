\set ON_ERROR_STOP on

BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 's1_core_writer') THEN
    CREATE ROLE s1_core_writer NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

GRANT CONNECT ON DATABASE hvac_s1 TO s1_core_writer;
GRANT USAGE ON SCHEMA core_registry TO s1_core_writer;
GRANT s1_core_writer TO s1_core_service;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE core_registry.registry_write_requests (
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
  action text NOT NULL CHECK (length(btrim(action)) BETWEEN 1 AND 128),
  request_digest text NOT NULL CHECK (request_digest ~ '^[a-f0-9]{64}$'),
  response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE TABLE core_registry.registry_audit_facts (
  event_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(event_id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  principal_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(principal_id)),
  action text NOT NULL CHECK (length(btrim(action)) BETWEEN 1 AND 128),
  resource_type text NOT NULL CHECK (resource_type IN ('SITE','SPACE','ASSET','DEVICE','SENSOR','POINT','BINDING','TEMPLATE_REVISION','TEMPLATE_ASSIGNMENT','IMPORT','RETIREMENT')),
  resource_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(resource_id)),
  before_revision bigint CHECK (before_revision IS NULL OR before_revision > 0),
  after_revision bigint CHECK (after_revision IS NULL OR after_revision > 0),
  outcome text NOT NULL CHECK (outcome IN ('COMMITTED','BLOCKED')),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
  occurred_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE INDEX registry_audit_facts_scope_idx
  ON core_registry.registry_audit_facts (tenant_id, site_id, occurred_at DESC, event_id);

CREATE TABLE core_registry.registry_external_ids (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  namespace text NOT NULL CHECK (length(btrim(namespace)) BETWEEN 1 AND 128),
  external_id text NOT NULL CHECK (length(btrim(external_id)) BETWEEN 1 AND 256),
  resource_type text NOT NULL CHECK (resource_type IN ('SPACE','ASSET','DEVICE','SENSOR','POINT')),
  resource_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(resource_id)),
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, namespace, external_id),
  UNIQUE (tenant_id, site_id, resource_type, resource_id, namespace),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE core_registry.registry_templates (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  template_key text NOT NULL CHECK (template_key ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
  template_kind text NOT NULL CHECK (template_kind IN ('ASSET','DEVICE','POINT')),
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, template_key),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE core_registry.registry_template_revisions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  template_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(template_id)),
  revision_number bigint NOT NULL CHECK (revision_number > 0),
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED')),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  release_references jsonb NOT NULL CHECK (jsonb_typeof(release_references) = 'object'),
  created_at timestamptz NOT NULL,
  released_at timestamptz,
  UNIQUE (tenant_id, template_id, revision_number),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, template_id) REFERENCES core_registry.registry_templates(tenant_id, id),
  CHECK ((status = 'DRAFT' AND released_at IS NULL) OR (status = 'RELEASED' AND released_at IS NOT NULL))
);

CREATE OR REPLACE FUNCTION core_registry.reject_released_template_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' OR OLD.status = 'RELEASED' THEN
    RAISE EXCEPTION 'released TemplateRevision is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER registry_template_revision_immutable
BEFORE UPDATE OR DELETE ON core_registry.registry_template_revisions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_released_template_revision_mutation();

CREATE TABLE core_registry.registry_template_assignments (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  target_type text NOT NULL CHECK (target_type IN ('ASSET','DEVICE','POINT')),
  target_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(target_id)),
  template_revision_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(template_revision_id)),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, target_type, target_id, valid_from),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, template_revision_id) REFERENCES core_registry.registry_template_revisions(tenant_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX registry_template_assignments_current_uidx
  ON core_registry.registry_template_assignments (tenant_id, site_id, target_type, target_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

CREATE OR REPLACE FUNCTION core_registry.validate_template_assignment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  revision_kind text;
  target_exists boolean;
BEGIN
  SELECT template.template_kind
    INTO revision_kind
  FROM core_registry.registry_template_revisions revision
  JOIN core_registry.registry_templates template
    ON template.tenant_id = revision.tenant_id AND template.id = revision.template_id
  WHERE revision.tenant_id = NEW.tenant_id
    AND revision.id = NEW.template_revision_id
    AND revision.status = 'RELEASED';
  IF revision_kind IS NULL OR revision_kind <> NEW.target_type THEN
    RAISE EXCEPTION 'TemplateRevision must be released and match assignment target type' USING ERRCODE = '23514';
  END IF;

  IF NEW.target_type = 'ASSET' THEN
    SELECT EXISTS (SELECT 1 FROM core_registry.assets WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.target_id) INTO target_exists;
  ELSIF NEW.target_type = 'DEVICE' THEN
    SELECT EXISTS (SELECT 1 FROM core_registry.devices WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.target_id) INTO target_exists;
  ELSE
    SELECT EXISTS (SELECT 1 FROM core_registry.telemetry_points WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.target_id) INTO target_exists;
  END IF;
  IF NOT target_exists THEN
    RAISE EXCEPTION 'TemplateAssignment target is outside Tenant/Site scope' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER registry_template_assignment_validate
BEFORE INSERT OR UPDATE ON core_registry.registry_template_assignments
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_template_assignment();

CREATE TABLE core_registry.registry_retirement_sagas (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  resource_type text NOT NULL CHECK (resource_type IN ('SITE','SPACE','ASSET','DEVICE','SENSOR','POINT')),
  resource_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(resource_id)),
  expected_revision bigint NOT NULL CHECK (expected_revision > 0),
  status text NOT NULL CHECK (status IN ('PENDING','BLOCKED','COMPLETED')),
  dependency_count integer NOT NULL CHECK (dependency_count >= 0),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
  requested_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(requested_by)),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  completed_at timestamptz,
  UNIQUE (tenant_id, site_id, resource_type, resource_id, expected_revision),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

DROP INDEX IF EXISTS core_registry.device_bindings_active_relation_uidx;
CREATE UNIQUE INDEX device_bindings_active_relation_uidx
  ON core_registry.device_bindings (tenant_id, site_id, device_id, asset_id, binding_role)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;
CREATE UNIQUE INDEX device_bindings_current_single_role_uidx
  ON core_registry.device_bindings (tenant_id, site_id, device_id, binding_role)
  WHERE status = 'ACTIVE' AND valid_to IS NULL AND binding_role <> 'GATEWAY';

CREATE OR REPLACE FUNCTION core_registry.reject_device_binding_overlap()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status <> 'ACTIVE' OR NEW.binding_role = 'GATEWAY' THEN
    RETURN NEW;
  END IF;
  IF EXISTS (
    SELECT 1 FROM core_registry.device_bindings existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.device_id = NEW.device_id
      AND existing.binding_role = NEW.binding_role
      AND existing.status = 'ACTIVE'
      AND existing.id <> NEW.id
      AND tstzrange(existing.valid_from, existing.valid_to, '[)') && tstzrange(NEW.valid_from, NEW.valid_to, '[)')
  ) THEN
    RAISE EXCEPTION 'Device binding role interval overlaps an existing assignment' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER device_bindings_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, device_id, binding_role, status, valid_from, valid_to
ON core_registry.device_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_device_binding_overlap();

CREATE UNIQUE INDEX point_subject_current_role_uidx
  ON core_registry.point_subject_bindings (tenant_id, site_id, point_id, binding_role)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

ALTER TABLE core_registry.registry_write_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_audit_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_external_ids ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_template_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_template_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_retirement_sagas ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_write_requests FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_audit_facts FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_external_ids FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_templates FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_template_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_template_assignments FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.registry_retirement_sagas FORCE ROW LEVEL SECURITY;

CREATE POLICY registry_write_requests_writer_scope ON core_registry.registry_write_requests
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY registry_audit_facts_writer_scope ON core_registry.registry_audit_facts
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY registry_external_ids_writer_scope ON core_registry.registry_external_ids
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY registry_templates_writer_scope ON core_registry.registry_templates
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id())
  WITH CHECK (tenant_id = core_registry.current_tenant_id());
CREATE POLICY registry_template_revisions_writer_scope ON core_registry.registry_template_revisions
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id())
  WITH CHECK (tenant_id = core_registry.current_tenant_id());
CREATE POLICY registry_template_assignments_writer_scope ON core_registry.registry_template_assignments
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY registry_retirement_sagas_writer_scope ON core_registry.registry_retirement_sagas
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY sites_writer_scope ON core_registry.sites
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id())
  WITH CHECK (tenant_id = core_registry.current_tenant_id());
CREATE POLICY assets_writer_scope ON core_registry.assets
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY devices_writer_scope ON core_registry.devices
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY spaces_writer_scope ON core_registry.spaces
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY sensors_writer_scope ON core_registry.sensors
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY telemetry_points_writer_scope ON core_registry.telemetry_points
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY device_bindings_writer_scope ON core_registry.device_bindings
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY asset_space_bindings_writer_scope ON core_registry.asset_space_bindings
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY device_space_bindings_writer_scope ON core_registry.device_space_bindings
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY sensor_device_bindings_writer_scope ON core_registry.sensor_device_bindings
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY sensor_space_bindings_writer_scope ON core_registry.sensor_space_bindings
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY point_subject_bindings_writer_scope ON core_registry.point_subject_bindings
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY domain_outbox_registry_writer_scope ON core_registry.domain_outbox_events
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));

GRANT SELECT, INSERT, UPDATE ON
  core_registry.sites,
  core_registry.assets,
  core_registry.devices,
  core_registry.spaces,
  core_registry.sensors,
  core_registry.telemetry_points,
  core_registry.device_bindings,
  core_registry.asset_space_bindings,
  core_registry.device_space_bindings,
  core_registry.sensor_device_bindings,
  core_registry.sensor_space_bindings,
  core_registry.point_subject_bindings,
  core_registry.registry_write_requests,
  core_registry.registry_external_ids,
  core_registry.registry_templates,
  core_registry.registry_template_revisions,
  core_registry.registry_template_assignments,
  core_registry.registry_retirement_sagas,
  core_registry.domain_outbox_events
TO s1_core_writer;
GRANT SELECT, INSERT ON core_registry.registry_audit_facts TO s1_core_writer;

REVOKE DELETE ON ALL TABLES IN SCHEMA core_registry FROM s1_core_writer;
REVOKE ALL ON core_registry.registry_write_requests, core_registry.registry_audit_facts,
  core_registry.registry_external_ids, core_registry.registry_templates,
  core_registry.registry_template_revisions, core_registry.registry_template_assignments,
  core_registry.registry_retirement_sagas FROM PUBLIC;

RESET ROLE;
SET LOCAL ROLE s1_iam_migrator;

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
VALUES (
  2,
  's05-v1',
  ARRAY[
    'registry.read', 'site.list', 'site.read', 'asset.list', 'asset.read', 'device.list', 'device.read',
    'device-binding.list', 'asset-model.read',
    'site.write', 'space.write', 'asset.write', 'device.write', 'sensor.write', 'point.write',
    'binding.write', 'template.manage', 'registry.import', 'registry.retire',
    'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read',
    'telemetry.resubscribe', 'telemetry.recovery.use', 'telemetry.recovery.checkpoint',
    'analytics.energy-series.read', 'alarm:read', 'alarm:ack',
    'work-order:list', 'work-order:read', 'work-order:create', 'work-order:assign', 'work-order:plan',
    'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete', 'work-order:cancel', 'work-order:reopen',
    'session.revoke', 'audit.read', 'iam.admin', 'api-credential.manage'
  ],
  'ACTIVE',
  '2026-08-19T00:00:00Z'
);

RESET ROLE;
COMMIT;
