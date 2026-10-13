-- FDD owns its rules (spec docs/specs/fdd-rule-definition.md): each Tenant's chilled-water
-- low-delta-T rule as immutable revisions. An evaluation uses the revision in effect and its
-- finding cites it; changing the threshold or window publishes a new revision.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.fdd_rule_revisions (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  rule_code text NOT NULL CHECK (rule_code IN ('CHILLED_WATER_LOW_DELTA_T')),
  revision integer NOT NULL CHECK (revision > 0),
  minimum_delta_tc double precision NOT NULL CHECK (minimum_delta_tc > 0),
  window_minutes integer NOT NULL CHECK (window_minutes BETWEEN 1 AND 1440),
  effective_from timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, rule_code, revision)
);

ALTER TABLE core_registry.fdd_rule_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.fdd_rule_revisions FORCE ROW LEVEL SECURITY;

CREATE POLICY fdd_rule_revisions_runtime_scope ON core_registry.fdd_rule_revisions FOR SELECT TO fdd_runtime
  USING (tenant_id = core_registry.current_tenant_id());

REVOKE ALL ON core_registry.fdd_rule_revisions FROM PUBLIC;
GRANT SELECT ON core_registry.fdd_rule_revisions TO fdd_runtime;

RESET ROLE;
COMMIT;
