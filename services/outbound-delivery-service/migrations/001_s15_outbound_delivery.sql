BEGIN;

GRANT USAGE ON SCHEMA iam, core_registry TO outbound_delivery_migrator;
GRANT REFERENCES (id) ON iam.tenants TO outbound_delivery_migrator;
GRANT REFERENCES (tenant_id, id) ON core_registry.sites TO outbound_delivery_migrator;

SET LOCAL ROLE outbound_delivery_migrator;

CREATE OR REPLACE FUNCTION outbound_delivery.is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
AS $fn$
  SELECT substring(value::text, 15, 1) = '7'
$fn$;

CREATE OR REPLACE FUNCTION outbound_delivery.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $fn$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$fn$;

REVOKE ALL ON FUNCTION outbound_delivery.is_uuid_v7(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION outbound_delivery.current_tenant_id() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION outbound_delivery.is_uuid_v7(uuid) TO outbound_delivery_runtime;
GRANT EXECUTE ON FUNCTION outbound_delivery.current_tenant_id() TO outbound_delivery_runtime;

CREATE TABLE outbound_delivery.integration_definitions (
  id uuid PRIMARY KEY CHECK (outbound_delivery.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (outbound_delivery.is_uuid_v7(tenant_id)),
  name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 256),
  current_revision bigint NOT NULL CHECK (current_revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, name),
  CHECK (updated_at >= created_at)
);

CREATE TABLE outbound_delivery.integration_definition_revisions (
  integration_id uuid NOT NULL,
  tenant_id uuid NOT NULL CHECK (outbound_delivery.is_uuid_v7(tenant_id)),
  revision bigint NOT NULL CHECK (revision > 0),
  adapter_type text NOT NULL CHECK (adapter_type = 'REST_WEBHOOK'),
  destination_url text NOT NULL CHECK (char_length(destination_url) BETWEEN 1 AND 2048),
  allowed_hosts text[] NOT NULL CHECK (cardinality(allowed_hosts) BETWEEN 1 AND 32),
  credential_ref text CHECK (credential_ref IS NULL OR char_length(credential_ref) BETWEEN 1 AND 512),
  enabled boolean NOT NULL,
  max_request_bytes bigint NOT NULL CHECK (max_request_bytes BETWEEN 1 AND 1048576),
  max_response_bytes bigint NOT NULL CHECK (max_response_bytes BETWEEN 1 AND 262144),
  timeout_ms integer NOT NULL CHECK (timeout_ms BETWEEN 1 AND 30000),
  max_concurrency integer NOT NULL CHECK (max_concurrency BETWEEN 1 AND 32),
  max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 5),
  retry_delay_ms bigint NOT NULL CHECK (retry_delay_ms BETWEEN 1 AND 86400000),
  created_by_principal_id uuid CHECK (created_by_principal_id IS NULL OR outbound_delivery.is_uuid_v7(created_by_principal_id)),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (integration_id, revision),
  FOREIGN KEY (tenant_id, integration_id) REFERENCES outbound_delivery.integration_definitions(tenant_id, id),
  UNIQUE (tenant_id, integration_id, revision)
);

ALTER TABLE outbound_delivery.integration_definitions
  ADD CONSTRAINT integration_current_revision_fk
  FOREIGN KEY (tenant_id, id, current_revision)
  REFERENCES outbound_delivery.integration_definition_revisions(tenant_id, integration_id, revision)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE outbound_delivery.delivery_intents (
  id uuid PRIMARY KEY CHECK (outbound_delivery.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (outbound_delivery.is_uuid_v7(tenant_id)),
  site_id uuid,
  integration_id uuid NOT NULL CHECK (outbound_delivery.is_uuid_v7(integration_id)),
  purpose text NOT NULL CHECK (char_length(btrim(purpose)) BETWEEN 1 AND 128),
  payload_schema text NOT NULL CHECK (char_length(btrim(payload_schema)) BETWEEN 1 AND 128),
  payload jsonb NOT NULL,
  payload_digest text NOT NULL CHECK (payload_digest ~ '^[a-f0-9]{64}$'),
  idempotency_key text NOT NULL CHECK (char_length(btrim(idempotency_key)) BETWEEN 1 AND 256),
  source_aggregate_type text NOT NULL CHECK (char_length(btrim(source_aggregate_type)) BETWEEN 1 AND 128),
  source_aggregate_id text NOT NULL CHECK (char_length(btrim(source_aggregate_id)) BETWEEN 1 AND 256),
  classification text NOT NULL CHECK (char_length(btrim(classification)) BETWEEN 1 AND 64),
  state text NOT NULL CHECK (state IN ('READY','LEASED','RETRY_WAIT','DELIVERED','OUTCOME_UNKNOWN','DEAD')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  next_retry_at timestamptz,
  lease_owner text,
  lease_until timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, integration_id, idempotency_key),
  FOREIGN KEY (tenant_id, integration_id) REFERENCES outbound_delivery.integration_definitions(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK ((state = 'LEASED') = (lease_owner IS NOT NULL AND lease_until IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE outbound_delivery.delivery_attempts (
  id uuid PRIMARY KEY CHECK (outbound_delivery.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (outbound_delivery.is_uuid_v7(tenant_id)),
  intent_id uuid NOT NULL REFERENCES outbound_delivery.delivery_intents(id),
  attempt_no integer NOT NULL CHECK (attempt_no > 0),
  integration_revision bigint NOT NULL CHECK (integration_revision > 0),
  outcome text NOT NULL CHECK (outcome IN ('NOT_SENT','MAYBE_SENT','ACCEPTED_NOT_CONFIRMED','DELIVERED','FAILED')),
  retryable boolean NOT NULL DEFAULT false,
  error_code text CHECK (error_code IS NULL OR char_length(error_code) BETWEEN 1 AND 128),
  provider_request_id text CHECK (provider_request_id IS NULL OR char_length(provider_request_id) <= 256),
  http_status integer CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
  response_digest text CHECK (response_digest IS NULL OR response_digest ~ '^[a-f0-9]{64}$'),
  started_at timestamptz NOT NULL,
  completed_at timestamptz,
  lease_owner text,
  lease_until timestamptz,
  UNIQUE (tenant_id, id),
  UNIQUE (intent_id, attempt_no),
  FOREIGN KEY (tenant_id, intent_id) REFERENCES outbound_delivery.delivery_intents(tenant_id, id),
  CHECK ((completed_at IS NULL) = (lease_owner IS NOT NULL AND lease_until IS NOT NULL))
);

CREATE TABLE outbound_delivery.delivery_receipts (
  id uuid PRIMARY KEY CHECK (outbound_delivery.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (outbound_delivery.is_uuid_v7(tenant_id)),
  intent_id uuid NOT NULL,
  attempt_id uuid NOT NULL UNIQUE,
  provider_request_id text CHECK (provider_request_id IS NULL OR char_length(provider_request_id) <= 256),
  provider_message_id text CHECK (provider_message_id IS NULL OR char_length(provider_message_id) <= 256),
  http_status integer CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
  response_digest text CHECK (response_digest IS NULL OR response_digest ~ '^[a-f0-9]{64}$'),
  final_outcome text NOT NULL CHECK (final_outcome IN ('DELIVERED','ACCEPTED_NOT_CONFIRMED')),
  accepted_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, intent_id) REFERENCES outbound_delivery.delivery_intents(tenant_id, id),
  FOREIGN KEY (tenant_id, attempt_id) REFERENCES outbound_delivery.delivery_attempts(tenant_id, id)
);

CREATE TABLE outbound_delivery.dead_letters (
  id uuid PRIMARY KEY CHECK (outbound_delivery.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (outbound_delivery.is_uuid_v7(tenant_id)),
  intent_id uuid NOT NULL,
  attempt_id uuid NOT NULL UNIQUE,
  reason_code text NOT NULL CHECK (char_length(btrim(reason_code)) BETWEEN 1 AND 128),
  requires_duplicate_risk_ack boolean NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, intent_id) REFERENCES outbound_delivery.delivery_intents(tenant_id, id),
  FOREIGN KEY (tenant_id, attempt_id) REFERENCES outbound_delivery.delivery_attempts(tenant_id, id)
);

CREATE TABLE outbound_delivery.replay_approvals (
  id uuid PRIMARY KEY CHECK (outbound_delivery.is_uuid_v7(id)),
  tenant_id uuid NOT NULL REFERENCES iam.tenants(id) CHECK (outbound_delivery.is_uuid_v7(tenant_id)),
  dead_letter_id uuid NOT NULL UNIQUE,
  intent_id uuid NOT NULL,
  approved_by_principal_id uuid NOT NULL CHECK (outbound_delivery.is_uuid_v7(approved_by_principal_id)),
  reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 1024),
  accept_duplicate_risk boolean NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, dead_letter_id) REFERENCES outbound_delivery.dead_letters(tenant_id, id),
  FOREIGN KEY (tenant_id, intent_id) REFERENCES outbound_delivery.delivery_intents(tenant_id, id)
);

CREATE INDEX delivery_intents_ready_idx
  ON outbound_delivery.delivery_intents (tenant_id, state, next_retry_at, created_at, id)
  WHERE state IN ('READY','RETRY_WAIT');
CREATE INDEX delivery_attempts_expired_lease_idx
  ON outbound_delivery.delivery_attempts (tenant_id, lease_until, id)
  WHERE completed_at IS NULL;
CREATE INDEX delivery_receipts_intent_idx
  ON outbound_delivery.delivery_receipts (tenant_id, intent_id, accepted_at);
CREATE INDEX dead_letters_intent_idx
  ON outbound_delivery.dead_letters (tenant_id, intent_id, created_at);

CREATE OR REPLACE FUNCTION outbound_delivery.enforce_attempt_completion_once()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
  IF OLD.completed_at IS NOT NULL THEN
    RAISE EXCEPTION 'completed delivery attempts are immutable';
  END IF;
  IF NEW.id <> OLD.id
     OR NEW.tenant_id <> OLD.tenant_id
     OR NEW.intent_id <> OLD.intent_id
     OR NEW.attempt_no <> OLD.attempt_no
     OR NEW.integration_revision <> OLD.integration_revision
     OR NEW.started_at <> OLD.started_at THEN
    RAISE EXCEPTION 'delivery attempt identity and frozen revision are immutable';
  END IF;
  IF NEW.completed_at IS NULL THEN
    RAISE EXCEPTION 'delivery attempt update must complete the attempt';
  END IF;
  RETURN NEW;
END
$fn$;

CREATE TRIGGER delivery_attempt_complete_once
BEFORE UPDATE ON outbound_delivery.delivery_attempts
FOR EACH ROW EXECUTE FUNCTION outbound_delivery.enforce_attempt_completion_once();

REVOKE ALL ON FUNCTION outbound_delivery.enforce_attempt_completion_once() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION outbound_delivery.enforce_attempt_completion_once() TO outbound_delivery_runtime;

ALTER TABLE outbound_delivery.integration_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.integration_definitions FORCE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.integration_definition_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.integration_definition_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.delivery_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.delivery_intents FORCE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.delivery_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.delivery_attempts FORCE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.delivery_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.delivery_receipts FORCE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.dead_letters ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.dead_letters FORCE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.replay_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_delivery.replay_approvals FORCE ROW LEVEL SECURITY;

CREATE POLICY integration_definitions_tenant_scope ON outbound_delivery.integration_definitions
  FOR ALL TO outbound_delivery_runtime
  USING (tenant_id = outbound_delivery.current_tenant_id())
  WITH CHECK (tenant_id = outbound_delivery.current_tenant_id());
CREATE POLICY integration_definition_revisions_tenant_scope ON outbound_delivery.integration_definition_revisions
  FOR ALL TO outbound_delivery_runtime
  USING (tenant_id = outbound_delivery.current_tenant_id())
  WITH CHECK (tenant_id = outbound_delivery.current_tenant_id());
CREATE POLICY delivery_intents_tenant_scope ON outbound_delivery.delivery_intents
  FOR ALL TO outbound_delivery_runtime
  USING (tenant_id = outbound_delivery.current_tenant_id())
  WITH CHECK (tenant_id = outbound_delivery.current_tenant_id());
CREATE POLICY delivery_attempts_tenant_scope ON outbound_delivery.delivery_attempts
  FOR ALL TO outbound_delivery_runtime
  USING (tenant_id = outbound_delivery.current_tenant_id())
  WITH CHECK (tenant_id = outbound_delivery.current_tenant_id());
CREATE POLICY delivery_receipts_tenant_scope ON outbound_delivery.delivery_receipts
  FOR ALL TO outbound_delivery_runtime
  USING (tenant_id = outbound_delivery.current_tenant_id())
  WITH CHECK (tenant_id = outbound_delivery.current_tenant_id());
CREATE POLICY dead_letters_tenant_scope ON outbound_delivery.dead_letters
  FOR ALL TO outbound_delivery_runtime
  USING (tenant_id = outbound_delivery.current_tenant_id())
  WITH CHECK (tenant_id = outbound_delivery.current_tenant_id());
CREATE POLICY replay_approvals_tenant_scope ON outbound_delivery.replay_approvals
  FOR ALL TO outbound_delivery_runtime
  USING (tenant_id = outbound_delivery.current_tenant_id())
  WITH CHECK (tenant_id = outbound_delivery.current_tenant_id());

REVOKE ALL ON ALL TABLES IN SCHEMA outbound_delivery FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON outbound_delivery.integration_definitions TO outbound_delivery_runtime;
GRANT SELECT, INSERT ON outbound_delivery.integration_definition_revisions TO outbound_delivery_runtime;
GRANT SELECT, INSERT, UPDATE ON outbound_delivery.delivery_intents TO outbound_delivery_runtime;
GRANT SELECT, INSERT, UPDATE ON outbound_delivery.delivery_attempts TO outbound_delivery_runtime;
GRANT SELECT, INSERT ON outbound_delivery.delivery_receipts TO outbound_delivery_runtime;
GRANT SELECT, INSERT ON outbound_delivery.dead_letters TO outbound_delivery_runtime;
GRANT SELECT, INSERT ON outbound_delivery.replay_approvals TO outbound_delivery_runtime;

RESET ROLE;
COMMIT;
