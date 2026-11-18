\set ON_ERROR_STOP on
BEGIN;
SET LOCAL ROLE connectivity_migrator;

ALTER TABLE connectivity.command_reply_correlations
    ADD COLUMN IF NOT EXISTS trace_id text NOT NULL DEFAULT ''
    CHECK (trace_id = '' OR trace_id ~ '^[0-9a-f]{32}$');

COMMENT ON COLUMN connectivity.command_reply_correlations.trace_id IS '控制指令下发关联记录对应的 W3C 32 位十六进制分布式追踪编号（未携带追踪上下文时为空字符串）。';

CREATE OR REPLACE FUNCTION connectivity.prevent_audit_row_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'connectivity audit table % is append-only; % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

COMMENT ON FUNCTION connectivity.prevent_audit_row_mutation() IS '阻止连接层只追加审计表执行行级更新或删除操作。';

DROP TRIGGER IF EXISTS trg_gateway_credential_audit_immutable ON connectivity.gateway_credential_audit;
CREATE TRIGGER trg_gateway_credential_audit_immutable
    BEFORE UPDATE OR DELETE ON connectivity.gateway_credential_audit
    FOR EACH ROW EXECUTE FUNCTION connectivity.prevent_audit_row_mutation();

DROP TRIGGER IF EXISTS trg_gateway_concentrator_audit_immutable ON connectivity.gateway_concentrator_audit;
CREATE TRIGGER trg_gateway_concentrator_audit_immutable
    BEFORE UPDATE OR DELETE ON connectivity.gateway_concentrator_audit
    FOR EACH ROW EXECUTE FUNCTION connectivity.prevent_audit_row_mutation();

DROP TRIGGER IF EXISTS trg_gateway_tunnel_audit_immutable ON connectivity.gateway_tunnel_audit;
CREATE TRIGGER trg_gateway_tunnel_audit_immutable
    BEFORE UPDATE OR DELETE ON connectivity.gateway_tunnel_audit
    FOR EACH ROW EXECUTE FUNCTION connectivity.prevent_audit_row_mutation();

REVOKE TRUNCATE ON
    connectivity.gateway_credential_audit,
    connectivity.gateway_concentrator_audit,
    connectivity.gateway_tunnel_audit
    FROM connectivity_runtime;

RESET ROLE;
COMMIT;
