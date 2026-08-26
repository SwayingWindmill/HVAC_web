BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- Settlement Boundary is the formal business accounting boundary. It is bound
-- to exactly one Topology Version and is defined by one Node or a set of Edges.
CREATE TABLE IF NOT EXISTS core_registry.settlement_boundaries (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  boundary_code text NOT NULL CHECK (boundary_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 256),
  boundary_type text NOT NULL CHECK (boundary_type IN ('GRID_CONNECTION','SITE','TENANT','BUILDING','SPACE','ASSET','ENERGY_SYSTEM','CUSTOM')),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  direction text NOT NULL,
  definition_mode text NOT NULL CHECK (definition_mode IN ('NODE','EDGE_SET')),
  node_id uuid,
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','VALIDATING','RELEASED','ACTIVE','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, topology_version_id, boundary_code),
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, topology_version_id, energy_type_id, direction),
  FOREIGN KEY (tenant_id, site_id, topology_version_id)
    REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, topology_version_id, node_id)
    REFERENCES core_registry.energy_nodes(tenant_id, site_id, topology_version_id, id),
  FOREIGN KEY (energy_type_id) REFERENCES core_registry.energy_types(id),
  FOREIGN KEY (direction) REFERENCES core_registry.energy_directions(direction_code),
  CHECK (
    (definition_mode = 'NODE' AND node_id IS NOT NULL)
    OR (definition_mode = 'EDGE_SET' AND node_id IS NULL)
  ),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.settlement_boundary_edges (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  boundary_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(boundary_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  direction text NOT NULL,
  energy_edge_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_edge_id)),
  ordinal integer NOT NULL CHECK (ordinal >= 0),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, boundary_id, energy_edge_id),
  UNIQUE (tenant_id, site_id, boundary_id, ordinal),
  FOREIGN KEY (tenant_id, site_id, boundary_id, topology_version_id, energy_type_id, direction)
    REFERENCES core_registry.settlement_boundaries(tenant_id, site_id, id, topology_version_id, energy_type_id, direction),
  FOREIGN KEY (tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id, direction)
    REFERENCES core_registry.energy_edges(tenant_id, site_id, topology_version_id, id, energy_type_id, direction),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_settlement_boundary_edge()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  mode text;
  boundary_status text;
BEGIN
  SELECT definition_mode, status INTO mode, boundary_status
  FROM core_registry.settlement_boundaries
  WHERE tenant_id = NEW.tenant_id
    AND site_id = NEW.site_id
    AND id = NEW.boundary_id;
  IF mode <> 'EDGE_SET' THEN
    RAISE EXCEPTION 'Settlement Boundary Edge is only valid for EDGE_SET boundaries' USING ERRCODE = '23514';
  END IF;
  IF boundary_status IN ('RELEASED','ACTIVE','SUPERSEDED') THEN
    RAISE EXCEPTION 'released Settlement Boundary definition is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settlement_boundary_edges_validate ON core_registry.settlement_boundary_edges;
CREATE TRIGGER settlement_boundary_edges_validate
BEFORE INSERT OR UPDATE ON core_registry.settlement_boundary_edges
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_settlement_boundary_edge();

CREATE OR REPLACE FUNCTION core_registry.validate_settlement_boundary_release()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  topology_status text;
BEGIN
  IF NEW.status IN ('RELEASED','ACTIVE','SUPERSEDED') AND OLD.status IS DISTINCT FROM NEW.status THEN
    SELECT status INTO topology_status
    FROM core_registry.energy_topology_versions
    WHERE tenant_id = NEW.tenant_id
      AND site_id = NEW.site_id
      AND id = NEW.topology_version_id;
    IF topology_status NOT IN ('RELEASED','ACTIVE','SUPERSEDED') THEN
      RAISE EXCEPTION 'Settlement Boundary cannot release before its Topology Version' USING ERRCODE = '23514';
    END IF;
    IF NEW.definition_mode = 'EDGE_SET' AND NOT EXISTS (
      SELECT 1 FROM core_registry.settlement_boundary_edges AS edge
      WHERE edge.tenant_id = NEW.tenant_id
        AND edge.site_id = NEW.site_id
        AND edge.boundary_id = NEW.id
    ) THEN
      RAISE EXCEPTION 'EDGE_SET Settlement Boundary must contain at least one Energy Edge before release' USING ERRCODE = '23514';
    END IF;
  END IF;
  IF OLD.status IN ('RELEASED','ACTIVE','SUPERSEDED') AND (
    NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
    OR NEW.site_id IS DISTINCT FROM OLD.site_id
    OR NEW.topology_version_id IS DISTINCT FROM OLD.topology_version_id
    OR NEW.boundary_code IS DISTINCT FROM OLD.boundary_code
    OR NEW.boundary_type IS DISTINCT FROM OLD.boundary_type
    OR NEW.energy_type_id IS DISTINCT FROM OLD.energy_type_id
    OR NEW.direction IS DISTINCT FROM OLD.direction
    OR NEW.definition_mode IS DISTINCT FROM OLD.definition_mode
    OR NEW.node_id IS DISTINCT FROM OLD.node_id
    OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
    OR NEW.effective_to IS DISTINCT FROM OLD.effective_to
  ) THEN
    RAISE EXCEPTION 'released Settlement Boundary definition is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settlement_boundaries_validate_release ON core_registry.settlement_boundaries;
CREATE TRIGGER settlement_boundaries_validate_release
BEFORE UPDATE ON core_registry.settlement_boundaries
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_settlement_boundary_release();

-- Tariff identity is stable; Tariff Version carries the effective time, Site
-- timezone snapshot, currency and billing-cycle policy used for historical billing.
CREATE TABLE IF NOT EXISTS core_registry.tariffs (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  tariff_code text NOT NULL CHECK (tariff_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 256),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  status text NOT NULL CHECK (status IN ('ACTIVE','INACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, tariff_code),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (energy_type_id) REFERENCES core_registry.energy_types(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.tariff_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  tariff_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tariff_id)),
  version bigint NOT NULL CHECK (version > 0),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  timezone text NOT NULL,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  billing_cycle text NOT NULL CHECK (billing_cycle IN ('CALENDAR_MONTH','CUSTOM_CYCLE')),
  custom_cycle_spec jsonb,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, tariff_id, version),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, tariff_id) REFERENCES core_registry.tariffs(tenant_id, site_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK ((billing_cycle = 'CALENDAR_MONTH' AND custom_cycle_spec IS NULL)
    OR (billing_cycle = 'CUSTOM_CYCLE' AND custom_cycle_spec IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

DROP TRIGGER IF EXISTS tariff_versions_iana_timezone ON core_registry.tariff_versions;
CREATE TRIGGER tariff_versions_iana_timezone
BEFORE INSERT OR UPDATE OF timezone ON core_registry.tariff_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.enforce_iana_timezone();

CREATE OR REPLACE FUNCTION core_registry.validate_tariff_version_site_timezone()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  site_timezone text;
BEGIN
  SELECT timezone INTO site_timezone
  FROM core_registry.sites
  WHERE tenant_id = NEW.tenant_id AND id = NEW.site_id;
  IF site_timezone IS NULL OR NEW.timezone <> site_timezone THEN
    RAISE EXCEPTION 'Tariff Version timezone must snapshot the Site timezone' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS tariff_versions_site_timezone ON core_registry.tariff_versions;
CREATE TRIGGER tariff_versions_site_timezone
BEFORE INSERT OR UPDATE OF tenant_id, site_id, timezone ON core_registry.tariff_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_tariff_version_site_timezone();

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_released_tariff_version()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status = 'RELEASED' AND EXISTS (
    SELECT 1
    FROM core_registry.tariff_versions AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.tariff_id = NEW.tariff_id
      AND existing.id <> NEW.id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'released Tariff Versions cannot overlap' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS tariff_versions_reject_overlap ON core_registry.tariff_versions;
CREATE TRIGGER tariff_versions_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, tariff_id, effective_from, effective_to, status
ON core_registry.tariff_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_released_tariff_version();

CREATE TABLE IF NOT EXISTS core_registry.tariff_periods (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  tariff_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tariff_version_id)),
  period_code text NOT NULL CHECK (period_code IN ('SUPER_PEAK','PEAK','FLAT','VALLEY')),
  day_type text NOT NULL CHECK (day_type IN ('WEEKDAY','WEEKEND','HOLIDAY','SPECIAL_DAY')),
  local_start_minute integer NOT NULL CHECK (local_start_minute BETWEEN 0 AND 1439),
  local_end_minute integer NOT NULL CHECK (local_end_minute BETWEEN 1 AND 1440),
  pricing_rule jsonb NOT NULL,
  ordinal integer NOT NULL CHECK (ordinal >= 0),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, tariff_version_id, day_type, ordinal),
  FOREIGN KEY (tenant_id, site_id, tariff_version_id)
    REFERENCES core_registry.tariff_versions(tenant_id, site_id, id),
  CHECK (local_end_minute > local_start_minute),
  CHECK (jsonb_typeof(pricing_rule) = 'object'),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_tariff_period()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM core_registry.tariff_periods AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.tariff_version_id = NEW.tariff_version_id
      AND existing.day_type = NEW.day_type
      AND existing.id <> NEW.id
      AND int4range(existing.local_start_minute, existing.local_end_minute, '[)')
          && int4range(NEW.local_start_minute, NEW.local_end_minute, '[)')
  ) THEN
    RAISE EXCEPTION 'Tariff Period time slices cannot overlap within the same day type' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS tariff_periods_reject_overlap ON core_registry.tariff_periods;
CREATE TRIGGER tariff_periods_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, tariff_version_id, day_type, local_start_minute, local_end_minute
ON core_registry.tariff_periods
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_tariff_period();

CREATE TABLE IF NOT EXISTS core_registry.tariff_assignments (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  boundary_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(boundary_id)),
  tariff_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tariff_id)),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, boundary_id) REFERENCES core_registry.settlement_boundaries(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, tariff_id) REFERENCES core_registry.tariffs(tenant_id, site_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_released_tariff_assignment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status = 'RELEASED' AND EXISTS (
    SELECT 1
    FROM core_registry.tariff_assignments AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.boundary_id = NEW.boundary_id
      AND existing.id <> NEW.id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'released Tariff Assignments cannot overlap for a Settlement Boundary' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS tariff_assignments_reject_overlap ON core_registry.tariff_assignments;
CREATE TRIGGER tariff_assignments_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, boundary_id, effective_from, effective_to, status
ON core_registry.tariff_assignments
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_released_tariff_assignment();

-- Settlement consumes released Metric results. It does not recalculate standard
-- energy/demand formulas from raw Telemetry or energy_interval_facts.
CREATE TABLE IF NOT EXISTS core_registry.settlement_metric_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  boundary_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(boundary_id)),
  metric_binding_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(metric_binding_id)),
  metric_role text NOT NULL CHECK (metric_role IN ('ENERGY','DEMAND')),
  tariff_period_code text,
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, boundary_id, metric_binding_id, effective_from),
  FOREIGN KEY (tenant_id, site_id, boundary_id) REFERENCES core_registry.settlement_boundaries(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, metric_binding_id) REFERENCES core_registry.metric_bindings(tenant_id, site_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK ((metric_role = 'DEMAND') OR tariff_period_code IS NULL OR length(btrim(tariff_period_code)) > 0),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.settlement_periods (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  boundary_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(boundary_id)),
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL,
  timezone text NOT NULL,
  grace_period_seconds integer NOT NULL DEFAULT 0 CHECK (grace_period_seconds >= 0),
  status text NOT NULL CHECK (status IN ('OPEN','CALCULATING','REVIEW','LOCKED','REVISED','CANCELLED')),
  locked_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, boundary_id, period_start, period_end),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, boundary_id) REFERENCES core_registry.settlement_boundaries(tenant_id, site_id, id),
  CHECK (period_end > period_start),
  CHECK (status NOT IN ('LOCKED','REVISED') OR locked_at IS NOT NULL),
  CHECK (updated_at >= created_at)
);

DROP TRIGGER IF EXISTS settlement_periods_iana_timezone ON core_registry.settlement_periods;
CREATE TRIGGER settlement_periods_iana_timezone
BEFORE INSERT OR UPDATE OF timezone ON core_registry.settlement_periods
FOR EACH ROW EXECUTE FUNCTION core_registry.enforce_iana_timezone();

CREATE OR REPLACE FUNCTION core_registry.validate_settlement_period_timezone()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  site_timezone text;
BEGIN
  SELECT timezone INTO site_timezone
  FROM core_registry.sites
  WHERE tenant_id = NEW.tenant_id AND id = NEW.site_id;
  IF site_timezone IS NULL OR NEW.timezone <> site_timezone THEN
    RAISE EXCEPTION 'Settlement Period timezone must snapshot the Site timezone' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settlement_periods_site_timezone ON core_registry.settlement_periods;
CREATE TRIGGER settlement_periods_site_timezone
BEFORE INSERT OR UPDATE OF tenant_id, site_id, timezone ON core_registry.settlement_periods
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_settlement_period_timezone();

CREATE TABLE IF NOT EXISTS core_registry.settlement_snapshots (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  settlement_period_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(settlement_period_id)),
  boundary_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(boundary_id)),
  revision_no integer NOT NULL CHECK (revision_no >= 0),
  previous_snapshot_id uuid,
  settlement_revision_id uuid,
  meter_binding_refs jsonb NOT NULL,
  metric_version_refs jsonb NOT NULL,
  tariff_version_refs jsonb NOT NULL,
  source_reading_refs jsonb NOT NULL,
  energy_breakdown jsonb NOT NULL,
  demand jsonb NOT NULL,
  cost jsonb NOT NULL,
  quality text NOT NULL CHECK (quality IN ('GOOD','PARTIAL','ESTIMATED','MANUAL','STALE','INVALID')),
  completeness numeric(9,8) NOT NULL CHECK (completeness >= 0 AND completeness <= 1),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, settlement_period_id, revision_no),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, settlement_period_id) REFERENCES core_registry.settlement_periods(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, boundary_id) REFERENCES core_registry.settlement_boundaries(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, previous_snapshot_id) REFERENCES core_registry.settlement_snapshots(tenant_id, site_id, id),
  CHECK ((revision_no = 0 AND previous_snapshot_id IS NULL AND settlement_revision_id IS NULL)
    OR (revision_no > 0 AND previous_snapshot_id IS NOT NULL AND settlement_revision_id IS NOT NULL)),
  CHECK (jsonb_typeof(meter_binding_refs) = 'array'),
  CHECK (jsonb_typeof(metric_version_refs) = 'array'),
  CHECK (jsonb_typeof(tariff_version_refs) = 'array'),
  CHECK (jsonb_typeof(source_reading_refs) = 'array'),
  CHECK (jsonb_typeof(energy_breakdown) = 'object'),
  CHECK (jsonb_typeof(demand) = 'object'),
  CHECK (jsonb_typeof(cost) = 'object')
);

CREATE TABLE IF NOT EXISTS core_registry.settlement_change_candidates (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  settlement_period_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(settlement_period_id)),
  reason_code text NOT NULL CHECK (reason_code IN ('LATE_DATA','METER_DATA','CT_PT','TIMEZONE','TARIFF','DEMAND_RULE','MISSING_DATA','METER_REPLACEMENT','SOURCE_BINDING','METRIC_REVISION','MANUAL_ENTRY','SYSTEM_BUG','UTILITY_BILL','UNKNOWN')),
  impact_summary jsonb NOT NULL,
  evidence jsonb NOT NULL,
  status text NOT NULL CHECK (status IN ('OPEN','DISMISSED','APPROVED','APPLIED')),
  detected_at timestamptz NOT NULL,
  resolved_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, settlement_period_id) REFERENCES core_registry.settlement_periods(tenant_id, site_id, id),
  CHECK (jsonb_typeof(impact_summary) = 'object'),
  CHECK (jsonb_typeof(evidence) = 'object'),
  CHECK (resolved_at IS NULL OR resolved_at >= detected_at),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.settlement_revisions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  settlement_period_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(settlement_period_id)),
  revision_no integer NOT NULL CHECK (revision_no > 0),
  change_candidate_id uuid,
  base_snapshot_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(base_snapshot_id)),
  revised_snapshot_id uuid,
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1024),
  status text NOT NULL CHECK (status IN ('DRAFT','APPROVED','APPLIED')),
  approved_at timestamptz,
  applied_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, settlement_period_id, revision_no),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, settlement_period_id) REFERENCES core_registry.settlement_periods(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, change_candidate_id) REFERENCES core_registry.settlement_change_candidates(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, base_snapshot_id) REFERENCES core_registry.settlement_snapshots(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, revised_snapshot_id) REFERENCES core_registry.settlement_snapshots(tenant_id, site_id, id),
  CHECK ((status = 'DRAFT' AND approved_at IS NULL AND applied_at IS NULL AND revised_snapshot_id IS NULL)
    OR (status = 'APPROVED' AND approved_at IS NOT NULL AND applied_at IS NULL AND revised_snapshot_id IS NULL)
    OR (status = 'APPLIED' AND approved_at IS NOT NULL AND applied_at IS NOT NULL AND revised_snapshot_id IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

ALTER TABLE core_registry.settlement_snapshots
  DROP CONSTRAINT IF EXISTS settlement_snapshots_revision_fk;
ALTER TABLE core_registry.settlement_snapshots
  ADD CONSTRAINT settlement_snapshots_revision_fk
  FOREIGN KEY (tenant_id, site_id, settlement_revision_id)
  REFERENCES core_registry.settlement_revisions(tenant_id, site_id, id);

CREATE OR REPLACE FUNCTION core_registry.validate_settlement_snapshot_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  period_status text;
  period_boundary uuid;
  latest_snapshot_id uuid;
  latest_revision_no integer;
  revision_status text;
  revision_period uuid;
  revision_base_snapshot uuid;
  revision_number integer;
BEGIN
  SELECT status, boundary_id INTO period_status, period_boundary
  FROM core_registry.settlement_periods
  WHERE tenant_id = NEW.tenant_id
    AND site_id = NEW.site_id
    AND id = NEW.settlement_period_id;
  IF period_boundary IS NULL OR NEW.boundary_id <> period_boundary THEN
    RAISE EXCEPTION 'Settlement Snapshot boundary must match its Settlement Period' USING ERRCODE = '23514';
  END IF;

  SELECT id, revision_no INTO latest_snapshot_id, latest_revision_no
  FROM core_registry.settlement_snapshots
  WHERE tenant_id = NEW.tenant_id
    AND site_id = NEW.site_id
    AND settlement_period_id = NEW.settlement_period_id
  ORDER BY revision_no DESC
  LIMIT 1;

  IF NEW.revision_no = 0 THEN
    IF period_status NOT IN ('CALCULATING','REVIEW') OR latest_snapshot_id IS NOT NULL THEN
      RAISE EXCEPTION 'initial Settlement Snapshot is created once during CALCULATING or REVIEW' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;

  IF period_status NOT IN ('LOCKED','REVISED') THEN
    RAISE EXCEPTION 'Settlement Revision Snapshot requires a LOCKED or REVISED period' USING ERRCODE = '23514';
  END IF;
  IF latest_snapshot_id IS NULL OR NEW.revision_no <> latest_revision_no + 1 OR NEW.previous_snapshot_id <> latest_snapshot_id THEN
    RAISE EXCEPTION 'Settlement Revision Snapshot must append exactly to the latest immutable Snapshot' USING ERRCODE = '23514';
  END IF;

  SELECT status, settlement_period_id, base_snapshot_id, revision_no
    INTO revision_status, revision_period, revision_base_snapshot, revision_number
  FROM core_registry.settlement_revisions
  WHERE tenant_id = NEW.tenant_id
    AND site_id = NEW.site_id
    AND id = NEW.settlement_revision_id;
  IF revision_status <> 'APPROVED'
    OR revision_period <> NEW.settlement_period_id
    OR revision_base_snapshot <> NEW.previous_snapshot_id
    OR revision_number <> NEW.revision_no THEN
    RAISE EXCEPTION 'Settlement Revision Snapshot requires the matching APPROVED Settlement Revision' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settlement_snapshots_validate_insert ON core_registry.settlement_snapshots;
CREATE TRIGGER settlement_snapshots_validate_insert
BEFORE INSERT ON core_registry.settlement_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_settlement_snapshot_insert();

CREATE OR REPLACE FUNCTION core_registry.reject_settlement_snapshot_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'Settlement Snapshot is immutable; create a Settlement Revision' USING ERRCODE = '23514';
END
$$;

DROP TRIGGER IF EXISTS settlement_snapshots_reject_update_delete ON core_registry.settlement_snapshots;
CREATE TRIGGER settlement_snapshots_reject_update_delete
BEFORE UPDATE OR DELETE ON core_registry.settlement_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_settlement_snapshot_mutation();

CREATE OR REPLACE FUNCTION core_registry.validate_settlement_revision_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  period_status text;
  latest_snapshot_id uuid;
  latest_revision_no integer;
BEGIN
  IF TG_OP = 'INSERT' THEN
    SELECT status INTO period_status
    FROM core_registry.settlement_periods
    WHERE tenant_id = NEW.tenant_id
      AND site_id = NEW.site_id
      AND id = NEW.settlement_period_id;
    IF period_status NOT IN ('LOCKED','REVISED') THEN
      RAISE EXCEPTION 'Settlement Revision can only be created after Settlement Lock' USING ERRCODE = '23514';
    END IF;
    SELECT id, revision_no INTO latest_snapshot_id, latest_revision_no
    FROM core_registry.settlement_snapshots
    WHERE tenant_id = NEW.tenant_id
      AND site_id = NEW.site_id
      AND settlement_period_id = NEW.settlement_period_id
    ORDER BY revision_no DESC
    LIMIT 1;
    IF latest_snapshot_id IS NULL OR NEW.base_snapshot_id <> latest_snapshot_id OR NEW.revision_no <> latest_revision_no + 1 THEN
      RAISE EXCEPTION 'Settlement Revision must target the latest immutable Snapshot and next revision number' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;

  IF OLD.status = 'APPLIED' THEN
    RAISE EXCEPTION 'applied Settlement Revision is immutable' USING ERRCODE = '23514';
  END IF;
  IF OLD.status = 'DRAFT' AND NEW.status NOT IN ('DRAFT','APPROVED') THEN
    RAISE EXCEPTION 'Settlement Revision must be APPROVED before it can be applied' USING ERRCODE = '23514';
  END IF;
  IF OLD.status = 'APPROVED' AND NEW.status NOT IN ('APPROVED','APPLIED') THEN
    RAISE EXCEPTION 'APPROVED Settlement Revision can only remain APPROVED or become APPLIED' USING ERRCODE = '23514';
  END IF;
  IF NEW.settlement_period_id <> OLD.settlement_period_id
    OR NEW.revision_no <> OLD.revision_no
    OR NEW.base_snapshot_id <> OLD.base_snapshot_id THEN
    RAISE EXCEPTION 'Settlement Revision identity and base Snapshot are immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settlement_revisions_validate_write ON core_registry.settlement_revisions;
CREATE TRIGGER settlement_revisions_validate_write
BEFORE INSERT OR UPDATE ON core_registry.settlement_revisions
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_settlement_revision_write();

CREATE OR REPLACE FUNCTION core_registry.apply_settlement_revision_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.revision_no > 0 THEN
    UPDATE core_registry.settlement_revisions
    SET revised_snapshot_id = NEW.id,
        status = 'APPLIED',
        applied_at = NEW.created_at,
        revision = revision + 1,
        updated_at = NEW.created_at
    WHERE tenant_id = NEW.tenant_id
      AND site_id = NEW.site_id
      AND id = NEW.settlement_revision_id
      AND status = 'APPROVED';
    IF NOT FOUND THEN
      RAISE EXCEPTION 'Settlement Revision was not APPROVED at Snapshot apply time' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settlement_snapshots_apply_revision ON core_registry.settlement_snapshots;
CREATE TRIGGER settlement_snapshots_apply_revision
AFTER INSERT ON core_registry.settlement_snapshots
FOR EACH ROW EXECUTE FUNCTION core_registry.apply_settlement_revision_snapshot();

CREATE OR REPLACE FUNCTION core_registry.validate_settlement_period_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  initial_snapshot_count bigint;
  revision_snapshot_count bigint;
BEGIN
  IF OLD.status IN ('LOCKED','REVISED') AND (
    NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
    OR NEW.site_id IS DISTINCT FROM OLD.site_id
    OR NEW.boundary_id IS DISTINCT FROM OLD.boundary_id
    OR NEW.period_start IS DISTINCT FROM OLD.period_start
    OR NEW.period_end IS DISTINCT FROM OLD.period_end
    OR NEW.timezone IS DISTINCT FROM OLD.timezone
    OR NEW.grace_period_seconds IS DISTINCT FROM OLD.grace_period_seconds
    OR NEW.locked_at IS DISTINCT FROM OLD.locked_at
  ) THEN
    RAISE EXCEPTION 'LOCKED Settlement Period definition is immutable' USING ERRCODE = '23514';
  END IF;

  IF OLD.status = 'OPEN' AND NEW.status NOT IN ('OPEN','CALCULATING','CANCELLED') THEN
    RAISE EXCEPTION 'invalid Settlement Period transition from OPEN' USING ERRCODE = '23514';
  ELSIF OLD.status = 'CALCULATING' AND NEW.status NOT IN ('CALCULATING','REVIEW','LOCKED','CANCELLED') THEN
    RAISE EXCEPTION 'invalid Settlement Period transition from CALCULATING' USING ERRCODE = '23514';
  ELSIF OLD.status = 'REVIEW' AND NEW.status NOT IN ('REVIEW','CALCULATING','LOCKED','CANCELLED') THEN
    RAISE EXCEPTION 'invalid Settlement Period transition from REVIEW' USING ERRCODE = '23514';
  ELSIF OLD.status = 'LOCKED' AND NEW.status NOT IN ('LOCKED','REVISED') THEN
    RAISE EXCEPTION 'LOCKED Settlement Period cannot be reopened; use Settlement Revision' USING ERRCODE = '23514';
  ELSIF OLD.status = 'REVISED' AND NEW.status <> 'REVISED' THEN
    RAISE EXCEPTION 'REVISED Settlement Period cannot be reopened' USING ERRCODE = '23514';
  ELSIF OLD.status = 'CANCELLED' AND NEW.status <> 'CANCELLED' THEN
    RAISE EXCEPTION 'CANCELLED Settlement Period is terminal' USING ERRCODE = '23514';
  END IF;

  IF NEW.status = 'LOCKED' AND OLD.status <> 'LOCKED' THEN
    SELECT count(*) INTO initial_snapshot_count
    FROM core_registry.settlement_snapshots
    WHERE tenant_id = NEW.tenant_id
      AND site_id = NEW.site_id
      AND settlement_period_id = NEW.id
      AND revision_no = 0;
    IF initial_snapshot_count <> 1 OR NEW.locked_at IS NULL THEN
      RAISE EXCEPTION 'Settlement Period requires exactly one initial Snapshot before LOCKED' USING ERRCODE = '23514';
    END IF;
  END IF;

  IF NEW.status = 'REVISED' AND OLD.status <> 'REVISED' THEN
    SELECT count(*) INTO revision_snapshot_count
    FROM core_registry.settlement_snapshots
    WHERE tenant_id = NEW.tenant_id
      AND site_id = NEW.site_id
      AND settlement_period_id = NEW.id
      AND revision_no > 0;
    IF revision_snapshot_count < 1 THEN
      RAISE EXCEPTION 'Settlement Period cannot become REVISED before a revision Snapshot is applied' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settlement_periods_validate_transition ON core_registry.settlement_periods;
CREATE TRIGGER settlement_periods_validate_transition
BEFORE UPDATE ON core_registry.settlement_periods
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_settlement_period_transition();

CREATE OR REPLACE FUNCTION core_registry.reject_locked_settlement_period_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.status IN ('LOCKED','REVISED') THEN
    RAISE EXCEPTION 'LOCKED or REVISED Settlement Period cannot be deleted' USING ERRCODE = '23514';
  END IF;
  RETURN OLD;
END
$$;

DROP TRIGGER IF EXISTS settlement_periods_reject_locked_delete ON core_registry.settlement_periods;
CREATE TRIGGER settlement_periods_reject_locked_delete
BEFORE DELETE ON core_registry.settlement_periods
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_locked_settlement_period_delete();

CREATE INDEX IF NOT EXISTS settlement_boundaries_topology_idx
  ON core_registry.settlement_boundaries (tenant_id, site_id, topology_version_id, energy_type_id, direction, boundary_code);
CREATE INDEX IF NOT EXISTS tariff_versions_effective_idx
  ON core_registry.tariff_versions (tenant_id, site_id, tariff_id, effective_from, effective_to);
CREATE INDEX IF NOT EXISTS tariff_assignments_boundary_effective_idx
  ON core_registry.tariff_assignments (tenant_id, site_id, boundary_id, effective_from, effective_to);
CREATE INDEX IF NOT EXISTS settlement_metric_bindings_boundary_idx
  ON core_registry.settlement_metric_bindings (tenant_id, site_id, boundary_id, metric_role, effective_from, effective_to);
CREATE INDEX IF NOT EXISTS settlement_periods_boundary_period_idx
  ON core_registry.settlement_periods (tenant_id, site_id, boundary_id, period_start, period_end, status);
CREATE INDEX IF NOT EXISTS settlement_snapshots_period_revision_idx
  ON core_registry.settlement_snapshots (tenant_id, site_id, settlement_period_id, revision_no DESC);
CREATE INDEX IF NOT EXISTS settlement_change_candidates_period_idx
  ON core_registry.settlement_change_candidates (tenant_id, site_id, settlement_period_id, status, detected_at);

ALTER TABLE core_registry.settlement_boundaries ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_boundaries FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_boundary_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_boundary_edges FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariffs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariffs FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariff_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariff_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariff_periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariff_periods FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariff_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tariff_assignments FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_metric_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_metric_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_periods FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_change_candidates ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_change_candidates FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.settlement_revisions FORCE ROW LEVEL SECURITY;

CREATE POLICY settlement_boundaries_runtime_scope ON core_registry.settlement_boundaries
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_boundary_edges_runtime_scope ON core_registry.settlement_boundary_edges
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariffs_runtime_scope ON core_registry.tariffs
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_versions_runtime_scope ON core_registry.tariff_versions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_periods_runtime_scope ON core_registry.tariff_periods
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY tariff_assignments_runtime_scope ON core_registry.tariff_assignments
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_metric_bindings_runtime_scope ON core_registry.settlement_metric_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_periods_runtime_scope ON core_registry.settlement_periods
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_snapshots_runtime_scope ON core_registry.settlement_snapshots
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_change_candidates_runtime_scope ON core_registry.settlement_change_candidates
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY settlement_revisions_runtime_scope ON core_registry.settlement_revisions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.settlement_boundaries, core_registry.settlement_boundary_edges,
  core_registry.tariffs, core_registry.tariff_versions, core_registry.tariff_periods,
  core_registry.tariff_assignments, core_registry.settlement_metric_bindings, core_registry.settlement_periods,
  core_registry.settlement_snapshots, core_registry.settlement_change_candidates,
  core_registry.settlement_revisions FROM PUBLIC;
GRANT SELECT ON core_registry.settlement_boundaries, core_registry.settlement_boundary_edges,
  core_registry.tariffs, core_registry.tariff_versions, core_registry.tariff_periods,
  core_registry.tariff_assignments, core_registry.settlement_metric_bindings, core_registry.settlement_periods,
  core_registry.settlement_snapshots, core_registry.settlement_change_candidates,
  core_registry.settlement_revisions TO s1_core_runtime;

COMMIT;
