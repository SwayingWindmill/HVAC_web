\set ON_ERROR_STOP on
BEGIN;
SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.gateway_credentials
  ADD COLUMN IF NOT EXISTS certificate_serial_hex text;

ALTER TABLE connectivity.gateway_credentials
  DROP CONSTRAINT IF EXISTS gateway_credentials_serial_hex_check;

ALTER TABLE connectivity.gateway_credentials
  ADD CONSTRAINT gateway_credentials_serial_hex_check
  CHECK (certificate_serial_hex IS NULL OR certificate_serial_hex ~ '^[0-9a-f]{1,64}$');

ALTER TABLE connectivity.gateway_credentials
  ADD COLUMN IF NOT EXISTS revocation_reason text;

ALTER TABLE connectivity.gateway_credentials
  DROP CONSTRAINT IF EXISTS gateway_credentials_revocation_reason_check;

ALTER TABLE connectivity.gateway_credentials
  ADD CONSTRAINT gateway_credentials_revocation_reason_check
  CHECK (revocation_reason IS NULL OR length(btrim(revocation_reason)) BETWEEN 1 AND 512);

ALTER TABLE connectivity.gateway_credential_audit
  ADD COLUMN IF NOT EXISTS credential_id uuid CHECK (credential_id IS NULL OR connectivity.is_uuid_v7(credential_id));

ALTER TABLE connectivity.gateway_credential_audit
  DROP CONSTRAINT IF EXISTS gateway_credential_audit_action_check;

ALTER TABLE connectivity.gateway_credential_audit
  ADD CONSTRAINT gateway_credential_audit_action_check
  CHECK (action IN ('CODE_GENERATED','ISSUED','RENEWED','REVOKED','CERTIFICATE_REVOKED','IDENTITY_REENABLED'));

DROP POLICY IF EXISTS gateway_credentials_migrator_crl_read ON connectivity.gateway_credentials;
CREATE POLICY gateway_credentials_migrator_crl_read ON connectivity.gateway_credentials
  FOR SELECT TO connectivity_migrator
  USING (true);

CREATE OR REPLACE FUNCTION connectivity.list_revoked_certificate_serials(reference_time timestamptz)
RETURNS TABLE (certificate_serial_hex text, revoked_at timestamptz)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  SELECT gc.certificate_serial_hex, gc.revoked_at
  FROM connectivity.gateway_credentials gc
  WHERE gc.status = 'REVOKED'
    AND gc.certificate_serial_hex IS NOT NULL
    AND gc.revoked_at IS NOT NULL
    AND gc.valid_until > reference_time
  ORDER BY gc.revoked_at DESC;
$$;

REVOKE ALL ON FUNCTION connectivity.list_revoked_certificate_serials(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION connectivity.list_revoked_certificate_serials(timestamptz) TO connectivity_runtime;

COMMENT ON COLUMN connectivity.gateway_credentials.certificate_serial_hex IS 'mTLS 客户端证书十六进制序列号，用于单证吊销与 CRL 导出。';
COMMENT ON COLUMN connectivity.gateway_credentials.revocation_reason IS '单张客户端证书或接入身份被吊销时记录的操作原因。';
COMMENT ON COLUMN connectivity.gateway_credential_audit.credential_id IS '关联的客户端证书记录 ID，用于追踪单证签发、续期与吊销审计事件。';
COMMENT ON FUNCTION connectivity.list_revoked_certificate_serials(timestamptz) IS '按参考时间返回尚未过期且已吊销的网关客户端证书序列号，供导出签名 CRL 使用。';

COMMIT;
