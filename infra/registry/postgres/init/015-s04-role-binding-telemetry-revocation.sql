CREATE OR REPLACE FUNCTION iam.emit_role_binding_telemetry_revocation()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $role_revocation$
DECLARE
  row_value iam.role_bindings%ROWTYPE;
BEGIN
  row_value := OLD;
  IF TG_OP <> 'DELETE' THEN
    IF NOT (
      NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
      OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
      OR NEW.role_template_id IS DISTINCT FROM OLD.role_template_id
      OR NEW.status IS DISTINCT FROM OLD.status
      OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
      OR NEW.valid_to IS DISTINCT FROM OLD.valid_to
    ) THEN
      RETURN row_value;
    END IF;
  END IF;
  INSERT INTO iam.telemetry_revocation_facts
    (tenant_id, principal_id, source_type, source_id, action, policy_revision, reason_code, occurred_at)
  VALUES
    (row_value.tenant_id, row_value.principal_id, 'ROLE_BINDING', row_value.id, 'telemetry.*',
     COALESCE(iam.active_telemetry_policy_revision(row_value.tenant_id), 'telemetry-access:unavailable'),
     'ROLE_BINDING_CHANGED', clock_timestamp());
  RETURN row_value;
END
$role_revocation$;
