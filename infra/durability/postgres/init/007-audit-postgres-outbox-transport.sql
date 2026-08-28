\set ON_ERROR_STOP on

BEGIN;

-- PostgreSQL outbox delivery has no Kafka partition/offset identity. Migration 003
-- introduced the Kafka transport uniqueness index, while migration 004 switched
-- the inbox back to generic transport metadata but did not drop that index.
DROP INDEX IF EXISTS audit_ledger.audit_inbox_kafka_offset_key;

CREATE INDEX IF NOT EXISTS audit_inbox_transport_metadata_idx
  ON audit_ledger.inbox (topic, partition_id, offset_value);

COMMIT;
