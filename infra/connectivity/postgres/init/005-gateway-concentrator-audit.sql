\set ON_ERROR_STOP on
BEGIN;
SET LOCAL ROLE connectivity_migrator;

CREATE TABLE connectivity.gateway_concentrator_audit (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  unit_id integer NOT NULL CHECK (unit_id BETWEEN 1 AND 247 AND unit_id NOT IN (2, 3, 4, 5, 10, 31)),
  actor_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(actor_id)),
  operation text NOT NULL CHECK (operation IN ('WRITE_PARAMS', 'WRITE_EIDS')),
  request_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(request_id)),
  outcome text NOT NULL CHECK (outcome IN ('APPLIED', 'REJECTED', 'FAILED', 'TIMEOUT')),
  reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 512),
  diff jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(diff) = 'object'),
  occurred_at timestamptz NOT NULL
);
CREATE INDEX gateway_concentrator_audit_lookup_idx
  ON connectivity.gateway_concentrator_audit (tenant_id, gateway_id, unit_id, occurred_at DESC, id DESC);
ALTER TABLE connectivity.gateway_concentrator_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_concentrator_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY gateway_concentrator_audit_tenant ON connectivity.gateway_concentrator_audit
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON connectivity.gateway_concentrator_audit TO connectivity_runtime;
COMMIT;
