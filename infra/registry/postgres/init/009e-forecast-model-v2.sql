BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.forecast_feature_sets (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  feature_set_code text NOT NULL CHECK (feature_set_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  target text NOT NULL CHECK (target IN ('SITE_LOAD','PV_GENERATION')),
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, feature_set_code),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_feature_set_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  feature_set_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(feature_set_id)),
  version bigint NOT NULL CHECK (version > 0),
  feature_schema jsonb NOT NULL,
  fallback_schema jsonb,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, feature_set_id, version),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, id, feature_set_id, version),
  FOREIGN KEY (tenant_id, feature_set_id) REFERENCES core_registry.forecast_feature_sets(tenant_id, id),
  CHECK (jsonb_typeof(feature_schema) = 'object'),
  CHECK (fallback_schema IS NULL OR jsonb_typeof(fallback_schema) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_dataset_snapshots (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  target text NOT NULL CHECK (target IN ('SITE_LOAD','PV_GENERATION')),
  subject_type text NOT NULL CHECK (subject_type IN ('SITE','ENERGY_NODE')),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  train_from timestamptz NOT NULL,
  train_to timestamptz NOT NULL,
  feature_set_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(feature_set_version_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  metric_version_refs jsonb NOT NULL,
  weather_source text,
  data_quality_summary jsonb NOT NULL,
  manifest_uri text NOT NULL CHECK (length(btrim(manifest_uri)) BETWEEN 1 AND 2048),
  manifest_checksum text NOT NULL CHECK (manifest_checksum ~ '^[a-f0-9]{64}$'),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, target, subject_type, subject_id, feature_set_version_id, topology_version_id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, feature_set_version_id) REFERENCES core_registry.forecast_feature_set_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, topology_version_id) REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id),
  CHECK (train_to > train_from),
  CHECK (jsonb_typeof(metric_version_refs) = 'array'),
  CHECK (jsonb_typeof(data_quality_summary) = 'object')
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_models (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  model_code text NOT NULL CHECK (model_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  target text NOT NULL CHECK (target IN ('SITE_LOAD','PV_GENERATION')),
  subject_type text NOT NULL CHECK (subject_type IN ('SITE','ENERGY_NODE')),
  horizon_minutes integer NOT NULL CHECK (horizon_minutes > 0),
  granularity text NOT NULL CHECK (granularity IN ('15MIN','30MIN','1H')),
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, model_code),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, id, target, subject_type, horizon_minutes, granularity),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_training_runs (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  model_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_id)),
  dataset_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(dataset_snapshot_id)),
  feature_set_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(feature_set_version_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  algorithm text NOT NULL CHECK (algorithm IN ('BASELINE','GBT','STATISTICAL','TREE_BASED','NEURAL','HYBRID')),
  hyperparameters jsonb NOT NULL,
  code_version text NOT NULL CHECK (length(btrim(code_version)) BETWEEN 1 AND 256),
  evaluation jsonb,
  status text NOT NULL CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED')),
  started_at timestamptz,
  finished_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, model_id, dataset_snapshot_id, feature_set_version_id, topology_version_id),
  FOREIGN KEY (tenant_id, model_id) REFERENCES core_registry.forecast_models(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, dataset_snapshot_id) REFERENCES core_registry.forecast_dataset_snapshots(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, feature_set_version_id) REFERENCES core_registry.forecast_feature_set_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, topology_version_id) REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id),
  CHECK (jsonb_typeof(hyperparameters) = 'object'),
  CHECK (evaluation IS NULL OR jsonb_typeof(evaluation) = 'object'),
  CHECK ((status = 'PENDING' AND started_at IS NULL AND finished_at IS NULL)
    OR (status = 'RUNNING' AND started_at IS NOT NULL AND finished_at IS NULL)
    OR (status IN ('SUCCEEDED','FAILED') AND started_at IS NOT NULL AND finished_at IS NOT NULL AND finished_at >= started_at)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_model_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  model_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_id)),
  model_version bigint NOT NULL CHECK (model_version > 0),
  training_run_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(training_run_id)),
  dataset_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(dataset_snapshot_id)),
  feature_set_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(feature_set_version_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  artifact_uri text NOT NULL CHECK (length(btrim(artifact_uri)) BETWEEN 1 AND 2048),
  artifact_checksum text NOT NULL CHECK (artifact_checksum ~ '^[a-f0-9]{64}$'),
  evaluation jsonb NOT NULL,
  compatibility jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL CHECK (status IN ('CANDIDATE','VALIDATED','SHADOW','ACTIVE','RETIRED','REJECTED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, model_id, model_version),
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, model_id, feature_set_version_id, topology_version_id),
  FOREIGN KEY (tenant_id, model_id) REFERENCES core_registry.forecast_models(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, training_run_id, model_id, dataset_snapshot_id, feature_set_version_id, topology_version_id)
    REFERENCES core_registry.forecast_training_runs(tenant_id, site_id, id, model_id, dataset_snapshot_id, feature_set_version_id, topology_version_id),
  CHECK (jsonb_typeof(evaluation) = 'object'),
  CHECK (jsonb_typeof(compatibility) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_deployments (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  target text NOT NULL CHECK (target IN ('SITE_LOAD','PV_GENERATION')),
  subject_type text NOT NULL CHECK (subject_type IN ('SITE','ENERGY_NODE')),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  model_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_version_id)),
  model_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_id)),
  feature_set_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(feature_set_version_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  status text NOT NULL CHECK (status IN ('SHADOW','ACTIVE','RETIRED')),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, model_version_id, target, subject_type, subject_id),
  UNIQUE (tenant_id, site_id, id, model_version_id, feature_set_version_id, topology_version_id),
  FOREIGN KEY (tenant_id, site_id, model_version_id, model_id, feature_set_version_id, topology_version_id)
    REFERENCES core_registry.forecast_model_versions(tenant_id, site_id, id, model_id, feature_set_version_id, topology_version_id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS forecast_deployments_one_active_target_uidx
  ON core_registry.forecast_deployments (tenant_id, site_id, target, subject_type, subject_id)
  WHERE status = 'ACTIVE';

CREATE TABLE IF NOT EXISTS core_registry.forecast_input_snapshots (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  deployment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_id)),
  model_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_version_id)),
  feature_set_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(feature_set_version_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  latest_data_time timestamptz NOT NULL,
  weather_issue_time timestamptz,
  metric_version_refs jsonb NOT NULL,
  feature_values jsonb NOT NULL,
  input_checksum text NOT NULL CHECK (input_checksum ~ '^[a-f0-9]{64}$'),
  captured_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, deployment_id, model_version_id, feature_set_version_id, topology_version_id)
    REFERENCES core_registry.forecast_deployments(tenant_id, site_id, id, model_version_id, feature_set_version_id, topology_version_id),
  FOREIGN KEY (tenant_id, site_id, model_version_id) REFERENCES core_registry.forecast_model_versions(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, feature_set_version_id) REFERENCES core_registry.forecast_feature_set_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, topology_version_id) REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id),
  CHECK (jsonb_typeof(metric_version_refs) = 'array'),
  CHECK (jsonb_typeof(feature_values) = 'object'),
  CHECK (latest_data_time <= captured_at),
  CHECK (weather_issue_time IS NULL OR weather_issue_time <= captured_at)
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_jobs (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  deployment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_id)),
  model_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_version_id)),
  input_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(input_snapshot_id)),
  target text NOT NULL CHECK (target IN ('SITE_LOAD','PV_GENERATION')),
  subject_type text NOT NULL CHECK (subject_type IN ('SITE','ENERGY_NODE')),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  forecast_origin timestamptz NOT NULL,
  horizon_minutes integer NOT NULL CHECK (horizon_minutes > 0),
  granularity text NOT NULL CHECK (granularity IN ('15MIN','30MIN','1H')),
  trigger_type text NOT NULL CHECK (trigger_type IN ('SCHEDULED','ON_DEMAND','REFRESH')),
  status text NOT NULL CHECK (status IN ('PENDING','RUNNING','PERSISTING','PERSISTED','FAILED')),
  started_at timestamptz,
  completed_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, deployment_id, model_version_id, input_snapshot_id),
  FOREIGN KEY (tenant_id, site_id, deployment_id, model_version_id, target, subject_type, subject_id)
    REFERENCES core_registry.forecast_deployments(tenant_id, site_id, id, model_version_id, target, subject_type, subject_id),
  FOREIGN KEY (tenant_id, site_id, input_snapshot_id) REFERENCES core_registry.forecast_input_snapshots(tenant_id, site_id, id),
  CHECK ((status = 'PENDING' AND started_at IS NULL AND completed_at IS NULL)
    OR (status IN ('RUNNING','PERSISTING') AND started_at IS NOT NULL AND completed_at IS NULL)
    OR (status IN ('PERSISTED','FAILED') AND started_at IS NOT NULL AND completed_at IS NOT NULL AND completed_at >= started_at)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.forecast_snapshots (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  forecast_job_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(forecast_job_id)),
  deployment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_id)),
  model_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_version_id)),
  input_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(input_snapshot_id)),
  forecast_origin timestamptz NOT NULL,
  window_start timestamptz NOT NULL,
  window_end timestamptz NOT NULL,
  result_count integer NOT NULL CHECK (result_count > 0),
  result_checksum text NOT NULL CHECK (result_checksum ~ '^[a-f0-9]{64}$'),
  quality_summary jsonb NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, forecast_job_id),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, forecast_job_id, deployment_id, model_version_id, input_snapshot_id)
    REFERENCES core_registry.forecast_jobs(tenant_id, site_id, id, deployment_id, model_version_id, input_snapshot_id),
  CHECK (window_end > window_start),
  CHECK (forecast_origin <= window_start),
  CHECK (jsonb_typeof(quality_summary) = 'object')
);

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_dataset_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_dataset_snapshot$
DECLARE
  feature_target text;
  feature_status text;
  topology_status text;
BEGIN
  SELECT feature_set.target, version.status
    INTO feature_target, feature_status
  FROM core_registry.forecast_feature_set_versions AS version
  JOIN core_registry.forecast_feature_sets AS feature_set
    ON feature_set.tenant_id = version.tenant_id AND feature_set.id = version.feature_set_id
  WHERE version.tenant_id = NEW.tenant_id AND version.id = NEW.feature_set_version_id;
  IF feature_status <> 'RELEASED' OR feature_target <> NEW.target THEN
    RAISE EXCEPTION 'Forecast Dataset Snapshot requires a RELEASED Feature Set Version for the same target' USING ERRCODE = '23514';
  END IF;
  SELECT status INTO topology_status
  FROM core_registry.energy_topology_versions
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.topology_version_id;
  IF topology_status NOT IN ('RELEASED','ACTIVE','SUPERSEDED') THEN
    RAISE EXCEPTION 'Forecast Dataset Snapshot requires a released Topology Version' USING ERRCODE = '23514';
  END IF;
  IF NEW.subject_type = 'SITE' AND NEW.subject_id <> NEW.site_id THEN
    RAISE EXCEPTION 'SITE Forecast Dataset subject must equal site_id' USING ERRCODE = '23514';
  END IF;
  IF NEW.subject_type = 'ENERGY_NODE' AND NOT EXISTS (
    SELECT 1 FROM core_registry.energy_nodes
    WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id
      AND topology_version_id = NEW.topology_version_id AND id = NEW.subject_id
  ) THEN
    RAISE EXCEPTION 'Forecast Dataset ENERGY_NODE subject must exist in its Topology Version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_dataset_snapshot$;

DROP TRIGGER IF EXISTS forecast_dataset_snapshots_validate ON core_registry.forecast_dataset_snapshots;
CREATE TRIGGER forecast_dataset_snapshots_validate BEFORE INSERT ON core_registry.forecast_dataset_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_dataset_snapshot();

CREATE OR REPLACE FUNCTION core_registry.reject_released_forecast_feature_set_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_feature_version_immutable$
BEGIN
  IF OLD.status IN ('RELEASED','SUPERSEDED') AND (
    NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
    OR NEW.feature_set_id IS DISTINCT FROM OLD.feature_set_id
    OR NEW.version IS DISTINCT FROM OLD.version
    OR NEW.feature_schema IS DISTINCT FROM OLD.feature_schema
    OR NEW.fallback_schema IS DISTINCT FROM OLD.fallback_schema
  ) THEN
    RAISE EXCEPTION 'released Forecast Feature Set Version is immutable; create a new version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_feature_version_immutable$;

DROP TRIGGER IF EXISTS forecast_feature_set_versions_reject_released_mutation ON core_registry.forecast_feature_set_versions;
CREATE TRIGGER forecast_feature_set_versions_reject_released_mutation
BEFORE UPDATE ON core_registry.forecast_feature_set_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_released_forecast_feature_set_version_mutation();

CREATE OR REPLACE FUNCTION core_registry.reject_forecast_immutable_row_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_immutable$
BEGIN
  RAISE EXCEPTION 'Forecast traceability Snapshot/Dataset row is immutable' USING ERRCODE = '23514';
END
$forecast_immutable$;

DROP TRIGGER IF EXISTS forecast_dataset_snapshots_reject_mutation ON core_registry.forecast_dataset_snapshots;
CREATE TRIGGER forecast_dataset_snapshots_reject_mutation BEFORE UPDATE OR DELETE ON core_registry.forecast_dataset_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_forecast_immutable_row_mutation();
DROP TRIGGER IF EXISTS forecast_input_snapshots_reject_mutation ON core_registry.forecast_input_snapshots;
CREATE TRIGGER forecast_input_snapshots_reject_mutation BEFORE UPDATE OR DELETE ON core_registry.forecast_input_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_forecast_immutable_row_mutation();
DROP TRIGGER IF EXISTS forecast_snapshots_reject_mutation ON core_registry.forecast_snapshots;
CREATE TRIGGER forecast_snapshots_reject_mutation BEFORE UPDATE OR DELETE ON core_registry.forecast_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_forecast_immutable_row_mutation();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_training_run_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_training_insert$
DECLARE
  model_target text;
  model_subject_type text;
  dataset_target text;
  dataset_subject_type text;
  dataset_feature_set_version_id uuid;
  dataset_topology_version_id uuid;
BEGIN
  SELECT target, subject_type INTO model_target, model_subject_type
  FROM core_registry.forecast_models
  WHERE tenant_id = NEW.tenant_id AND id = NEW.model_id;
  SELECT target, subject_type, feature_set_version_id, topology_version_id
    INTO dataset_target, dataset_subject_type, dataset_feature_set_version_id, dataset_topology_version_id
  FROM core_registry.forecast_dataset_snapshots
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.dataset_snapshot_id;
  IF model_target <> dataset_target OR model_subject_type <> dataset_subject_type THEN
    RAISE EXCEPTION 'Forecast Training Run Model target/subject must match Dataset Snapshot' USING ERRCODE = '23514';
  END IF;
  IF NEW.feature_set_version_id <> dataset_feature_set_version_id OR NEW.topology_version_id <> dataset_topology_version_id THEN
    RAISE EXCEPTION 'Forecast Training Run feature/topology lineage must match Dataset Snapshot' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_training_insert$;

DROP TRIGGER IF EXISTS forecast_training_runs_validate_insert ON core_registry.forecast_training_runs;
CREATE TRIGGER forecast_training_runs_validate_insert BEFORE INSERT ON core_registry.forecast_training_runs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_training_run_insert();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_training_run_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_training_transition$
BEGIN
  IF OLD.status = 'PENDING' AND NEW.status NOT IN ('PENDING','RUNNING','FAILED') THEN
    RAISE EXCEPTION 'Forecast Training Run must start before succeeding' USING ERRCODE = '23514';
  ELSIF OLD.status = 'RUNNING' AND NEW.status NOT IN ('RUNNING','SUCCEEDED','FAILED') THEN
    RAISE EXCEPTION 'RUNNING Forecast Training Run can only succeed or fail' USING ERRCODE = '23514';
  ELSIF OLD.status IN ('SUCCEEDED','FAILED') AND NEW.status <> OLD.status THEN
    RAISE EXCEPTION 'completed Forecast Training Run is terminal' USING ERRCODE = '23514';
  END IF;
  IF NEW.model_id <> OLD.model_id OR NEW.dataset_snapshot_id <> OLD.dataset_snapshot_id
    OR NEW.feature_set_version_id <> OLD.feature_set_version_id OR NEW.topology_version_id <> OLD.topology_version_id
    OR NEW.algorithm <> OLD.algorithm OR NEW.hyperparameters <> OLD.hyperparameters OR NEW.code_version <> OLD.code_version THEN
    RAISE EXCEPTION 'Forecast Training Run input identity is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_training_transition$;

DROP TRIGGER IF EXISTS forecast_training_runs_validate_transition ON core_registry.forecast_training_runs;
CREATE TRIGGER forecast_training_runs_validate_transition BEFORE UPDATE ON core_registry.forecast_training_runs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_training_run_transition();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_model_version_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_model_version_insert$
DECLARE
  run_status text;
BEGIN
  SELECT status INTO run_status FROM core_registry.forecast_training_runs
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.training_run_id;
  IF run_status <> 'SUCCEEDED' THEN
    RAISE EXCEPTION 'Forecast Model Version requires a SUCCEEDED Training Run' USING ERRCODE = '23514';
  END IF;
  IF NEW.status <> 'CANDIDATE' THEN
    RAISE EXCEPTION 'Forecast Model Version must enter lifecycle as CANDIDATE' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_model_version_insert$;

DROP TRIGGER IF EXISTS forecast_model_versions_validate_insert ON core_registry.forecast_model_versions;
CREATE TRIGGER forecast_model_versions_validate_insert BEFORE INSERT ON core_registry.forecast_model_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_model_version_insert();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_model_version_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_model_version_transition$
BEGIN
  IF OLD.status = 'CANDIDATE' AND NEW.status NOT IN ('CANDIDATE','VALIDATED','REJECTED') THEN
    RAISE EXCEPTION 'CANDIDATE Forecast Model Version must be validated before shadow/active use' USING ERRCODE = '23514';
  ELSIF OLD.status = 'VALIDATED' AND NEW.status NOT IN ('VALIDATED','SHADOW','ACTIVE','RETIRED','REJECTED') THEN
    RAISE EXCEPTION 'invalid transition from VALIDATED Forecast Model Version' USING ERRCODE = '23514';
  ELSIF OLD.status = 'SHADOW' AND NEW.status NOT IN ('SHADOW','ACTIVE','RETIRED','REJECTED') THEN
    RAISE EXCEPTION 'invalid transition from SHADOW Forecast Model Version' USING ERRCODE = '23514';
  ELSIF OLD.status = 'ACTIVE' AND NEW.status NOT IN ('ACTIVE','RETIRED') THEN
    RAISE EXCEPTION 'ACTIVE Forecast Model Version can only remain active or retire' USING ERRCODE = '23514';
  ELSIF OLD.status IN ('RETIRED','REJECTED') AND NEW.status <> OLD.status THEN
    RAISE EXCEPTION 'terminal Forecast Model Version cannot change status' USING ERRCODE = '23514';
  END IF;
  IF NEW.tenant_id <> OLD.tenant_id OR NEW.site_id <> OLD.site_id OR NEW.model_id <> OLD.model_id
    OR NEW.model_version <> OLD.model_version OR NEW.training_run_id <> OLD.training_run_id
    OR NEW.dataset_snapshot_id <> OLD.dataset_snapshot_id OR NEW.feature_set_version_id <> OLD.feature_set_version_id
    OR NEW.topology_version_id <> OLD.topology_version_id OR NEW.artifact_uri <> OLD.artifact_uri
    OR NEW.artifact_checksum <> OLD.artifact_checksum OR NEW.evaluation <> OLD.evaluation
    OR NEW.compatibility <> OLD.compatibility THEN
    RAISE EXCEPTION 'Forecast Model Version lineage/artifact is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_model_version_transition$;

DROP TRIGGER IF EXISTS forecast_model_versions_validate_transition ON core_registry.forecast_model_versions;
CREATE TRIGGER forecast_model_versions_validate_transition BEFORE UPDATE ON core_registry.forecast_model_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_model_version_transition();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_deployment_write()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_deployment_write$
DECLARE
  model_status text;
  model_target text;
  model_subject_type text;
BEGIN
  SELECT version.status, model.target, model.subject_type
    INTO model_status, model_target, model_subject_type
  FROM core_registry.forecast_model_versions AS version
  JOIN core_registry.forecast_models AS model ON model.tenant_id = version.tenant_id AND model.id = version.model_id
  WHERE version.tenant_id = NEW.tenant_id AND version.site_id = NEW.site_id AND version.id = NEW.model_version_id;
  IF NEW.status = 'ACTIVE' AND model_status NOT IN ('VALIDATED','SHADOW','ACTIVE') THEN
    RAISE EXCEPTION 'ACTIVE Forecast Deployment requires a validated Model Version' USING ERRCODE = '23514';
  END IF;
  IF NEW.target <> model_target OR NEW.subject_type <> model_subject_type THEN
    RAISE EXCEPTION 'Forecast Deployment target/subject must match its Model' USING ERRCODE = '23514';
  END IF;
  IF NEW.subject_type = 'SITE' AND NEW.subject_id <> NEW.site_id THEN
    RAISE EXCEPTION 'SITE Forecast Deployment subject must equal site_id' USING ERRCODE = '23514';
  END IF;
  IF NEW.subject_type = 'ENERGY_NODE' AND NOT EXISTS (
    SELECT 1 FROM core_registry.energy_nodes
    WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id
      AND topology_version_id = NEW.topology_version_id AND id = NEW.subject_id
  ) THEN
    RAISE EXCEPTION 'Forecast Deployment ENERGY_NODE subject must exist in its Topology Version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_deployment_write$;

DROP TRIGGER IF EXISTS forecast_deployments_validate_write ON core_registry.forecast_deployments;
CREATE TRIGGER forecast_deployments_validate_write BEFORE INSERT OR UPDATE ON core_registry.forecast_deployments
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_deployment_write();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_job_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_job_insert$
DECLARE
  deployment_status text;
  model_horizon_minutes integer;
  model_granularity text;
  input_captured_at timestamptz;
BEGIN
  SELECT deployment.status, model.horizon_minutes, model.granularity
    INTO deployment_status, model_horizon_minutes, model_granularity
  FROM core_registry.forecast_deployments AS deployment
  JOIN core_registry.forecast_model_versions AS version
    ON version.tenant_id = deployment.tenant_id AND version.site_id = deployment.site_id AND version.id = deployment.model_version_id
  JOIN core_registry.forecast_models AS model
    ON model.tenant_id = version.tenant_id AND model.id = version.model_id
  WHERE deployment.tenant_id = NEW.tenant_id AND deployment.site_id = NEW.site_id AND deployment.id = NEW.deployment_id;
  IF deployment_status <> 'ACTIVE' THEN
    RAISE EXCEPTION 'Forecast Job requires an ACTIVE Deployment' USING ERRCODE = '23514';
  END IF;
  IF NEW.horizon_minutes <> model_horizon_minutes OR NEW.granularity <> model_granularity THEN
    RAISE EXCEPTION 'Forecast Job horizon/granularity must match its Model definition' USING ERRCODE = '23514';
  END IF;
  SELECT captured_at INTO input_captured_at
  FROM core_registry.forecast_input_snapshots
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.input_snapshot_id;
  IF input_captured_at IS NULL OR input_captured_at > NEW.forecast_origin THEN
    RAISE EXCEPTION 'Forecast Job input snapshot must be captured no later than forecast_origin' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_job_insert$;

DROP TRIGGER IF EXISTS forecast_jobs_validate_insert ON core_registry.forecast_jobs;
CREATE TRIGGER forecast_jobs_validate_insert BEFORE INSERT ON core_registry.forecast_jobs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_job_insert();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_job_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_job_transition$
BEGIN
  IF OLD.status = 'PENDING' AND NEW.status NOT IN ('PENDING','RUNNING','FAILED') THEN
    RAISE EXCEPTION 'Forecast Job must start before persisting' USING ERRCODE = '23514';
  ELSIF OLD.status = 'RUNNING' AND NEW.status NOT IN ('RUNNING','PERSISTING','FAILED') THEN
    RAISE EXCEPTION 'RUNNING Forecast Job can only enter persistence or fail' USING ERRCODE = '23514';
  ELSIF OLD.status = 'PERSISTING' AND NEW.status NOT IN ('PERSISTING','PERSISTED','FAILED') THEN
    RAISE EXCEPTION 'PERSISTING Forecast Job can only persist or fail' USING ERRCODE = '23514';
  ELSIF OLD.status IN ('PERSISTED','FAILED') AND NEW.status <> OLD.status THEN
    RAISE EXCEPTION 'completed Forecast Job is terminal' USING ERRCODE = '23514';
  END IF;
  IF NEW.deployment_id <> OLD.deployment_id OR NEW.model_version_id <> OLD.model_version_id
    OR NEW.input_snapshot_id <> OLD.input_snapshot_id OR NEW.target <> OLD.target
    OR NEW.subject_type <> OLD.subject_type OR NEW.subject_id <> OLD.subject_id
    OR NEW.forecast_origin <> OLD.forecast_origin OR NEW.horizon_minutes <> OLD.horizon_minutes
    OR NEW.granularity <> OLD.granularity OR NEW.trigger_type <> OLD.trigger_type THEN
    RAISE EXCEPTION 'Forecast Job input identity is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_job_transition$;

DROP TRIGGER IF EXISTS forecast_jobs_validate_transition ON core_registry.forecast_jobs;
CREATE TRIGGER forecast_jobs_validate_transition BEFORE UPDATE ON core_registry.forecast_jobs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_job_transition();

CREATE OR REPLACE FUNCTION core_registry.validate_forecast_snapshot_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $forecast_snapshot_insert$
DECLARE
  job_status text;
BEGIN
  SELECT status INTO job_status FROM core_registry.forecast_jobs
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.forecast_job_id;
  IF job_status <> 'PERSISTED' THEN
    RAISE EXCEPTION 'Forecast Snapshot requires a PERSISTED Forecast Job' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$forecast_snapshot_insert$;

DROP TRIGGER IF EXISTS forecast_snapshots_validate_insert ON core_registry.forecast_snapshots;
CREATE TRIGGER forecast_snapshots_validate_insert BEFORE INSERT ON core_registry.forecast_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_forecast_snapshot_insert();

CREATE INDEX IF NOT EXISTS forecast_dataset_snapshots_lookup_idx
  ON core_registry.forecast_dataset_snapshots (tenant_id, site_id, target, subject_type, subject_id, train_from, train_to);
CREATE INDEX IF NOT EXISTS forecast_model_versions_lookup_idx
  ON core_registry.forecast_model_versions (tenant_id, site_id, model_id, model_version, status);
CREATE INDEX IF NOT EXISTS forecast_jobs_lookup_idx
  ON core_registry.forecast_jobs (tenant_id, site_id, target, subject_type, subject_id, forecast_origin, status);
CREATE INDEX IF NOT EXISTS forecast_snapshots_lookup_idx
  ON core_registry.forecast_snapshots (tenant_id, site_id, forecast_origin, window_start, window_end);

ALTER TABLE core_registry.forecast_feature_sets ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_feature_sets FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_feature_set_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_feature_set_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_dataset_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_dataset_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_models ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_models FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_training_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_training_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_model_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_model_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_deployments ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_deployments FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_input_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_input_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_jobs FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.forecast_snapshots FORCE ROW LEVEL SECURITY;

CREATE POLICY forecast_feature_sets_runtime_scope ON core_registry.forecast_feature_sets FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY forecast_feature_set_versions_runtime_scope ON core_registry.forecast_feature_set_versions FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY forecast_dataset_snapshots_runtime_scope ON core_registry.forecast_dataset_snapshots FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_models_runtime_scope ON core_registry.forecast_models FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY forecast_training_runs_runtime_scope ON core_registry.forecast_training_runs FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_model_versions_runtime_scope ON core_registry.forecast_model_versions FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_deployments_runtime_scope ON core_registry.forecast_deployments FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_input_snapshots_runtime_scope ON core_registry.forecast_input_snapshots FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_jobs_runtime_scope ON core_registry.forecast_jobs FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_snapshots_runtime_scope ON core_registry.forecast_snapshots FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.forecast_feature_sets, core_registry.forecast_feature_set_versions,
  core_registry.forecast_dataset_snapshots, core_registry.forecast_models,
  core_registry.forecast_training_runs, core_registry.forecast_model_versions,
  core_registry.forecast_deployments, core_registry.forecast_input_snapshots,
  core_registry.forecast_jobs, core_registry.forecast_snapshots FROM PUBLIC;
GRANT SELECT ON core_registry.forecast_feature_sets, core_registry.forecast_feature_set_versions,
  core_registry.forecast_dataset_snapshots, core_registry.forecast_models,
  core_registry.forecast_training_runs, core_registry.forecast_model_versions,
  core_registry.forecast_deployments, core_registry.forecast_input_snapshots,
  core_registry.forecast_jobs, core_registry.forecast_snapshots TO s1_core_runtime;

COMMIT;
