BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE OR REPLACE FUNCTION core_registry.validate_current_control_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  point_kind_value text;
  writable_value boolean;
  point_status_value text;
BEGIN
  IF NEW.binding_role <> 'CONTROLS' OR NEW.status <> 'ACTIVE' OR NEW.valid_to IS NOT NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.subject_type <> 'EQUIPMENT' OR NEW.equipment_id IS NULL THEN
    RAISE EXCEPTION 'CONTROLS binding must target Equipment' USING ERRCODE = '23514';
  END IF;

  SELECT point_kind, writable, status
    INTO point_kind_value, writable_value, point_status_value
  FROM core_registry.telemetry_points
  WHERE tenant_id = NEW.tenant_id
    AND organization_id = NEW.organization_id
    AND site_id = NEW.site_id
    AND id = NEW.point_id;

  IF NOT FOUND OR point_kind_value <> 'COMMAND' OR writable_value IS NOT TRUE OR point_status_value <> 'ACTIVE' THEN
    RAISE EXCEPTION 'current CONTROLS binding requires an active writable COMMAND point' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS point_subject_bindings_validate_control ON core_registry.point_subject_bindings;
CREATE TRIGGER point_subject_bindings_validate_control
BEFORE INSERT OR UPDATE OF tenant_id, organization_id, site_id, point_id, subject_type, equipment_id, binding_role, status, valid_to
ON core_registry.point_subject_bindings
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_current_control_binding();

CREATE OR REPLACE FUNCTION core_registry.protect_current_control_point()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.point_kind = 'COMMAND' AND NEW.writable IS TRUE AND NEW.status = 'ACTIVE' THEN
    RETURN NEW;
  END IF;
  IF EXISTS (
    SELECT 1
    FROM core_registry.point_subject_bindings binding
    WHERE binding.tenant_id = NEW.tenant_id
      AND binding.organization_id = NEW.organization_id
      AND binding.site_id = NEW.site_id
      AND binding.point_id = NEW.id
      AND binding.binding_role = 'CONTROLS'
      AND binding.status = 'ACTIVE'
      AND binding.valid_to IS NULL
  ) THEN
    RAISE EXCEPTION 'point with a current CONTROLS binding must remain an active writable COMMAND point' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS telemetry_points_protect_current_control ON core_registry.telemetry_points;
CREATE TRIGGER telemetry_points_protect_current_control
BEFORE UPDATE OF point_kind, writable, status
ON core_registry.telemetry_points
FOR EACH ROW EXECUTE FUNCTION core_registry.protect_current_control_point();

CREATE UNIQUE INDEX point_subject_bindings_current_control_uidx
  ON core_registry.point_subject_bindings
  (tenant_id, organization_id, site_id, point_id)
  WHERE binding_role = 'CONTROLS' AND status = 'ACTIVE' AND valid_to IS NULL;

RESET ROLE;
COMMIT;
