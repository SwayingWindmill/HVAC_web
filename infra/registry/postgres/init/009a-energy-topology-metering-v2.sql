BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- V2 topology is an immutable/versioned accounting graph. Existing unversioned
-- rows must be migrated explicitly rather than silently assigned to a version.
CREATE TABLE IF NOT EXISTS core_registry.energy_topology_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  version bigint NOT NULL CHECK (version > 0),
  status text NOT NULL CHECK (status IN ('DRAFT','VALIDATING','RELEASED','ACTIVE','SUPERSEDED')),
  effective_from timestamptz,
  effective_to timestamptz,
  released_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, version),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (effective_to IS NULL OR (effective_from IS NOT NULL AND effective_to > effective_from)),
  CHECK (status <> 'ACTIVE' OR effective_from IS NOT NULL),
  CHECK (status NOT IN ('RELEASED','ACTIVE','SUPERSEDED') OR released_at IS NOT NULL),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS energy_topology_versions_one_active_site_uidx
  ON core_registry.energy_topology_versions (tenant_id, site_id)
  WHERE status = 'ACTIVE';

ALTER TABLE core_registry.energy_nodes
  ADD COLUMN IF NOT EXISTS topology_version_id uuid;
ALTER TABLE core_registry.energy_edges
  ADD COLUMN IF NOT EXISTS topology_version_id uuid;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM core_registry.energy_nodes WHERE topology_version_id IS NULL)
     OR EXISTS (SELECT 1 FROM core_registry.energy_edges WHERE topology_version_id IS NULL) THEN
    RAISE EXCEPTION 'unversioned energy topology rows require explicit V2 migration' USING ERRCODE = '23514';
  END IF;
END
$$;

ALTER TABLE core_registry.energy_nodes
  ALTER COLUMN topology_version_id SET NOT NULL;
ALTER TABLE core_registry.energy_edges
  ALTER COLUMN topology_version_id SET NOT NULL;

ALTER TABLE core_registry.energy_nodes
  DROP CONSTRAINT IF EXISTS energy_nodes_topology_version_fk;
ALTER TABLE core_registry.energy_nodes
  ADD CONSTRAINT energy_nodes_topology_version_fk
  FOREIGN KEY (tenant_id, site_id, topology_version_id)
  REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id);
ALTER TABLE core_registry.energy_nodes
  DROP CONSTRAINT IF EXISTS energy_nodes_topology_identity_uniq;
ALTER TABLE core_registry.energy_nodes
  ADD CONSTRAINT energy_nodes_topology_identity_uniq
  UNIQUE (tenant_id, site_id, topology_version_id, id);

ALTER TABLE core_registry.energy_edges
  DROP CONSTRAINT IF EXISTS energy_edges_topology_version_fk;
ALTER TABLE core_registry.energy_edges
  ADD CONSTRAINT energy_edges_topology_version_fk
  FOREIGN KEY (tenant_id, site_id, topology_version_id)
  REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id);
ALTER TABLE core_registry.energy_edges
  DROP CONSTRAINT IF EXISTS energy_edges_tenant_id_site_id_from_node_id_fkey;
ALTER TABLE core_registry.energy_edges
  DROP CONSTRAINT IF EXISTS energy_edges_tenant_id_site_id_to_node_id_fkey;
ALTER TABLE core_registry.energy_edges
  ADD CONSTRAINT energy_edges_topology_from_node_fk
  FOREIGN KEY (tenant_id, site_id, topology_version_id, from_node_id)
  REFERENCES core_registry.energy_nodes(tenant_id, site_id, topology_version_id, id);
ALTER TABLE core_registry.energy_edges
  ADD CONSTRAINT energy_edges_topology_to_node_fk
  FOREIGN KEY (tenant_id, site_id, topology_version_id, to_node_id)
  REFERENCES core_registry.energy_nodes(tenant_id, site_id, topology_version_id, id);
ALTER TABLE core_registry.energy_edges
  DROP CONSTRAINT IF EXISTS energy_edges_topology_identity_uniq;
ALTER TABLE core_registry.energy_edges
  ADD CONSTRAINT energy_edges_topology_identity_uniq
  UNIQUE (tenant_id, site_id, topology_version_id, id);
ALTER TABLE core_registry.energy_edges
  DROP CONSTRAINT IF EXISTS energy_edges_meter_binding_identity_uniq;
ALTER TABLE core_registry.energy_edges
  ADD CONSTRAINT energy_edges_meter_binding_identity_uniq
  UNIQUE (tenant_id, site_id, topology_version_id, id, energy_type_id, direction);

CREATE OR REPLACE FUNCTION core_registry.validate_energy_edge_direction_semantics()
RETURNS trigger
LANGUAGE plpgsql
AS $energy_edge_direction$
DECLARE
  from_type text;
  to_type text;
BEGIN
  SELECT node_type INTO from_type
  FROM core_registry.energy_nodes
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id
    AND topology_version_id = NEW.topology_version_id AND id = NEW.from_node_id;
  SELECT node_type INTO to_type
  FROM core_registry.energy_nodes
  WHERE tenant_id = NEW.tenant_id AND site_id = NEW.site_id
    AND topology_version_id = NEW.topology_version_id AND id = NEW.to_node_id;

  IF from_type = 'GRID' AND to_type <> 'GRID' AND NEW.direction <> 'IMPORT' THEN
    RAISE EXCEPTION 'Grid outward accounting flow must use IMPORT direction' USING ERRCODE = '23514';
  END IF;
  IF to_type = 'GRID' AND from_type <> 'GRID' AND NEW.direction <> 'EXPORT' THEN
    RAISE EXCEPTION 'Grid inward accounting flow must use EXPORT direction' USING ERRCODE = '23514';
  END IF;
  IF from_type = 'ESS' AND to_type <> 'ESS' AND NEW.direction <> 'DISCHARGE' THEN
    RAISE EXCEPTION 'ESS outward accounting flow must use DISCHARGE direction' USING ERRCODE = '23514';
  END IF;
  IF to_type = 'ESS' AND from_type <> 'ESS' AND NEW.direction <> 'CHARGE' THEN
    RAISE EXCEPTION 'ESS inward accounting flow must use CHARGE direction' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$energy_edge_direction$;

DROP TRIGGER IF EXISTS energy_edges_validate_direction_semantics ON core_registry.energy_edges;
CREATE TRIGGER energy_edges_validate_direction_semantics
BEFORE INSERT OR UPDATE OF tenant_id, site_id, topology_version_id, from_node_id, to_node_id, direction
ON core_registry.energy_edges
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_energy_edge_direction_semantics();

-- Meter is a physical metering identity. The measured Point is bound to an
-- accounting edge separately so device identity is never mistaken for topology.
CREATE TABLE IF NOT EXISTS core_registry.energy_meters (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  meter_code text NOT NULL CHECK (meter_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 256),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  status text NOT NULL CHECK (status IN ('ACTIVE','INACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, meter_code),
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, device_id, energy_type_id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (energy_type_id) REFERENCES core_registry.energy_types(id),
  CHECK (updated_at >= created_at)
);

-- Make Point type/device identity available to Meter Binding composite FKs.
ALTER TABLE core_registry.telemetry_points
  DROP CONSTRAINT IF EXISTS telemetry_points_meter_binding_identity_uniq;
ALTER TABLE core_registry.telemetry_points
  ADD CONSTRAINT telemetry_points_meter_binding_identity_uniq
  UNIQUE (tenant_id, site_id, reporting_device_id, id, point_type);

CREATE TABLE IF NOT EXISTS core_registry.meter_ratio_versions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  meter_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(meter_id)),
  version bigint NOT NULL CHECK (version > 0),
  ct_ratio numeric(24,8) NOT NULL DEFAULT 1 CHECK (ct_ratio > 0),
  pt_ratio numeric(24,8) NOT NULL DEFAULT 1 CHECK (pt_ratio > 0),
  meter_multiplier numeric(24,8) NOT NULL DEFAULT 1 CHECK (meter_multiplier > 0),
  ratio_application_mode text NOT NULL CHECK (ratio_application_mode IN ('DEVICE_APPLIED','EDGE_APPLIED','PLATFORM_APPLIED')),
  cloud_multiplier numeric(36,12) GENERATED ALWAYS AS (
    CASE
      WHEN ratio_application_mode = 'PLATFORM_APPLIED' THEN ct_ratio * pt_ratio * meter_multiplier
      ELSE 1::numeric
    END
  ) STORED,
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, meter_id, version),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, meter_id) REFERENCES core_registry.energy_meters(tenant_id, site_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_meter_ratio_version()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status = 'RELEASED' AND EXISTS (
    SELECT 1
    FROM core_registry.meter_ratio_versions AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.meter_id = NEW.meter_id
      AND existing.id <> NEW.id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'released Meter ratio versions cannot overlap' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS meter_ratio_versions_reject_overlap ON core_registry.meter_ratio_versions;
CREATE TRIGGER meter_ratio_versions_reject_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, meter_id, effective_from, effective_to, status
ON core_registry.meter_ratio_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_meter_ratio_version();

CREATE TABLE IF NOT EXISTS core_registry.meter_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  energy_edge_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_edge_id)),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  meter_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(meter_id)),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  point_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(point_id)),
  point_type text NOT NULL DEFAULT 'COUNTER' CHECK (point_type = 'COUNTER'),
  meter_role text NOT NULL CHECK (meter_role IN ('PRIMARY','CHECK','MONITORING','BACKUP')),
  direction text NOT NULL,
  priority integer NOT NULL DEFAULT 0 CHECK (priority >= 0),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  version bigint NOT NULL CHECK (version > 0),
  status text NOT NULL CHECK (status IN ('DRAFT','RELEASED','ACTIVE','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, topology_version_id, energy_type_id),
  UNIQUE (tenant_id, site_id, meter_id, version),
  FOREIGN KEY (tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id, direction)
    REFERENCES core_registry.energy_edges(tenant_id, site_id, topology_version_id, id, energy_type_id, direction),
  FOREIGN KEY (tenant_id, site_id, meter_id, device_id, energy_type_id)
    REFERENCES core_registry.energy_meters(tenant_id, site_id, id, device_id, energy_type_id),
  FOREIGN KEY (tenant_id, site_id, device_id, point_id, point_type)
    REFERENCES core_registry.telemetry_points(tenant_id, site_id, reporting_device_id, id, point_type),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_primary_meter_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.meter_role = 'PRIMARY' AND NEW.status IN ('RELEASED','ACTIVE') AND EXISTS (
    SELECT 1
    FROM core_registry.meter_bindings AS existing
    WHERE existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.topology_version_id = NEW.topology_version_id
      AND existing.energy_edge_id = NEW.energy_edge_id
      AND existing.direction = NEW.direction
      AND existing.id <> NEW.id
      AND existing.meter_role = 'PRIMARY'
      AND existing.status IN ('RELEASED','ACTIVE')
      AND tstzrange(existing.effective_from, coalesce(existing.effective_to, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.effective_from, coalesce(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'overlapping PRIMARY Meter Binding is not allowed for an accounting edge' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS meter_bindings_reject_primary_overlap ON core_registry.meter_bindings;
CREATE TRIGGER meter_bindings_reject_primary_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, topology_version_id, energy_edge_id, direction,
  meter_role, effective_from, effective_to, status
ON core_registry.meter_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_primary_meter_binding();

CREATE TABLE IF NOT EXISTS core_registry.virtual_meters (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  virtual_meter_code text NOT NULL CHECK (virtual_meter_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 256),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  direction text NOT NULL,
  calculation_type text NOT NULL CHECK (calculation_type IN ('SUM','DIFFERENCE','BALANCE','EXPRESSION')),
  expression_text text,
  version bigint NOT NULL CHECK (version > 0),
  effective_from timestamptz NOT NULL,
  effective_to timestamptz,
  status text NOT NULL CHECK (status IN ('DRAFT','VALIDATING','RELEASED','ACTIVE','SUPERSEDED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, topology_version_id, virtual_meter_code, version),
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, id, topology_version_id, energy_type_id),
  FOREIGN KEY (tenant_id, site_id, topology_version_id)
    REFERENCES core_registry.energy_topology_versions(tenant_id, site_id, id),
  FOREIGN KEY (energy_type_id) REFERENCES core_registry.energy_types(id),
  FOREIGN KEY (direction) REFERENCES core_registry.energy_directions(direction_code),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK ((calculation_type = 'EXPRESSION' AND expression_text IS NOT NULL AND length(btrim(expression_text)) > 0)
    OR (calculation_type <> 'EXPRESSION' AND expression_text IS NULL)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.virtual_meter_sources (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  topology_version_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(topology_version_id)),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  virtual_meter_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(virtual_meter_id)),
  source_type text NOT NULL CHECK (source_type IN ('METER_BINDING','VIRTUAL_METER')),
  source_meter_binding_id uuid,
  source_virtual_meter_id uuid,
  coefficient numeric(24,8) NOT NULL DEFAULT 1 CHECK (coefficient <> 0),
  ordinal integer NOT NULL CHECK (ordinal >= 0),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, virtual_meter_id, ordinal),
  FOREIGN KEY (tenant_id, site_id, virtual_meter_id, topology_version_id, energy_type_id)
    REFERENCES core_registry.virtual_meters(tenant_id, site_id, id, topology_version_id, energy_type_id),
  FOREIGN KEY (tenant_id, site_id, source_meter_binding_id, topology_version_id, energy_type_id)
    REFERENCES core_registry.meter_bindings(tenant_id, site_id, id, topology_version_id, energy_type_id),
  FOREIGN KEY (tenant_id, site_id, source_virtual_meter_id, topology_version_id, energy_type_id)
    REFERENCES core_registry.virtual_meters(tenant_id, site_id, id, topology_version_id, energy_type_id),
  CHECK (
    (source_type = 'METER_BINDING' AND source_meter_binding_id IS NOT NULL AND source_virtual_meter_id IS NULL)
    OR
    (source_type = 'VIRTUAL_METER' AND source_meter_binding_id IS NULL AND source_virtual_meter_id IS NOT NULL)
  ),
  CHECK (source_virtual_meter_id IS NULL OR source_virtual_meter_id <> virtual_meter_id),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS virtual_meter_sources_meter_binding_uidx
  ON core_registry.virtual_meter_sources (tenant_id, site_id, virtual_meter_id, source_meter_binding_id)
  WHERE source_type = 'METER_BINDING';
CREATE UNIQUE INDEX IF NOT EXISTS virtual_meter_sources_virtual_meter_uidx
  ON core_registry.virtual_meter_sources (tenant_id, site_id, virtual_meter_id, source_virtual_meter_id)
  WHERE source_type = 'VIRTUAL_METER';

CREATE OR REPLACE FUNCTION core_registry.reject_virtual_meter_cycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.source_type <> 'VIRTUAL_METER' THEN
    RETURN NEW;
  END IF;
  IF NEW.source_virtual_meter_id = NEW.virtual_meter_id THEN
    RAISE EXCEPTION 'Virtual Meter cannot reference itself' USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    WITH RECURSIVE dependencies(id) AS (
      SELECT NEW.source_virtual_meter_id
      UNION
      SELECT source.source_virtual_meter_id
      FROM core_registry.virtual_meter_sources AS source
      JOIN dependencies AS dependency ON source.virtual_meter_id = dependency.id
      WHERE source.tenant_id = NEW.tenant_id
        AND source.site_id = NEW.site_id
        AND source.topology_version_id = NEW.topology_version_id
        AND source.source_type = 'VIRTUAL_METER'
        AND source.source_virtual_meter_id IS NOT NULL
    )
    SELECT 1 FROM dependencies WHERE id = NEW.virtual_meter_id
  ) THEN
    RAISE EXCEPTION 'Virtual Meter dependency cycle is not allowed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS virtual_meter_sources_reject_cycle ON core_registry.virtual_meter_sources;
CREATE TRIGGER virtual_meter_sources_reject_cycle
BEFORE INSERT OR UPDATE OF tenant_id, site_id, topology_version_id, virtual_meter_id,
  source_type, source_virtual_meter_id
ON core_registry.virtual_meter_sources
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_virtual_meter_cycle();

-- Released/active topology graph structure is immutable. New versions are the
-- only supported way to change nodes or edges after release.
CREATE OR REPLACE FUNCTION core_registry.reject_frozen_topology_graph_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  version_id uuid;
  version_status text;
BEGIN
  version_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.topology_version_id ELSE NEW.topology_version_id END;
  SELECT status INTO version_status
  FROM core_registry.energy_topology_versions
  WHERE id = version_id;
  IF version_status IN ('RELEASED','ACTIVE','SUPERSEDED') THEN
    RAISE EXCEPTION 'released Energy Topology graph is immutable; create a new Topology Version' USING ERRCODE = '23514';
  END IF;
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END
$$;

DROP TRIGGER IF EXISTS energy_nodes_reject_frozen_mutation ON core_registry.energy_nodes;
CREATE TRIGGER energy_nodes_reject_frozen_mutation
BEFORE INSERT OR UPDATE OR DELETE ON core_registry.energy_nodes
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_frozen_topology_graph_mutation();
DROP TRIGGER IF EXISTS energy_edges_reject_frozen_mutation ON core_registry.energy_edges;
CREATE TRIGGER energy_edges_reject_frozen_mutation
BEFORE INSERT OR UPDATE OR DELETE ON core_registry.energy_edges
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_frozen_topology_graph_mutation();

CREATE INDEX IF NOT EXISTS energy_nodes_topology_version_idx
  ON core_registry.energy_nodes (tenant_id, site_id, topology_version_id, node_type, id);
CREATE INDEX IF NOT EXISTS energy_edges_topology_version_idx
  ON core_registry.energy_edges (tenant_id, site_id, topology_version_id, energy_type_id, direction, id);
CREATE INDEX IF NOT EXISTS meter_bindings_topology_edge_idx
  ON core_registry.meter_bindings (tenant_id, site_id, topology_version_id, energy_edge_id, meter_role, effective_from);
CREATE INDEX IF NOT EXISTS meter_ratio_versions_effective_idx
  ON core_registry.meter_ratio_versions (tenant_id, site_id, meter_id, effective_from, effective_to);
CREATE INDEX IF NOT EXISTS virtual_meters_topology_idx
  ON core_registry.virtual_meters (tenant_id, site_id, topology_version_id, energy_type_id, virtual_meter_code, version);

ALTER TABLE core_registry.energy_topology_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_topology_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_meters ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_meters FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.meter_ratio_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.meter_ratio_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.meter_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.meter_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.virtual_meters ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.virtual_meters FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.virtual_meter_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.virtual_meter_sources FORCE ROW LEVEL SECURITY;

CREATE POLICY energy_topology_versions_runtime_scope ON core_registry.energy_topology_versions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY energy_meters_runtime_scope ON core_registry.energy_meters
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY meter_ratio_versions_runtime_scope ON core_registry.meter_ratio_versions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY meter_bindings_runtime_scope ON core_registry.meter_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY virtual_meters_runtime_scope ON core_registry.virtual_meters
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY virtual_meter_sources_runtime_scope ON core_registry.virtual_meter_sources
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.energy_topology_versions, core_registry.energy_meters,
  core_registry.meter_ratio_versions, core_registry.meter_bindings,
  core_registry.virtual_meters, core_registry.virtual_meter_sources FROM PUBLIC;
GRANT SELECT ON core_registry.energy_topology_versions, core_registry.energy_meters,
  core_registry.meter_ratio_versions, core_registry.meter_bindings,
  core_registry.virtual_meters, core_registry.virtual_meter_sources TO s1_core_runtime;

COMMIT;
