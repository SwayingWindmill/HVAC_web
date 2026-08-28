BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- S22 Intelligence product contracts. Provider credentials are referenced by
-- opaque CredentialRef values only; secret material belongs to the secret owner.
CREATE TABLE IF NOT EXISTS core_registry.ai_model_definitions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 256),
  provider text NOT NULL CHECK (provider IN ('LOCAL','OPENAI','AZURE_OPENAI','GOOGLE','AWS_BEDROCK','OTHER')),
  model_id text NOT NULL CHECK (length(btrim(model_id)) BETWEEN 1 AND 512),
  capabilities text[] NOT NULL CHECK (cardinality(capabilities) > 0),
  credential_ref text,
  endpoint_policy_id text,
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, name, revision),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (provider = 'LOCAL' OR (credential_ref IS NOT NULL AND length(btrim(credential_ref)) BETWEEN 1 AND 512)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.ai_data_egress_policies (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 256),
  allowed_data_classes text[] NOT NULL DEFAULT '{}'::text[],
  allowed_regions text[] NOT NULL DEFAULT '{}'::text[],
  max_input_bytes bigint NOT NULL CHECK (max_input_bytes > 0),
  enabled boolean NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, name, revision),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id)
);

-- DeploymentRevision is immutable. Disable and rollback create/select a different
-- immutable revision through ai_deployment_bindings; historical invocations keep
-- their original revision forever.
CREATE TABLE IF NOT EXISTS core_registry.ai_deployment_revisions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  model_definition_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(model_definition_id)),
  use_case text NOT NULL CHECK (use_case IN ('FORECAST','FDD','OPTIMIZATION','OPERATIONS_SYNTHESIS')),
  revision bigint NOT NULL CHECK (revision > 0),
  output_schema_version text NOT NULL CHECK (length(btrim(output_schema_version)) BETWEEN 1 AND 256),
  data_egress_policy_id uuid,
  prompt_policy_version text,
  enabled boolean NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, model_definition_id, use_case, revision),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, model_definition_id) REFERENCES core_registry.ai_model_definitions(tenant_id, id),
  FOREIGN KEY (tenant_id, data_egress_policy_id) REFERENCES core_registry.ai_data_egress_policies(tenant_id, id)
);

CREATE OR REPLACE FUNCTION core_registry.reject_ai_deployment_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $ai_deployment_immutable$
BEGIN
  RAISE EXCEPTION 'AI Deployment Revision is immutable; create/select another revision' USING ERRCODE = '23514';
END
$ai_deployment_immutable$;

DROP TRIGGER IF EXISTS ai_deployment_revisions_reject_update ON core_registry.ai_deployment_revisions;
CREATE TRIGGER ai_deployment_revisions_reject_update
BEFORE UPDATE OR DELETE ON core_registry.ai_deployment_revisions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_ai_deployment_revision_mutation();

CREATE TABLE IF NOT EXISTS core_registry.ai_deployment_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  use_case text NOT NULL CHECK (use_case IN ('FORECAST','FDD','OPTIMIZATION','OPERATIONS_SYNTHESIS')),
  deployment_revision_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_revision_id)),
  status text NOT NULL CHECK (status IN ('ACTIVE','DISABLED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, use_case),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, deployment_revision_id) REFERENCES core_registry.ai_deployment_revisions(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_ai_deployment_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $ai_binding_validate$
DECLARE
  deployment_use_case text;
  deployment_enabled boolean;
BEGIN
  SELECT use_case, enabled INTO deployment_use_case, deployment_enabled
  FROM core_registry.ai_deployment_revisions
  WHERE tenant_id = NEW.tenant_id AND id = NEW.deployment_revision_id;
  IF deployment_use_case IS NULL OR deployment_use_case <> NEW.use_case THEN
    RAISE EXCEPTION 'AI deployment binding use case must match immutable Deployment Revision' USING ERRCODE = '23514';
  END IF;
  IF NEW.status = 'ACTIVE' AND NOT deployment_enabled THEN
    RAISE EXCEPTION 'disabled AI Deployment Revision cannot become ACTIVE binding' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$ai_binding_validate$;

DROP TRIGGER IF EXISTS ai_deployment_bindings_validate ON core_registry.ai_deployment_bindings;
CREATE TRIGGER ai_deployment_bindings_validate
BEFORE INSERT OR UPDATE ON core_registry.ai_deployment_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_ai_deployment_binding();

CREATE TABLE IF NOT EXISTS core_registry.ai_invocations (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  use_case text NOT NULL CHECK (use_case IN ('FORECAST','FDD','OPTIMIZATION','OPERATIONS_SYNTHESIS')),
  deployment_revision_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_revision_id)),
  input_snapshot_id uuid,
  input_digest text NOT NULL CHECK (input_digest ~ '^[a-f0-9]{64}$'),
  evidence_ids text[] NOT NULL DEFAULT '{}'::text[],
  output_schema_version text NOT NULL CHECK (length(btrim(output_schema_version)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('SUCCEEDED','FALLBACK','FAILED')),
  provider_request_id text,
  token_usage bigint CHECK (token_usage IS NULL OR token_usage >= 0),
  cost_micros bigint CHECK (cost_micros IS NULL OR cost_micros >= 0),
  latency_millis bigint NOT NULL CHECK (latency_millis >= 0),
  fallback_reason text,
  failure_code text,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, deployment_revision_id) REFERENCES core_registry.ai_deployment_revisions(tenant_id, id),
  CHECK ((status = 'FALLBACK' AND length(btrim(fallback_reason)) > 0) OR (status <> 'FALLBACK' AND fallback_reason IS NULL)),
  CHECK ((status = 'FAILED' AND length(btrim(failure_code)) > 0) OR (status <> 'FAILED' AND failure_code IS NULL))
);

-- FDD Finding is an evidence-backed intelligence fact, not an Alarm. Alarm and
-- Work Order IDs are explicit cross-bounded-context references because those
-- products own their own runtime databases and cannot be foreign-keyed here.
CREATE TABLE IF NOT EXISTS core_registry.fdd_findings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  asset_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(asset_id)),
  finding_type text NOT NULL CHECK (length(btrim(finding_type)) BETWEEN 1 AND 128),
  evaluation_from timestamptz NOT NULL,
  evaluation_to timestamptz NOT NULL,
  evidence_ids text[] NOT NULL CHECK (cardinality(evidence_ids) > 0),
  model_deployment_revision_id uuid,
  rule_revision_id text,
  confidence double precision NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  quality_blocker text,
  alarm_id uuid,
  work_order_id uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, asset_id) REFERENCES core_registry.assets(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, model_deployment_revision_id) REFERENCES core_registry.ai_deployment_revisions(tenant_id, id),
  CHECK (evaluation_to > evaluation_from),
  CHECK (model_deployment_revision_id IS NOT NULL OR length(btrim(rule_revision_id)) > 0)
);

CREATE TABLE IF NOT EXISTS core_registry.optimization_recommendations (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  optimization_run_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(optimization_run_id)),
  input_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(input_snapshot_id)),
  deployment_revision_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(deployment_revision_id)),
  baseline jsonb NOT NULL CHECK (jsonb_typeof(baseline) = 'object' AND baseline <> '{}'::jsonb),
  objective jsonb NOT NULL CHECK (jsonb_typeof(objective) = 'object' AND objective <> '{}'::jsonb),
  constraints jsonb NOT NULL CHECK (jsonb_typeof(constraints) = 'array' AND jsonb_array_length(constraints) > 0),
  candidate jsonb NOT NULL CHECK (jsonb_typeof(candidate) = 'object' AND candidate <> '{}'::jsonb),
  expected_impact jsonb NOT NULL CHECK (jsonb_typeof(expected_impact) = 'object' AND expected_impact <> '{}'::jsonb),
  uncertainty jsonb NOT NULL CHECK (jsonb_typeof(uncertainty) = 'object' AND uncertainty <> '{}'::jsonb),
  risk jsonb NOT NULL CHECK (jsonb_typeof(risk) = 'object' AND risk <> '{}'::jsonb),
  rollback_plan jsonb NOT NULL CHECK (jsonb_typeof(rollback_plan) = 'object' AND rollback_plan <> '{}'::jsonb),
  verification_plan jsonb NOT NULL CHECK (jsonb_typeof(verification_plan) = 'object' AND verification_plan <> '{}'::jsonb),
  approval_state text NOT NULL CHECK (approval_state IN ('DRAFT','APPROVED','REJECTED')),
  current_state_revalidation jsonb,
  command_intent_id uuid,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, optimization_run_id),
  FOREIGN KEY (tenant_id, site_id, optimization_run_id) REFERENCES core_registry.optimization_runs(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, input_snapshot_id) REFERENCES core_registry.optimization_input_snapshots(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, deployment_revision_id) REFERENCES core_registry.ai_deployment_revisions(tenant_id, id),
  CHECK (current_state_revalidation IS NULL OR jsonb_typeof(current_state_revalidation) = 'object'),
  CHECK (updated_at >= created_at),
  CHECK (command_intent_id IS NULL OR (approval_state = 'APPROVED' AND current_state_revalidation IS NOT NULL))
);

CREATE OR REPLACE FUNCTION core_registry.validate_optimization_recommendation_control_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $optimization_recommendation_control$
DECLARE
  accepted boolean;
  snapshot_id text;
  validated_at timestamptz;
  expires_at timestamptz;
BEGIN
  IF NEW.command_intent_id IS NOT NULL AND OLD.command_intent_id IS NULL THEN
    accepted := coalesce((NEW.current_state_revalidation ->> 'accepted')::boolean, false);
    snapshot_id := btrim(coalesce(NEW.current_state_revalidation ->> 'snapshotId', ''));
    validated_at := NULLIF(NEW.current_state_revalidation ->> 'validatedAt', '')::timestamptz;
    expires_at := NULLIF(NEW.current_state_revalidation ->> 'expiresAt', '')::timestamptz;
    IF NEW.approval_state <> 'APPROVED' OR NOT accepted OR snapshot_id = '' OR validated_at IS NULL OR validated_at <= NEW.created_at OR expires_at IS NULL OR expires_at <= NEW.updated_at THEN
      RAISE EXCEPTION 'Command intent requires APPROVED Recommendation and fresh independent current-state revalidation' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$optimization_recommendation_control$;

DROP TRIGGER IF EXISTS optimization_recommendations_validate_control ON core_registry.optimization_recommendations;
CREATE TRIGGER optimization_recommendations_validate_control
BEFORE UPDATE ON core_registry.optimization_recommendations
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_optimization_recommendation_control_transition();

-- S22 retires the ESS-only execution assumption. The frozen Site snapshot remains
-- canonical, but it no longer requires an ESS child row before sealing.
ALTER TABLE core_registry.optimization_policies
  DROP CONSTRAINT IF EXISTS optimization_policies_resource_type_check;
ALTER TABLE core_registry.optimization_policies
  ADD CONSTRAINT optimization_policies_resource_type_check CHECK (resource_type = 'HVAC');

CREATE OR REPLACE FUNCTION core_registry.validate_optimization_input_snapshot_write()
RETURNS trigger
LANGUAGE plpgsql
AS $optimization_input_write$
DECLARE
  policy_status text;
  topology_status text;
  tariff_status text;
  load_target text;
  load_subject_type text;
  load_subject_id uuid;
  pv_target text;
  pv_subject_type text;
  pv_subject_id uuid;
BEGIN
  IF TG_OP = 'UPDATE' AND OLD.status = 'SEALED' THEN
    RAISE EXCEPTION 'SEALED Optimization Input Snapshot is immutable' USING ERRCODE = '23514';
  END IF;
  SELECT status INTO policy_status FROM core_registry.optimization_policy_versions
  WHERE tenant_id = NEW.tenant_id AND id = NEW.policy_version_id;
  IF policy_status <> 'RELEASED' THEN
    RAISE EXCEPTION 'Optimization Input Snapshot requires a RELEASED Policy Version' USING ERRCODE = '23514';
  END IF;
  SELECT status INTO topology_status FROM core_registry.energy_topology_versions
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.topology_version_id;
  IF topology_status NOT IN ('RELEASED','ACTIVE','SUPERSEDED') THEN
    RAISE EXCEPTION 'Optimization Input Snapshot requires a released Topology Version' USING ERRCODE = '23514';
  END IF;
  SELECT status INTO tariff_status FROM core_registry.tariff_versions
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.tariff_version_id;
  IF tariff_status <> 'RELEASED' THEN
    RAISE EXCEPTION 'Optimization Input Snapshot requires a RELEASED Tariff Version' USING ERRCODE = '23514';
  END IF;
  SELECT job.target, job.subject_type, job.subject_id INTO load_target, load_subject_type, load_subject_id
  FROM core_registry.forecast_snapshots AS snapshot
  JOIN core_registry.forecast_jobs AS job
    ON job.tenant_id = snapshot.tenant_id AND job.site_id = snapshot.site_id AND job.id = snapshot.forecast_job_id
  WHERE snapshot.tenant_id = NEW.tenant_id AND snapshot.site_id = NEW.site_id AND snapshot.id = NEW.load_forecast_snapshot_id;
  IF load_target <> 'SITE_LOAD' OR load_subject_type <> NEW.subject_type OR load_subject_id <> NEW.subject_id THEN
    RAISE EXCEPTION 'Optimization load forecast reference must be the exact SITE_LOAD Forecast Snapshot for the subject' USING ERRCODE = '23514';
  END IF;
  IF NEW.pv_forecast_snapshot_id IS NOT NULL THEN
    SELECT job.target, job.subject_type, job.subject_id INTO pv_target, pv_subject_type, pv_subject_id
    FROM core_registry.forecast_snapshots AS snapshot
    JOIN core_registry.forecast_jobs AS job
      ON job.tenant_id = snapshot.tenant_id AND job.site_id = snapshot.site_id AND job.id = snapshot.forecast_job_id
    WHERE snapshot.tenant_id = NEW.tenant_id AND snapshot.site_id = NEW.site_id AND snapshot.id = NEW.pv_forecast_snapshot_id;
    IF pv_target <> 'PV_GENERATION' OR pv_subject_type <> NEW.subject_type OR pv_subject_id <> NEW.subject_id THEN
      RAISE EXCEPTION 'Optimization PV forecast reference must be the exact PV_GENERATION Forecast Snapshot for the subject' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$optimization_input_write$;

ALTER TABLE core_registry.ai_model_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_model_definitions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_data_egress_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_data_egress_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_deployment_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_deployment_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_deployment_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_deployment_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_invocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.ai_invocations FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.fdd_findings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.fdd_findings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_recommendations ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_recommendations FORCE ROW LEVEL SECURITY;

CREATE POLICY ai_model_definitions_core_scope ON core_registry.ai_model_definitions FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_data_egress_policies_core_scope ON core_registry.ai_data_egress_policies FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_deployment_revisions_core_scope ON core_registry.ai_deployment_revisions FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_deployment_bindings_core_scope ON core_registry.ai_deployment_bindings FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY ai_invocations_core_scope ON core_registry.ai_invocations FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY fdd_findings_core_scope ON core_registry.fdd_findings FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY optimization_recommendations_core_scope ON core_registry.optimization_recommendations FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY ai_model_definitions_forecast_scope ON core_registry.ai_model_definitions FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_data_egress_policies_forecast_scope ON core_registry.ai_data_egress_policies FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_deployment_revisions_forecast_scope ON core_registry.ai_deployment_revisions FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_deployment_bindings_forecast_scope ON core_registry.ai_deployment_bindings FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY ai_invocations_forecast_scope ON core_registry.ai_invocations FOR ALL TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));

CREATE POLICY ai_model_definitions_optimization_scope ON core_registry.ai_model_definitions FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_data_egress_policies_optimization_scope ON core_registry.ai_data_egress_policies FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_deployment_revisions_optimization_scope ON core_registry.ai_deployment_revisions FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY ai_deployment_bindings_optimization_scope ON core_registry.ai_deployment_bindings FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY ai_invocations_optimization_scope ON core_registry.ai_invocations FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY optimization_recommendations_runtime_scope ON core_registry.optimization_recommendations FOR ALL TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY fdd_findings_runtime_scope ON core_registry.fdd_findings FOR ALL TO fdd_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY ai_deployment_revisions_fdd_scope ON core_registry.ai_deployment_revisions FOR SELECT TO fdd_runtime
  USING (tenant_id = core_registry.current_tenant_id());

-- Forecast owns preparation and execution. Callers identify the requested subject/target;
-- the runtime resolves released lineage, freezes the input snapshot, creates the Forecast
-- Job, and later publishes the immutable result snapshot.
CREATE POLICY forecast_models_forecast_worker_scope ON core_registry.forecast_models FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY forecast_model_versions_forecast_worker_scope ON core_registry.forecast_model_versions FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_feature_set_versions_forecast_worker_scope ON core_registry.forecast_feature_set_versions FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY forecast_dataset_snapshots_forecast_worker_scope ON core_registry.forecast_dataset_snapshots FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_deployments_forecast_worker_scope ON core_registry.forecast_deployments FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_input_snapshots_forecast_worker_scope ON core_registry.forecast_input_snapshots FOR SELECT TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_input_snapshots_forecast_worker_insert_scope ON core_registry.forecast_input_snapshots FOR INSERT TO forecast_runtime
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_jobs_forecast_worker_domain_scope ON core_registry.forecast_jobs FOR ALL TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY forecast_snapshots_forecast_worker_domain_scope ON core_registry.forecast_snapshots FOR ALL TO forecast_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY job_instances_forecast_worker_scope ON core_registry.job_instances FOR SELECT TO forecast_runtime
  USING (job_type = 'FORECAST_RUN');
CREATE POLICY job_instances_forecast_worker_insert_scope ON core_registry.job_instances FOR INSERT TO forecast_runtime
  WITH CHECK (job_type = 'FORECAST_RUN');
CREATE POLICY job_instances_forecast_worker_update_scope ON core_registry.job_instances FOR UPDATE TO forecast_runtime
  USING (job_type = 'FORECAST_RUN') WITH CHECK (job_type = 'FORECAST_RUN');
CREATE POLICY job_attempts_forecast_worker_scope ON core_registry.job_attempts FOR ALL TO forecast_runtime
  USING (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'FORECAST_RUN'))
  WITH CHECK (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'FORECAST_RUN'));
CREATE POLICY job_instances_optimization_worker_scope ON core_registry.job_instances FOR SELECT TO optimization_runtime
  USING (job_type = 'OPTIMIZATION_RUN');
CREATE POLICY job_instances_optimization_worker_update_scope ON core_registry.job_instances FOR UPDATE TO optimization_runtime
  USING (job_type = 'OPTIMIZATION_RUN') WITH CHECK (job_type = 'OPTIMIZATION_RUN');
CREATE POLICY job_attempts_optimization_worker_scope ON core_registry.job_attempts FOR ALL TO optimization_runtime
  USING (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'OPTIMIZATION_RUN'))
  WITH CHECK (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'OPTIMIZATION_RUN'));

REVOKE ALL ON core_registry.ai_model_definitions, core_registry.ai_data_egress_policies,
  core_registry.ai_deployment_revisions, core_registry.ai_deployment_bindings, core_registry.ai_invocations,
  core_registry.fdd_findings, core_registry.optimization_recommendations FROM PUBLIC;
GRANT SELECT ON core_registry.ai_model_definitions, core_registry.ai_data_egress_policies,
  core_registry.ai_deployment_revisions, core_registry.ai_deployment_bindings, core_registry.ai_invocations,
  core_registry.fdd_findings, core_registry.optimization_recommendations TO s1_core_runtime;
GRANT SELECT ON core_registry.ai_model_definitions, core_registry.ai_data_egress_policies,
  core_registry.ai_deployment_revisions, core_registry.ai_deployment_bindings TO forecast_runtime, optimization_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.ai_invocations TO forecast_runtime, optimization_runtime;
GRANT SELECT ON core_registry.forecast_models, core_registry.forecast_model_versions,
  core_registry.forecast_feature_set_versions, core_registry.forecast_dataset_snapshots,
  core_registry.forecast_deployments, core_registry.forecast_input_snapshots TO forecast_runtime;
GRANT INSERT ON core_registry.forecast_input_snapshots TO forecast_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.forecast_jobs TO forecast_runtime;
GRANT SELECT, INSERT ON core_registry.forecast_snapshots TO forecast_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.optimization_recommendations TO optimization_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.fdd_findings TO fdd_runtime;
GRANT SELECT ON core_registry.ai_deployment_revisions TO fdd_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.job_instances TO forecast_runtime;
GRANT SELECT, UPDATE ON core_registry.job_instances TO optimization_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.job_attempts TO forecast_runtime, optimization_runtime;

-- Runtime execution no longer writes the obsolete ESS DispatchPlan surface.
REVOKE INSERT, UPDATE, DELETE ON core_registry.dispatch_plans FROM optimization_runtime;
REVOKE INSERT, UPDATE, DELETE ON core_registry.dispatch_intervals FROM optimization_runtime;
REVOKE SELECT ON core_registry.optimization_input_resources FROM optimization_runtime;

CREATE INDEX IF NOT EXISTS ai_model_definitions_tenant_status_idx ON core_registry.ai_model_definitions (tenant_id, status, name);
CREATE INDEX IF NOT EXISTS ai_deployment_bindings_site_use_case_idx ON core_registry.ai_deployment_bindings (tenant_id, site_id, use_case, status);
CREATE INDEX IF NOT EXISTS ai_invocations_site_use_case_idx ON core_registry.ai_invocations (tenant_id, site_id, use_case, created_at DESC);
CREATE INDEX IF NOT EXISTS fdd_findings_asset_time_idx ON core_registry.fdd_findings (tenant_id, site_id, asset_id, evaluation_to DESC);
CREATE INDEX IF NOT EXISTS optimization_recommendations_run_idx ON core_registry.optimization_recommendations (tenant_id, site_id, optimization_run_id);

RESET ROLE;
COMMIT;
