BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- Metric identity is stable. Business calculation semantics live only in
-- effective-dated Metric Versions; Metric is never represented as a Point.
CREATE TABLE IF NOT EXISTS core_registry.metrics (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  metric_code text NOT NULL CHECK (metric_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  metric_name text NOT NULL CHECK (length(btrim(metric_name)) BETWEEN 1 AND 256),
  category text NOT NULL CHECK (length(btrim(category)) BETWEEN 1 AND 64),
  description text,
  status text NOT NULL CHECK (status IN ('ACTIVE','INACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, metric_code),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (description IS NULL OR length(btrim(description)) BETWEEN 1 AND 2048),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.metric_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  metric_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_id)),
  version bigint NOT NULL CHECK (version > 0),
  unit_code text,
  data_type text NOT NULL CHECK (data_type IN ('NUMBER','STRING','BOOLEAN','JSON')),
  subject_type text NOT NULL CHECK (subject_type IN ('TENANT','SITE','SPACE','ASSET','DEVICE','ENERGY_NODE','TAG_GROUP')),
  time_granularity text NOT NULL CHECK (time_granularity IN ('REALTIME','1MIN','5MIN','15MIN','HOUR','DAY','MONTH','QUARTER','YEAR')),
  aggregation text NOT NULL CHECK (length(btrim(aggregation)) BETWEEN 1 AND 64),
  calculation_method text NOT NULL CHECK (length(btrim(calculation_method)) BETWEEN 1 AND 128),
  formula text,
  quality_policy text NOT NULL CHECK (quality_policy IN ('STRICT','TOLERANT','ESTIMATION_ALLOWED')),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, metric_id, version),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, id, metric_id, version, subject_type, time_granularity),
  FOREIGN KEY (tenant_id, metric_id) REFERENCES core_registry.metrics(tenant_id, id),
  FOREIGN KEY (unit_code) REFERENCES core_registry.unit_registry(unit_code),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (formula IS NULL OR length(btrim(formula)) BETWEEN 1 AND 4096),
  CHECK (jsonb_typeof(metadata) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_released_metric_version()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status = 'RELEASED' AND EXISTS (
    SELECT 1
    FROM core_registry.metric_versions AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.metric_id = NEW.metric_id
      AND existing.id <> NEW.id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'released Metric Versions cannot overlap' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS metric_versions_reject_overlap ON core_registry.metric_versions;
CREATE TRIGGER metric_versions_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, metric_id, effective_from, effective_to, status
ON core_registry.metric_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_released_metric_version();

CREATE OR REPLACE FUNCTION core_registry.reject_released_metric_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.status IN ('RELEASED','SUPERSEDED') AND (
    NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
    OR NEW.metric_id IS DISTINCT FROM OLD.metric_id
    OR NEW.version IS DISTINCT FROM OLD.version
    OR NEW.unit_code IS DISTINCT FROM OLD.unit_code
    OR NEW.data_type IS DISTINCT FROM OLD.data_type
    OR NEW.subject_type IS DISTINCT FROM OLD.subject_type
    OR NEW.time_granularity IS DISTINCT FROM OLD.time_granularity
    OR NEW.aggregation IS DISTINCT FROM OLD.aggregation
    OR NEW.calculation_method IS DISTINCT FROM OLD.calculation_method
    OR NEW.formula IS DISTINCT FROM OLD.formula
    OR NEW.quality_policy IS DISTINCT FROM OLD.quality_policy
    OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
    OR NEW.effective_to IS DISTINCT FROM OLD.effective_to
    OR NEW.metadata IS DISTINCT FROM OLD.metadata
  ) THEN
    RAISE EXCEPTION 'released Metric Version is immutable; create a new Metric Version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS metric_versions_reject_released_mutation ON core_registry.metric_versions;
CREATE TRIGGER metric_versions_reject_released_mutation
BEFORE UPDATE ON core_registry.metric_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_released_metric_version_mutation();

CREATE TABLE IF NOT EXISTS core_registry.metric_dependencies (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  metric_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_version_id)),
  dependency_type text NOT NULL CHECK (dependency_type IN ('POINT','METRIC','EXTERNAL')),
  dependency_code text NOT NULL CHECK (dependency_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  dependency_metric_id uuid,
  sort_order integer NOT NULL CHECK (sort_order >= 0),
  required boolean NOT NULL DEFAULT true,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, metric_version_id, dependency_type, dependency_code),
  UNIQUE (tenant_id, metric_version_id, sort_order),
  FOREIGN KEY (tenant_id, metric_version_id) REFERENCES core_registry.metric_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, dependency_metric_id) REFERENCES core_registry.metrics(tenant_id, id),
  CHECK ((dependency_type = 'METRIC' AND dependency_metric_id IS NOT NULL)
    OR (dependency_type IN ('POINT','EXTERNAL') AND dependency_metric_id IS NULL)),
  CHECK (jsonb_typeof(metadata) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_metric_dependency_cycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  source_metric_id uuid;
BEGIN
  IF NEW.dependency_type <> 'METRIC' THEN
    RETURN NEW;
  END IF;
  SELECT metric_id INTO source_metric_id
  FROM core_registry.metric_versions
  WHERE tenant_id = NEW.tenant_id AND id = NEW.metric_version_id;
  IF source_metric_id = NEW.dependency_metric_id THEN
    RAISE EXCEPTION 'Metric cannot depend on itself' USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    WITH RECURSIVE reachable(metric_id) AS (
      SELECT NEW.dependency_metric_id
      UNION
      SELECT dependency.dependency_metric_id
      FROM core_registry.metric_dependencies AS dependency
      JOIN core_registry.metric_versions AS source_version
        ON source_version.tenant_id = dependency.tenant_id
       AND source_version.id = dependency.metric_version_id
      JOIN reachable AS parent ON source_version.metric_id = parent.metric_id
      WHERE dependency.tenant_id = NEW.tenant_id
        AND dependency.dependency_type = 'METRIC'
        AND dependency.dependency_metric_id IS NOT NULL
        AND source_version.status <> 'SUPERSEDED'
    )
    SELECT 1 FROM reachable WHERE metric_id = source_metric_id
  ) THEN
    RAISE EXCEPTION 'Metric dependency cycle is not allowed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS metric_dependencies_reject_cycle ON core_registry.metric_dependencies;
CREATE TRIGGER metric_dependencies_reject_cycle
BEFORE INSERT OR UPDATE OF tenant_id, metric_version_id, dependency_type, dependency_metric_id
ON core_registry.metric_dependencies
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_metric_dependency_cycle();

CREATE OR REPLACE FUNCTION core_registry.reject_released_metric_dependency_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  source_status text;
  source_version_id uuid;
BEGIN
  source_version_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.metric_version_id ELSE NEW.metric_version_id END;
  SELECT status INTO source_status
  FROM core_registry.metric_versions
  WHERE id = source_version_id;
  IF source_status IN ('RELEASED','SUPERSEDED') THEN
    RAISE EXCEPTION 'dependencies of a released Metric Version are immutable' USING ERRCODE = '23514';
  END IF;
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END
$$;

DROP TRIGGER IF EXISTS metric_dependencies_reject_released_mutation ON core_registry.metric_dependencies;
CREATE TRIGGER metric_dependencies_reject_released_mutation
BEFORE INSERT OR UPDATE OR DELETE ON core_registry.metric_dependencies
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_released_metric_dependency_mutation();

CREATE TABLE IF NOT EXISTS core_registry.metric_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  metric_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_version_id)),
  metric_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_id)),
  metric_version bigint NOT NULL CHECK (metric_version > 0),
  binding_version bigint NOT NULL CHECK (binding_version > 0),
  subject_type text NOT NULL CHECK (subject_type IN ('TENANT','SITE','SPACE','ASSET','DEVICE','ENERGY_NODE','TAG_GROUP')),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  time_granularity text NOT NULL CHECK (time_granularity IN ('REALTIME','1MIN','5MIN','15MIN','HOUR','DAY','MONTH','QUARTER','YEAR')),
  source_definition jsonb NOT NULL,
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, metric_version_id, subject_type, subject_id, binding_version, time_granularity),
  UNIQUE (tenant_id, site_id, metric_version_id, subject_type, subject_id, binding_version, time_granularity),
  FOREIGN KEY (tenant_id, metric_version_id, metric_id, metric_version, subject_type, time_granularity)
    REFERENCES core_registry.metric_versions(tenant_id, id, metric_id, version, subject_type, time_granularity),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (jsonb_typeof(source_definition) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_metric_binding_subject()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  CASE NEW.subject_type
    WHEN 'TENANT' THEN
      IF NEW.subject_id <> NEW.tenant_id THEN
        RAISE EXCEPTION 'TENANT Metric Binding subject must equal tenant_id' USING ERRCODE = '23514';
      END IF;
    WHEN 'SITE' THEN
      IF NEW.subject_id <> NEW.site_id THEN
        RAISE EXCEPTION 'SITE Metric Binding subject must equal site_id' USING ERRCODE = '23514';
      END IF;
    WHEN 'SPACE' THEN
      IF NOT EXISTS (SELECT 1 FROM core_registry.spaces WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.subject_id) THEN
        RAISE EXCEPTION 'SPACE Metric Binding subject does not exist in the Site' USING ERRCODE = '23514';
      END IF;
    WHEN 'ASSET' THEN
      IF NOT EXISTS (SELECT 1 FROM core_registry.assets WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.subject_id) THEN
        RAISE EXCEPTION 'ASSET Metric Binding subject does not exist in the Site' USING ERRCODE = '23514';
      END IF;
    WHEN 'DEVICE' THEN
      IF NOT EXISTS (SELECT 1 FROM core_registry.devices WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.subject_id) THEN
        RAISE EXCEPTION 'DEVICE Metric Binding subject does not exist in the Site' USING ERRCODE = '23514';
      END IF;
    WHEN 'ENERGY_NODE' THEN
      IF NOT EXISTS (SELECT 1 FROM core_registry.energy_nodes WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id AND id = NEW.subject_id) THEN
        RAISE EXCEPTION 'ENERGY_NODE Metric Binding subject does not exist in the Site' USING ERRCODE = '23514';
      END IF;
    WHEN 'TAG_GROUP' THEN
      RAISE EXCEPTION 'TAG_GROUP Metric Binding requires the Tag Group model before release' USING ERRCODE = '23514';
    ELSE
      RAISE EXCEPTION 'unsupported Metric Binding subject type' USING ERRCODE = '23514';
  END CASE;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS metric_bindings_validate_subject ON core_registry.metric_bindings;
CREATE TRIGGER metric_bindings_validate_subject
BEFORE INSERT OR UPDATE OF tenant_id, site_id, subject_type, subject_id
ON core_registry.metric_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_metric_binding_subject();

CREATE OR REPLACE FUNCTION core_registry.validate_metric_binding_release()
RETURNS trigger
LANGUAGE plpgsql
AS $metric_binding_release$
DECLARE
  version_status text;
BEGIN
  IF NEW.status = 'RELEASED' THEN
    SELECT status INTO version_status
    FROM core_registry.metric_versions
    WHERE tenant_id = NEW.tenant_id AND id = NEW.metric_version_id;
    IF version_status <> 'RELEASED' THEN
      RAISE EXCEPTION 'Metric Binding cannot release before its Metric Version' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$metric_binding_release$;

DROP TRIGGER IF EXISTS metric_bindings_validate_release ON core_registry.metric_bindings;
CREATE TRIGGER metric_bindings_validate_release
BEFORE INSERT OR UPDATE OF tenant_id, metric_version_id, status
ON core_registry.metric_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_metric_binding_release();

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_released_metric_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status = 'RELEASED' AND EXISTS (
    SELECT 1
    FROM core_registry.metric_bindings AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.metric_id = NEW.metric_id
      AND existing.subject_type = NEW.subject_type
      AND existing.subject_id = NEW.subject_id
      AND existing.id <> NEW.id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'released Metric Bindings cannot overlap for the same Metric and Subject' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS metric_bindings_reject_overlap ON core_registry.metric_bindings;
CREATE TRIGGER metric_bindings_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, metric_id, subject_type, subject_id, effective_from, effective_to, status
ON core_registry.metric_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_released_metric_binding();

CREATE OR REPLACE FUNCTION core_registry.reject_released_metric_binding_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.status IN ('RELEASED','SUPERSEDED') AND (
    NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
    OR NEW.site_id IS DISTINCT FROM OLD.site_id
    OR NEW.metric_version_id IS DISTINCT FROM OLD.metric_version_id
    OR NEW.metric_id IS DISTINCT FROM OLD.metric_id
    OR NEW.metric_version IS DISTINCT FROM OLD.metric_version
    OR NEW.binding_version IS DISTINCT FROM OLD.binding_version
    OR NEW.subject_type IS DISTINCT FROM OLD.subject_type
    OR NEW.subject_id IS DISTINCT FROM OLD.subject_id
    OR NEW.time_granularity IS DISTINCT FROM OLD.time_granularity
    OR NEW.source_definition IS DISTINCT FROM OLD.source_definition
    OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
    OR NEW.effective_to IS DISTINCT FROM OLD.effective_to
  ) THEN
    RAISE EXCEPTION 'released Metric Binding is immutable; create a new binding version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS metric_bindings_reject_released_mutation ON core_registry.metric_bindings;
CREATE TRIGGER metric_bindings_reject_released_mutation
BEFORE UPDATE ON core_registry.metric_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_released_metric_binding_mutation();

CREATE TABLE IF NOT EXISTS core_registry.metric_calculation_runs (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  metric_binding_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_binding_id)),
  metric_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_version_id)),
  subject_type text NOT NULL,
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  binding_version bigint NOT NULL CHECK (binding_version > 0),
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL,
  granularity text NOT NULL CHECK (granularity IN ('REALTIME','1MIN','5MIN','15MIN','HOUR','DAY','MONTH','QUARTER','YEAR')),
  run_reason text NOT NULL CHECK (run_reason IN ('SCHEDULED','LATE_DATA','BACKFILL','MANUAL','SETTLEMENT')),
  input_refs jsonb NOT NULL,
  status text NOT NULL CHECK (status IN ('PENDING','RUNNING','PERSISTING','PERSISTED','FAILED')),
  started_at timestamptz,
  completed_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, metric_binding_id, metric_version_id, subject_type, subject_id, binding_version, granularity)
    REFERENCES core_registry.metric_bindings(tenant_id, site_id, id, metric_version_id, subject_type, subject_id, binding_version, time_granularity),
  CHECK (period_end > period_start),
  CHECK (jsonb_typeof(input_refs) = 'array'),
  CHECK ((status = 'PENDING' AND started_at IS NULL AND completed_at IS NULL)
    OR (status IN ('RUNNING','PERSISTING') AND started_at IS NOT NULL AND completed_at IS NULL)
    OR (status IN ('PERSISTED','FAILED') AND started_at IS NOT NULL AND completed_at IS NOT NULL AND completed_at >= started_at)),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_metric_calculation_run_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.status = 'PENDING' AND NEW.status NOT IN ('PENDING','RUNNING','FAILED') THEN
      RAISE EXCEPTION 'Metric Calculation Run must start before publication' USING ERRCODE = '23514';
    ELSIF OLD.status = 'RUNNING' AND NEW.status NOT IN ('RUNNING','PERSISTING','FAILED') THEN
      RAISE EXCEPTION 'RUNNING Metric Calculation Run can only begin publication or fail' USING ERRCODE = '23514';
    ELSIF OLD.status = 'PERSISTING' AND NEW.status NOT IN ('PERSISTING','PERSISTED','FAILED') THEN
      RAISE EXCEPTION 'PERSISTING Metric Calculation Run can only persist or fail' USING ERRCODE = '23514';
    ELSIF OLD.status IN ('PERSISTED','FAILED') AND NEW.status <> OLD.status THEN
      RAISE EXCEPTION 'completed Metric Calculation Run is terminal' USING ERRCODE = '23514';
    END IF;
    IF NEW.metric_binding_id <> OLD.metric_binding_id
      OR NEW.metric_version_id <> OLD.metric_version_id
      OR NEW.subject_type <> OLD.subject_type
      OR NEW.subject_id <> OLD.subject_id
      OR NEW.binding_version <> OLD.binding_version
      OR NEW.period_start <> OLD.period_start
      OR NEW.period_end <> OLD.period_end
      OR NEW.granularity <> OLD.granularity
      OR NEW.run_reason <> OLD.run_reason
      OR NEW.input_refs <> OLD.input_refs THEN
      RAISE EXCEPTION 'Metric Calculation Run input identity is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS metric_calculation_runs_validate_transition ON core_registry.metric_calculation_runs;
CREATE TRIGGER metric_calculation_runs_validate_transition
BEFORE UPDATE ON core_registry.metric_calculation_runs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_metric_calculation_run_transition();

CREATE INDEX IF NOT EXISTS metric_versions_effective_idx
  ON core_registry.metric_versions (tenant_id, metric_id, effective_from, effective_to, status);
CREATE INDEX IF NOT EXISTS metric_dependencies_version_idx
  ON core_registry.metric_dependencies (tenant_id, metric_version_id, sort_order);
CREATE INDEX IF NOT EXISTS metric_bindings_subject_idx
  ON core_registry.metric_bindings (tenant_id, site_id, metric_id, subject_type, subject_id, effective_from, effective_to);
CREATE INDEX IF NOT EXISTS metric_calculation_runs_period_idx
  ON core_registry.metric_calculation_runs (tenant_id, site_id, metric_binding_id, period_start, period_end, status);
CREATE UNIQUE INDEX IF NOT EXISTS metric_calculation_runs_scheduled_active_idx
  ON core_registry.metric_calculation_runs (tenant_id, site_id, metric_binding_id, period_start, period_end)
  WHERE run_reason = 'SCHEDULED' AND status <> 'FAILED';

ALTER TABLE core_registry.metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metrics FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_dependencies ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_dependencies FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_calculation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_calculation_runs FORCE ROW LEVEL SECURITY;

CREATE POLICY metrics_runtime_scope ON core_registry.metrics
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_versions_runtime_scope ON core_registry.metric_versions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_dependencies_runtime_scope ON core_registry.metric_dependencies
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_bindings_runtime_scope ON core_registry.metric_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_calculation_runs_runtime_scope ON core_registry.metric_calculation_runs
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY sites_metric_engine_scope ON core_registry.sites
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(id));
CREATE POLICY metrics_metric_engine_scope ON core_registry.metrics
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_versions_metric_engine_scope ON core_registry.metric_versions
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_dependencies_metric_engine_scope ON core_registry.metric_dependencies
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY metric_bindings_metric_engine_scope ON core_registry.metric_bindings
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY metric_calculation_runs_metric_engine_scope ON core_registry.metric_calculation_runs
  FOR ALL TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.metrics, core_registry.metric_versions,
  core_registry.metric_dependencies, core_registry.metric_bindings,
  core_registry.metric_calculation_runs FROM PUBLIC;
GRANT SELECT ON core_registry.metrics, core_registry.metric_versions,
  core_registry.metric_dependencies, core_registry.metric_bindings,
  core_registry.metric_calculation_runs TO s1_core_runtime;
GRANT SELECT ON core_registry.sites, core_registry.metrics, core_registry.metric_versions,
  core_registry.metric_dependencies, core_registry.metric_bindings TO metric_engine_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.metric_calculation_runs TO metric_engine_runtime;

COMMIT;
