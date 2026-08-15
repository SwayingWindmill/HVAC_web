BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- Object Storage purpose is explicit. Archive and Backup are deliberately
-- different governance products even when both use the same S3-compatible
-- provider technology.
CREATE TABLE IF NOT EXISTS core_registry.object_storage_buckets (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  bucket_code text NOT NULL CHECK (bucket_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  bucket_name text NOT NULL CHECK (length(btrim(bucket_name)) BETWEEN 3 AND 255),
  provider text NOT NULL CHECK (provider IN ('S3_COMPATIBLE','AWS_S3','AZURE_BLOB','GCS')),
  purpose text NOT NULL CHECK (purpose IN ('BACKUP','ARCHIVE','COLD_DATA','EVIDENCE','DATASET','MODEL_ARTIFACT','REPORT','OTA')),
  endpoint_reference text CHECK (endpoint_reference IS NULL OR length(btrim(endpoint_reference)) BETWEEN 1 AND 512),
  region text CHECK (region IS NULL OR length(btrim(region)) BETWEEN 1 AND 128),
  versioning_required boolean NOT NULL DEFAULT true,
  immutability_required boolean NOT NULL DEFAULT false,
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, bucket_code),
  UNIQUE (tenant_id, bucket_name),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at),
  CHECK (purpose NOT IN ('BACKUP','ARCHIVE') OR versioning_required),
  CHECK (purpose <> 'BACKUP' OR immutability_required)
);

CREATE TABLE IF NOT EXISTS core_registry.archive_manifests (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  dataset_code text NOT NULL CHECK (dataset_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  lifecycle_policy_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(lifecycle_policy_id)),
  bucket_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(bucket_id)),
  object_key text NOT NULL CHECK (length(btrim(object_key)) BETWEEN 1 AND 2048),
  source_system text NOT NULL CHECK (source_system IN ('CLICKHOUSE','POSTGRESQL','APPLICATION')),
  source_snapshot_ref text NOT NULL CHECK (length(btrim(source_snapshot_ref)) BETWEEN 1 AND 1024),
  format text NOT NULL CHECK (format IN ('PARQUET','JSONL','NATIVE','BINARY')),
  window_start timestamptz,
  window_end timestamptz,
  row_count bigint CHECK (row_count IS NULL OR row_count >= 0),
  byte_count bigint CHECK (byte_count IS NULL OR byte_count >= 0),
  content_sha256 text CHECK (content_sha256 IS NULL OR content_sha256 ~ '^[a-f0-9]{64}$'),
  scope_selector jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL CHECK (status IN ('STAGING','SEALED','VERIFIED','FAILED')),
  archived_at timestamptz,
  verified_at timestamptz,
  verification_evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, bucket_id, object_key),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, lifecycle_policy_id) REFERENCES core_registry.data_lifecycle_policies(tenant_id, id),
  FOREIGN KEY (tenant_id, bucket_id) REFERENCES core_registry.object_storage_buckets(tenant_id, id),
  CHECK (window_end IS NULL OR (window_start IS NOT NULL AND window_end > window_start)),
  CHECK (jsonb_typeof(scope_selector) = 'object'),
  CHECK (jsonb_typeof(verification_evidence) = 'object'),
  CHECK ((status = 'STAGING' AND archived_at IS NULL AND verified_at IS NULL)
    OR (status = 'SEALED' AND archived_at IS NOT NULL AND verified_at IS NULL AND content_sha256 IS NOT NULL AND byte_count IS NOT NULL)
    OR (status = 'VERIFIED' AND archived_at IS NOT NULL AND verified_at IS NOT NULL AND verified_at >= archived_at AND content_sha256 IS NOT NULL AND byte_count IS NOT NULL)
    OR (status = 'FAILED' AND verified_at IS NULL)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.backup_manifests (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  bucket_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(bucket_id)),
  source_system text NOT NULL CHECK (source_system IN ('POSTGRESQL','CLICKHOUSE')),
  backup_type text NOT NULL CHECK (backup_type IN ('FULL','INCREMENTAL','WAL','SNAPSHOT')),
  object_key text NOT NULL CHECK (length(btrim(object_key)) BETWEEN 1 AND 2048),
  source_snapshot_ref text NOT NULL CHECK (length(btrim(source_snapshot_ref)) BETWEEN 1 AND 1024),
  recovery_point_at timestamptz NOT NULL,
  byte_count bigint CHECK (byte_count IS NULL OR byte_count >= 0),
  content_sha256 text CHECK (content_sha256 IS NULL OR content_sha256 ~ '^[a-f0-9]{64}$'),
  status text NOT NULL CHECK (status IN ('STAGING','VERIFIED','FAILED')),
  started_at timestamptz NOT NULL,
  completed_at timestamptz,
  verified_at timestamptz,
  verification_evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, bucket_id, object_key),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, bucket_id) REFERENCES core_registry.object_storage_buckets(tenant_id, id),
  CHECK (jsonb_typeof(verification_evidence) = 'object'),
  CHECK (recovery_point_at >= started_at),
  CHECK ((status = 'STAGING' AND completed_at IS NULL AND verified_at IS NULL)
    OR (status = 'VERIFIED' AND completed_at IS NOT NULL AND completed_at >= started_at
        AND verified_at IS NOT NULL AND verified_at >= completed_at
        AND content_sha256 IS NOT NULL AND byte_count IS NOT NULL)
    OR (status = 'FAILED' AND completed_at IS NOT NULL AND completed_at >= started_at AND verified_at IS NULL)),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_archive_manifest_write()
RETURNS trigger
LANGUAGE plpgsql
AS $archive_manifest_write$
DECLARE
  bucket_purpose text;
  bucket_status text;
  policy_dataset text;
  policy_status text;
  policy_archive_required boolean;
BEGIN
  SELECT purpose, status INTO bucket_purpose, bucket_status
  FROM core_registry.object_storage_buckets
  WHERE tenant_id = NEW.tenant_id AND id = NEW.bucket_id;
  IF bucket_purpose <> 'ARCHIVE' OR bucket_status <> 'ACTIVE' THEN
    RAISE EXCEPTION 'Archive Manifest requires an ACTIVE ARCHIVE bucket' USING ERRCODE = '23514';
  END IF;
  SELECT dataset_code, status, archive_required
    INTO policy_dataset, policy_status, policy_archive_required
  FROM core_registry.data_lifecycle_policies
  WHERE tenant_id = NEW.tenant_id AND id = NEW.lifecycle_policy_id;
  IF policy_dataset <> NEW.dataset_code OR policy_status <> 'RELEASED' OR NOT policy_archive_required THEN
    RAISE EXCEPTION 'Archive Manifest requires a RELEASED archive-required Lifecycle Policy for the same dataset' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'INSERT' AND NEW.status <> 'STAGING' THEN
    RAISE EXCEPTION 'Archive Manifest must start as STAGING' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'UPDATE' THEN
    IF OLD.status = 'STAGING' AND NEW.status NOT IN ('STAGING','SEALED','FAILED') THEN
      RAISE EXCEPTION 'Archive Manifest must seal before verification' USING ERRCODE = '23514';
    ELSIF OLD.status = 'SEALED' AND NEW.status NOT IN ('SEALED','VERIFIED','FAILED') THEN
      RAISE EXCEPTION 'SEALED Archive Manifest can only verify or fail' USING ERRCODE = '23514';
    ELSIF OLD.status IN ('VERIFIED','FAILED') AND NEW.status <> OLD.status THEN
      RAISE EXCEPTION 'terminal Archive Manifest cannot change status' USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('SEALED','VERIFIED','FAILED') AND (
      NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
      OR NEW.site_id IS DISTINCT FROM OLD.site_id
      OR NEW.dataset_code IS DISTINCT FROM OLD.dataset_code
      OR NEW.lifecycle_policy_id IS DISTINCT FROM OLD.lifecycle_policy_id
      OR NEW.bucket_id IS DISTINCT FROM OLD.bucket_id
      OR NEW.object_key IS DISTINCT FROM OLD.object_key
      OR NEW.source_system IS DISTINCT FROM OLD.source_system
      OR NEW.source_snapshot_ref IS DISTINCT FROM OLD.source_snapshot_ref
      OR NEW.format IS DISTINCT FROM OLD.format
      OR NEW.window_start IS DISTINCT FROM OLD.window_start
      OR NEW.window_end IS DISTINCT FROM OLD.window_end
      OR NEW.row_count IS DISTINCT FROM OLD.row_count
      OR NEW.byte_count IS DISTINCT FROM OLD.byte_count
      OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256
      OR NEW.scope_selector IS DISTINCT FROM OLD.scope_selector
      OR NEW.archived_at IS DISTINCT FROM OLD.archived_at
    ) THEN
      RAISE EXCEPTION 'sealed Archive Manifest content is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$archive_manifest_write$;

DROP TRIGGER IF EXISTS archive_manifests_validate_write ON core_registry.archive_manifests;
CREATE TRIGGER archive_manifests_validate_write
BEFORE INSERT OR UPDATE ON core_registry.archive_manifests
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_archive_manifest_write();

CREATE OR REPLACE FUNCTION core_registry.validate_backup_manifest_write()
RETURNS trigger
LANGUAGE plpgsql
AS $backup_manifest_write$
DECLARE
  bucket_purpose text;
  bucket_status text;
BEGIN
  SELECT purpose, status INTO bucket_purpose, bucket_status
  FROM core_registry.object_storage_buckets
  WHERE tenant_id = NEW.tenant_id AND id = NEW.bucket_id;
  IF bucket_purpose <> 'BACKUP' OR bucket_status <> 'ACTIVE' THEN
    RAISE EXCEPTION 'Backup Manifest requires an ACTIVE BACKUP bucket' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'INSERT' AND NEW.status <> 'STAGING' THEN
    RAISE EXCEPTION 'Backup Manifest must start as STAGING' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'UPDATE' THEN
    IF OLD.status = 'STAGING' AND NEW.status NOT IN ('STAGING','VERIFIED','FAILED') THEN
      RAISE EXCEPTION 'Backup Manifest has invalid next status' USING ERRCODE = '23514';
    ELSIF OLD.status IN ('VERIFIED','FAILED') AND NEW.status <> OLD.status THEN
      RAISE EXCEPTION 'terminal Backup Manifest cannot change status' USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('VERIFIED','FAILED') AND (
      NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
      OR NEW.bucket_id IS DISTINCT FROM OLD.bucket_id
      OR NEW.source_system IS DISTINCT FROM OLD.source_system
      OR NEW.backup_type IS DISTINCT FROM OLD.backup_type
      OR NEW.object_key IS DISTINCT FROM OLD.object_key
      OR NEW.source_snapshot_ref IS DISTINCT FROM OLD.source_snapshot_ref
      OR NEW.recovery_point_at IS DISTINCT FROM OLD.recovery_point_at
      OR NEW.byte_count IS DISTINCT FROM OLD.byte_count
      OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256
      OR NEW.started_at IS DISTINCT FROM OLD.started_at
      OR NEW.completed_at IS DISTINCT FROM OLD.completed_at
    ) THEN
      RAISE EXCEPTION 'verified Backup Manifest content is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$backup_manifest_write$;

DROP TRIGGER IF EXISTS backup_manifests_validate_write ON core_registry.backup_manifests;
CREATE TRIGGER backup_manifests_validate_write
BEFORE INSERT OR UPDATE ON core_registry.backup_manifests
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_backup_manifest_write();

-- Retention deletion may only proceed after a verified Archive when the active
-- Lifecycle Policy requires one. Backup is never accepted as a substitute.
ALTER TABLE core_registry.deletion_requests
  ADD COLUMN IF NOT EXISTS archive_manifest_id uuid;
ALTER TABLE core_registry.deletion_requests
  DROP CONSTRAINT IF EXISTS deletion_requests_archive_manifest_fk;
ALTER TABLE core_registry.deletion_requests
  ADD CONSTRAINT deletion_requests_archive_manifest_fk
  FOREIGN KEY (tenant_id, archive_manifest_id) REFERENCES core_registry.archive_manifests(tenant_id, id);

CREATE OR REPLACE FUNCTION core_registry.validate_deletion_request_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $deletion_transition$
DECLARE
  decision_time timestamptz;
  archive_required_value boolean;
  archive_status text;
  archive_dataset text;
  archive_site_id uuid;
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
      OR NEW.archive_manifest_id IS DISTINCT FROM OLD.archive_manifest_id
    ) THEN
      RAISE EXCEPTION 'approved Deletion Request identity/evidence/archive reference is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;

  decision_time := coalesce(NEW.approved_at, NEW.applied_at, NEW.requested_at, now());
  IF NEW.status IN ('APPROVED','APPLIED')
     AND core_registry.legal_hold_blocks_deletion(
       NEW.tenant_id, NEW.site_id, NEW.dataset_code, NEW.resource_key, decision_time
     ) THEN
    RAISE EXCEPTION 'Deletion Request is blocked by an active Legal Hold' USING ERRCODE = '23514';
  END IF;

  IF NEW.status IN ('APPROVED','APPLIED') THEN
    SELECT policy.archive_required INTO archive_required_value
    FROM core_registry.data_lifecycle_policies AS policy
    WHERE policy.tenant_id = NEW.tenant_id
      AND policy.dataset_code = NEW.dataset_code
      AND policy.status = 'RELEASED'
      AND policy.effective_from <= decision_time
      AND (policy.effective_to IS NULL OR decision_time < policy.effective_to)
    ORDER BY policy.effective_from DESC
    LIMIT 1;
    IF coalesce(archive_required_value, false) THEN
      IF NEW.archive_manifest_id IS NULL THEN
        RAISE EXCEPTION 'archive-required deletion needs a VERIFIED Archive Manifest; Backup is not Archive' USING ERRCODE = '23514';
      END IF;
      SELECT status, dataset_code, site_id INTO archive_status, archive_dataset, archive_site_id
      FROM core_registry.archive_manifests
      WHERE tenant_id = NEW.tenant_id AND id = NEW.archive_manifest_id;
      IF archive_status <> 'VERIFIED' OR archive_dataset <> NEW.dataset_code OR archive_site_id IS DISTINCT FROM NEW.site_id THEN
        RAISE EXCEPTION 'Deletion Request Archive Manifest must be VERIFIED and match dataset/site scope' USING ERRCODE = '23514';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END
$deletion_transition$;

-- Restore must point to a verified Backup Manifest; a free-form backup id is
-- intentionally removed from the CURRENT model.
DO $restore_upgrade$
BEGIN
  IF EXISTS (SELECT 1 FROM core_registry.restore_runs) THEN
    RAISE EXCEPTION 'restore_runs contains legacy rows; migrate them to Backup Manifest identity before V2 activation';
  END IF;
END
$restore_upgrade$;

ALTER TABLE core_registry.restore_runs DROP COLUMN IF EXISTS backup_id;
ALTER TABLE core_registry.restore_runs DROP COLUMN IF EXISTS backup_created_at;
ALTER TABLE core_registry.restore_runs ADD COLUMN IF NOT EXISTS backup_manifest_id uuid NOT NULL;
ALTER TABLE core_registry.restore_runs
  DROP CONSTRAINT IF EXISTS restore_runs_backup_manifest_fk;
ALTER TABLE core_registry.restore_runs
  ADD CONSTRAINT restore_runs_backup_manifest_fk
  FOREIGN KEY (tenant_id, backup_manifest_id) REFERENCES core_registry.backup_manifests(tenant_id, id);

CREATE OR REPLACE FUNCTION core_registry.validate_restore_run_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $restore_transition$
DECLARE
  backup_status text;
  backup_recovery_point timestamptz;
BEGIN
  SELECT status, recovery_point_at INTO backup_status, backup_recovery_point
  FROM core_registry.backup_manifests
  WHERE tenant_id = NEW.tenant_id AND id = NEW.backup_manifest_id;
  IF backup_status IS DISTINCT FROM 'VERIFIED' THEN
    RAISE EXCEPTION 'Restore Run requires a VERIFIED Backup Manifest; Archive is not Backup' USING ERRCODE = '23514';
  END IF;
  IF NEW.tombstone_cutoff_at < backup_recovery_point THEN
    RAISE EXCEPTION 'Restore tombstone cutoff cannot precede Backup recovery point' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'PENDING' THEN
      RAISE EXCEPTION 'Restore Run must start as PENDING' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;

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

  IF NEW.backup_manifest_id <> OLD.backup_manifest_id
    OR NEW.tombstone_cutoff_at <> OLD.tombstone_cutoff_at THEN
    RAISE EXCEPTION 'Restore Run Backup Manifest identity/cutoff are immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$restore_transition$;

DROP TRIGGER IF EXISTS restore_runs_validate_transition ON core_registry.restore_runs;
CREATE TRIGGER restore_runs_validate_transition
BEFORE INSERT OR UPDATE ON core_registry.restore_runs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_restore_run_transition();

CREATE INDEX IF NOT EXISTS archive_manifests_lookup_idx
  ON core_registry.archive_manifests (tenant_id, dataset_code, site_id, status, archived_at DESC);
CREATE INDEX IF NOT EXISTS backup_manifests_lookup_idx
  ON core_registry.backup_manifests (tenant_id, source_system, status, recovery_point_at DESC);

ALTER TABLE core_registry.object_storage_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.object_storage_buckets FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.archive_manifests ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.archive_manifests FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.backup_manifests ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.backup_manifests FORCE ROW LEVEL SECURITY;

CREATE POLICY object_storage_buckets_runtime_scope ON core_registry.object_storage_buckets
FOR SELECT TO s1_core_runtime
USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY archive_manifests_runtime_scope ON core_registry.archive_manifests
FOR SELECT TO s1_core_runtime
USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY backup_manifests_runtime_scope ON core_registry.backup_manifests
FOR SELECT TO s1_core_runtime
USING (tenant_id = core_registry.current_tenant_id());

REVOKE ALL ON core_registry.object_storage_buckets, core_registry.archive_manifests, core_registry.backup_manifests FROM PUBLIC;
GRANT SELECT ON core_registry.object_storage_buckets, core_registry.archive_manifests, core_registry.backup_manifests TO s1_core_runtime;

COMMIT;
