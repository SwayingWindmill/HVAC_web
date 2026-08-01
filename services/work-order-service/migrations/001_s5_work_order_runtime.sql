BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_current (
  work_order_id uuid PRIMARY KEY,
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 256),
  description text NOT NULL CHECK (length(btrim(description)) BETWEEN 1 AND 4096),
  priority text NOT NULL CHECK (priority IN ('LOW', 'MEDIUM', 'HIGH', 'URGENT')),
  status text NOT NULL CHECK (status IN ('DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED', 'COMPLETED', 'CANCELLED')),
  assignee_id text CHECK (assignee_id IS NULL OR length(btrim(assignee_id)) BETWEEN 1 AND 256),
  team_id text CHECK (team_id IS NULL OR length(btrim(team_id)) BETWEEN 1 AND 256),
  scheduled_start timestamptz,
  due_at timestamptz CHECK (due_at IS NULL OR scheduled_start IS NULL OR due_at >= scheduled_start),
  task_total bigint NOT NULL DEFAULT 0 CHECK (task_total >= 0),
  task_completed bigint NOT NULL DEFAULT 0 CHECK (task_completed >= 0 AND task_completed <= task_total),
  task_blocked bigint NOT NULL DEFAULT 0 CHECK (task_blocked >= 0 AND task_blocked <= task_total - task_completed),
  note_count bigint NOT NULL DEFAULT 0 CHECK (note_count >= 0),
  attachment_count bigint NOT NULL DEFAULT 0 CHECK (attachment_count >= 0),
  version bigint NOT NULL CHECK (version > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
  UNIQUE (organization_id, site_id, work_order_id)
);

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_source_reference (
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  work_order_id uuid NOT NULL,
  source_domain text NOT NULL CHECK (source_domain IN ('MANUAL', 'ALARM', 'ASSET', 'EQUIPMENT', 'INVESTIGATION', 'EXTERNAL')),
  source_resource_id text NOT NULL CHECK (length(btrim(source_resource_id)) BETWEEN 1 AND 512),
  relationship text NOT NULL CHECK (relationship IN ('ORIGIN', 'RELATED')),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (organization_id, site_id, work_order_id, source_domain, source_resource_id),
  FOREIGN KEY (organization_id, site_id, work_order_id)
    REFERENCES work_order_runtime.work_order_current (organization_id, site_id, work_order_id)
    ON DELETE RESTRICT
);
CREATE UNIQUE INDEX IF NOT EXISTS work_order_one_origin_idx
  ON work_order_runtime.work_order_source_reference (organization_id, site_id, work_order_id)
  WHERE relationship = 'ORIGIN';

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_timeline (
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  work_order_id uuid NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  operation text NOT NULL CHECK (operation IN ('CREATE', 'OPEN', 'ASSIGN', 'UNASSIGN', 'SCHEDULE', 'START', 'BLOCK', 'RESUME', 'COMPLETE', 'CANCEL', 'REOPEN')),
  from_status text CHECK (from_status IS NULL OR from_status IN ('DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED', 'COMPLETED', 'CANCELLED')),
  to_status text NOT NULL CHECK (to_status IN ('DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED', 'COMPLETED', 'CANCELLED')),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 256),
  actor_type text NOT NULL CHECK (length(btrim(actor_type)) BETWEEN 1 AND 64),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  policy_revision text CHECK (policy_revision IS NULL OR length(btrim(policy_revision)) BETWEEN 1 AND 128),
  correlation_id text CHECK (correlation_id IS NULL OR length(btrim(correlation_id)) BETWEEN 1 AND 256),
  occurred_at timestamptz NOT NULL,
  PRIMARY KEY (organization_id, site_id, work_order_id, version),
  FOREIGN KEY (organization_id, site_id, work_order_id)
    REFERENCES work_order_runtime.work_order_current (organization_id, site_id, work_order_id)
    ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_task (
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  work_order_id uuid NOT NULL,
  task_id uuid NOT NULL,
  position integer NOT NULL CHECK (position >= 0),
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('OPEN', 'COMPLETED', 'BLOCKED')),
  version bigint NOT NULL CHECK (version > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
  PRIMARY KEY (organization_id, site_id, work_order_id, task_id),
  UNIQUE (organization_id, site_id, work_order_id, position),
  FOREIGN KEY (organization_id, site_id, work_order_id)
    REFERENCES work_order_runtime.work_order_current (organization_id, site_id, work_order_id)
    ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_note (
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  work_order_id uuid NOT NULL,
  note_id uuid NOT NULL,
  author_id text NOT NULL CHECK (length(btrim(author_id)) BETWEEN 1 AND 256),
  body text NOT NULL CHECK (length(btrim(body)) BETWEEN 1 AND 4096),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (organization_id, site_id, work_order_id, note_id),
  FOREIGN KEY (organization_id, site_id, work_order_id)
    REFERENCES work_order_runtime.work_order_current (organization_id, site_id, work_order_id)
    ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_attachment_metadata (
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  work_order_id uuid NOT NULL,
  attachment_id uuid NOT NULL,
  object_reference text NOT NULL CHECK (length(btrim(object_reference)) BETWEEN 1 AND 512),
  media_type text NOT NULL CHECK (length(btrim(media_type)) BETWEEN 1 AND 128),
  byte_size bigint NOT NULL CHECK (byte_size >= 0),
  sha256 text NOT NULL CHECK (sha256 ~ '^[a-f0-9]{64}$'),
  created_by text NOT NULL CHECK (length(btrim(created_by)) BETWEEN 1 AND 256),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (organization_id, site_id, work_order_id, attachment_id),
  FOREIGN KEY (organization_id, site_id, work_order_id)
    REFERENCES work_order_runtime.work_order_current (organization_id, site_id, work_order_id)
    ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_completion_evidence (
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  work_order_id uuid NOT NULL,
  kind text NOT NULL CHECK (length(btrim(kind)) BETWEEN 1 AND 128),
  reference text NOT NULL CHECK (length(btrim(reference)) BETWEEN 1 AND 512),
  captured_at timestamptz NOT NULL,
  PRIMARY KEY (organization_id, site_id, work_order_id, kind, reference),
  FOREIGN KEY (organization_id, site_id, work_order_id)
    REFERENCES work_order_runtime.work_order_current (organization_id, site_id, work_order_id)
    ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS work_order_current_site_updated_idx
  ON work_order_runtime.work_order_current (organization_id, site_id, updated_at DESC, work_order_id ASC);
CREATE INDEX IF NOT EXISTS work_order_current_site_status_idx
  ON work_order_runtime.work_order_current (organization_id, site_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS work_order_current_site_priority_idx
  ON work_order_runtime.work_order_current (organization_id, site_id, priority, updated_at DESC);
CREATE INDEX IF NOT EXISTS work_order_current_site_assignee_idx
  ON work_order_runtime.work_order_current (organization_id, site_id, assignee_id, updated_at DESC)
  WHERE assignee_id IS NOT NULL;

DO $policy$
DECLARE
  table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'work_order_current',
    'work_order_source_reference',
    'work_order_timeline',
    'work_order_task',
    'work_order_note',
    'work_order_attachment_metadata',
    'work_order_completion_evidence'
  ]
  LOOP
    EXECUTE format('ALTER TABLE work_order_runtime.%I ENABLE ROW LEVEL SECURITY', table_name);
    EXECUTE format('ALTER TABLE work_order_runtime.%I FORCE ROW LEVEL SECURITY', table_name);
    EXECUTE format('DROP POLICY IF EXISTS %I ON work_order_runtime.%I', table_name || '_runtime_org', table_name);
    EXECUTE format(
      'CREATE POLICY %I ON work_order_runtime.%I FOR SELECT TO s5_work_order_runtime USING (organization_id = NULLIF(current_setting(''app.organization_id'', true), '''')::uuid)',
      table_name || '_runtime_org',
      table_name
    );
  END LOOP;
END
$policy$;

REVOKE ALL ON SCHEMA work_order_runtime FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA work_order_runtime FROM PUBLIC;
GRANT USAGE ON SCHEMA work_order_runtime TO s5_work_order_runtime;
GRANT SELECT ON ALL TABLES IN SCHEMA work_order_runtime TO s5_work_order_runtime;

COMMIT;
