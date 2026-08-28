\set ON_ERROR_STOP on

BEGIN;

CREATE INDEX audit_records_tenant_occurred_idx
  ON audit_ledger.records (tenant_id, occurred_at DESC, ledger_sequence DESC);
CREATE INDEX audit_records_tenant_actor_occurred_idx
  ON audit_ledger.records (tenant_id, initiating_subject, occurred_at DESC);
CREATE INDEX audit_records_tenant_action_occurred_idx
  ON audit_ledger.records (tenant_id, action, occurred_at DESC);
CREATE INDEX audit_records_tenant_resource_occurred_idx
  ON audit_ledger.records (tenant_id, aggregate_type, aggregate_id, occurred_at DESC);
CREATE INDEX audit_records_tenant_outcome_occurred_idx
  ON audit_ledger.records (tenant_id, result, occurred_at DESC);

COMMIT;
