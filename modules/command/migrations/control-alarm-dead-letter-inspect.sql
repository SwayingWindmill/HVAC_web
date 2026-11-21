\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE s3_command_migrator;

CREATE INDEX IF NOT EXISTS control_alarm_outbox_dead_letter_idx
    ON command_runtime.control_alarm_outbox (tenant_id, site_id, updated_at DESC, outbox_id DESC)
    WHERE status = 'DEAD_LETTER';

CREATE OR REPLACE FUNCTION command_runtime.control_alarm_outbox_state_counts()
RETURNS TABLE (
    pending_count bigint,
    dead_letter_count bigint
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, command_runtime
AS $$
    SELECT
        count(*) FILTER (WHERE status = 'PENDING')::bigint AS pending_count,
        count(*) FILTER (WHERE status = 'DEAD_LETTER')::bigint AS dead_letter_count
    FROM command_runtime.control_alarm_outbox;
$$;

COMMENT ON FUNCTION command_runtime.control_alarm_outbox_state_counts() IS '跨租户统计控制告警发件箱待投递与死信条数，供平台指标采集而不暴露租户标识。';

REVOKE ALL ON FUNCTION command_runtime.control_alarm_outbox_state_counts() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION command_runtime.control_alarm_outbox_state_counts() TO s3_command_runtime;

RESET ROLE;
COMMIT;
