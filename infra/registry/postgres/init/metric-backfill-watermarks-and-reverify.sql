\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.metric_backfill_watermarks (
    watermark_key text PRIMARY KEY CHECK (btrim(watermark_key) <> ''),
    tenant_id uuid,
    site_id uuid,
    scope text NOT NULL CHECK (btrim(scope) <> ''),
    last_processed_sequence bigint NOT NULL DEFAULT 0 CHECK (last_processed_sequence >= 0),
    last_processed_ingested_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT metric_backfill_watermarks_scope_consistency CHECK (
        (tenant_id IS NULL AND site_id IS NULL) OR tenant_id IS NOT NULL
    )
);

COMMENT ON TABLE core_registry.metric_backfill_watermarks IS '持久化指标回溯重算探测器已处理的 ClickHouse 序列水位与摄入时间游标，防止进程重启丢失待重算窗口。';

CREATE UNIQUE INDEX IF NOT EXISTS metric_backfill_watermarks_tenant_site_scope_uidx
    ON core_registry.metric_backfill_watermarks (tenant_id, site_id, scope) NULLS NOT DISTINCT;

ALTER TABLE core_registry.metric_backfill_watermarks ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.metric_backfill_watermarks FORCE ROW LEVEL SECURITY;

REVOKE ALL ON core_registry.metric_backfill_watermarks FROM PUBLIC;
GRANT USAGE ON SCHEMA core_registry TO metric_engine_runtime, scheduler_runtime, mv_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.metric_backfill_watermarks TO metric_engine_runtime, scheduler_runtime;

DROP POLICY IF EXISTS metric_backfill_watermarks_metric_runtime_scope ON core_registry.metric_backfill_watermarks;
CREATE POLICY metric_backfill_watermarks_metric_runtime_scope ON core_registry.metric_backfill_watermarks
    FOR ALL
    TO metric_engine_runtime
    USING (
        (tenant_id IS NULL AND site_id IS NULL AND core_registry.current_tenant_id() IS NULL)
        OR (
            tenant_id = core_registry.current_tenant_id()
            AND (site_id IS NULL OR core_registry.is_authorized_site(site_id))
        )
    )
    WITH CHECK (
        (tenant_id IS NULL AND site_id IS NULL AND core_registry.current_tenant_id() IS NULL)
        OR (
            tenant_id = core_registry.current_tenant_id()
            AND (site_id IS NULL OR core_registry.is_authorized_site(site_id))
        )
    );

DROP POLICY IF EXISTS metric_backfill_watermarks_scheduler_runtime_scope ON core_registry.metric_backfill_watermarks;
CREATE POLICY metric_backfill_watermarks_scheduler_runtime_scope ON core_registry.metric_backfill_watermarks
    FOR ALL
    TO scheduler_runtime
    USING (
        (tenant_id IS NULL AND site_id IS NULL AND core_registry.current_tenant_id() IS NULL)
        OR (
            tenant_id = core_registry.current_tenant_id()
            AND (site_id IS NULL OR core_registry.is_authorized_site(site_id))
        )
    )
    WITH CHECK (
        (tenant_id IS NULL AND site_id IS NULL AND core_registry.current_tenant_id() IS NULL)
        OR (
            tenant_id = core_registry.current_tenant_id()
            AND (site_id IS NULL OR core_registry.is_authorized_site(site_id))
        )
    );

GRANT UPDATE ON core_registry.savings_daily_results TO mv_runtime;

DROP POLICY IF EXISTS savings_daily_results_runtime_update_scope ON core_registry.savings_daily_results;
CREATE POLICY savings_daily_results_runtime_update_scope ON core_registry.savings_daily_results
    FOR UPDATE
    TO mv_runtime
    USING (
        tenant_id = core_registry.current_tenant_id()
        AND core_registry.is_authorized_site(site_id)
    )
    WITH CHECK (
        tenant_id = core_registry.current_tenant_id()
        AND core_registry.is_authorized_site(site_id)
    );

RESET ROLE;

COMMIT;
