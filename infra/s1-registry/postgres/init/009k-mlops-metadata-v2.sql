BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- SE-ARCH-004 V2.1.2 MLOps Metadata Domain.
-- Metadata only: Forecast/Optimization remain the production serving owners.

CREATE TABLE IF NOT EXISTS core_registry.mlops_artifacts (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST','OPTIMIZATION')),
  artifact_type text NOT NULL CHECK (artifact_type IN ('MODEL','DATASET','FEATURE_SET','EVALUATION','EXPLAINABILITY','OTHER')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  object_uri text NOT NULL CHECK (length(btrim(object_uri)) BETWEEN 1 AND 2048),
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[a-f0-9]{64}$'),
  media_type text NOT NULL CHECK (length(btrim(media_type)) BETWEEN 1 AND 128),
  size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes >= 0),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, owner_domain, owner_version_id, artifact_type, checksum_sha256),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_evaluations (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST','OPTIMIZATION')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  evaluation_type text NOT NULL CHECK (evaluation_type IN ('OFFLINE','SHADOW','PRODUCTION','REGRESSION')),
  dataset_artifact_id uuid,
  metrics jsonb NOT NULL CHECK (jsonb_typeof(metrics) = 'object'),
  outcome text NOT NULL CHECK (outcome IN ('PASSED','FAILED','INCONCLUSIVE')),
  evaluator text NOT NULL CHECK (length(btrim(evaluator)) BETWEEN 1 AND 256),
  evaluated_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, dataset_artifact_id) REFERENCES core_registry.mlops_artifacts(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_approvals (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST','OPTIMIZATION')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  evaluation_id uuid,
  decision text NOT NULL CHECK (decision IN ('APPROVED','REJECTED')),
  actor_type text NOT NULL CHECK (actor_type IN ('USER','WORKLOAD')),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2048),
  policy_revision text NOT NULL CHECK (length(btrim(policy_revision)) BETWEEN 1 AND 256),
  decided_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, evaluation_id) REFERENCES core_registry.mlops_evaluations(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_deployment_metadata (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  owner_domain text NOT NULL CHECK (owner_domain IN ('FORECAST','OPTIMIZATION')),
  owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(owner_version_id)),
  approval_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(approval_id)),
  environment text NOT NULL CHECK (environment IN ('SHADOW','PRODUCTION')),
  status text NOT NULL CHECK (status IN ('PLANNED','ACTIVE','RETIRED','ROLLED_BACK')),
  serving_ref text NOT NULL CHECK (length(btrim(serving_ref)) BETWEEN 1 AND 512),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, approval_id) REFERENCES core_registry.mlops_approvals(tenant_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_drift_observations (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  deployment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_id)),
  metric_code text NOT NULL CHECK (length(btrim(metric_code)) BETWEEN 1 AND 128),
  observed_value double precision NOT NULL,
  threshold_value double precision NOT NULL,
  drift_status text NOT NULL CHECK (drift_status IN ('NORMAL','WARNING','BREACHED')),
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence) = 'object'),
  observed_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, deployment_id) REFERENCES core_registry.mlops_deployment_metadata(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.mlops_rollback_records (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  deployment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_id)),
  from_owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(from_owner_version_id)),
  to_owner_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(to_owner_version_id)),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2048),
  actor_type text NOT NULL CHECK (actor_type IN ('USER','WORKLOAD')),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('REQUESTED','APPLIED','FAILED')),
  requested_at timestamptz NOT NULL,
  applied_at timestamptz,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, deployment_id) REFERENCES core_registry.mlops_deployment_metadata(tenant_id, id),
  CHECK ((status = 'APPLIED' AND applied_at IS NOT NULL) OR (status <> 'APPLIED')),
  CHECK (applied_at IS NULL OR applied_at >= requested_at)
);

CREATE INDEX IF NOT EXISTS mlops_artifacts_owner_idx ON core_registry.mlops_artifacts (tenant_id, owner_domain, owner_version_id, artifact_type);
CREATE INDEX IF NOT EXISTS mlops_evaluations_owner_idx ON core_registry.mlops_evaluations (tenant_id, owner_domain, owner_version_id, evaluated_at DESC);
CREATE INDEX IF NOT EXISTS mlops_deployment_active_idx ON core_registry.mlops_deployment_metadata (tenant_id, owner_domain, owner_version_id, environment, status);
CREATE INDEX IF NOT EXISTS mlops_drift_deployment_idx ON core_registry.mlops_drift_observations (tenant_id, deployment_id, observed_at DESC);

ALTER TABLE core_registry.mlops_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_artifacts FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_evaluations FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_approvals FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_deployment_metadata ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_deployment_metadata FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_drift_observations ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_drift_observations FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_rollback_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.mlops_rollback_records FORCE ROW LEVEL SECURITY;

CREATE POLICY mlops_artifacts_runtime_scope ON core_registry.mlops_artifacts FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY mlops_evaluations_runtime_scope ON core_registry.mlops_evaluations FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY mlops_approvals_runtime_scope ON core_registry.mlops_approvals FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY mlops_deployment_metadata_runtime_scope ON core_registry.mlops_deployment_metadata FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY mlops_drift_observations_runtime_scope ON core_registry.mlops_drift_observations FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY mlops_rollback_records_runtime_scope ON core_registry.mlops_rollback_records FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));

REVOKE ALL ON core_registry.mlops_artifacts, core_registry.mlops_evaluations, core_registry.mlops_approvals,
  core_registry.mlops_deployment_metadata, core_registry.mlops_drift_observations, core_registry.mlops_rollback_records FROM PUBLIC;
GRANT SELECT ON core_registry.mlops_artifacts, core_registry.mlops_evaluations, core_registry.mlops_approvals,
  core_registry.mlops_deployment_metadata, core_registry.mlops_drift_observations, core_registry.mlops_rollback_records TO s1_core_runtime;

COMMIT;
