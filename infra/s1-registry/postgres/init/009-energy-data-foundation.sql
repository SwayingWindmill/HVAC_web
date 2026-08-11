BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- Phase 1 data-architecture foundation derived from the canonical data design:
-- Product -> Point Template -> Device -> Point Instance,
-- Unit Registry, Energy Type/Direction, Energy Topology and Metric Definition.

CREATE TABLE IF NOT EXISTS core_registry.unit_registry (
  unit_code text PRIMARY KEY CHECK (length(btrim(unit_code)) BETWEEN 1 AND 32),
  display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 64),
  quantity_kind text NOT NULL CHECK (quantity_kind IN (
    'VOLTAGE','CURRENT','POWER','ENERGY','FREQUENCY','RATIO','TEMPERATURE',
    'PRESSURE','VOLUME','FLOW','MASS','OTHER'
  )),
  canonical_unit_code text,
  multiplier numeric NOT NULL DEFAULT 1,
  conversion_offset numeric NOT NULL DEFAULT 0,
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (updated_at >= created_at)
);

ALTER TABLE core_registry.unit_registry
  DROP CONSTRAINT IF EXISTS unit_registry_canonical_fk;
ALTER TABLE core_registry.unit_registry
  ADD CONSTRAINT unit_registry_canonical_fk
  FOREIGN KEY (canonical_unit_code) REFERENCES core_registry.unit_registry(unit_code);

INSERT INTO core_registry.unit_registry (
  unit_code, display_name, quantity_kind, canonical_unit_code, multiplier, conversion_offset,
  status, revision, created_at, updated_at
) VALUES
  ('V', 'volt', 'VOLTAGE', 'V', 1, 0, 'ACTIVE', 1, now(), now()),
  ('A', 'ampere', 'CURRENT', 'A', 1, 0, 'ACTIVE', 1, now(), now()),
  ('W', 'watt', 'POWER', 'kW', 0.001, 0, 'ACTIVE', 1, now(), now()),
  ('kW', 'kilowatt', 'POWER', 'kW', 1, 0, 'ACTIVE', 1, now(), now()),
  ('MW', 'megawatt', 'POWER', 'kW', 1000, 0, 'ACTIVE', 1, now(), now()),
  ('kWh', 'kilowatt-hour', 'ENERGY', 'kWh', 1, 0, 'ACTIVE', 1, now(), now()),
  ('MWh', 'megawatt-hour', 'ENERGY', 'kWh', 1000, 0, 'ACTIVE', 1, now(), now()),
  ('Hz', 'hertz', 'FREQUENCY', 'Hz', 1, 0, 'ACTIVE', 1, now(), now()),
  ('%', 'percent', 'RATIO', '%', 1, 0, 'ACTIVE', 1, now(), now()),
  ('°C', 'degree Celsius', 'TEMPERATURE', '°C', 1, 0, 'ACTIVE', 1, now(), now()),
  ('Pa', 'pascal', 'PRESSURE', 'Pa', 1, 0, 'ACTIVE', 1, now(), now()),
  ('m³', 'cubic metre', 'VOLUME', 'm³', 1, 0, 'ACTIVE', 1, now(), now()),
  ('m³/h', 'cubic metre per hour', 'FLOW', 'm³/h', 1, 0, 'ACTIVE', 1, now(), now()),
  ('t', 'tonne', 'MASS', 't', 1, 0, 'ACTIVE', 1, now(), now()),
  ('kg', 'kilogram', 'MASS', 'kg', 1, 0, 'ACTIVE', 1, now(), now())
ON CONFLICT (unit_code) DO NOTHING;

CREATE TABLE IF NOT EXISTS core_registry.device_products (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  product_code text NOT NULL CHECK (product_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$'),
  product_name text NOT NULL CHECK (length(btrim(product_name)) BETWEEN 1 AND 256),
  manufacturer text,
  model text,
  status text NOT NULL CHECK (status IN ('ACTIVE','INACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, product_code),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.point_templates (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  device_product_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_product_id)),
  point_code text NOT NULL CHECK (point_code ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  point_name text NOT NULL CHECK (length(btrim(point_name)) BETWEEN 1 AND 256),
  point_type text NOT NULL CHECK (point_type IN ('TELEMETRY','STATE','COUNTER','COMMAND','SETTING')),
  data_type text NOT NULL CHECK (data_type IN ('BOOLEAN','NUMBER','STRING','JSON')),
  unit_code text,
  access_type text NOT NULL CHECK (access_type IN ('READ_ONLY','WRITE_ONLY','READ_WRITE')),
  sampling_interval_ms integer CHECK (sampling_interval_ms IS NULL OR sampling_interval_ms BETWEEN 100 AND 86400000),
  minimum_number double precision,
  maximum_number double precision,
  precision_digits integer CHECK (precision_digits IS NULL OR precision_digits BETWEEN 0 AND 12),
  multiplier double precision NOT NULL DEFAULT 1,
  value_offset double precision NOT NULL DEFAULT 0,
  enabled boolean NOT NULL DEFAULT true,
  status text NOT NULL CHECK (status IN ('ACTIVE','INACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, device_product_id, point_code),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, device_product_id) REFERENCES core_registry.device_products(tenant_id, id),
  FOREIGN KEY (unit_code) REFERENCES core_registry.unit_registry(unit_code),
  CHECK (minimum_number IS NULL OR maximum_number IS NULL OR minimum_number <= maximum_number),
  CHECK (updated_at >= created_at)
);

ALTER TABLE core_registry.devices
  ADD COLUMN IF NOT EXISTS product_id uuid;
ALTER TABLE core_registry.devices
  DROP CONSTRAINT IF EXISTS devices_tenant_product_fk;
ALTER TABLE core_registry.devices
  ADD CONSTRAINT devices_tenant_product_fk
  FOREIGN KEY (tenant_id, product_id) REFERENCES core_registry.device_products(tenant_id, id);
CREATE INDEX IF NOT EXISTS devices_tenant_product_idx
  ON core_registry.devices (tenant_id, product_id, site_id, id)
  WHERE product_id IS NOT NULL;

ALTER TABLE core_registry.telemetry_points
  ADD COLUMN IF NOT EXISTS point_template_id uuid;
ALTER TABLE core_registry.telemetry_points
  DROP CONSTRAINT IF EXISTS telemetry_points_tenant_template_fk;
ALTER TABLE core_registry.telemetry_points
  ADD CONSTRAINT telemetry_points_tenant_template_fk
  FOREIGN KEY (tenant_id, point_template_id) REFERENCES core_registry.point_templates(tenant_id, id);
CREATE INDEX IF NOT EXISTS telemetry_points_tenant_template_idx
  ON core_registry.telemetry_points (tenant_id, point_template_id, reporting_device_id, id)
  WHERE point_template_id IS NOT NULL;

ALTER TABLE core_registry.equipment
  ADD COLUMN IF NOT EXISTS parent_equipment_id uuid,
  ADD COLUMN IF NOT EXISTS rated_capacity double precision,
  ADD COLUMN IF NOT EXISTS rated_power double precision,
  ADD COLUMN IF NOT EXISTS manufacturer text,
  ADD COLUMN IF NOT EXISTS model text,
  ADD COLUMN IF NOT EXISTS commission_date date;
ALTER TABLE core_registry.equipment
  DROP CONSTRAINT IF EXISTS equipment_parent_not_self_check;
ALTER TABLE core_registry.equipment
  ADD CONSTRAINT equipment_parent_not_self_check CHECK (parent_equipment_id IS NULL OR parent_equipment_id <> id);
ALTER TABLE core_registry.equipment
  DROP CONSTRAINT IF EXISTS equipment_tenant_parent_fk;
ALTER TABLE core_registry.equipment
  ADD CONSTRAINT equipment_tenant_parent_fk
  FOREIGN KEY (tenant_id, organization_id, site_id, parent_equipment_id)
  REFERENCES core_registry.equipment(tenant_id, organization_id, site_id, id);

CREATE OR REPLACE FUNCTION core_registry.reject_equipment_cycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.parent_equipment_id IS NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.parent_equipment_id = NEW.id THEN
    RAISE EXCEPTION 'equipment cannot be its own parent' USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    WITH RECURSIVE descendants AS (
      SELECT equipment.id
      FROM core_registry.equipment AS equipment
      WHERE equipment.parent_equipment_id = NEW.id
      UNION ALL
      SELECT child.id
      FROM core_registry.equipment AS child
      JOIN descendants AS parent ON child.parent_equipment_id = parent.id
    )
    SELECT 1 FROM descendants WHERE id = NEW.parent_equipment_id
  ) THEN
    RAISE EXCEPTION 'equipment hierarchy cycle is not allowed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS equipment_reject_cycle ON core_registry.equipment;
CREATE TRIGGER equipment_reject_cycle
BEFORE INSERT OR UPDATE OF parent_equipment_id ON core_registry.equipment
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_equipment_cycle();

CREATE TABLE IF NOT EXISTS core_registry.energy_types (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  energy_code text NOT NULL UNIQUE CHECK (energy_code ~ '^[a-z][a-z0-9_]{0,63}$'),
  energy_name text NOT NULL CHECK (length(btrim(energy_name)) BETWEEN 1 AND 128),
  unit_code text,
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (unit_code) REFERENCES core_registry.unit_registry(unit_code),
  CHECK (updated_at >= created_at)
);

INSERT INTO core_registry.energy_types (
  id, energy_code, energy_name, unit_code, status, revision, created_at, updated_at
) VALUES
  ('01990000-0000-7000-8000-000000000001', 'electricity', 'Electricity', 'kWh', 'ACTIVE', 1, now(), now()),
  ('01990000-0000-7000-8000-000000000002', 'water', 'Water', NULL, 'ACTIVE', 1, now(), now()),
  ('01990000-0000-7000-8000-000000000003', 'gas', 'Gas', NULL, 'ACTIVE', 1, now(), now()),
  ('01990000-0000-7000-8000-000000000004', 'steam', 'Steam', NULL, 'ACTIVE', 1, now(), now()),
  ('01990000-0000-7000-8000-000000000005', 'heat', 'Heat', NULL, 'ACTIVE', 1, now(), now()),
  ('01990000-0000-7000-8000-000000000006', 'cooling', 'Cooling', NULL, 'ACTIVE', 1, now(), now()),
  ('01990000-0000-7000-8000-000000000007', 'compressed_air', 'CompressedAir', NULL, 'ACTIVE', 1, now(), now()),
  ('01990000-0000-7000-8000-000000000008', 'hydrogen', 'Hydrogen', NULL, 'ACTIVE', 1, now(), now())
ON CONFLICT (energy_code) DO NOTHING;

CREATE TABLE IF NOT EXISTS core_registry.energy_directions (
  direction_code text PRIMARY KEY CHECK (direction_code IN ('IMPORT','EXPORT','GENERATE','CONSUME','CHARGE','DISCHARGE')),
  display_name text NOT NULL,
  status text NOT NULL CHECK (status IN ('ACTIVE','RETIRED'))
);

INSERT INTO core_registry.energy_directions (direction_code, display_name, status) VALUES
  ('IMPORT', 'Import', 'ACTIVE'),
  ('EXPORT', 'Export', 'ACTIVE'),
  ('GENERATE', 'Generate', 'ACTIVE'),
  ('CONSUME', 'Consume', 'ACTIVE'),
  ('CHARGE', 'Charge', 'ACTIVE'),
  ('DISCHARGE', 'Discharge', 'ACTIVE')
ON CONFLICT (direction_code) DO NOTHING;

CREATE TABLE IF NOT EXISTS core_registry.energy_nodes (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  node_type text NOT NULL CHECK (node_type IN ('GRID','TRANSFORMER','SWITCHBOARD','FEEDER','METER','LOAD','PV','ESS','EV','HVAC','OTHER')),
  equipment_id uuid,
  device_id uuid,
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('ACTIVE','INACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, organization_id, site_id, id),
  FOREIGN KEY (tenant_id, organization_id, site_id) REFERENCES core_registry.sites(tenant_id, organization_id, id),
  FOREIGN KEY (tenant_id, organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(tenant_id, organization_id, site_id, id),
  FOREIGN KEY (tenant_id, organization_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, organization_id, site_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.energy_edges (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  from_node_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(from_node_id)),
  to_node_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(to_node_id)),
  energy_type_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(energy_type_id)),
  direction text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, organization_id, site_id, id),
  FOREIGN KEY (tenant_id, organization_id, site_id, from_node_id) REFERENCES core_registry.energy_nodes(tenant_id, organization_id, site_id, id),
  FOREIGN KEY (tenant_id, organization_id, site_id, to_node_id) REFERENCES core_registry.energy_nodes(tenant_id, organization_id, site_id, id),
  FOREIGN KEY (energy_type_id) REFERENCES core_registry.energy_types(id),
  FOREIGN KEY (direction) REFERENCES core_registry.energy_directions(direction_code),
  CHECK (from_node_id <> to_node_id),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS energy_nodes_site_type_idx
  ON core_registry.energy_nodes (tenant_id, organization_id, site_id, node_type, name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS energy_edges_site_flow_idx
  ON core_registry.energy_edges (tenant_id, organization_id, site_id, from_node_id, to_node_id, energy_type_id, enabled);

CREATE TABLE IF NOT EXISTS core_registry.metric_definitions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  metric_code text NOT NULL CHECK (metric_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  metric_name text NOT NULL CHECK (length(btrim(metric_name)) BETWEEN 1 AND 256),
  metric_type text NOT NULL CHECK (metric_type IN ('RAW','AGGREGATE','ENERGY','EFFICIENCY','FINANCIAL','CARBON')),
  unit_code text,
  calculation_method text NOT NULL CHECK (length(btrim(calculation_method)) BETWEEN 1 AND 1024),
  aggregation text NOT NULL CHECK (length(btrim(aggregation)) BETWEEN 1 AND 64),
  period text NOT NULL CHECK (period IN ('INSTANT','MINUTE','15_MINUTE','HOUR','DAY','MONTH','CUSTOM')),
  status text NOT NULL CHECK (status IN ('DRAFT','ACTIVE','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, metric_code, revision),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (unit_code) REFERENCES core_registry.unit_registry(unit_code),
  CHECK (updated_at >= created_at)
);

ALTER TABLE core_registry.device_products ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_products FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.point_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.point_templates FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_nodes FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_edges FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_definitions FORCE ROW LEVEL SECURITY;

CREATE POLICY device_products_runtime_scope ON core_registry.device_products
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY point_templates_runtime_scope ON core_registry.point_templates
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY energy_nodes_runtime_scope ON core_registry.energy_nodes
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY energy_edges_runtime_scope ON core_registry.energy_edges
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY metric_definitions_runtime_scope ON core_registry.metric_definitions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());

REVOKE ALL ON core_registry.unit_registry, core_registry.device_products, core_registry.point_templates,
  core_registry.energy_types, core_registry.energy_directions, core_registry.energy_nodes,
  core_registry.energy_edges, core_registry.metric_definitions FROM PUBLIC;

GRANT SELECT ON core_registry.unit_registry, core_registry.device_products, core_registry.point_templates,
  core_registry.energy_types, core_registry.energy_directions, core_registry.energy_nodes,
  core_registry.energy_edges, core_registry.metric_definitions TO s1_core_runtime;

COMMIT;
