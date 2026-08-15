BEGIN;

ALTER TABLE audit_ledger.inbox
  DROP CONSTRAINT IF EXISTS inbox_topic_partition_id_offset_value_key;

CREATE INDEX IF NOT EXISTS audit_inbox_transport_metadata_idx
  ON audit_ledger.inbox (topic, partition_id, offset_value);

COMMIT;
