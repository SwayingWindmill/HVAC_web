BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.metric_calculation_runs
  ADD COLUMN result_id uuid CHECK (result_id IS NULL OR core_registry.is_uuid_v7(result_id));
CREATE UNIQUE INDEX metric_calculation_runs_result_id_idx
  ON core_registry.metric_calculation_runs (tenant_id, site_id, result_id)
  WHERE result_id IS NOT NULL;

-- Result revision allocation is PostgreSQL-authoritative. ClickHouse stores every
-- immutable result fact, while this head table owns the monotonic business
-- revision and the explicit current projection for each logical Metric window.
CREATE TABLE core_registry.metric_result_heads (
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  metric_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_id)),
  subject_type text NOT NULL CHECK (subject_type IN ('TENANT','SITE','SPACE','ASSET','DEVICE','ENERGY_NODE','TAG_GROUP')),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  granularity text NOT NULL CHECK (granularity IN ('REALTIME','1MIN','5MIN','15MIN','HOUR','DAY','MONTH','QUARTER','YEAR')),
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL,
  last_allocated_revision bigint NOT NULL CHECK (last_allocated_revision > 0),
  current_revision bigint NOT NULL DEFAULT 0 CHECK (current_revision >= 0),
  current_result_id uuid,
  current_run_id uuid,
  metric_version_id uuid,
  metric_binding_id uuid,
  binding_version bigint,
  value_type text,
  value_number double precision,
  unit_code text,
  quality text,
  completeness double precision,
  calculated_at timestamptz,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, metric_id, subject_type, subject_id, granularity, period_start, period_end),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, metric_id) REFERENCES core_registry.metrics(tenant_id, id),
  FOREIGN KEY (tenant_id, metric_version_id) REFERENCES core_registry.metric_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, metric_binding_id) REFERENCES core_registry.metric_bindings(tenant_id, site_id, id),
  CHECK (period_end > period_start),
  CHECK (current_revision <= last_allocated_revision),
  CHECK ((current_revision = 0 AND current_result_id IS NULL AND current_run_id IS NULL AND metric_version_id IS NULL AND metric_binding_id IS NULL AND binding_version IS NULL AND value_type IS NULL AND value_number IS NULL AND unit_code IS NULL AND quality IS NULL AND completeness IS NULL AND calculated_at IS NULL)
    OR (current_revision > 0 AND current_result_id IS NOT NULL AND current_run_id IS NOT NULL AND metric_version_id IS NOT NULL AND metric_binding_id IS NOT NULL AND binding_version IS NOT NULL AND value_type = 'NUMBER' AND value_number IS NOT NULL AND quality IS NOT NULL AND completeness BETWEEN 0 AND 1 AND calculated_at IS NOT NULL))
);

CREATE TABLE core_registry.metric_result_revisions (
  result_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(result_id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  run_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(run_id)),
  scheduler_job_id uuid CHECK (scheduler_job_id IS NULL OR core_registry.is_uuid_v7(scheduler_job_id)),
  metric_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_id)),
  metric_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_version_id)),
  metric_binding_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_binding_id)),
  binding_version bigint NOT NULL CHECK (binding_version > 0),
  subject_type text NOT NULL CHECK (subject_type IN ('TENANT','SITE','SPACE','ASSET','DEVICE','ENERGY_NODE','TAG_GROUP')),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  granularity text NOT NULL CHECK (granularity IN ('REALTIME','1MIN','5MIN','15MIN','HOUR','DAY','MONTH','QUARTER','YEAR')),
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL,
  result_revision bigint NOT NULL CHECK (result_revision > 0),
  provenance jsonb NOT NULL CHECK (jsonb_typeof(provenance) = 'object'),
  publication_payload jsonb,
  status text NOT NULL CHECK (status IN ('PERSISTING','PERSISTED','FAILED')),
  calculated_at timestamptz NOT NULL,
  persisted_at timestamptz,
  failed_at timestamptz,
  failure_code text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, run_id),
  UNIQUE (tenant_id, scheduler_job_id),
  UNIQUE (tenant_id, site_id, metric_id, subject_type, subject_id, granularity, period_start, period_end, result_revision),
  FOREIGN KEY (tenant_id, site_id, run_id) REFERENCES core_registry.metric_calculation_runs(tenant_id, site_id, id),
  FOREIGN KEY (scheduler_job_id) REFERENCES core_registry.job_instances(job_id),
  FOREIGN KEY (tenant_id, metric_version_id) REFERENCES core_registry.metric_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, metric_binding_id) REFERENCES core_registry.metric_bindings(tenant_id, site_id, id),
  CHECK (period_end > period_start),
  CHECK ((status = 'PERSISTING' AND publication_payload IS NOT NULL AND jsonb_typeof(publication_payload) = 'object' AND persisted_at IS NULL AND failed_at IS NULL AND failure_code IS NULL)
    OR (status = 'PERSISTED' AND publication_payload IS NULL AND persisted_at IS NOT NULL AND failed_at IS NULL AND failure_code IS NULL)
    OR (status = 'FAILED' AND publication_payload IS NULL AND persisted_at IS NULL AND failed_at IS NOT NULL AND failure_code IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE INDEX metric_result_revisions_logical_idx
  ON core_registry.metric_result_revisions (tenant_id, site_id, metric_id, subject_type, subject_id, granularity, period_start, period_end, result_revision DESC);
CREATE INDEX metric_result_revisions_status_idx
  ON core_registry.metric_result_revisions (tenant_id, site_id, status, updated_at);

-- Lifecycle worker audit is append-only. Durable claim/lease/retry state remains
-- in the shared Scheduler Job tables; policy/hold/archive/delete/tombstone facts
-- remain in their owning governance tables.
CREATE TABLE core_registry.lifecycle_execution_events (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  job_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(job_id)),
  dataset_code text NOT NULL CHECK (dataset_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  resource_key text NOT NULL CHECK (length(btrim(resource_key)) BETWEEN 1 AND 1024),
  event_type text NOT NULL CHECK (event_type IN ('CLAIMED','HOLD_BLOCKED','CURRENT_BLOCKED','ARCHIVE_STARTED','ARCHIVE_VERIFIED','ARCHIVE_FAILED','DELETE_STARTED','DELETE_APPLIED','RETRY_SCHEDULED','FAILED')),
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence) = 'object'),
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (job_id) REFERENCES core_registry.job_instances(job_id)
);
CREATE INDEX lifecycle_execution_events_job_idx
  ON core_registry.lifecycle_execution_events (job_id, occurred_at, id);

ALTER TABLE core_registry.metric_result_heads ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_result_heads FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_result_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_result_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.lifecycle_execution_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.lifecycle_execution_events FORCE ROW LEVEL SECURITY;

CREATE POLICY metric_result_heads_core_read_scope ON core_registry.metric_result_heads
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_result_revisions_core_read_scope ON core_registry.metric_result_revisions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_result_heads_metric_engine_scope ON core_registry.metric_result_heads
  FOR ALL TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_result_revisions_metric_engine_scope ON core_registry.metric_result_revisions
  FOR ALL TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY lifecycle_execution_events_metric_engine_scope ON core_registry.lifecycle_execution_events
  FOR ALL TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));

-- S08's first lifecycle worker runs beside the Metric worker and consumes only
-- the two lifecycle job classes. This extends the existing worker policies
-- without giving it access to unrelated Scheduler jobs.
DROP POLICY IF EXISTS job_instances_metric_worker_scope ON core_registry.job_instances;
CREATE POLICY job_instances_metric_worker_scope ON core_registry.job_instances
  FOR SELECT TO metric_engine_runtime
  USING (job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','DATA_RETENTION_SCAN','DATA_ARCHIVE'));
DROP POLICY IF EXISTS job_instances_metric_worker_update_scope ON core_registry.job_instances;
CREATE POLICY job_instances_metric_worker_update_scope ON core_registry.job_instances
  FOR UPDATE TO metric_engine_runtime
  USING (job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','DATA_RETENTION_SCAN','DATA_ARCHIVE'))
  WITH CHECK (job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','DATA_RETENTION_SCAN','DATA_ARCHIVE'));
DROP POLICY IF EXISTS job_attempts_metric_worker_scope ON core_registry.job_attempts;
CREATE POLICY job_attempts_metric_worker_scope ON core_registry.job_attempts
  FOR ALL TO metric_engine_runtime
  USING (EXISTS (
    SELECT 1 FROM core_registry.job_instances j
    WHERE j.job_id = job_attempts.job_id
      AND j.job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','DATA_RETENTION_SCAN','DATA_ARCHIVE')
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM core_registry.job_instances j
    WHERE j.job_id = job_attempts.job_id
      AND j.job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','DATA_RETENTION_SCAN','DATA_ARCHIVE')
  ));

CREATE POLICY lifecycle_policies_metric_engine_scope ON core_registry.data_lifecycle_policies
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY legal_holds_metric_engine_scope ON core_registry.legal_holds
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY deletion_requests_metric_engine_scope ON core_registry.deletion_requests
  FOR ALL TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY deletion_tombstones_metric_engine_scope ON core_registry.deletion_tombstones
  FOR INSERT TO metric_engine_runtime
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY archive_manifests_metric_engine_scope ON core_registry.archive_manifests
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY archive_buckets_metric_engine_scope ON core_registry.object_storage_buckets
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND status = 'ACTIVE' AND purpose = 'ARCHIVE');

REVOKE ALL ON core_registry.metric_result_heads, core_registry.metric_result_revisions, core_registry.lifecycle_execution_events FROM PUBLIC;
GRANT SELECT ON core_registry.metric_result_heads, core_registry.metric_result_revisions TO s1_core_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.metric_result_heads, core_registry.metric_result_revisions TO metric_engine_runtime;
GRANT SELECT, INSERT ON core_registry.lifecycle_execution_events TO metric_engine_runtime;
GRANT SELECT ON core_registry.data_lifecycle_policies, core_registry.legal_holds, core_registry.object_storage_buckets, core_registry.archive_manifests TO metric_engine_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.deletion_requests TO metric_engine_runtime;
GRANT SELECT, INSERT ON core_registry.deletion_tombstones TO metric_engine_runtime;

COMMIT;
