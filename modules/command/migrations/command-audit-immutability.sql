BEGIN;

SET LOCAL ROLE s3_command_migrator;

CREATE OR REPLACE FUNCTION command_runtime.prevent_audit_row_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'command audit table % is append-only; % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

COMMENT ON FUNCTION command_runtime.prevent_audit_row_mutation() IS '阻止指令域只追加审计与流转记录表执行行级更新或删除操作。';

DROP TRIGGER IF EXISTS trg_command_transitions_immutable ON command_runtime.command_transitions;
CREATE TRIGGER trg_command_transitions_immutable
    BEFORE UPDATE OR DELETE ON command_runtime.command_transitions
    FOR EACH ROW EXECUTE FUNCTION command_runtime.prevent_audit_row_mutation();

DROP TRIGGER IF EXISTS trg_command_grant_uses_immutable ON command_runtime.command_grant_uses;
CREATE TRIGGER trg_command_grant_uses_immutable
    BEFORE UPDATE OR DELETE ON command_runtime.command_grant_uses
    FOR EACH ROW EXECUTE FUNCTION command_runtime.prevent_audit_row_mutation();

CREATE OR REPLACE FUNCTION command_runtime.guard_connector_evidence_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'command audit table % is append-only; DELETE is not allowed', TG_TABLE_NAME
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.completed_at IS NOT NULL OR NEW.completed_at IS NULL THEN
        RAISE EXCEPTION 'command audit table % allows only a single completion transition from NULL completed_at', TG_TABLE_NAME
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.attempt_id IS DISTINCT FROM OLD.attempt_id OR
       NEW.execution_fence IS DISTINCT FROM OLD.execution_fence OR
       NEW.command_id IS DISTINCT FROM OLD.command_id OR
       NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
       NEW.site_id IS DISTINCT FROM OLD.site_id OR
       NEW.device_id IS DISTINCT FROM OLD.device_id OR
       NEW.external_device_id IS DISTINCT FROM OLD.external_device_id OR
       NEW.payload_hash IS DISTINCT FROM OLD.payload_hash OR
       NEW.mapping_revision IS DISTINCT FROM OLD.mapping_revision OR
       NEW.binding_revision IS DISTINCT FROM OLD.binding_revision OR
       NEW.provider_endpoint IS DISTINCT FROM OLD.provider_endpoint OR
       NEW.provider_method IS DISTINCT FROM OLD.provider_method OR
       NEW.request_sha256 IS DISTINCT FROM OLD.request_sha256 OR
       NEW.prepared_at IS DISTINCT FROM OLD.prepared_at THEN
        RAISE EXCEPTION 'command audit table % prepared fields are immutable after insert', TG_TABLE_NAME
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION command_runtime.guard_connector_evidence_mutation() IS '限制连接器执行凭据表仅允许从未完成到完成的一次性写入回执字段并禁止删除或篡改准备期字段。';

DROP TRIGGER IF EXISTS trg_connector_evidence_immutable ON command_runtime.connector_evidence;
CREATE TRIGGER trg_connector_evidence_immutable
    BEFORE UPDATE OR DELETE ON command_runtime.connector_evidence
    FOR EACH ROW EXECUTE FUNCTION command_runtime.guard_connector_evidence_mutation();

CREATE OR REPLACE FUNCTION command_runtime.guard_command_audit_intent_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'command audit table % is append-only; DELETE is not allowed', TG_TABLE_NAME
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.relayed_at IS NOT NULL OR NEW.relayed_at IS NULL THEN
        RAISE EXCEPTION 'command audit table % allows only a single relay transition from NULL relayed_at', TG_TABLE_NAME
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.audit_intent_id IS DISTINCT FROM OLD.audit_intent_id OR
       NEW.command_id IS DISTINCT FROM OLD.command_id OR
       NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
       NEW.site_id IS DISTINCT FROM OLD.site_id OR
       NEW.device_id IS DISTINCT FROM OLD.device_id OR
       NEW.event_kind IS DISTINCT FROM OLD.event_kind OR
       NEW.payload_hash IS DISTINCT FROM OLD.payload_hash OR
       NEW.redacted_payload IS DISTINCT FROM OLD.redacted_payload OR
       NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'command audit table % payload fields are immutable after insert', TG_TABLE_NAME
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION command_runtime.guard_command_audit_intent_mutation() IS '限制指令审计外发意向表仅允许从未投递到已投递的一次性更新且禁止删除或篡改负载字段。';

DROP TRIGGER IF EXISTS trg_command_audit_intents_immutable ON command_runtime.command_audit_intents;
CREATE TRIGGER trg_command_audit_intents_immutable
    BEFORE UPDATE OR DELETE ON command_runtime.command_audit_intents
    FOR EACH ROW EXECUTE FUNCTION command_runtime.guard_command_audit_intent_mutation();

REVOKE TRUNCATE ON
    command_runtime.command_transitions,
    command_runtime.connector_evidence,
    command_runtime.command_grant_uses,
    command_runtime.command_audit_intents
    FROM s3_command_runtime;

RESET ROLE;

COMMIT;
