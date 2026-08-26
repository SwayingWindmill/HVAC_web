BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- Optimization Policy is versioned separately from execution. V2 production
-- optimization computes plans only; Control/Safety remain the execution authority.
CREATE TABLE IF NOT EXISTS core_registry.optimization_policies (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  policy_code text NOT NULL CHECK (policy_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  subject_type text NOT NULL CHECK (subject_type = 'SITE'),
  resource_type text NOT NULL CHECK (resource_type = 'ESS'),
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, policy_code),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.optimization_policy_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  policy_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(policy_id)),
  version bigint NOT NULL CHECK (version > 0),
  objective text NOT NULL CHECK (objective IN ('COST','DEMAND','CARBON','WEIGHTED')),
  weights jsonb NOT NULL,
  constraints jsonb NOT NULL,
  dispatch_mode text NOT NULL CHECK (dispatch_mode IN ('SHADOW','ASSISTED','AUTO_LIMITED','AUTO')),
  fallback_policy text NOT NULL CHECK (fallback_policy IN ('PREVIOUS_VALID_PLAN','RULE_STRATEGY','NO_DISPATCH')),
  risk_level text NOT NULL CHECK (risk_level IN ('LOW','MEDIUM','HIGH')),
  horizon text NOT NULL CHECK (horizon IN ('INTRADAY','DAY_AHEAD','MULTI_DAY')),
  horizon_minutes integer NOT NULL CHECK (horizon_minutes > 0),
  granularity text NOT NULL CHECK (granularity = '15MIN'),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, policy_id, version),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, policy_id) REFERENCES core_registry.optimization_policies(tenant_id, id),
  CHECK (jsonb_typeof(weights) = 'object'),
  CHECK (jsonb_typeof(constraints) = 'object'),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_optimization_policy_version()
RETURNS trigger
LANGUAGE plpgsql
AS $optimization_policy_overlap$
BEGIN
  IF NEW.status = 'RELEASED' AND EXISTS (
    SELECT 1 FROM core_registry.optimization_policy_versions AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.policy_id = NEW.policy_id
      AND existing.id <> NEW.id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'released Optimization Policy Versions cannot overlap' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$optimization_policy_overlap$;

DROP TRIGGER IF EXISTS optimization_policy_versions_reject_overlap ON core_registry.optimization_policy_versions;
CREATE TRIGGER optimization_policy_versions_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, policy_id, effective_from, effective_to, status
ON core_registry.optimization_policy_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_optimization_policy_version();

CREATE OR REPLACE FUNCTION core_registry.reject_released_optimization_policy_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $optimization_policy_immutable$
BEGIN
  IF OLD.status IN ('RELEASED','SUPERSEDED') AND (
    NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
    OR NEW.policy_id IS DISTINCT FROM OLD.policy_id
    OR NEW.version IS DISTINCT FROM OLD.version
    OR NEW.objective IS DISTINCT FROM OLD.objective
    OR NEW.weights IS DISTINCT FROM OLD.weights
    OR NEW.constraints IS DISTINCT FROM OLD.constraints
    OR NEW.dispatch_mode IS DISTINCT FROM OLD.dispatch_mode
    OR NEW.fallback_policy IS DISTINCT FROM OLD.fallback_policy
    OR NEW.risk_level IS DISTINCT FROM OLD.risk_level
    OR NEW.horizon IS DISTINCT FROM OLD.horizon
    OR NEW.horizon_minutes IS DISTINCT FROM OLD.horizon_minutes
    OR NEW.granularity IS DISTINCT FROM OLD.granularity
    OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
    OR NEW.effective_to IS DISTINCT FROM OLD.effective_to
  ) THEN
    RAISE EXCEPTION 'released Optimization Policy Version is immutable; create a new version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$optimization_policy_immutable$;

DROP TRIGGER IF EXISTS optimization_policy_versions_reject_released_mutation ON core_registry.optimization_policy_versions;
CREATE TRIGGER optimization_policy_versions_reject_released_mutation
BEFORE UPDATE ON core_registry.optimization_policy_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_released_optimization_policy_version_mutation();

-- Input Snapshot is assembled in BUILDING state, then SEALED. Once SEALED it is
-- immutable and every Run must refer to the exact frozen Forecast/Tariff/Topology/current-state inputs.
CREATE TABLE IF NOT EXISTS core_registry.optimization_input_snapshots (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  subject_type text NOT NULL CHECK (subject_type = 'SITE'),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  policy_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(policy_version_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  load_forecast_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(load_forecast_snapshot_id)),
  pv_forecast_snapshot_id uuid,
  tariff_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tariff_version_id)),
  current_state jsonb NOT NULL,
  safety_constraints jsonb NOT NULL,
  maintenance_constraints jsonb NOT NULL,
  manual_locks jsonb NOT NULL,
  captured_at timestamptz NOT NULL,
  input_checksum text,
  status text NOT NULL CHECK (status IN ('BUILDING','SEALED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, policy_version_id) REFERENCES core_registry.optimization_policy_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, topology_version_id) REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, load_forecast_snapshot_id) REFERENCES core_registry.forecast_snapshots(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, pv_forecast_snapshot_id) REFERENCES core_registry.forecast_snapshots(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, tariff_version_id) REFERENCES core_registry.tariff_versions(tenant_id, site_id, id),
  CHECK (subject_id = site_id),
  CHECK (jsonb_typeof(current_state) = 'object'),
  CHECK (jsonb_typeof(safety_constraints) = 'object'),
  CHECK (jsonb_typeof(maintenance_constraints) = 'object'),
  CHECK (jsonb_typeof(manual_locks) = 'object'),
  CHECK ((status = 'BUILDING' AND input_checksum IS NULL)
    OR (status = 'SEALED' AND input_checksum ~ '^[a-f0-9]{64}$')),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.optimization_input_resources (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  input_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(input_snapshot_id)),
  resource_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(resource_id)),
  resource_type text NOT NULL CHECK (resource_type = 'ESS'),
  rated_power_kw double precision NOT NULL CHECK (rated_power_kw > 0),
  rated_capacity_kwh double precision NOT NULL CHECK (rated_capacity_kwh > 0),
  usable_capacity_kwh double precision NOT NULL CHECK (usable_capacity_kwh > 0 AND usable_capacity_kwh <= rated_capacity_kwh),
  soc double precision NOT NULL CHECK (soc BETWEEN 0 AND 1),
  soh double precision NOT NULL CHECK (soh > 0 AND soh <= 1),
  current_power_kw double precision NOT NULL,
  charge_power_limit_kw double precision NOT NULL CHECK (charge_power_limit_kw >= 0),
  discharge_power_limit_kw double precision NOT NULL CHECK (discharge_power_limit_kw >= 0),
  min_soc double precision NOT NULL CHECK (min_soc BETWEEN 0 AND 1),
  max_soc double precision NOT NULL CHECK (max_soc BETWEEN 0 AND 1 AND max_soc > min_soc),
  charge_efficiency double precision NOT NULL CHECK (charge_efficiency > 0 AND charge_efficiency <= 1),
  discharge_efficiency double precision NOT NULL CHECK (discharge_efficiency > 0 AND discharge_efficiency <= 1),
  availability boolean NOT NULL,
  control_mode text NOT NULL CHECK (control_mode IN ('REMOTE','LOCAL','DISABLED')),
  ordinal integer NOT NULL CHECK (ordinal >= 0),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, input_snapshot_id, resource_id),
  UNIQUE (tenant_id, site_id, input_snapshot_id, ordinal),
  FOREIGN KEY (tenant_id, site_id, input_snapshot_id) REFERENCES core_registry.optimization_input_snapshots(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, resource_id) REFERENCES core_registry.assets(tenant_id, site_id, id),
  CHECK (soc >= min_soc AND soc <= max_soc)
);

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
  resource_count bigint;
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
  SELECT job.target, job.subject_type, job.subject_id
    INTO load_target, load_subject_type, load_subject_id
  FROM core_registry.forecast_snapshots AS snapshot
  JOIN core_registry.forecast_jobs AS job
    ON job.tenant_id = snapshot.tenant_id AND job.site_id = snapshot.site_id AND job.id = snapshot.forecast_job_id
  WHERE snapshot.tenant_id = NEW.tenant_id AND snapshot.site_id = NEW.site_id AND snapshot.id = NEW.load_forecast_snapshot_id;
  IF load_target <> 'SITE_LOAD' OR load_subject_type <> NEW.subject_type OR load_subject_id <> NEW.subject_id THEN
    RAISE EXCEPTION 'Optimization load forecast reference must be the exact SITE_LOAD Forecast Snapshot for the subject' USING ERRCODE = '23514';
  END IF;
  IF NEW.pv_forecast_snapshot_id IS NOT NULL THEN
    SELECT job.target, job.subject_type, job.subject_id
      INTO pv_target, pv_subject_type, pv_subject_id
    FROM core_registry.forecast_snapshots AS snapshot
    JOIN core_registry.forecast_jobs AS job
      ON job.tenant_id = snapshot.tenant_id AND job.site_id = snapshot.site_id AND job.id = snapshot.forecast_job_id
    WHERE snapshot.tenant_id = NEW.tenant_id AND snapshot.site_id = NEW.site_id AND snapshot.id = NEW.pv_forecast_snapshot_id;
    IF pv_target <> 'PV_GENERATION' OR pv_subject_type <> NEW.subject_type OR pv_subject_id <> NEW.subject_id THEN
      RAISE EXCEPTION 'Optimization PV forecast reference must be the exact PV_GENERATION Forecast Snapshot for the subject' USING ERRCODE = '23514';
    END IF;
  END IF;

  IF TG_OP = 'UPDATE' AND NEW.status = 'SEALED' AND OLD.status = 'BUILDING' THEN
    SELECT count(*) INTO resource_count FROM core_registry.optimization_input_resources
    WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND input_snapshot_id = NEW.id;
    IF resource_count < 1 THEN
      RAISE EXCEPTION 'Optimization Input Snapshot requires at least one ESS Resource before sealing' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$optimization_input_write$;

DROP TRIGGER IF EXISTS optimization_input_snapshots_validate_write ON core_registry.optimization_input_snapshots;
CREATE TRIGGER optimization_input_snapshots_validate_write
BEFORE INSERT OR UPDATE ON core_registry.optimization_input_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_optimization_input_snapshot_write();

CREATE OR REPLACE FUNCTION core_registry.validate_optimization_input_resource_write()
RETURNS trigger
LANGUAGE plpgsql
AS $optimization_resource_write$
DECLARE
  snapshot_status text;
  asset_type_value text;
BEGIN
  SELECT status INTO snapshot_status FROM core_registry.optimization_input_snapshots
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.input_snapshot_id;
  IF snapshot_status <> 'BUILDING' THEN
    RAISE EXCEPTION 'Optimization Input Resources can only change while Snapshot is BUILDING' USING ERRCODE = '23514';
  END IF;
  SELECT asset_type INTO asset_type_value FROM core_registry.assets
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.resource_id;
  IF upper(asset_type_value) <> 'ESS' THEN
    RAISE EXCEPTION 'Optimization P0 Resource must be ESS asset' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$optimization_resource_write$;

DROP TRIGGER IF EXISTS optimization_input_resources_validate_write ON core_registry.optimization_input_resources;
CREATE TRIGGER optimization_input_resources_validate_write
BEFORE INSERT OR UPDATE ON core_registry.optimization_input_resources
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_optimization_input_resource_write();

CREATE OR REPLACE FUNCTION core_registry.reject_optimization_input_resource_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $optimization_resource_delete$
DECLARE
  snapshot_status text;
BEGIN
  SELECT status INTO snapshot_status FROM core_registry.optimization_input_snapshots
  WHERE tenant_id = OLD.tenant_id AND site_id = OLD.site_id AND id = OLD.input_snapshot_id;
  IF snapshot_status <> 'BUILDING' THEN
    RAISE EXCEPTION 'SEALED Optimization Input Resources are immutable' USING ERRCODE = '23514';
  END IF;
  RETURN OLD;
END
$optimization_resource_delete$;

DROP TRIGGER IF EXISTS optimization_input_resources_reject_delete ON core_registry.optimization_input_resources;
CREATE TRIGGER optimization_input_resources_reject_delete
BEFORE DELETE ON core_registry.optimization_input_resources
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_optimization_input_resource_delete();

CREATE TABLE IF NOT EXISTS core_registry.optimization_runs (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  subject_type text NOT NULL CHECK (subject_type = 'SITE'),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  policy_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(policy_version_id)),
  input_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(input_snapshot_id)),
  objective text NOT NULL CHECK (objective IN ('COST','DEMAND','CARBON','WEIGHTED')),
  horizon text NOT NULL CHECK (horizon IN ('INTRADAY','DAY_AHEAD','MULTI_DAY')),
  horizon_minutes integer NOT NULL CHECK (horizon_minutes > 0),
  granularity text NOT NULL CHECK (granularity = '15MIN'),
  solver text NOT NULL CHECK (length(btrim(solver)) BETWEEN 1 AND 128),
  solver_version text NOT NULL CHECK (length(btrim(solver_version)) BETWEEN 1 AND 128),
  status text NOT NULL CHECK (status IN ('CREATED','VALIDATING','SOLVING','FEASIBLE','PERSISTING','INFEASIBLE','FAILED','PUBLISHED','EXPIRED')),
  quality text CHECK (quality IS NULL OR quality IN ('OPTIMAL','FEASIBLE','DEGRADED','FALLBACK','INVALID')),
  objective_value double precision,
  constraint_status jsonb,
  started_at timestamptz,
  finished_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, policy_version_id) REFERENCES core_registry.optimization_policy_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, input_snapshot_id) REFERENCES core_registry.optimization_input_snapshots(tenant_id, site_id, id),
  CHECK (subject_id = site_id),
  CHECK (constraint_status IS NULL OR jsonb_typeof(constraint_status) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_optimization_run_write()
RETURNS trigger
LANGUAGE plpgsql
AS $optimization_run_write$
DECLARE
  snapshot_status text;
  snapshot_policy_version_id uuid;
  snapshot_subject_id uuid;
  policy_objective text;
  policy_horizon text;
  policy_horizon_minutes integer;
  policy_granularity text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    SELECT status, policy_version_id, subject_id
      INTO snapshot_status, snapshot_policy_version_id, snapshot_subject_id
    FROM core_registry.optimization_input_snapshots
    WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.input_snapshot_id;
    SELECT objective, horizon, horizon_minutes, granularity
      INTO policy_objective, policy_horizon, policy_horizon_minutes, policy_granularity
    FROM core_registry.optimization_policy_versions
    WHERE tenant_id = NEW.tenant_id AND id = NEW.policy_version_id;
    IF snapshot_status <> 'SEALED' OR snapshot_policy_version_id <> NEW.policy_version_id OR snapshot_subject_id <> NEW.subject_id THEN
      RAISE EXCEPTION 'Optimization Run requires the exact SEALED Input Snapshot for its Policy/Subject' USING ERRCODE = '23514';
    END IF;
    IF NEW.objective <> policy_objective OR NEW.horizon <> policy_horizon
      OR NEW.horizon_minutes <> policy_horizon_minutes OR NEW.granularity <> policy_granularity THEN
      RAISE EXCEPTION 'Optimization Run objective/horizon/granularity must match Policy Version' USING ERRCODE = '23514';
    END IF;
    IF NEW.status <> 'CREATED' THEN
      RAISE EXCEPTION 'Optimization Run must start as CREATED' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;

  IF OLD.status = 'CREATED' AND NEW.status NOT IN ('CREATED','VALIDATING','FAILED') THEN
    RAISE EXCEPTION 'Optimization Run must validate before solving' USING ERRCODE = '23514';
  ELSIF OLD.status = 'VALIDATING' AND NEW.status NOT IN ('VALIDATING','SOLVING','INFEASIBLE','FAILED') THEN
    RAISE EXCEPTION 'Optimization Run must enter SOLVING after validation' USING ERRCODE = '23514';
  ELSIF OLD.status = 'SOLVING' AND NEW.status NOT IN ('SOLVING','FEASIBLE','INFEASIBLE','FAILED') THEN
    RAISE EXCEPTION 'SOLVING Optimization Run can only become feasible, infeasible or failed' USING ERRCODE = '23514';
  ELSIF OLD.status = 'FEASIBLE' AND NEW.status NOT IN ('FEASIBLE','PERSISTING','EXPIRED') THEN
    RAISE EXCEPTION 'FEASIBLE Optimization Run can only enter evaluation persistence or expire' USING ERRCODE = '23514';
  ELSIF OLD.status = 'PERSISTING' AND NEW.status NOT IN ('PERSISTING','PUBLISHED','FAILED') THEN
    RAISE EXCEPTION 'PERSISTING Optimization Run can only publish or fail' USING ERRCODE = '23514';
  ELSIF OLD.status IN ('INFEASIBLE','FAILED','PUBLISHED','EXPIRED') AND NEW.status <> OLD.status THEN
    RAISE EXCEPTION 'terminal Optimization Run cannot change status' USING ERRCODE = '23514';
  END IF;
  IF NEW.policy_version_id <> OLD.policy_version_id OR NEW.input_snapshot_id <> OLD.input_snapshot_id
    OR NEW.subject_type <> OLD.subject_type OR NEW.subject_id <> OLD.subject_id
    OR NEW.objective <> OLD.objective OR NEW.horizon <> OLD.horizon
    OR NEW.horizon_minutes <> OLD.horizon_minutes OR NEW.granularity <> OLD.granularity
    OR NEW.solver <> OLD.solver OR NEW.solver_version <> OLD.solver_version THEN
    RAISE EXCEPTION 'Optimization Run input/solver identity is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$optimization_run_write$;

DROP TRIGGER IF EXISTS optimization_runs_validate_write ON core_registry.optimization_runs;
CREATE TRIGGER optimization_runs_validate_write
BEFORE INSERT OR UPDATE ON core_registry.optimization_runs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_optimization_run_write();

CREATE TABLE IF NOT EXISTS core_registry.dispatch_plans (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  optimization_run_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(optimization_run_id)),
  input_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(input_snapshot_id)),
  policy_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(policy_version_id)),
  subject_type text NOT NULL CHECK (subject_type = 'SITE'),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  plan_version bigint NOT NULL CHECK (plan_version > 0),
  quality text NOT NULL CHECK (quality IN ('OPTIMAL','FEASIBLE','DEGRADED','FALLBACK','INVALID')),
  status text NOT NULL CHECK (status IN ('DRAFT','VALIDATED','SHADOW','APPROVED','ACTIVE','SUPERSEDED','COMPLETED','CANCELLED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz NOT NULL,
  expected_cost double precision,
  expected_saving double precision,
  objective_value double precision,
  explanation jsonb NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, optimization_run_id, plan_version),
  FOREIGN KEY (tenant_id, site_id, optimization_run_id) REFERENCES core_registry.optimization_runs(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, input_snapshot_id) REFERENCES core_registry.optimization_input_snapshots(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, policy_version_id) REFERENCES core_registry.optimization_policy_versions(tenant_id, id),
  CHECK (subject_id = site_id),
  CHECK (valid_to > valid_from),
  CHECK (jsonb_typeof(explanation) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.dispatch_intervals (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  dispatch_plan_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(dispatch_plan_id)),
  resource_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(resource_id)),
  start_time timestamptz NOT NULL,
  end_time timestamptz NOT NULL,
  target_type text NOT NULL CHECK (target_type = 'POWER_SETPOINT'),
  target_value double precision NOT NULL,
  unit text NOT NULL CHECK (unit = 'kW'),
  expected_soc double precision CHECK (expected_soc IS NULL OR expected_soc BETWEEN 0 AND 1),
  expected_grid_power double precision,
  expected_cost double precision,
  constraint_margin jsonb NOT NULL,
  ordinal integer NOT NULL CHECK (ordinal >= 0),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, dispatch_plan_id, resource_id, start_time),
  UNIQUE (tenant_id, site_id, dispatch_plan_id, resource_id, ordinal),
  FOREIGN KEY (tenant_id, site_id, dispatch_plan_id) REFERENCES core_registry.dispatch_plans(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, resource_id) REFERENCES core_registry.assets(tenant_id, site_id, id),
  CHECK (end_time > start_time),
  CHECK (jsonb_typeof(constraint_margin) = 'object')
);

CREATE OR REPLACE FUNCTION core_registry.validate_dispatch_plan_write()
RETURNS trigger
LANGUAGE plpgsql
AS $dispatch_plan_write$
DECLARE
  run_status text;
  run_input_snapshot_id uuid;
  run_policy_version_id uuid;
  run_subject_id uuid;
  dispatch_mode_value text;
  run_horizon_minutes integer;
  interval_count bigint;
  resource_count bigint;
  incomplete_resource_count bigint;
BEGIN
  SELECT status, input_snapshot_id, policy_version_id, subject_id, horizon_minutes
    INTO run_status, run_input_snapshot_id, run_policy_version_id, run_subject_id, run_horizon_minutes
  FROM core_registry.optimization_runs
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.optimization_run_id;
  SELECT policy.dispatch_mode INTO dispatch_mode_value
  FROM core_registry.optimization_policy_versions AS policy
  WHERE policy.tenant_id = NEW.tenant_id AND policy.id = NEW.policy_version_id;

  IF TG_OP = 'INSERT' THEN
    IF run_status <> 'FEASIBLE' OR NEW.input_snapshot_id <> run_input_snapshot_id
      OR NEW.policy_version_id <> run_policy_version_id OR NEW.subject_id <> run_subject_id THEN
      RAISE EXCEPTION 'Dispatch Plan requires a FEASIBLE Optimization Run with identical Input/Policy/Subject lineage' USING ERRCODE = '23514';
    END IF;
    IF NEW.status <> 'DRAFT' THEN
      RAISE EXCEPTION 'Dispatch Plan must start as DRAFT' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;

  IF OLD.status IN ('VALIDATED','SHADOW','APPROVED','ACTIVE','SUPERSEDED','COMPLETED','CANCELLED') AND (
    NEW.optimization_run_id <> OLD.optimization_run_id OR NEW.input_snapshot_id <> OLD.input_snapshot_id
    OR NEW.policy_version_id <> OLD.policy_version_id OR NEW.subject_type <> OLD.subject_type
    OR NEW.subject_id <> OLD.subject_id OR NEW.plan_version <> OLD.plan_version
    OR NEW.quality <> OLD.quality OR NEW.valid_from <> OLD.valid_from OR NEW.valid_to <> OLD.valid_to
    OR NEW.expected_cost IS DISTINCT FROM OLD.expected_cost OR NEW.expected_saving IS DISTINCT FROM OLD.expected_saving
    OR NEW.objective_value IS DISTINCT FROM OLD.objective_value OR NEW.explanation <> OLD.explanation
  ) THEN
    RAISE EXCEPTION 'validated Dispatch Plan content is immutable; rolling optimization creates a new Plan Version' USING ERRCODE = '23514';
  END IF;

  IF OLD.status = 'DRAFT' AND NEW.status = 'VALIDATED' THEN
    SELECT count(*) INTO resource_count FROM core_registry.optimization_input_resources
    WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND input_snapshot_id = NEW.input_snapshot_id;
    SELECT count(*) INTO interval_count FROM core_registry.dispatch_intervals
    WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND dispatch_plan_id = NEW.id;
    IF resource_count < 1 OR interval_count <> resource_count * (run_horizon_minutes / 15) THEN
      RAISE EXCEPTION 'Dispatch Plan must contain the complete 15-minute horizon for every ESS Resource before VALIDATED' USING ERRCODE = '23514';
    END IF;
    SELECT count(*) INTO incomplete_resource_count
    FROM core_registry.optimization_input_resources AS resource
    WHERE resource.tenant_id = NEW.tenant_id
      AND resource.site_id = NEW.site_id
      AND resource.input_snapshot_id = NEW.input_snapshot_id
      AND (
        SELECT count(*)
        FROM core_registry.dispatch_intervals AS interval_row
        WHERE interval_row.tenant_id = NEW.tenant_id
          AND interval_row.site_id = NEW.site_id
          AND interval_row.dispatch_plan_id = NEW.id
          AND interval_row.resource_id = resource.resource_id
      ) <> run_horizon_minutes / 15;
    IF incomplete_resource_count > 0 THEN
      RAISE EXCEPTION 'Dispatch Plan has an incomplete ESS Resource interval grid' USING ERRCODE = '23514';
    END IF;
  END IF;
  IF OLD.status = 'DRAFT' AND NEW.status NOT IN ('DRAFT','VALIDATED','CANCELLED') THEN
    RAISE EXCEPTION 'DRAFT Dispatch Plan must validate before publication' USING ERRCODE = '23514';
  ELSIF OLD.status = 'VALIDATED' AND NEW.status NOT IN ('VALIDATED','SHADOW','APPROVED','CANCELLED') THEN
    RAISE EXCEPTION 'VALIDATED Dispatch Plan has invalid next status' USING ERRCODE = '23514';
  ELSIF OLD.status = 'SHADOW' AND NEW.status NOT IN ('SHADOW','SUPERSEDED','COMPLETED','CANCELLED') THEN
    RAISE EXCEPTION 'SHADOW Dispatch Plan cannot enter execution states' USING ERRCODE = '23514';
  ELSIF OLD.status = 'APPROVED' AND NEW.status NOT IN ('APPROVED','ACTIVE','SUPERSEDED','CANCELLED') THEN
    RAISE EXCEPTION 'APPROVED Dispatch Plan has invalid next status' USING ERRCODE = '23514';
  ELSIF OLD.status = 'ACTIVE' AND NEW.status NOT IN ('ACTIVE','SUPERSEDED','COMPLETED','CANCELLED') THEN
    RAISE EXCEPTION 'ACTIVE Dispatch Plan has invalid next status' USING ERRCODE = '23514';
  ELSIF OLD.status IN ('SUPERSEDED','COMPLETED','CANCELLED') AND NEW.status <> OLD.status THEN
    RAISE EXCEPTION 'terminal Dispatch Plan cannot change status' USING ERRCODE = '23514';
  END IF;
  IF dispatch_mode_value = 'SHADOW' AND NEW.status IN ('APPROVED','ACTIVE') THEN
    RAISE EXCEPTION 'SHADOW Optimization Policy cannot produce an executable Dispatch Plan' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$dispatch_plan_write$;

DROP TRIGGER IF EXISTS dispatch_plans_validate_write ON core_registry.dispatch_plans;
CREATE TRIGGER dispatch_plans_validate_write
BEFORE INSERT OR UPDATE ON core_registry.dispatch_plans
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_dispatch_plan_write();

CREATE OR REPLACE FUNCTION core_registry.validate_dispatch_interval_write()
RETURNS trigger
LANGUAGE plpgsql
AS $dispatch_interval_write$
DECLARE
  plan_status text;
  plan_valid_from timestamptz;
  plan_valid_to timestamptz;
  plan_input_snapshot_id uuid;
  resource_type_value text;
  charge_power_limit_kw_value double precision;
  discharge_power_limit_kw_value double precision;
  availability_value boolean;
  control_mode_value text;
BEGIN
  SELECT status, valid_from, valid_to, input_snapshot_id
    INTO plan_status, plan_valid_from, plan_valid_to, plan_input_snapshot_id
  FROM core_registry.dispatch_plans
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.dispatch_plan_id;
  IF plan_status <> 'DRAFT' THEN
    RAISE EXCEPTION 'Dispatch Intervals are immutable after Plan validation' USING ERRCODE = '23514';
  END IF;
  IF NEW.start_time < plan_valid_from OR NEW.end_time > plan_valid_to
    OR NEW.end_time <> NEW.start_time + interval '15 minutes' THEN
    RAISE EXCEPTION 'Dispatch Interval must be a 15-minute slice within Plan validity' USING ERRCODE = '23514';
  END IF;
  SELECT asset.asset_type, resource.charge_power_limit_kw, resource.discharge_power_limit_kw,
         resource.availability, resource.control_mode
    INTO resource_type_value, charge_power_limit_kw_value, discharge_power_limit_kw_value,
         availability_value, control_mode_value
  FROM core_registry.optimization_input_resources AS resource
  JOIN core_registry.assets AS asset
    ON asset.tenant_id = resource.tenant_id
   AND asset.site_id = resource.site_id
   AND asset.id = resource.resource_id
  WHERE resource.tenant_id = NEW.tenant_id
    AND resource.site_id = NEW.site_id
    AND resource.input_snapshot_id = plan_input_snapshot_id
    AND resource.resource_id = NEW.resource_id;
  IF resource_type_value IS NULL OR upper(resource_type_value) <> 'ESS' THEN
    RAISE EXCEPTION 'Dispatch Resource must exist as an ESS in the Plan Input Snapshot' USING ERRCODE = '23514';
  END IF;
  IF NEW.target_value > discharge_power_limit_kw_value OR NEW.target_value < -charge_power_limit_kw_value THEN
    RAISE EXCEPTION 'Dispatch setpoint exceeds snapshotted ESS charge/discharge power limit' USING ERRCODE = '23514';
  END IF;
  IF (NOT availability_value OR control_mode_value <> 'REMOTE') AND NEW.target_value <> 0 THEN
    RAISE EXCEPTION 'unavailable or non-REMOTE ESS can only receive a zero Dispatch setpoint' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$dispatch_interval_write$;

DROP TRIGGER IF EXISTS dispatch_intervals_validate_write ON core_registry.dispatch_intervals;
CREATE TRIGGER dispatch_intervals_validate_write
BEFORE INSERT OR UPDATE ON core_registry.dispatch_intervals
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_dispatch_interval_write();

CREATE OR REPLACE FUNCTION core_registry.reject_dispatch_interval_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $dispatch_interval_delete$
DECLARE
  plan_status text;
BEGIN
  SELECT status INTO plan_status FROM core_registry.dispatch_plans
  WHERE tenant_id = OLD.tenant_id AND site_id = OLD.site_id AND id = OLD.dispatch_plan_id;
  IF plan_status <> 'DRAFT' THEN
    RAISE EXCEPTION 'Dispatch Intervals are immutable after Plan validation' USING ERRCODE = '23514';
  END IF;
  RETURN OLD;
END
$dispatch_interval_delete$;

DROP TRIGGER IF EXISTS dispatch_intervals_reject_delete ON core_registry.dispatch_intervals;
CREATE TRIGGER dispatch_intervals_reject_delete
BEFORE DELETE ON core_registry.dispatch_intervals
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_dispatch_interval_delete();

CREATE INDEX IF NOT EXISTS optimization_input_snapshots_lookup_idx
  ON core_registry.optimization_input_snapshots (tenant_id, site_id, subject_id, captured_at, status);
CREATE INDEX IF NOT EXISTS optimization_runs_lookup_idx
  ON core_registry.optimization_runs (tenant_id, site_id, subject_id, created_at, status);
CREATE INDEX IF NOT EXISTS dispatch_plans_lookup_idx
  ON core_registry.dispatch_plans (tenant_id, site_id, subject_id, valid_from, valid_to, status);
CREATE INDEX IF NOT EXISTS dispatch_intervals_plan_idx
  ON core_registry.dispatch_intervals (tenant_id, site_id, dispatch_plan_id, resource_id, start_time);

ALTER TABLE core_registry.optimization_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_policy_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_policy_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_input_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_input_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_input_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_input_resources FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.optimization_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.dispatch_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.dispatch_plans FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.dispatch_intervals ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.dispatch_intervals FORCE ROW LEVEL SECURITY;

CREATE POLICY optimization_policies_runtime_scope ON core_registry.optimization_policies FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY optimization_policy_versions_runtime_scope ON core_registry.optimization_policy_versions FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY optimization_input_snapshots_runtime_scope ON core_registry.optimization_input_snapshots FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY optimization_input_resources_runtime_scope ON core_registry.optimization_input_resources FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY optimization_runs_runtime_scope ON core_registry.optimization_runs FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY dispatch_plans_runtime_scope ON core_registry.dispatch_plans FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY dispatch_intervals_runtime_scope ON core_registry.dispatch_intervals FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.optimization_policies, core_registry.optimization_policy_versions,
  core_registry.optimization_input_snapshots, core_registry.optimization_input_resources,
  core_registry.optimization_runs, core_registry.dispatch_plans, core_registry.dispatch_intervals FROM PUBLIC;
GRANT SELECT ON core_registry.optimization_policies, core_registry.optimization_policy_versions,
  core_registry.optimization_input_snapshots, core_registry.optimization_input_resources,
  core_registry.optimization_runs, core_registry.dispatch_plans, core_registry.dispatch_intervals TO s1_core_runtime;

COMMIT;
