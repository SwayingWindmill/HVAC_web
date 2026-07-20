BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gateway_runtime') THEN
    CREATE ROLE gateway_runtime LOGIN PASSWORD 'gateway-runtime-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gateway_relay_runtime') THEN
    CREATE ROLE gateway_relay_runtime LOGIN PASSWORD 'gateway-relay-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'audit_consumer_runtime') THEN
    CREATE ROLE audit_consumer_runtime LOGIN PASSWORD 'audit-consumer-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'audit_query_runtime') THEN
    CREATE ROLE audit_query_runtime LOGIN PASSWORD 'audit-query-local-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

CREATE SCHEMA IF NOT EXISTS gateway AUTHORIZATION postgres;
CREATE SCHEMA IF NOT EXISTS audit_ledger AUTHORIZATION postgres;
REVOKE ALL ON SCHEMA gateway FROM PUBLIC;
REVOKE ALL ON SCHEMA audit_ledger FROM PUBLIC;

CREATE TABLE IF NOT EXISTS gateway.sessions (
  session_id text PRIMARY KEY,
  principal_subject text NOT NULL,
  principal_issuer text NOT NULL,
  display_name text NOT NULL,
  email text NOT NULL,
  roles jsonb NOT NULL CHECK (jsonb_typeof(roles) = 'array'),
  acting_organization_id text NOT NULL,
  csrf_token_ciphertext bytea NOT NULL,
  provider_tokens_ciphertext bytea NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  last_audit_message_id text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS sessions_active_idx
  ON gateway.sessions (expires_at)
  WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS gateway.audit_intents (
  message_id text PRIMARY KEY,
  session_aggregate_id text NOT NULL CHECK (session_aggregate_id ~ '^[a-f0-9]{64}$'),
  organization_id text NOT NULL,
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  initiating_subject text NOT NULL,
  initiating_issuer text NOT NULL,
  executing_service text NOT NULL,
  executing_spiffe_id text NOT NULL,
  action text NOT NULL,
  result text NOT NULL,
  policy_revision text NOT NULL,
  correlation_id text NOT NULL,
  causation_id text NOT NULL DEFAULT '',
  trace_id text NOT NULL,
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[a-f0-9]{64}$'),
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (session_aggregate_id, aggregate_version)
);

CREATE TABLE IF NOT EXISTS gateway.outbox (
  message_id text PRIMARY KEY REFERENCES gateway.audit_intents(message_id),
  topic text NOT NULL,
  partition_key text NOT NULL,
  schema_version integer NOT NULL CHECK (schema_version = 1),
  aggregate_type text NOT NULL CHECK (aggregate_type = 'bff-session'),
  aggregate_id text NOT NULL CHECK (length(aggregate_id) = 64 AND aggregate_id !~ '[^a-f0-9]'),
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  organization_id text NOT NULL,
  payload bytea NOT NULL,
  envelope_sha256 text NOT NULL CHECK (envelope_sha256 ~ '^[a-f0-9]{64}$'),
  created_at timestamptz NOT NULL,
  available_at timestamptz NOT NULL,
  publish_attempts integer NOT NULL DEFAULT 0 CHECK (publish_attempts >= 0),
  published_at timestamptz,
  last_error_code text NOT NULL DEFAULT '',
  claim_owner text NOT NULL DEFAULT '',
  claim_expires_at timestamptz,
  UNIQUE (aggregate_type, aggregate_id, aggregate_version),
  CHECK (partition_key = aggregate_type || ':' || aggregate_id)
);

CREATE INDEX IF NOT EXISTS outbox_pending_idx
  ON gateway.outbox (available_at, created_at)
  WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS gateway.route_audit_records (
  message_id text PRIMARY KEY,
  event_type text NOT NULL CHECK (event_type IN ('ROUTE_DECIDED', 'ROUTE_POLICY_CHANGED')),
  route_key text NOT NULL,
  method text NOT NULL,
  path_template text NOT NULL,
  selected_owner text NOT NULL,
  previous_owner text NOT NULL DEFAULT '',
  registry_revision bigint NOT NULL CHECK (registry_revision > 0),
  previous_revision bigint NOT NULL DEFAULT 0 CHECK (previous_revision >= 0),
  route_revision bigint NOT NULL CHECK (route_revision > 0),
  compatibility_mode text NOT NULL,
  cohort_bucket integer CHECK (cohort_bucket BETWEEN 0 AND 99),
  organization_id text NOT NULL DEFAULT '',
  initiating_subject text NOT NULL DEFAULT '',
  initiating_issuer text NOT NULL DEFAULT '',
  executing_service text NOT NULL,
  executing_spiffe_id text NOT NULL DEFAULT '',
  policy_revision text NOT NULL DEFAULT '',
  correlation_id text NOT NULL DEFAULT '',
  trace_id text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS route_audit_route_time_idx
  ON gateway.route_audit_records (route_key, occurred_at DESC);

CREATE OR REPLACE FUNCTION gateway.reject_route_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS 'BEGIN
  RAISE EXCEPTION ''route audit records are append-only'' USING ERRCODE = ''55000'';
END';

DROP TRIGGER IF EXISTS route_audit_append_only ON gateway.route_audit_records;
CREATE TRIGGER route_audit_append_only
BEFORE UPDATE OR DELETE ON gateway.route_audit_records
FOR EACH ROW EXECUTE FUNCTION gateway.reject_route_audit_mutation();

CREATE TABLE IF NOT EXISTS audit_ledger.inbox (
  message_id text PRIMARY KEY,
  organization_id text NOT NULL,
  topic text NOT NULL,
  partition_id integer NOT NULL,
  offset_value bigint NOT NULL,
  envelope_sha256 text NOT NULL CHECK (envelope_sha256 ~ '^[a-f0-9]{64}$'),
  received_at timestamptz NOT NULL,
  UNIQUE (topic, partition_id, offset_value)
);

CREATE TABLE IF NOT EXISTS audit_ledger.organization_heads (
  organization_id text PRIMARY KEY,
  last_record_hash text NOT NULL CHECK (last_record_hash ~ '^[a-f0-9]{64}$'),
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_ledger.records (
  ledger_sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  message_id text NOT NULL UNIQUE REFERENCES audit_ledger.inbox(message_id),
  schema_version integer NOT NULL CHECK (schema_version = 1),
  organization_id text NOT NULL,
  aggregate_type text NOT NULL CHECK (aggregate_type = 'bff-session'),
  aggregate_id text NOT NULL CHECK (length(aggregate_id) = 64 AND aggregate_id !~ '[^a-f0-9]'),
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  occurred_at timestamptz NOT NULL,
  initiating_subject text NOT NULL,
  initiating_issuer text NOT NULL,
  executing_service text NOT NULL,
  executing_spiffe_id text NOT NULL,
  acting_organization_id text NOT NULL,
  action text NOT NULL,
  result text NOT NULL,
  policy_revision text NOT NULL,
  correlation_id text NOT NULL,
  causation_id text NOT NULL DEFAULT '',
  trace_id text NOT NULL,
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[a-f0-9]{64}$'),
  previous_record_hash text NOT NULL CHECK (previous_record_hash ~ '^[a-f0-9]{64}$'),
  record_hash text NOT NULL UNIQUE CHECK (record_hash ~ '^[a-f0-9]{64}$'),
  recorded_at timestamptz NOT NULL,
  UNIQUE (aggregate_type, aggregate_id, aggregate_version),
  CHECK (organization_id = acting_organization_id)
);

CREATE INDEX IF NOT EXISTS audit_records_org_time_idx
  ON audit_ledger.records (organization_id, occurred_at DESC, ledger_sequence DESC);

CREATE OR REPLACE FUNCTION audit_ledger.reject_record_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'audit ledger records are append-only' USING ERRCODE = '55000';
END
$$;

DROP TRIGGER IF EXISTS audit_records_append_only ON audit_ledger.records;
CREATE TRIGGER audit_records_append_only
BEFORE UPDATE OR DELETE ON audit_ledger.records
FOR EACH ROW EXECUTE FUNCTION audit_ledger.reject_record_mutation();

ALTER TABLE audit_ledger.inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_ledger.organization_heads ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_ledger.records ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_ledger.inbox FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_ledger.organization_heads FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_ledger.records FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS inbox_consumer_scope ON audit_ledger.inbox;
CREATE POLICY inbox_consumer_scope ON audit_ledger.inbox
  FOR ALL TO audit_consumer_runtime
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

DROP POLICY IF EXISTS heads_consumer_scope ON audit_ledger.organization_heads;
CREATE POLICY heads_consumer_scope ON audit_ledger.organization_heads
  FOR ALL TO audit_consumer_runtime
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

DROP POLICY IF EXISTS records_consumer_scope ON audit_ledger.records;
CREATE POLICY records_consumer_scope ON audit_ledger.records
  FOR ALL TO audit_consumer_runtime
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

DROP POLICY IF EXISTS records_query_scope ON audit_ledger.records;
CREATE POLICY records_query_scope ON audit_ledger.records
  FOR SELECT TO audit_query_runtime
  USING (organization_id = current_setting('app.organization_id', true));

GRANT CONNECT ON DATABASE hvac_s0 TO gateway_runtime, gateway_relay_runtime, audit_consumer_runtime, audit_query_runtime;
GRANT USAGE ON SCHEMA gateway TO gateway_runtime, gateway_relay_runtime;
GRANT SELECT, INSERT, UPDATE ON gateway.sessions TO gateway_runtime;
GRANT SELECT, INSERT ON gateway.audit_intents TO gateway_runtime;
GRANT SELECT, INSERT ON gateway.outbox TO gateway_runtime;
GRANT SELECT, INSERT ON gateway.route_audit_records TO gateway_runtime;
GRANT SELECT, UPDATE ON gateway.outbox TO gateway_relay_runtime;

GRANT USAGE ON SCHEMA audit_ledger TO audit_consumer_runtime, audit_query_runtime;
GRANT SELECT, INSERT ON audit_ledger.inbox TO audit_consumer_runtime;
GRANT SELECT, INSERT, UPDATE ON audit_ledger.organization_heads TO audit_consumer_runtime;
GRANT SELECT, INSERT ON audit_ledger.records TO audit_consumer_runtime;
GRANT USAGE, SELECT ON SEQUENCE audit_ledger.records_ledger_sequence_seq TO audit_consumer_runtime;
GRANT SELECT ON audit_ledger.records TO audit_query_runtime;

REVOKE ALL ON ALL TABLES IN SCHEMA gateway FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA audit_ledger FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA gateway FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA audit_ledger FROM PUBLIC;

COMMIT;
