BEGIN;

-- Simulate the previous compatible writers omitting the newly expanded traceparent column.
SET LOCAL ROLE gateway_runtime;
INSERT INTO gateway.audit_intents (
  message_id, session_aggregate_id, organization_id, aggregate_version,
  initiating_subject, initiating_issuer, executing_service, executing_spiffe_id,
  action, result, policy_revision, correlation_id, causation_id, trace_id,
  payload_sha256, occurred_at
) VALUES (
  'compat-gateway-message-00000001', repeat('a', 64), 'compat-org', 1,
  'compat-subject', 'compat-issuer', 'platform-gateway', 'spiffe://hvac.local/platform-gateway',
  'SESSION_CREATED', 'SUCCESS', 'policy-v1', 'compat-correlation', '', repeat('b', 32),
  repeat('c', 64), clock_timestamp()
);
INSERT INTO gateway.outbox (
  message_id, topic, partition_key, schema_version, aggregate_type, aggregate_id,
  aggregate_version, organization_id, correlation_id, causation_id, trace_id,
  payload, envelope_sha256, created_at, available_at
) VALUES (
  'compat-gateway-message-00000001', 'control.security.session.v1',
  'bff-session:' || repeat('a', 64), 1, 'bff-session', repeat('a', 64),
  1, 'compat-org', 'compat-correlation', '', repeat('b', 32),
  decode('00', 'hex'), repeat('d', 64), clock_timestamp(), clock_timestamp()
);
DO $$
BEGIN
  IF (SELECT traceparent FROM gateway.audit_intents WHERE message_id = 'compat-gateway-message-00000001') <> '' THEN
    RAISE EXCEPTION 'previous Gateway writer did not receive traceparent default';
  END IF;
  IF (SELECT traceparent FROM gateway.outbox WHERE message_id = 'compat-gateway-message-00000001') <> '' THEN
    RAISE EXCEPTION 'previous Outbox writer did not receive traceparent default';
  END IF;
END
$$;

RESET ROLE;
SET LOCAL app.organization_id = 'compat-org';
SET LOCAL ROLE audit_consumer_runtime;
INSERT INTO audit_ledger.inbox (
  message_id, organization_id, topic, partition_id, offset_value, envelope_sha256, received_at
) VALUES (
  'compat-audit-message-00000001', 'compat-org', 'control.security.session.v1', 0, 999999,
  repeat('e', 64), clock_timestamp()
);
INSERT INTO audit_ledger.organization_heads (organization_id, last_record_hash, updated_at)
VALUES ('compat-org', repeat('0', 64), clock_timestamp());
INSERT INTO audit_ledger.records (
  message_id, schema_version, organization_id, aggregate_type, aggregate_id,
  aggregate_version, occurred_at, initiating_subject, initiating_issuer,
  executing_service, executing_spiffe_id, acting_organization_id, action, result,
  policy_revision, correlation_id, causation_id, trace_id, payload_sha256,
  previous_record_hash, record_hash, recorded_at
) VALUES (
  'compat-audit-message-00000001', 1, 'compat-org', 'bff-session', repeat('f', 64),
  1, clock_timestamp(), 'compat-subject', 'compat-issuer', 'platform-gateway',
  'spiffe://hvac.local/platform-gateway', 'compat-org', 'SESSION_CREATED', 'SUCCESS',
  'policy-v1', 'compat-correlation', '', repeat('1', 32), repeat('2', 64),
  repeat('0', 64), repeat('3', 64), clock_timestamp()
);
DO $$
BEGIN
  IF (SELECT traceparent FROM audit_ledger.records WHERE message_id = 'compat-audit-message-00000001') <> '' THEN
    RAISE EXCEPTION 'previous Audit writer did not receive traceparent default';
  END IF;
END
$$;

ROLLBACK;
