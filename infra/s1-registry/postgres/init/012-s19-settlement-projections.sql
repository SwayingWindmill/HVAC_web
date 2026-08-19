BEGIN;
SET LOCAL ROLE s1_core_migrator;

CREATE POLICY energy_edges_settlement_lineage_scope ON core_registry.energy_edges
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY meter_bindings_settlement_lineage_scope ON core_registry.meter_bindings
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_boundaries_runtime_exec_scope ON core_registry.settlement_boundaries
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_boundary_edges_runtime_exec_scope ON core_registry.settlement_boundary_edges
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariffs_runtime_exec_scope ON core_registry.tariffs
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_versions_runtime_exec_scope ON core_registry.tariff_versions
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_periods_runtime_exec_scope ON core_registry.tariff_periods
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_assignments_runtime_exec_scope ON core_registry.tariff_assignments
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_metric_bindings_runtime_exec_scope ON core_registry.settlement_metric_bindings
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_bindings_settlement_scope ON core_registry.metric_bindings
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_versions_settlement_scope ON core_registry.metric_versions
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY settlement_periods_runtime_exec_scope ON core_registry.settlement_periods
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_snapshots_runtime_exec_scope ON core_registry.settlement_snapshots
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_change_candidates_runtime_exec_scope ON core_registry.settlement_change_candidates
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_revisions_runtime_exec_scope ON core_registry.settlement_revisions
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

GRANT SELECT ON core_registry.energy_edges, core_registry.meter_bindings,
  core_registry.settlement_boundaries, core_registry.settlement_boundary_edges,
  core_registry.tariffs, core_registry.tariff_versions, core_registry.tariff_periods,
  core_registry.tariff_assignments, core_registry.settlement_metric_bindings,
  core_registry.metric_bindings, core_registry.metric_versions TO settlement_runtime;
GRANT SELECT, UPDATE ON core_registry.settlement_periods TO settlement_runtime;
GRANT SELECT, INSERT ON core_registry.settlement_snapshots TO settlement_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.settlement_change_candidates TO settlement_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.settlement_revisions TO settlement_runtime;

ALTER TABLE core_registry.settlement_snapshots
  ADD COLUMN source_metric_revisions jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN source_watermark timestamptz,
  ADD COLUMN missing_metric_binding_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN dataset_revision bigint;

UPDATE core_registry.settlement_snapshots
SET dataset_revision = revision_no + 1
WHERE dataset_revision IS NULL;

ALTER TABLE core_registry.settlement_snapshots
  ALTER COLUMN dataset_revision SET NOT NULL,
  ADD CONSTRAINT settlement_snapshots_dataset_revision_check CHECK (dataset_revision > 0),
  ADD CONSTRAINT settlement_snapshots_source_metric_revisions_check CHECK (jsonb_typeof(source_metric_revisions) = 'object'),
  ADD CONSTRAINT settlement_snapshots_missing_metric_binding_refs_check CHECK (jsonb_typeof(missing_metric_binding_refs) = 'array'),
  ADD CONSTRAINT settlement_snapshots_dataset_revision_matches_history_check CHECK (dataset_revision = revision_no + 1);

ALTER TABLE core_registry.settlement_change_candidates
  ADD COLUMN base_snapshot_id uuid,
  ADD COLUMN calculation_digest text;

UPDATE core_registry.settlement_change_candidates
SET base_snapshot_id = NULLIF(evidence->>'baseSnapshotId', '')::uuid
WHERE base_snapshot_id IS NULL AND evidence ? 'baseSnapshotId';

ALTER TABLE core_registry.settlement_change_candidates
  ADD CONSTRAINT settlement_change_candidates_calculation_digest_check
    CHECK (calculation_digest IS NULL OR calculation_digest ~ '^[a-f0-9]{64}$'),
  ADD CONSTRAINT settlement_change_candidates_base_snapshot_fk
    FOREIGN KEY (tenant_id, site_id, base_snapshot_id)
    REFERENCES core_registry.settlement_snapshots(tenant_id, site_id, id);

CREATE UNIQUE INDEX settlement_change_candidates_open_digest_idx
  ON core_registry.settlement_change_candidates (
    tenant_id, site_id, settlement_period_id, base_snapshot_id, calculation_digest
  )
  WHERE calculation_digest IS NOT NULL AND status IN ('OPEN', 'APPROVED');

CREATE TABLE core_registry.settlement_current_projection (
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  settlement_period_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(settlement_period_id)),
  snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(snapshot_id)),
  dataset_revision bigint NOT NULL CHECK (dataset_revision > 0),
  settlement_revision_no integer NOT NULL CHECK (settlement_revision_no >= 0),
  quality text NOT NULL CHECK (quality IN ('GOOD','PARTIAL','ESTIMATED','MANUAL','STALE','INVALID')),
  completeness numeric(9,8) NOT NULL CHECK (completeness >= 0 AND completeness <= 1),
  source_watermark timestamptz,
  source_metric_revisions jsonb NOT NULL CHECK (jsonb_typeof(source_metric_revisions) = 'object'),
  missing_metric_binding_refs jsonb NOT NULL CHECK (jsonb_typeof(missing_metric_binding_refs) = 'array'),
  cost jsonb NOT NULL CHECK (jsonb_typeof(cost) = 'object'),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, settlement_period_id),
  UNIQUE (tenant_id, site_id, snapshot_id),
  FOREIGN KEY (tenant_id, site_id, settlement_period_id)
    REFERENCES core_registry.settlement_periods(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, snapshot_id)
    REFERENCES core_registry.settlement_snapshots(tenant_id, site_id, id)
);

-- Rebuildable Current: on upgrade (and in restore runbooks) select the highest
-- immutable dataset revision for each Settlement Period. Legacy snapshots retain
-- empty source-revision evidence rather than fabricating lineage that was never stored.
INSERT INTO core_registry.settlement_current_projection(
  tenant_id,site_id,settlement_period_id,snapshot_id,dataset_revision,settlement_revision_no,
  quality,completeness,source_watermark,source_metric_revisions,missing_metric_binding_refs,cost,updated_at)
SELECT DISTINCT ON (tenant_id,site_id,settlement_period_id)
  tenant_id,site_id,settlement_period_id,id,dataset_revision,revision_no,
  quality,completeness,source_watermark,source_metric_revisions,missing_metric_binding_refs,cost,created_at
FROM core_registry.settlement_snapshots
ORDER BY tenant_id,site_id,settlement_period_id,dataset_revision DESC,id DESC;

ALTER TABLE core_registry.settlement_current_projection ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_current_projection FORCE ROW LEVEL SECURITY;
CREATE POLICY settlement_current_projection_runtime_exec_scope ON core_registry.settlement_current_projection
  FOR ALL TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.settlement_current_projection FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON core_registry.settlement_current_projection TO settlement_runtime;

COMMIT;
