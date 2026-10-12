-- Savings verification (M&V, ADR 0021): a Site's ledger of energy conservation measures, each
-- measure's frozen baseline, and each verified day. mv-service writes them as mv_runtime under
-- the same Tenant/Site row security as the other intelligence owners; a verified day is
-- inserted once and never changed.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.savings_measures (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 100),
  kind text NOT NULL CHECK (kind IN ('SETPOINT_RESET','CONTROL_SEQUENCE','EQUIPMENT_RETROFIT','OPERATING_SCHEDULE','OTHER')),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
  evidence text NOT NULL DEFAULT '' CHECK (length(evidence) <= 200),
  -- The executed command the measure came from, owned by Command Governance (whose ids are random
  -- UUIDs, not v7); a reference, not a key.
  evidence_command_id uuid,
  planned_at timestamptz NOT NULL,
  implemented_at timestamptz,
  ended_at timestamptz,
  investment_cny double precision CHECK (investment_cny IS NULL OR investment_cny >= 0),
  expected_annual_kwh double precision CHECK (expected_annual_kwh IS NULL OR expected_annual_kwh >= 0),
  status text NOT NULL CHECK (status IN ('PLANNED','IMPLEMENTED','ENDED')),
  baseline_from timestamptz,
  baseline_to timestamptz,
  baseline_intercept_kwh double precision,
  baseline_slope double precision,
  baseline_samples integer CHECK (baseline_samples IS NULL OR baseline_samples >= 0),
  baseline_expected_hours integer CHECK (baseline_expected_hours IS NULL OR baseline_expected_hours >= 0),
  baseline_cv_rmse double precision,
  baseline_nmbe double precision,
  baseline_trusted boolean,
  baseline_reason text NOT NULL DEFAULT '' CHECK (baseline_reason IN ('', 'TOO_FEW_HOURS', 'LOW_COVERAGE', 'POOR_FIT')),
  created_by uuid NOT NULL,
  updated_by uuid NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK ((status = 'PLANNED') = (implemented_at IS NULL)),
  CHECK ((status = 'ENDED') = (ended_at IS NOT NULL)),
  CHECK (ended_at IS NULL OR ended_at > implemented_at),
  CHECK ((implemented_at IS NULL) = (baseline_trusted IS NULL)),
  CHECK (baseline_to IS NULL OR (baseline_from < baseline_to AND baseline_to <= implemented_at)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.savings_daily_results (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  measure_id uuid NOT NULL,
  day date NOT NULL,
  expected_hours integer NOT NULL CHECK (expected_hours BETWEEN 0 AND 25),
  hours integer NOT NULL CHECK (hours BETWEEN 0 AND expected_hours),
  unpriced_hours integer NOT NULL CHECK (unpriced_hours BETWEEN 0 AND hours),
  adjusted_baseline_kwh double precision NOT NULL,
  actual_kwh double precision NOT NULL,
  avoided_kwh double precision NOT NULL,
  avoided_cost double precision NOT NULL,
  verified_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, measure_id, day),
  FOREIGN KEY (tenant_id, site_id, measure_id) REFERENCES core_registry.savings_measures(tenant_id, site_id, id)
);

-- At most one measure reports on a Site at a time (IPMVP Option C on the whole plant).
CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_savings_reporting()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.implemented_at IS NULL THEN
    RETURN NEW;
  END IF;
  -- Writers on one Site take turns, so two measures implemented at once cannot both pass the
  -- check below; it then reads with a fresh snapshot that includes the other's commit.
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id::text || '/' || NEW.site_id::text, 0));
  IF EXISTS (
    SELECT 1 FROM core_registry.savings_measures AS other
    WHERE other.tenant_id = NEW.tenant_id AND other.site_id = NEW.site_id AND other.id <> NEW.id
      AND other.implemented_at IS NOT NULL
      AND tstzrange(other.implemented_at, coalesce(other.ended_at, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.implemented_at, coalesce(NEW.ended_at, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'savings measures on a Site cannot report over the same period' USING ERRCODE = '23P01';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS savings_measures_reject_overlap ON core_registry.savings_measures;
CREATE TRIGGER savings_measures_reject_overlap
BEFORE INSERT OR UPDATE OF implemented_at, ended_at ON core_registry.savings_measures
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_savings_reporting();

ALTER TABLE core_registry.savings_measures ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.savings_measures FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.savings_daily_results ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.savings_daily_results FORCE ROW LEVEL SECURITY;

CREATE POLICY savings_measures_runtime_scope ON core_registry.savings_measures FOR ALL TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY savings_daily_results_runtime_scope ON core_registry.savings_daily_results FOR ALL TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

-- The Site's clock and its import Tariff price each verified hour.
CREATE POLICY sites_mv_scope ON core_registry.sites FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(id));
CREATE POLICY settlement_boundaries_mv_scope ON core_registry.settlement_boundaries FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariffs_mv_scope ON core_registry.tariffs FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_versions_mv_scope ON core_registry.tariff_versions FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_periods_mv_scope ON core_registry.tariff_periods FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_assignments_mv_scope ON core_registry.tariff_assignments FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.savings_measures, core_registry.savings_daily_results FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON core_registry.savings_measures TO mv_runtime;
GRANT SELECT, INSERT ON core_registry.savings_daily_results TO mv_runtime;
GRANT SELECT ON core_registry.sites, core_registry.settlement_boundaries, core_registry.tariffs,
  core_registry.tariff_versions, core_registry.tariff_periods, core_registry.tariff_assignments TO mv_runtime;

-- The Scheduler fires MV_EVALUATION jobs; the M&V runtime claims, runs and completes only those.
ALTER TABLE core_registry.schedule_definitions
  DROP CONSTRAINT IF EXISTS schedule_definitions_job_type_check;
ALTER TABLE core_registry.schedule_definitions
  ADD CONSTRAINT schedule_definitions_job_type_check CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN','FDD_EVALUATION','MV_EVALUATION',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN','OUTBOX_CLEANUP','INBOX_CLEANUP','PROJECTION_REPAIR',
    'DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'
  ));

ALTER TABLE core_registry.job_instances
  DROP CONSTRAINT IF EXISTS job_instances_job_type_check;
ALTER TABLE core_registry.job_instances
  ADD CONSTRAINT job_instances_job_type_check CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN','FDD_EVALUATION','MV_EVALUATION',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN','OUTBOX_CLEANUP','INBOX_CLEANUP','PROJECTION_REPAIR',
    'DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'
  ));

CREATE POLICY job_instances_mv_worker_scope ON core_registry.job_instances FOR SELECT TO mv_runtime
  USING (job_type = 'MV_EVALUATION');
CREATE POLICY job_instances_mv_worker_update_scope ON core_registry.job_instances FOR UPDATE TO mv_runtime
  USING (job_type = 'MV_EVALUATION') WITH CHECK (job_type = 'MV_EVALUATION');
CREATE POLICY job_attempts_mv_worker_scope ON core_registry.job_attempts FOR ALL TO mv_runtime
  USING (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'MV_EVALUATION'))
  WITH CHECK (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'MV_EVALUATION'));

GRANT SELECT, UPDATE ON core_registry.job_instances TO mv_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.job_attempts TO mv_runtime;

CREATE INDEX IF NOT EXISTS savings_measures_site_idx ON core_registry.savings_measures (tenant_id, site_id, planned_at DESC);

-- savings.manage registers and lifecycles measures on a Site; reads stay on site.read. Tenant
-- administrators receive it with the catalog revision that introduces it.
RESET ROLE;
SET LOCAL ROLE s1_iam_migrator;

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT 5, 's25-savings-v1', array_append(capabilities, 'savings.manage'), 'ACTIVE', '2026-10-12T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 4;

UPDATE iam.role_templates
SET capabilities = array_append(capabilities, 'savings.manage'),
    revision = revision + 1,
    updated_at = '2026-10-12T00:00:00Z'
WHERE status = 'ACTIVE'
  AND capabilities @> ARRAY['iam.admin']::text[]
  AND NOT capabilities @> ARRAY['savings.manage']::text[];

RESET ROLE;
COMMIT;
