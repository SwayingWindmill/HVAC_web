-- Site operating schedules and exceptions (#780): each Site has one weekly operating schedule
-- with pre-cool lead and coast-down lag grace windows and immutable per-revision cooling slots,
-- plus full-day date-range operating exceptions with operator request and site-manager approval.
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE core_registry.operating_schedules (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  name text NOT NULL DEFAULT '默认周运行计划' CHECK (length(btrim(name)) BETWEEN 1 AND 100),
  pre_cool_lead_minutes integer NOT NULL DEFAULT 45 CHECK (pre_cool_lead_minutes BETWEEN 0 AND 120),
  coast_down_lag_minutes integer NOT NULL DEFAULT 30 CHECK (coast_down_lag_minutes BETWEEN 0 AND 60),
  revision bigint NOT NULL CHECK (revision >= 1),
  updated_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(updated_by)),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE core_registry.operating_schedule_slots (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  schedule_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(schedule_id)),
  schedule_revision bigint NOT NULL CHECK (schedule_revision >= 1),
  day_of_week smallint NOT NULL CHECK (day_of_week BETWEEN 1 AND 7),
  start_minute smallint NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
  end_minute smallint NOT NULL CHECK (end_minute BETWEEN 1 AND 1440 AND end_minute > start_minute),
  target_supply_temp_c double precision CHECK (target_supply_temp_c IS NULL OR (target_supply_temp_c >= 4.0 AND target_supply_temp_c <= 20.0)),
  span_group_id uuid CHECK (span_group_id IS NULL OR core_registry.is_uuid_v7(span_group_id)),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, schedule_id, schedule_revision, day_of_week, start_minute),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, schedule_id) REFERENCES core_registry.operating_schedules(tenant_id, site_id, id)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_operating_schedule_slot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id::text || '/' || NEW.site_id::text || '/schedule-slots/' || NEW.schedule_revision::text, 0));
  IF EXISTS (
    SELECT 1 FROM core_registry.operating_schedule_slots AS other
    WHERE other.tenant_id = NEW.tenant_id
      AND other.site_id = NEW.site_id
      AND other.schedule_id = NEW.schedule_id
      AND other.schedule_revision = NEW.schedule_revision
      AND other.day_of_week = NEW.day_of_week
      AND other.id <> NEW.id
      AND int4range(other.start_minute::integer, other.end_minute::integer, '[)')
          && int4range(NEW.start_minute::integer, NEW.end_minute::integer, '[)')
  ) THEN
    RAISE EXCEPTION 'operating schedule slots on the same day cannot overlap' USING ERRCODE = '23P01';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER operating_schedule_slots_reject_overlap
BEFORE INSERT OR UPDATE ON core_registry.operating_schedule_slots
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_operating_schedule_slot();

CREATE TABLE core_registry.operating_exceptions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 100),
  kind text NOT NULL CHECK (kind IN ('HOLIDAY_SHUTDOWN', 'OVERTIME_COOLING', 'MAINTENANCE_WINDOW')),
  start_date date NOT NULL,
  end_date date NOT NULL CHECK (end_date >= start_date),
  replacement_slots jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(replacement_slots) = 'array'),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
  status text NOT NULL CHECK (status IN ('PENDING_APPROVAL', 'APPROVED', 'REJECTED', 'CANCELLED')),
  requested_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(requested_by)),
  requested_by_display text NOT NULL CHECK (length(btrim(requested_by_display)) BETWEEN 1 AND 256),
  decided_by uuid CHECK (decided_by IS NULL OR core_registry.is_uuid_v7(decided_by)),
  decided_by_display text CHECK (decided_by_display IS NULL OR length(btrim(decided_by_display)) BETWEEN 1 AND 256),
  decided_at timestamptz,
  decision_note text NOT NULL DEFAULT '' CHECK (length(decision_note) <= 1000),
  revision bigint NOT NULL CHECK (revision >= 1),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (
    (kind IN ('HOLIDAY_SHUTDOWN', 'MAINTENANCE_WINDOW') AND jsonb_array_length(replacement_slots) = 0)
    OR (kind = 'OVERTIME_COOLING' AND jsonb_array_length(replacement_slots) >= 1)
  ),
  CHECK ((status = 'PENDING_APPROVAL') = (decided_at IS NULL)),
  CHECK (updated_at >= created_at)
);

CREATE INDEX operating_exceptions_site_dates_idx
  ON core_registry.operating_exceptions (tenant_id, site_id, start_date DESC, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_approved_operating_exception()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status <> 'APPROVED' THEN
    RETURN NEW;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id::text || '/' || NEW.site_id::text || '/operating-exceptions', 0));
  IF EXISTS (
    SELECT 1 FROM core_registry.operating_exceptions AS other
    WHERE other.tenant_id = NEW.tenant_id
      AND other.site_id = NEW.site_id
      AND other.id <> NEW.id
      AND other.status = 'APPROVED'
      AND daterange(other.start_date, other.end_date, '[]')
          && daterange(NEW.start_date, NEW.end_date, '[]')
  ) THEN
    RAISE EXCEPTION 'approved operating exceptions on a Site cannot overlap in date range' USING ERRCODE = '23P01';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER operating_exceptions_reject_approved_overlap
BEFORE INSERT OR UPDATE ON core_registry.operating_exceptions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_approved_operating_exception();

ALTER TABLE core_registry.registry_audit_facts
  DROP CONSTRAINT IF EXISTS registry_audit_facts_resource_type_check;
ALTER TABLE core_registry.registry_audit_facts
  ADD CONSTRAINT registry_audit_facts_resource_type_check
  CHECK (resource_type IN (
    'SITE', 'SPACE', 'ASSET', 'DEVICE', 'SENSOR', 'POINT', 'BINDING',
    'TEMPLATE_REVISION', 'TEMPLATE_ASSIGNMENT', 'IMPORT', 'RETIREMENT',
    'OPERATING_SCHEDULE', 'OPERATING_EXCEPTION'
  ));

ALTER TABLE core_registry.operating_schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.operating_schedules FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.operating_schedule_slots ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.operating_schedule_slots FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.operating_exceptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.operating_exceptions FORCE ROW LEVEL SECURITY;

CREATE POLICY operating_schedules_migrator_all ON core_registry.operating_schedules
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
CREATE POLICY operating_schedule_slots_migrator_all ON core_registry.operating_schedule_slots
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
CREATE POLICY operating_exceptions_migrator_all ON core_registry.operating_exceptions
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

CREATE POLICY operating_schedules_runtime_scope ON core_registry.operating_schedules
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY operating_schedule_slots_runtime_scope ON core_registry.operating_schedule_slots
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY operating_exceptions_runtime_scope ON core_registry.operating_exceptions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY operating_schedules_writer_scope ON core_registry.operating_schedules
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY operating_schedule_slots_writer_scope ON core_registry.operating_schedule_slots
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY operating_exceptions_writer_scope ON core_registry.operating_exceptions
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.operating_schedules, core_registry.operating_schedule_slots, core_registry.operating_exceptions FROM PUBLIC;
GRANT SELECT ON core_registry.operating_schedules, core_registry.operating_schedule_slots, core_registry.operating_exceptions TO s1_core_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.operating_schedules, core_registry.operating_exceptions TO s1_core_writer;
GRANT SELECT, INSERT ON core_registry.operating_schedule_slots TO s1_core_writer;

RESET ROLE;
SET LOCAL ROLE s1_iam_migrator;

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT 9, 'operating-schedules-v1', capabilities || ARRAY['schedule.request', 'schedule.manage']::text[], 'ACTIVE', '2026-11-11T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 8;

UPDATE iam.role_templates
SET capabilities = array_append(capabilities, 'schedule.request'),
    revision = revision + 1,
    updated_at = '2026-11-11T00:00:00Z'
WHERE tenant_id IS NULL
  AND role_key = 'operator'
  AND NOT capabilities @> ARRAY['schedule.request']::text[];

UPDATE iam.role_templates
SET capabilities = capabilities || ARRAY['schedule.request', 'schedule.manage']::text[],
    revision = revision + 1,
    updated_at = '2026-11-11T00:00:00Z'
WHERE tenant_id IS NULL
  AND role_key IN ('site-manager', 'admin')
  AND NOT capabilities @> ARRAY['schedule.manage']::text[];

RESET ROLE;
COMMIT;
