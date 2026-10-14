-- Report snapshots (#483): a Site's closed month as its report shows it, taken once by
-- report-service on a REPORT_GENERATE job and never changed. Taking it again inserts the next
-- version; readers show the latest.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.report_snapshots (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  month text NOT NULL CHECK (month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
  version integer NOT NULL CHECK (version > 0),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND payload->>'month' = month),
  scheduler_job_id uuid,
  generated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, month, version),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

ALTER TABLE core_registry.report_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.report_snapshots FORCE ROW LEVEL SECURITY;

CREATE POLICY report_snapshots_runtime_scope ON core_registry.report_snapshots FOR ALL TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

-- The Site's clock and import Tariff price the month; its verified savings and FDD findings are
-- part of the report.
CREATE POLICY sites_report_scope ON core_registry.sites FOR SELECT TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(id));
CREATE POLICY settlement_boundaries_report_scope ON core_registry.settlement_boundaries FOR SELECT TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_assignments_report_scope ON core_registry.tariff_assignments FOR SELECT TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_versions_report_scope ON core_registry.tariff_versions FOR SELECT TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_periods_report_scope ON core_registry.tariff_periods FOR SELECT TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY savings_daily_results_report_scope ON core_registry.savings_daily_results FOR SELECT TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY fdd_findings_report_scope ON core_registry.fdd_findings FOR SELECT TO report_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

-- The Scheduler fires REPORT_GENERATE jobs; the report runtime claims, runs and completes only those.
CREATE POLICY job_instances_report_worker_scope ON core_registry.job_instances FOR SELECT TO report_runtime
  USING (job_type = 'REPORT_GENERATE');
CREATE POLICY job_instances_report_worker_update_scope ON core_registry.job_instances FOR UPDATE TO report_runtime
  USING (job_type = 'REPORT_GENERATE') WITH CHECK (job_type = 'REPORT_GENERATE');
CREATE POLICY job_attempts_report_worker_scope ON core_registry.job_attempts FOR ALL TO report_runtime
  USING (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'REPORT_GENERATE'))
  WITH CHECK (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'REPORT_GENERATE'));

REVOKE ALL ON core_registry.report_snapshots FROM PUBLIC;
GRANT SELECT, INSERT ON core_registry.report_snapshots TO report_runtime;
GRANT SELECT ON core_registry.sites, core_registry.settlement_boundaries, core_registry.tariff_assignments,
  core_registry.tariff_versions, core_registry.tariff_periods, core_registry.savings_daily_results, core_registry.fdd_findings TO report_runtime;
GRANT SELECT, UPDATE ON core_registry.job_instances TO report_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.job_attempts TO report_runtime;

CREATE INDEX IF NOT EXISTS report_snapshots_month_idx ON core_registry.report_snapshots (tenant_id, site_id, month, version DESC);

COMMIT;
