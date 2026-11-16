\set ON_ERROR_STOP on
BEGIN;
SET LOCAL ROLE connectivity_migrator;

CREATE TABLE IF NOT EXISTS connectivity.gateway_tunnel_audit (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid REFERENCES iam.tenants(id),
  gateway_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(gateway_id)),
  session_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(session_id)),
  event_type text NOT NULL CHECK (event_type IN ('SESSION_OPENED', 'SESSION_CLOSED', 'STREAM_OPENED', 'STREAM_REJECTED')),
  stream_id bigint CHECK (stream_id IS NULL OR stream_id >= 0),
  target_port integer CHECK (target_port IS NULL OR (target_port BETWEEN 1 AND 65535)),
  remote_addr text NOT NULL DEFAULT '' CHECK (length(remote_addr) <= 256),
  reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 512),
  bytes_tx bigint NOT NULL DEFAULT 0 CHECK (bytes_tx >= 0),
  bytes_rx bigint NOT NULL DEFAULT 0 CHECK (bytes_rx >= 0),
  occurred_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS gateway_tunnel_audit_lookup_idx
  ON connectivity.gateway_tunnel_audit (gateway_id, occurred_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS gateway_tunnel_audit_tenant_lookup_idx
  ON connectivity.gateway_tunnel_audit (tenant_id, gateway_id, occurred_at DESC);

ALTER TABLE connectivity.gateway_tunnel_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.gateway_tunnel_audit FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS gateway_tunnel_audit_migrator_all ON connectivity.gateway_tunnel_audit;
CREATE POLICY gateway_tunnel_audit_migrator_all ON connectivity.gateway_tunnel_audit
  FOR ALL TO connectivity_migrator
  USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS gateway_tunnel_audit_tenant_scope ON connectivity.gateway_tunnel_audit;
CREATE POLICY gateway_tunnel_audit_tenant_scope ON connectivity.gateway_tunnel_audit
  FOR ALL TO connectivity_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid OR tenant_id IS NULL)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid OR tenant_id IS NULL);

GRANT SELECT, INSERT ON connectivity.gateway_tunnel_audit TO connectivity_runtime;
REVOKE ALL ON connectivity.gateway_tunnel_audit FROM PUBLIC;

COMMENT ON TABLE connectivity.gateway_tunnel_audit IS '网关反向运维隧道会话建立、流打开拒绝与断开流量的审计记录，由设备接入服务写入。';

COMMIT;
