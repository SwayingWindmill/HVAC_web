BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.areas ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.equipment_area_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.device_area_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.sensors ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.sensor_device_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.sensor_area_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.sensor_subject_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.telemetry_points ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.point_subject_bindings ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE core_registry.calculated_point_inputs ADD COLUMN tenant_id uuid NOT NULL;

ALTER TABLE core_registry.areas
  ADD CONSTRAINT areas_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT areas_tenant_site_fk FOREIGN KEY (tenant_id, organization_id, site_id) REFERENCES core_registry.sites(tenant_id, organization_id, id),
  ADD CONSTRAINT areas_tenant_site_id_key UNIQUE (tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.equipment_area_bindings
  ADD CONSTRAINT equipment_area_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT equipment_area_bindings_tenant_equipment_fk FOREIGN KEY (tenant_id, organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT equipment_area_bindings_tenant_area_fk FOREIGN KEY (tenant_id, organization_id, site_id, area_id) REFERENCES core_registry.areas(tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.device_area_bindings
  ADD CONSTRAINT device_area_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT device_area_bindings_tenant_device_fk FOREIGN KEY (tenant_id, organization_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT device_area_bindings_tenant_area_fk FOREIGN KEY (tenant_id, organization_id, site_id, area_id) REFERENCES core_registry.areas(tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.sensors
  ADD CONSTRAINT sensors_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT sensors_tenant_site_fk FOREIGN KEY (tenant_id, organization_id, site_id) REFERENCES core_registry.sites(tenant_id, organization_id, id),
  ADD CONSTRAINT sensors_tenant_site_id_key UNIQUE (tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.sensor_device_bindings
  ADD CONSTRAINT sensor_device_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT sensor_device_bindings_tenant_sensor_fk FOREIGN KEY (tenant_id, organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT sensor_device_bindings_tenant_device_fk FOREIGN KEY (tenant_id, organization_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.sensor_area_bindings
  ADD CONSTRAINT sensor_area_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT sensor_area_bindings_tenant_sensor_fk FOREIGN KEY (tenant_id, organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT sensor_area_bindings_tenant_area_fk FOREIGN KEY (tenant_id, organization_id, site_id, area_id) REFERENCES core_registry.areas(tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.sensor_subject_bindings
  ADD CONSTRAINT sensor_subject_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT sensor_subject_bindings_tenant_sensor_fk FOREIGN KEY (tenant_id, organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT sensor_subject_bindings_tenant_area_fk FOREIGN KEY (tenant_id, organization_id, site_id, area_id) REFERENCES core_registry.areas(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT sensor_subject_bindings_tenant_equipment_fk FOREIGN KEY (tenant_id, organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.telemetry_points
  ADD CONSTRAINT telemetry_points_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT telemetry_points_tenant_device_fk FOREIGN KEY (tenant_id, organization_id, site_id, reporting_device_id) REFERENCES core_registry.devices(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT telemetry_points_tenant_sensor_fk FOREIGN KEY (tenant_id, organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT telemetry_points_tenant_site_id_key UNIQUE (tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.point_subject_bindings
  ADD CONSTRAINT point_subject_bindings_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT point_subject_bindings_tenant_point_fk FOREIGN KEY (tenant_id, organization_id, site_id, point_id) REFERENCES core_registry.telemetry_points(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT point_subject_bindings_tenant_area_fk FOREIGN KEY (tenant_id, organization_id, site_id, area_id) REFERENCES core_registry.areas(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT point_subject_bindings_tenant_equipment_fk FOREIGN KEY (tenant_id, organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(tenant_id, organization_id, site_id, id);

ALTER TABLE core_registry.calculated_point_inputs
  ADD CONSTRAINT calculated_point_inputs_tenant_fk FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  ADD CONSTRAINT calculated_point_inputs_tenant_calculated_fk FOREIGN KEY (tenant_id, organization_id, site_id, calculated_point_id) REFERENCES core_registry.telemetry_points(tenant_id, organization_id, site_id, id),
  ADD CONSTRAINT calculated_point_inputs_tenant_input_fk FOREIGN KEY (tenant_id, organization_id, site_id, input_point_id) REFERENCES core_registry.telemetry_points(tenant_id, organization_id, site_id, id);

CREATE INDEX areas_tenant_registry_page_idx
  ON core_registry.areas (tenant_id, organization_id, site_id, parent_area_id, display_name COLLATE "C", id);
CREATE INDEX sensors_tenant_registry_page_idx
  ON core_registry.sensors (tenant_id, organization_id, site_id, display_name COLLATE "C", id);
CREATE INDEX telemetry_points_tenant_device_key_idx
  ON core_registry.telemetry_points (tenant_id, organization_id, site_id, reporting_device_id, point_key COLLATE "C", id);
CREATE INDEX point_subject_bindings_tenant_scope_idx
  ON core_registry.point_subject_bindings (tenant_id, organization_id, site_id, subject_type, area_id, equipment_id, point_id);

DROP POLICY IF EXISTS areas_runtime_scope ON core_registry.areas;
CREATE POLICY areas_runtime_scope ON core_registry.areas
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS equipment_area_bindings_runtime_scope ON core_registry.equipment_area_bindings;
CREATE POLICY equipment_area_bindings_runtime_scope ON core_registry.equipment_area_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS device_area_bindings_runtime_scope ON core_registry.device_area_bindings;
CREATE POLICY device_area_bindings_runtime_scope ON core_registry.device_area_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS sensors_runtime_scope ON core_registry.sensors;
CREATE POLICY sensors_runtime_scope ON core_registry.sensors
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS sensor_device_bindings_runtime_scope ON core_registry.sensor_device_bindings;
CREATE POLICY sensor_device_bindings_runtime_scope ON core_registry.sensor_device_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS sensor_area_bindings_runtime_scope ON core_registry.sensor_area_bindings;
CREATE POLICY sensor_area_bindings_runtime_scope ON core_registry.sensor_area_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS sensor_subject_bindings_runtime_scope ON core_registry.sensor_subject_bindings;
CREATE POLICY sensor_subject_bindings_runtime_scope ON core_registry.sensor_subject_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS telemetry_points_runtime_scope ON core_registry.telemetry_points;
CREATE POLICY telemetry_points_runtime_scope ON core_registry.telemetry_points
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS point_subject_bindings_runtime_scope ON core_registry.point_subject_bindings;
CREATE POLICY point_subject_bindings_runtime_scope ON core_registry.point_subject_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

DROP POLICY IF EXISTS calculated_point_inputs_runtime_scope ON core_registry.calculated_point_inputs;
CREATE POLICY calculated_point_inputs_runtime_scope ON core_registry.calculated_point_inputs
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(organization_id, site_id));

RESET ROLE;
COMMIT;
