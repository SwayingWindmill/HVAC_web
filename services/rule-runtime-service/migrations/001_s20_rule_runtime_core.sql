BEGIN;

GRANT USAGE ON SCHEMA iam, core_registry TO rule_runtime_migrator;
GRANT REFERENCES (id) ON iam.tenants TO rule_runtime_migrator;
GRANT REFERENCES (tenant_id, id) ON core_registry.sites TO rule_runtime_migrator;

SET LOCAL ROLE rule_runtime_migrator;

CREATE OR REPLACE FUNCTION rule_runtime.is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
AS $fn$
  SELECT (get_byte(uuid_send(value), 6) >> 4) = 7
     AND (get_byte(uuid_send(value), 8) >> 6) = 2
$fn$;

CREATE OR REPLACE FUNCTION rule_runtime.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $fn$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$fn$;

CREATE TABLE rule_runtime.node_definitions (
  catalog_version text NOT NULL,
  definition_id text NOT NULL,
  definition_version integer NOT NULL CHECK (definition_version > 0),
  input_ports jsonb NOT NULL,
  output_ports jsonb NOT NULL,
  required_permission text,
  effect_owner text,
  state_schema_version integer NOT NULL CHECK (state_schema_version >= 0),
  deterministic boolean NOT NULL CHECK (deterministic),
  resource_cost integer NOT NULL CHECK (resource_cost > 0),
  definition_digest text NOT NULL CHECK (definition_digest ~ '^[a-f0-9]{64}$'),
  PRIMARY KEY (catalog_version, definition_id, definition_version)
);

INSERT INTO rule_runtime.node_definitions (
  catalog_version, definition_id, definition_version, input_ports, output_ports,
  required_permission, effect_owner, state_schema_version, deterministic, resource_cost, definition_digest
) VALUES
  ('core.v1','event_type_filter',1,'{"in":"EVENT"}','{"match":"EVENT","no_match":"EVENT"}',NULL,NULL,0,true,1,encode(sha256(convert_to('core.v1:event_type_filter:1','UTF8')),'hex')),
  ('core.v1','json_number',1,'{"in":"EVENT"}','{"value":"NUMBER"}',NULL,NULL,0,true,1,encode(sha256(convert_to('core.v1:json_number:1','UTF8')),'hex')),
  ('core.v1','math_number',1,'{"in":"NUMBER"}','{"value":"NUMBER"}',NULL,NULL,0,true,1,encode(sha256(convert_to('core.v1:math_number:1','UTF8')),'hex')),
  ('core.v1','owner_snapshot_read',1,'{"in":"EVENT"}','{"snapshot":"SNAPSHOT"}','owner.snapshot.read',NULL,0,true,2,encode(sha256(convert_to('core.v1:owner_snapshot_read:1','UTF8')),'hex')),
  ('core.v1','alarm_intent',1,'{"in":"SNAPSHOT"}','{"intent":"INTENT"}','alarm.intent.publish','ALARM',0,true,2,encode(sha256(convert_to('core.v1:alarm_intent:1','UTF8')),'hex')),
  ('core.v1','delay_event',1,'{"in":"EVENT"}','{"resume":"EVENT"}',NULL,NULL,0,true,1,encode(sha256(convert_to('core.v1:delay_event:1','UTF8')),'hex')),
  ('core.v1','terminal_event',1,'{"in":"EVENT"}','{}',NULL,NULL,0,true,1,encode(sha256(convert_to('core.v1:terminal_event:1','UTF8')),'hex')),
  ('core.v1','terminal_number',1,'{"in":"NUMBER"}','{}',NULL,NULL,0,true,1,encode(sha256(convert_to('core.v1:terminal_number:1','UTF8')),'hex')),
  ('core.v1','terminal_intent',1,'{"in":"INTENT"}','{}',NULL,NULL,0,true,1,encode(sha256(convert_to('core.v1:terminal_intent:1','UTF8')),'hex'));

CREATE TABLE rule_runtime.rule_revisions (
  id uuid PRIMARY KEY CHECK (rule_runtime.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (rule_runtime.is_uuid_v7(tenant_id)),
  rule_id uuid NOT NULL CHECK (rule_runtime.is_uuid_v7(rule_id)),
  revision bigint NOT NULL CHECK (revision > 0),
  catalog_version text NOT NULL,
  content jsonb NOT NULL,
  content_digest text NOT NULL CHECK (content_digest ~ '^[a-f0-9]{64}$'),
  released_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, rule_id, revision)
);

CREATE TABLE rule_runtime.rule_bindings (
  binding_id uuid NOT NULL CHECK (rule_runtime.is_uuid_v7(binding_id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (rule_runtime.is_uuid_v7(tenant_id)),
  site_id uuid,
  revision bigint NOT NULL CHECK (revision > 0),
  rule_revision_id uuid NOT NULL CHECK (rule_runtime.is_uuid_v7(rule_revision_id)),
  priority integer NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (binding_id, revision),
  UNIQUE (tenant_id, binding_id, revision),
  FOREIGN KEY (tenant_id, rule_revision_id) REFERENCES rule_runtime.rule_revisions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE TABLE rule_runtime.rule_states (
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (rule_runtime.is_uuid_v7(tenant_id)),
  rule_revision_id uuid NOT NULL CHECK (rule_runtime.is_uuid_v7(rule_revision_id)),
  node_instance_id text NOT NULL CHECK (char_length(node_instance_id) BETWEEN 1 AND 128),
  scope_key text NOT NULL CHECK (char_length(scope_key) BETWEEN 1 AND 512),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  revision bigint NOT NULL CHECK (revision > 0),
  value jsonb NOT NULL,
  expires_at timestamptz,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, rule_revision_id, node_instance_id, scope_key),
  FOREIGN KEY (tenant_id, rule_revision_id) REFERENCES rule_runtime.rule_revisions(tenant_id, id)
);

CREATE TABLE rule_runtime.executions (
  execution_id text PRIMARY KEY CHECK (execution_id ~ '^[a-f0-9]{64}$'),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (rule_runtime.is_uuid_v7(tenant_id)),
  site_id uuid,
  rule_revision_id uuid NOT NULL CHECK (rule_runtime.is_uuid_v7(rule_revision_id)),
  binding_id uuid NOT NULL CHECK (rule_runtime.is_uuid_v7(binding_id)),
  binding_revision bigint NOT NULL CHECK (binding_revision > 0),
  event_id text NOT NULL CHECK (char_length(event_id) BETWEEN 1 AND 256),
  ordering_key text NOT NULL CHECK (char_length(ordering_key) BETWEEN 1 AND 512),
  status text NOT NULL CHECK (status IN ('READY','RUNNING','WAITING','BLOCKED_EFFECT','SUCCEEDED','DEAD','QUARANTINED','FAILED')),
  attempt_budget integer NOT NULL CHECK (attempt_budget > 0),
  lease_owner text,
  lease_until timestamptz,
  lease_fence bigint NOT NULL DEFAULT 0 CHECK (lease_fence >= 0),
  terminal_code text,
  rule_digest text NOT NULL CHECK (rule_digest ~ '^[a-f0-9]{64}$'),
  snapshot jsonb NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, execution_id),
  UNIQUE (tenant_id, rule_revision_id, binding_id, binding_revision, event_id),
  FOREIGN KEY (tenant_id, rule_revision_id) REFERENCES rule_runtime.rule_revisions(tenant_id, id),
  FOREIGN KEY (tenant_id, binding_id, binding_revision) REFERENCES rule_runtime.rule_bindings(tenant_id, binding_id, revision),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK ((lease_owner IS NULL) = (lease_until IS NULL)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE rule_runtime.ordering_locks (
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  ordering_key text NOT NULL CHECK (char_length(ordering_key) BETWEEN 1 AND 512),
  execution_id text NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, ordering_key),
  FOREIGN KEY (tenant_id, execution_id) REFERENCES rule_runtime.executions(tenant_id, execution_id)
);

CREATE INDEX rule_runtime_execution_resume_idx
  ON rule_runtime.executions (tenant_id, status, updated_at, execution_id)
  WHERE status IN ('READY','RUNNING','WAITING','BLOCKED_EFFECT');
CREATE INDEX rule_runtime_execution_ordering_idx
  ON rule_runtime.executions (tenant_id, ordering_key, created_at, execution_id);

CREATE OR REPLACE FUNCTION rule_runtime.reject_immutable_change()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
  RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
END
$fn$;

CREATE TRIGGER node_definitions_immutable
BEFORE UPDATE OR DELETE ON rule_runtime.node_definitions
FOR EACH ROW EXECUTE FUNCTION rule_runtime.reject_immutable_change();
CREATE TRIGGER rule_revisions_immutable
BEFORE UPDATE OR DELETE ON rule_runtime.rule_revisions
FOR EACH ROW EXECUTE FUNCTION rule_runtime.reject_immutable_change();
CREATE TRIGGER rule_bindings_immutable
BEFORE UPDATE OR DELETE ON rule_runtime.rule_bindings
FOR EACH ROW EXECUTE FUNCTION rule_runtime.reject_immutable_change();

ALTER TABLE rule_runtime.rule_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.rule_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.rule_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.rule_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.rule_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.rule_states FORCE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.executions FORCE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.ordering_locks ENABLE ROW LEVEL SECURITY;
ALTER TABLE rule_runtime.ordering_locks FORCE ROW LEVEL SECURITY;

CREATE POLICY rule_revisions_tenant_scope ON rule_runtime.rule_revisions
  FOR ALL TO rule_runtime_runtime
  USING (tenant_id = rule_runtime.current_tenant_id())
  WITH CHECK (tenant_id = rule_runtime.current_tenant_id());
CREATE POLICY rule_bindings_tenant_scope ON rule_runtime.rule_bindings
  FOR ALL TO rule_runtime_runtime
  USING (tenant_id = rule_runtime.current_tenant_id())
  WITH CHECK (tenant_id = rule_runtime.current_tenant_id());
CREATE POLICY rule_states_tenant_scope ON rule_runtime.rule_states
  FOR ALL TO rule_runtime_runtime
  USING (tenant_id = rule_runtime.current_tenant_id())
  WITH CHECK (tenant_id = rule_runtime.current_tenant_id());
CREATE POLICY executions_tenant_scope ON rule_runtime.executions
  FOR ALL TO rule_runtime_runtime
  USING (tenant_id = rule_runtime.current_tenant_id())
  WITH CHECK (tenant_id = rule_runtime.current_tenant_id());
CREATE POLICY ordering_locks_tenant_scope ON rule_runtime.ordering_locks
  FOR ALL TO rule_runtime_runtime
  USING (tenant_id = rule_runtime.current_tenant_id())
  WITH CHECK (tenant_id = rule_runtime.current_tenant_id());

CREATE VIEW rule_runtime.execution_work
WITH (security_invoker = true) AS
SELECT e.tenant_id, e.execution_id, item.value AS work
FROM rule_runtime.executions e
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(e.snapshot->'work','[]'::jsonb)) item(value);

CREATE VIEW rule_runtime.execution_continuations
WITH (security_invoker = true) AS
SELECT e.tenant_id, e.execution_id, item.value AS continuation
FROM rule_runtime.executions e
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(e.snapshot->'continuations','[]'::jsonb)) item(value);

CREATE VIEW rule_runtime.execution_effects
WITH (security_invoker = true) AS
SELECT e.tenant_id, e.execution_id, item.value AS effect
FROM rule_runtime.executions e
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(e.snapshot->'effects','[]'::jsonb)) item(value);

CREATE VIEW rule_runtime.execution_trace
WITH (security_invoker = true) AS
SELECT e.tenant_id, e.execution_id, item.value AS trace
FROM rule_runtime.executions e
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(e.snapshot->'trace','[]'::jsonb)) item(value);

CREATE VIEW rule_runtime.execution_state_transitions
WITH (security_invoker = true) AS
SELECT e.tenant_id, e.execution_id, item.value AS state_transition
FROM rule_runtime.executions e
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(e.snapshot->'stateTransitions','[]'::jsonb)) item(value);

CREATE VIEW rule_runtime.current_rule_state
WITH (security_invoker = true) AS
SELECT tenant_id, rule_revision_id, node_instance_id, scope_key, schema_version, revision, value, expires_at, updated_at
FROM rule_runtime.rule_states;

REVOKE ALL ON FUNCTION rule_runtime.is_uuid_v7(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION rule_runtime.current_tenant_id() FROM PUBLIC;
REVOKE ALL ON FUNCTION rule_runtime.reject_immutable_change() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION rule_runtime.is_uuid_v7(uuid), rule_runtime.current_tenant_id() TO rule_runtime_runtime;
GRANT SELECT ON rule_runtime.node_definitions TO rule_runtime_runtime;
GRANT SELECT, INSERT ON rule_runtime.rule_revisions, rule_runtime.rule_bindings TO rule_runtime_runtime;
GRANT SELECT, INSERT, UPDATE ON rule_runtime.executions, rule_runtime.ordering_locks, rule_runtime.rule_states TO rule_runtime_runtime;
GRANT DELETE ON rule_runtime.ordering_locks TO rule_runtime_runtime;
GRANT SELECT ON rule_runtime.execution_work, rule_runtime.execution_continuations, rule_runtime.execution_effects, rule_runtime.execution_trace, rule_runtime.execution_state_transitions, rule_runtime.current_rule_state TO rule_runtime_runtime;

RESET ROLE;
COMMIT;
