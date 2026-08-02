\set ON_ERROR_STOP on

BEGIN;

ALTER TABLE audit_ledger.inbox
  DROP CONSTRAINT IF EXISTS inbox_topic_partition_id_offset_value_key;

CREATE UNIQUE INDEX IF NOT EXISTS audit_inbox_kafka_offset_key
  ON audit_ledger.inbox (topic, partition_id, offset_value)
  WHERE topic <> 'operations-http';

ALTER TABLE audit_ledger.records
  DROP CONSTRAINT IF EXISTS records_aggregate_type_check,
  DROP CONSTRAINT IF EXISTS records_aggregate_type_aggregate_id_aggregate_version_key,
  ADD CONSTRAINT records_aggregate_type_check
    CHECK (aggregate_type IN ('bff-session', 'operations-investigation'));

CREATE UNIQUE INDEX IF NOT EXISTS audit_records_session_aggregate_version_key
  ON audit_ledger.records (aggregate_type, aggregate_id, aggregate_version)
  WHERE aggregate_type = 'bff-session';

COMMIT;
