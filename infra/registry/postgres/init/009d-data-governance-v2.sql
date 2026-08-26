BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- Lifecycle policy is the PostgreSQL authority for retention/archive/delete
-- decisions. ClickHouse table TTL is intentionally not the governance source.
CREATE TABLE IF NOT EXISTS core_registry.data_lifecycle_policies (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  dataset_code text NOT NULL CHECK (dataset_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  data_class text NOT NULL CHECK (data_class IN ('CRITICAL','IMPORTANT','STANDARD','LOW_VALUE')),
  hot_retention_days integer NOT NULL CHECK (hot_retention_days >= 0),
  archive_after_days integer CHECK (archive_after_days IS NULL OR archive_after_days >= 0),
  delete_after_days integer CHECK (delete_after_days IS NULL OR delete_after_days >= 0),
  archive_required boolean NOT NULL DEFAULT false,
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, dataset_code, data_class, revision),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (archive_after_days IS NULL OR archive_after_days >= hot_retention_days),
  CHECK (delete_after_days IS NULL OR delete_after_days >= hot_retention_days),
  CHECK (archive_after_days IS NULL OR delete_after_days IS NULL OR delete_after_days >= archive_after_days),
  CHECK (NOT archive_required OR archive_after_days IS NOT NULL),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_lifecycle_policy()
RETURNS trigger
LANGUAGE plpgsql
AS $lifecycle_overlap$
BEGIN
  IF NEW.status = 'RELEASED' AND EXISTS (
    SELECT 1
    FROM core_registry.data_lifecycle_policies AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.dataset_code = NEW.dataset_code
      AND existing.data_class = NEW.data_class
      AND existing.id <> NEW.id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'released Data Lifecycle Policies cannot overlap' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$lifecycle_overlap$;

DROP TRIGGER IF EXISTS data_lifecycle_policies_reject_overlap ON core_registry.data_lifecycle_policies;
CREATE TRIGGER data_lifecycle_policies_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, dataset_code, data_class, effective_from, effective_to, status
ON core_registry.data_lifecycle_policies
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_lifecycle_policy();

CREATE TABLE IF NOT EXISTS core_registry.legal_holds (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  dataset_code text CHECK (dataset_code IS NULL OR dataset_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  scope_type text NOT NULL CHECK (scope_type IN ('TENANT','SITE','RESOURCE')),
  resource_key text,
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2048),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('ACTIVE','LIFTED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (
    (scope_type = 'TENANT' AND site_id IS NULL AND resource_key IS NULL)
    OR (scope_type = 'SITE' AND site_id IS NOT NULL AND resource_key IS NULL)
    OR (scope_type = 'RESOURCE' AND resource_key IS NOT NULL)
  ),
  CHECK (scope_type <> 'RESOURCE' OR dataset_code IS NOT NULL),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.deletion_requests (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  dataset_code text NOT NULL CHECK (dataset_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  resource_key text NOT NULL CHECK (length(btrim(resource_key)) BETWEEN 1 AND 1024),
  reason_code text NOT NULL CHECK (reason_code IN ('CONTRACT','REGULATORY','RETENTION','USER_REQUEST','SECURITY','OTHER')),
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL CHECK (status IN ('DRAFT','APPROVED','APPLIED','REJECTED')),
  requested_at timestamptz NOT NULL,
  approved_at timestamptz,
  applied_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, dataset_code, resource_key, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (jsonb_typeof(evidence) = 'object'),
  CHECK ((status = 'DRAFT' AND approved_at IS NULL AND applied_at IS NULL)
    OR (status = 'APPROVED' AND approved_at IS NOT NULL AND applied_at IS NULL)
    OR (status = 'APPLIED' AND approved_at IS NOT NULL AND applied_at IS NOT NULL)
    OR (status = 'REJECTED' AND applied_at IS NULL)),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.legal_hold_blocks_deletion(
  check_tenant_id uuid,
  check_site_id uuid,
  check_dataset_code text,
  check_resource_key text,
  check_time timestamptz
)
RETURNS boolean
LANGUAGE sql
STABLE
AS $legal_hold_match$
  SELECT EXISTS (
    SELECT 1
    FROM core_registry.legal_holds AS hold
    WHERE hold.tenant_id = check_tenant_id
      AND hold.status = 'ACTIVE'
      AND hold.effective_from <= check_time
      AND (hold.effective_to IS NULL OR check_time < hold.effective_to)
      AND (hold.dataset_code IS NULL OR hold.dataset_code = check_dataset_code)
      AND (
        hold.scope_type = 'TENANT'
        OR (hold.scope_type = 'SITE' AND hold.site_id = check_site_id)
        OR (hold.scope_type = 'RESOURCE' AND hold.resource_key = check_resource_key
            AND (hold.site_id IS NULL OR hold.site_id = check_site_id))
      )
  );
$legal_hold_match$;

CREATE OR REPLACE FUNCTION core_registry.validate_deletion_request_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $deletion_transition$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.status = 'DRAFT' AND NEW.status NOT IN ('DRAFT','APPROVED','REJECTED') THEN
      RAISE EXCEPTION 'Deletion Request must be APPROVED before APPLIED' USING ERRCODE = '23514';
    ELSIF OLD.status = 'APPROVED' AND NEW.status NOT IN ('APPROVED','APPLIED','REJECTED') THEN
      RAISE EXCEPTION 'APPROVED Deletion Request can only be applied or rejected' USING ERRCODE = '23514';
    ELSIF OLD.status IN ('APPLIED','REJECTED') AND NEW.status <> OLD.status THEN
      RAISE EXCEPTION 'completed Deletion Request is terminal' USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('APPROVED','APPLIED','REJECTED') AND (
      NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
      OR NEW.site_id IS DISTINCT FROM OLD.site_id
      OR NEW.dataset_code IS DISTINCT FROM OLD.dataset_code
      OR NEW.resource_key IS DISTINCT FROM OLD.resource_key
      OR NEW.reason_code IS DISTINCT FROM OLD.reason_code
      OR NEW.evidence IS DISTINCT FROM OLD.evidence
    ) THEN
      RAISE EXCEPTION 'approved Deletion Request identity/evidence is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;

  IF NEW.status IN ('APPROVED','APPLIED')
     AND core_registry.legal_hold_blocks_deletion(
       NEW.tenant_id, NEW.site_id, NEW.dataset_code, NEW.resource_key, coalesce(NEW.approved_at, NEW.applied_at, now())
     ) THEN
    RAISE EXCEPTION 'Deletion Request is blocked by an active Legal Hold' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$deletion_transition$;

DROP TRIGGER IF EXISTS deletion_requests_validate_transition ON core_registry.deletion_requests;
CREATE TRIGGER deletion_requests_validate_transition
BEFORE INSERT OR UPDATE ON core_registry.deletion_requests
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_deletion_request_transition();

CREATE TABLE IF NOT EXISTS core_registry.deletion_tombstones (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  deletion_request_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deletion_request_id)),
  dataset_code text NOT NULL CHECK (dataset_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  resource_key text NOT NULL CHECK (length(btrim(resource_key)) BETWEEN 1 AND 1024),
  deleted_at timestamptz NOT NULL,
  source_revision text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, dataset_code, resource_key),
  FOREIGN KEY (tenant_id, deletion_request_id) REFERENCES core_registry.deletion_requests(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE OR REPLACE FUNCTION core_registry.validate_deletion_tombstone_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $tombstone_insert$
DECLARE
  request_status text;
  request_site_id uuid;
  request_dataset_code text;
  request_resource_key text;
  request_applied_at timestamptz;
BEGIN
  SELECT status, site_id, dataset_code, resource_key, applied_at
    INTO request_status, request_site_id, request_dataset_code, request_resource_key, request_applied_at
  FROM core_registry.deletion_requests
  WHERE tenant_id = NEW.tenant_id AND id = NEW.deletion_request_id;
  IF request_status <> 'APPLIED'
    OR request_site_id IS DISTINCT FROM NEW.site_id
    OR request_dataset_code <> NEW.dataset_code
    OR request_resource_key <> NEW.resource_key
    OR request_applied_at IS NULL
    OR NEW.deleted_at < request_applied_at THEN
    RAISE EXCEPTION 'Deletion Tombstone must exactly represent an APPLIED Deletion Request' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$tombstone_insert$;

DROP TRIGGER IF EXISTS deletion_tombstones_validate_insert ON core_registry.deletion_tombstones;
CREATE TRIGGER deletion_tombstones_validate_insert
BEFORE INSERT ON core_registry.deletion_tombstones
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_deletion_tombstone_insert();

CREATE OR REPLACE FUNCTION core_registry.reject_deletion_tombstone_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $tombstone_immutable$
BEGIN
  RAISE EXCEPTION 'Deletion Tombstone is immutable' USING ERRCODE = '23514';
END
$tombstone_immutable$;

DROP TRIGGER IF EXISTS deletion_tombstones_reject_update_delete ON core_registry.deletion_tombstones;
CREATE TRIGGER deletion_tombstones_reject_update_delete
BEFORE UPDATE OR DELETE ON core_registry.deletion_tombstones
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_deletion_tombstone_mutation();

-- A restore cannot be marked complete until every deletion tombstone known at
-- the restore cutoff has been re-applied (re-delete or exclude) to restored data.
CREATE TABLE IF NOT EXISTS core_registry.restore_runs (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  backup_id text NOT NULL CHECK (length(btrim(backup_id)) BETWEEN 1 AND 512),
  backup_created_at timestamptz NOT NULL,
  tombstone_cutoff_at timestamptz NOT NULL,
  status text NOT NULL CHECK (status IN ('PENDING','APPLYING_TOMBSTONES','VALIDATING','COMPLETED','FAILED')),
  started_at timestamptz,
  completed_at timestamptz,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (tombstone_cutoff_at >= backup_created_at),
  CHECK (jsonb_typeof(evidence) = 'object'),
  CHECK ((status = 'PENDING' AND started_at IS NULL AND completed_at IS NULL)
    OR (status IN ('APPLYING_TOMBSTONES','VALIDATING') AND started_at IS NOT NULL AND completed_at IS NULL)
    OR (status IN ('COMPLETED','FAILED') AND started_at IS NOT NULL AND completed_at IS NOT NULL AND completed_at >= started_at)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.restore_tombstone_applications (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  restore_run_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(restore_run_id)),
  tombstone_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tombstone_id)),
  action text NOT NULL CHECK (action IN ('REDELETE','EXCLUDE')),
  applied_at timestamptz NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, restore_run_id, tombstone_id),
  FOREIGN KEY (tenant_id, restore_run_id) REFERENCES core_registry.restore_runs(tenant_id, id),
  FOREIGN KEY (tenant_id, tombstone_id) REFERENCES core_registry.deletion_tombstones(tenant_id, id),
  CHECK (jsonb_typeof(evidence) = 'object')
);

CREATE OR REPLACE FUNCTION core_registry.validate_restore_tombstone_application()
RETURNS trigger
LANGUAGE plpgsql
AS $restore_application$
DECLARE
  run_status text;
  cutoff_at timestamptz;
  tombstone_deleted_at timestamptz;
BEGIN
  SELECT status, tombstone_cutoff_at INTO run_status, cutoff_at
  FROM core_registry.restore_runs
  WHERE tenant_id = NEW.tenant_id AND id = NEW.restore_run_id;
  SELECT deleted_at INTO tombstone_deleted_at
  FROM core_registry.deletion_tombstones
  WHERE tenant_id = NEW.tenant_id AND id = NEW.tombstone_id;
  IF run_status NOT IN ('APPLYING_TOMBSTONES','VALIDATING') THEN
    RAISE EXCEPTION 'Restore Tombstone can only be applied during restore tombstone/validation stages' USING ERRCODE = '23514';
  END IF;
  IF tombstone_deleted_at > cutoff_at THEN
    RAISE EXCEPTION 'Restore Tombstone is newer than the restore cutoff' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$restore_application$;

DROP TRIGGER IF EXISTS restore_tombstone_applications_validate ON core_registry.restore_tombstone_applications;
CREATE TRIGGER restore_tombstone_applications_validate
BEFORE INSERT ON core_registry.restore_tombstone_applications
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_restore_tombstone_application();

CREATE OR REPLACE FUNCTION core_registry.validate_restore_run_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $restore_transition$
BEGIN
  IF OLD.status = 'PENDING' AND NEW.status NOT IN ('PENDING','APPLYING_TOMBSTONES','FAILED') THEN
    RAISE EXCEPTION 'Restore Run must apply Tombstones before validation/completion' USING ERRCODE = '23514';
  ELSIF OLD.status = 'APPLYING_TOMBSTONES' AND NEW.status NOT IN ('APPLYING_TOMBSTONES','VALIDATING','FAILED') THEN
    RAISE EXCEPTION 'Restore Run must validate after applying Tombstones' USING ERRCODE = '23514';
  ELSIF OLD.status = 'VALIDATING' AND NEW.status NOT IN ('VALIDATING','COMPLETED','FAILED') THEN
    RAISE EXCEPTION 'Restore Run can only complete or fail after validation' USING ERRCODE = '23514';
  ELSIF OLD.status IN ('COMPLETED','FAILED') AND NEW.status <> OLD.status THEN
    RAISE EXCEPTION 'completed Restore Run is terminal' USING ERRCODE = '23514';
  END IF;

  IF NEW.status = 'COMPLETED' AND OLD.status <> 'COMPLETED' AND EXISTS (
    SELECT 1
    FROM core_registry.deletion_tombstones AS tombstone
    WHERE tombstone.tenant_id = NEW.tenant_id
      AND tombstone.deleted_at <= NEW.tombstone_cutoff_at
      AND NOT EXISTS (
        SELECT 1
        FROM core_registry.restore_tombstone_applications AS application
        WHERE application.tenant_id = NEW.tenant_id
          AND application.restore_run_id = NEW.id
          AND application.tombstone_id = tombstone.id
      )
  ) THEN
    RAISE EXCEPTION 'Restore Run cannot complete until all deletion Tombstones are re-applied' USING ERRCODE = '23514';
  END IF;

  IF NEW.backup_id <> OLD.backup_id
    OR NEW.backup_created_at <> OLD.backup_created_at
    OR NEW.tombstone_cutoff_at <> OLD.tombstone_cutoff_at THEN
    RAISE EXCEPTION 'Restore Run backup identity/cutoff are immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$restore_transition$;

DROP TRIGGER IF EXISTS restore_runs_validate_transition ON core_registry.restore_runs;
CREATE TRIGGER restore_runs_validate_transition
BEFORE UPDATE ON core_registry.restore_runs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_restore_run_transition();

-- Correction never mutates raw historical facts. Approved requests produce an
-- immutable Correction Fact that downstream Metric/Settlement engines merge.
CREATE TABLE IF NOT EXISTS core_registry.correction_requests (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  target_type text NOT NULL CHECK (target_type IN ('READING','SETTLEMENT_INPUT','TARIFF_ASSIGNMENT','SOURCE_BINDING','SETTLEMENT_RESULT')),
  device_id uuid,
  point_id uuid,
  effective_time timestamptz NOT NULL,
  correction_type text NOT NULL CHECK (correction_type IN ('BOUNDARY_READING','COUNTER_ADJUSTMENT','METER_REPLACEMENT','RATIO_CORRECTION','MANUAL_ESTIMATE','TARIFF_CORRECTION')),
  original_value jsonb NOT NULL,
  corrected_value jsonb NOT NULL,
  delta_value numeric(36,12),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2048),
  evidence_refs jsonb NOT NULL,
  impact_preview jsonb NOT NULL,
  requested_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(requested_by)),
  approved_by uuid,
  status text NOT NULL CHECK (status IN ('DRAFT','REVIEW','APPROVED','APPLIED','REJECTED')),
  requested_at timestamptz NOT NULL,
  approved_at timestamptz,
  applied_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, point_id) REFERENCES core_registry.telemetry_points(tenant_id, site_id, id),
  CHECK (jsonb_typeof(evidence_refs) = 'array'),
  CHECK (jsonb_array_length(evidence_refs) > 0),
  CHECK (jsonb_typeof(impact_preview) = 'object'),
  CHECK ((status IN ('DRAFT','REVIEW','REJECTED') AND approved_by IS NULL AND approved_at IS NULL AND applied_at IS NULL)
    OR (status = 'APPROVED' AND approved_by IS NOT NULL AND approved_at IS NOT NULL AND applied_at IS NULL)
    OR (status = 'APPLIED' AND approved_by IS NOT NULL AND approved_at IS NOT NULL AND applied_at IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_correction_request_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $correction_transition$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.status = 'DRAFT' AND NEW.status NOT IN ('DRAFT','REVIEW','REJECTED') THEN
      RAISE EXCEPTION 'Correction Request must enter REVIEW before approval' USING ERRCODE = '23514';
    ELSIF OLD.status = 'REVIEW' AND NEW.status NOT IN ('REVIEW','APPROVED','REJECTED') THEN
      RAISE EXCEPTION 'Correction Request must be APPROVED before apply' USING ERRCODE = '23514';
    ELSIF OLD.status = 'APPROVED' AND NEW.status NOT IN ('APPROVED','APPLIED') THEN
      RAISE EXCEPTION 'APPROVED Correction Request can only be applied by a Correction Fact' USING ERRCODE = '23514';
    ELSIF OLD.status IN ('APPLIED','REJECTED') AND NEW.status <> OLD.status THEN
      RAISE EXCEPTION 'completed Correction Request is terminal' USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('APPROVED','APPLIED','REJECTED') AND (
      NEW.target_type IS DISTINCT FROM OLD.target_type
      OR NEW.device_id IS DISTINCT FROM OLD.device_id
      OR NEW.point_id IS DISTINCT FROM OLD.point_id
      OR NEW.effective_time IS DISTINCT FROM OLD.effective_time
      OR NEW.correction_type IS DISTINCT FROM OLD.correction_type
      OR NEW.original_value IS DISTINCT FROM OLD.original_value
      OR NEW.corrected_value IS DISTINCT FROM OLD.corrected_value
      OR NEW.delta_value IS DISTINCT FROM OLD.delta_value
      OR NEW.reason IS DISTINCT FROM OLD.reason
      OR NEW.evidence_refs IS DISTINCT FROM OLD.evidence_refs
      OR NEW.impact_preview IS DISTINCT FROM OLD.impact_preview
    ) THEN
      RAISE EXCEPTION 'approved Correction Request content is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$correction_transition$;

DROP TRIGGER IF EXISTS correction_requests_validate_transition ON core_registry.correction_requests;
CREATE TRIGGER correction_requests_validate_transition
BEFORE INSERT OR UPDATE ON core_registry.correction_requests
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_correction_request_transition();

CREATE TABLE IF NOT EXISTS core_registry.correction_facts (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  correction_request_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(correction_request_id)),
  target_type text NOT NULL,
  device_id uuid,
  point_id uuid,
  effective_time timestamptz NOT NULL,
  correction_type text NOT NULL,
  original_value jsonb NOT NULL,
  corrected_value jsonb NOT NULL,
  delta_value numeric(36,12),
  reason text NOT NULL,
  evidence_refs jsonb NOT NULL,
  applied_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, correction_request_id),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, correction_request_id) REFERENCES core_registry.correction_requests(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, point_id) REFERENCES core_registry.telemetry_points(tenant_id, site_id, id),
  CHECK (jsonb_typeof(evidence_refs) = 'array' AND jsonb_array_length(evidence_refs) > 0)
);

CREATE OR REPLACE FUNCTION core_registry.validate_correction_fact_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $correction_fact_insert$
DECLARE
  request_record core_registry.correction_requests%ROWTYPE;
BEGIN
  SELECT * INTO request_record
  FROM core_registry.correction_requests
  WHERE tenant_id = NEW.tenant_id
    AND site_id = NEW.site_id
    AND id = NEW.correction_request_id;
  IF request_record.status <> 'APPROVED'
    OR NEW.target_type <> request_record.target_type
    OR NEW.device_id IS DISTINCT FROM request_record.device_id
    OR NEW.point_id IS DISTINCT FROM request_record.point_id
    OR NEW.effective_time <> request_record.effective_time
    OR NEW.correction_type <> request_record.correction_type
    OR NEW.original_value <> request_record.original_value
    OR NEW.corrected_value <> request_record.corrected_value
    OR NEW.delta_value IS DISTINCT FROM request_record.delta_value
    OR NEW.reason <> request_record.reason
    OR NEW.evidence_refs <> request_record.evidence_refs
    OR NEW.applied_at < request_record.approved_at THEN
    RAISE EXCEPTION 'Correction Fact must exactly materialize an APPROVED Correction Request' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$correction_fact_insert$;

DROP TRIGGER IF EXISTS correction_facts_validate_insert ON core_registry.correction_facts;
CREATE TRIGGER correction_facts_validate_insert
BEFORE INSERT ON core_registry.correction_facts
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_correction_fact_insert();

CREATE OR REPLACE FUNCTION core_registry.mark_correction_request_applied()
RETURNS trigger
LANGUAGE plpgsql
AS $correction_apply$
BEGIN
  UPDATE core_registry.correction_requests
  SET status = 'APPLIED', applied_at = NEW.applied_at, revision = revision + 1, updated_at = NEW.applied_at
  WHERE tenant_id = NEW.tenant_id
    AND site_id = NEW.site_id
    AND id = NEW.correction_request_id
    AND status = 'APPROVED';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'Correction Request was not APPROVED at apply time' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$correction_apply$;

DROP TRIGGER IF EXISTS correction_facts_mark_applied ON core_registry.correction_facts;
CREATE TRIGGER correction_facts_mark_applied
AFTER INSERT ON core_registry.correction_facts
FOR EACH ROW EXECUTE FUNCTION core_registry.mark_correction_request_applied();

CREATE OR REPLACE FUNCTION core_registry.reject_correction_fact_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $correction_fact_immutable$
BEGIN
  RAISE EXCEPTION 'Correction Fact is immutable; create another Correction Request' USING ERRCODE = '23514';
END
$correction_fact_immutable$;

DROP TRIGGER IF EXISTS correction_facts_reject_update_delete ON core_registry.correction_facts;
CREATE TRIGGER correction_facts_reject_update_delete
BEFORE UPDATE OR DELETE ON core_registry.correction_facts
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_correction_fact_mutation();

CREATE INDEX IF NOT EXISTS lifecycle_policy_lookup_idx
  ON core_registry.data_lifecycle_policies (tenant_id, dataset_code, data_class, status, effective_from, effective_to);
CREATE INDEX IF NOT EXISTS legal_holds_lookup_idx
  ON core_registry.legal_holds (tenant_id, status, dataset_code, scope_type, site_id, effective_from, effective_to);
CREATE INDEX IF NOT EXISTS deletion_tombstones_lookup_idx
  ON core_registry.deletion_tombstones (tenant_id, dataset_code, resource_key, deleted_at);
CREATE INDEX IF NOT EXISTS restore_tombstone_applications_run_idx
  ON core_registry.restore_tombstone_applications (tenant_id, restore_run_id, tombstone_id);
CREATE INDEX IF NOT EXISTS correction_requests_effective_idx
  ON core_registry.correction_requests (tenant_id, site_id, point_id, effective_time, status);
CREATE INDEX IF NOT EXISTS correction_facts_effective_idx
  ON core_registry.correction_facts (tenant_id, site_id, point_id, effective_time, applied_at);

ALTER TABLE core_registry.data_lifecycle_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.data_lifecycle_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.legal_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.legal_holds FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.deletion_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.deletion_requests FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.deletion_tombstones ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.deletion_tombstones FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.restore_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.restore_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.restore_tombstone_applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.restore_tombstone_applications FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.correction_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.correction_requests FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.correction_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.correction_facts FORCE ROW LEVEL SECURITY;

CREATE POLICY data_lifecycle_policies_runtime_scope ON core_registry.data_lifecycle_policies
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY legal_holds_runtime_scope ON core_registry.legal_holds
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY deletion_requests_runtime_scope ON core_registry.deletion_requests
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY deletion_tombstones_runtime_scope ON core_registry.deletion_tombstones
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY restore_runs_runtime_scope ON core_registry.restore_runs
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY restore_tombstone_applications_runtime_scope ON core_registry.restore_tombstone_applications
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY correction_requests_runtime_scope ON core_registry.correction_requests
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY correction_facts_runtime_scope ON core_registry.correction_facts
  FOR SELECT TO s1_core_runtime USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.data_lifecycle_policies, core_registry.legal_holds,
  core_registry.deletion_requests, core_registry.deletion_tombstones,
  core_registry.restore_runs, core_registry.restore_tombstone_applications,
  core_registry.correction_requests, core_registry.correction_facts FROM PUBLIC;
GRANT SELECT ON core_registry.data_lifecycle_policies, core_registry.legal_holds,
  core_registry.deletion_requests, core_registry.deletion_tombstones,
  core_registry.restore_runs, core_registry.restore_tombstone_applications,
  core_registry.correction_requests, core_registry.correction_facts TO s1_core_runtime;

COMMIT;
