BEGIN;

SET LOCAL ROLE rule_runtime_migrator;

CREATE TABLE rule_runtime.rule_binding_retirements (
  id uuid PRIMARY KEY CHECK (rule_runtime.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (rule_runtime.is_uuid_v7(tenant_id)),
  binding_id uuid NOT NULL CHECK (rule_runtime.is_uuid_v7(binding_id)),
  binding_revision bigint NOT NULL CHECK (binding_revision > 0),
  reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 256),
  retired_at timestamptz NOT NULL,
  UNIQUE (tenant_id, binding_id, binding_revision),
  FOREIGN KEY (tenant_id, binding_id, binding_revision)
    REFERENCES rule_runtime.rule_bindings(tenant_id, binding_id, revision)
);

CREATE TRIGGER rule_binding_retirements_immutable
BEFORE UPDATE OR DELETE ON rule_runtime.rule_binding_retirements
FOR EACH ROW EXECUTE FUNCTION rule_runtime.reject_immutable_change();

ALTER TABLE rule_runtime.rule_binding_retirements ENABLE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.rule_binding_retirements FORCE ROW LEVEL SECURITY;
CREATE POLICY rule_binding_retirements_tenant_scope ON rule_runtime.rule_binding_retirements
  FOR ALL TO rule_runtime_runtime
  USING (tenant_id = rule_runtime.current_tenant_id())
  WITH CHECK (tenant_id = rule_runtime.current_tenant_id());

GRANT SELECT, INSERT ON rule_runtime.rule_binding_retirements TO rule_runtime_runtime;
REVOKE ALL ON rule_runtime.rule_binding_retirements FROM PUBLIC;

RESET ROLE;
COMMIT;
