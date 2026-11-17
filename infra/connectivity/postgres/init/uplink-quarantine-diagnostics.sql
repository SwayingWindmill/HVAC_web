\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.uplink_quarantine
  ADD COLUMN IF NOT EXISTS occurrence_count bigint NOT NULL DEFAULT 1;

ALTER TABLE connectivity.uplink_quarantine
  DROP CONSTRAINT IF EXISTS uplink_quarantine_occurrence_count_check;

ALTER TABLE connectivity.uplink_quarantine
  ADD CONSTRAINT uplink_quarantine_occurrence_count_check
  CHECK (occurrence_count >= 1);

ALTER TABLE connectivity.uplink_quarantine
  ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;

DROP POLICY IF EXISTS uplink_quarantine_migrator_maintenance ON connectivity.uplink_quarantine;
CREATE POLICY uplink_quarantine_migrator_maintenance ON connectivity.uplink_quarantine
  FOR ALL TO connectivity_migrator
  USING (true)
  WITH CHECK (true);

UPDATE connectivity.uplink_quarantine
SET last_seen_at = received_at
WHERE last_seen_at IS NULL;

ALTER TABLE connectivity.uplink_quarantine
  ALTER COLUMN last_seen_at SET NOT NULL;

ALTER TABLE connectivity.uplink_quarantine
  ADD COLUMN IF NOT EXISTS payload_preview text NOT NULL DEFAULT '';

ALTER TABLE connectivity.uplink_quarantine
  DROP CONSTRAINT IF EXISTS uplink_quarantine_payload_preview_len_check;

ALTER TABLE connectivity.uplink_quarantine
  ADD CONSTRAINT uplink_quarantine_payload_preview_len_check
  CHECK (octet_length(payload_preview) <= 4096);

ALTER TABLE connectivity.uplink_quarantine
  ADD COLUMN IF NOT EXISTS payload_encoding text NOT NULL DEFAULT 'empty';

ALTER TABLE connectivity.uplink_quarantine
  DROP CONSTRAINT IF EXISTS uplink_quarantine_payload_encoding_check;

ALTER TABLE connectivity.uplink_quarantine
  ADD CONSTRAINT uplink_quarantine_payload_encoding_check
  CHECK (payload_encoding IN ('utf8', 'hex', 'empty'));

ALTER TABLE connectivity.uplink_quarantine
  ADD COLUMN IF NOT EXISTS payload_truncated boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS uplink_quarantine_tenant_gateway_last_seen_idx
  ON connectivity.uplink_quarantine (tenant_id, gateway_id, last_seen_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS uplink_quarantine_dedup_lookup_idx
  ON connectivity.uplink_quarantine (gateway_id, reason_code, topic, payload_sha256, last_seen_at DESC);

CREATE INDEX IF NOT EXISTS uplink_quarantine_retention_idx
  ON connectivity.uplink_quarantine (last_seen_at ASC, id ASC);

GRANT SELECT, INSERT, UPDATE, DELETE ON connectivity.uplink_quarantine TO connectivity_runtime;

CREATE OR REPLACE FUNCTION connectivity.prune_expired_uplink_quarantine(cutoff timestamptz, batch_limit integer DEFAULT 500)
RETURNS bigint
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  WITH victims AS (
    SELECT q.id
    FROM connectivity.uplink_quarantine q
    WHERE q.last_seen_at < cutoff
    ORDER BY q.last_seen_at ASC, q.id ASC
    LIMIT LEAST(GREATEST(COALESCE(batch_limit, 500), 1), 2000)
  ),
  deleted AS (
    DELETE FROM connectivity.uplink_quarantine target
    USING victims
    WHERE target.id = victims.id
    RETURNING 1
  )
  SELECT count(*)::bigint FROM deleted;
$$;

REVOKE ALL ON FUNCTION connectivity.prune_expired_uplink_quarantine(timestamptz, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION connectivity.prune_expired_uplink_quarantine(timestamptz, integer) TO connectivity_runtime;

COMMENT ON COLUMN connectivity.uplink_quarantine.occurrence_count IS '同一网关、原因与报文摘要在去重窗口内累计命中的接收隔离次数。';
COMMENT ON COLUMN connectivity.uplink_quarantine.last_seen_at IS '该隔离报文最近一次被网关接入层接收到的时间（UTC），用于 7 天留存清理与去重排序。';
COMMENT ON COLUMN connectivity.uplink_quarantine.payload_preview IS '隔离报文的安全截断预览片段（不超过 4096 字节），用于现场排障定位。';
COMMENT ON COLUMN connectivity.uplink_quarantine.payload_encoding IS '报文预览片段编码方式：utf8、hex 或 empty。';
COMMENT ON COLUMN connectivity.uplink_quarantine.payload_truncated IS '原始隔离报文是否因超过 4096 字节预览上限而被截断。';
COMMENT ON FUNCTION connectivity.prune_expired_uplink_quarantine(timestamptz, integer) IS '按保留截止时间分批清理过期的网关上行接收隔离记录。';

RESET ROLE;

COMMIT;
