\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- 1. Extend tariff period codes to include DEEP_VALLEY (深谷).
ALTER TABLE core_registry.tariff_periods
  DROP CONSTRAINT IF EXISTS tariff_periods_period_code_check;

ALTER TABLE core_registry.tariff_periods
  ADD CONSTRAINT tariff_periods_period_code_check
  CHECK (period_code IN ('SUPER_PEAK','PEAK','FLAT','VALLEY','DEEP_VALLEY'));

-- 2. Site-level calendar overrides for holidays / special workdays used by TOU resolution.
CREATE TABLE IF NOT EXISTS core_registry.site_calendar_holidays (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  holiday_date date NOT NULL,
  day_type text NOT NULL CHECK (day_type IN ('HOLIDAY','SPECIAL_DAY','WEEKDAY','WEEKEND')),
  label text NOT NULL DEFAULT '',
  revision bigint NOT NULL DEFAULT 1 CHECK (revision >= 1),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, site_id, holiday_date),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (updated_at >= created_at)
);

COMMENT ON TABLE core_registry.site_calendar_holidays IS '站点级分时电价日历覆盖表，记录节假日、特殊日及调休工作日覆盖';

CREATE INDEX IF NOT EXISTS site_calendar_holidays_site_date_idx
  ON core_registry.site_calendar_holidays (tenant_id, site_id, holiday_date DESC);

ALTER TABLE core_registry.site_calendar_holidays ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.site_calendar_holidays FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS site_calendar_holidays_migrator_all ON core_registry.site_calendar_holidays;
CREATE POLICY site_calendar_holidays_migrator_all
  ON core_registry.site_calendar_holidays
  FOR ALL TO s1_core_migrator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS site_calendar_holidays_runtime_select ON core_registry.site_calendar_holidays;
CREATE POLICY site_calendar_holidays_runtime_select
  ON core_registry.site_calendar_holidays
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS site_calendar_holidays_settlement_select ON core_registry.site_calendar_holidays;
CREATE POLICY site_calendar_holidays_settlement_select
  ON core_registry.site_calendar_holidays
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS site_calendar_holidays_mv_select ON core_registry.site_calendar_holidays;
CREATE POLICY site_calendar_holidays_mv_select
  ON core_registry.site_calendar_holidays
  FOR SELECT TO mv_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS site_calendar_holidays_writer_all ON core_registry.site_calendar_holidays;
CREATE POLICY site_calendar_holidays_writer_all
  ON core_registry.site_calendar_holidays
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

-- 3. Extend core_registry.meter_ratio_versions (created in 009a) with binding/point/unit dimensions,
--    non-overlap enforcement per (tenant_id, site_id, point_id), and writer/engine grants.
ALTER TABLE core_registry.meter_ratio_versions
  ALTER COLUMN meter_id DROP NOT NULL,
  ALTER COLUMN version DROP NOT NULL,
  ALTER COLUMN ratio_application_mode SET DEFAULT 'PLATFORM_APPLIED',
  ALTER COLUMN status SET DEFAULT 'RELEASED',
  ALTER COLUMN revision SET DEFAULT 1,
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN updated_at SET DEFAULT now(),
  ADD COLUMN IF NOT EXISTS meter_binding_id uuid,
  ADD COLUMN IF NOT EXISTS point_id uuid,
  ADD COLUMN IF NOT EXISTS reading_unit text NOT NULL DEFAULT 'kWh';

ALTER TABLE core_registry.meter_ratio_versions
  DROP CONSTRAINT IF EXISTS meter_ratio_versions_meter_binding_id_uuid_check;
ALTER TABLE core_registry.meter_ratio_versions
  ADD CONSTRAINT meter_ratio_versions_meter_binding_id_uuid_check
  CHECK (meter_binding_id IS NULL OR core_registry.is_uuid_v7(meter_binding_id));

ALTER TABLE core_registry.meter_ratio_versions
  DROP CONSTRAINT IF EXISTS meter_ratio_versions_point_id_uuid_check;
ALTER TABLE core_registry.meter_ratio_versions
  ADD CONSTRAINT meter_ratio_versions_point_id_uuid_check
  CHECK (point_id IS NULL OR core_registry.is_uuid_v7(point_id));

ALTER TABLE core_registry.meter_ratio_versions
  DROP CONSTRAINT IF EXISTS meter_ratio_versions_reading_unit_check;
ALTER TABLE core_registry.meter_ratio_versions
  ADD CONSTRAINT meter_ratio_versions_reading_unit_check
  CHECK (reading_unit IN ('Wh','kWh','MWh','GJ','MJ','RTh'));

ALTER TABLE core_registry.meter_ratio_versions
  DROP CONSTRAINT IF EXISTS meter_ratio_versions_target_check;
ALTER TABLE core_registry.meter_ratio_versions
  ADD CONSTRAINT meter_ratio_versions_target_check
  CHECK (meter_id IS NOT NULL OR (meter_binding_id IS NOT NULL AND point_id IS NOT NULL));

COMMENT ON TABLE core_registry.meter_ratio_versions IS '电表/冷量表互感器变比与计量单位生效版本表，支持按点位与时间区间隔离历史倍率';

CREATE INDEX IF NOT EXISTS meter_ratio_versions_point_effective_idx
  ON core_registry.meter_ratio_versions (tenant_id, site_id, point_id, effective_from DESC, effective_to)
  WHERE point_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS meter_ratio_versions_binding_idx
  ON core_registry.meter_ratio_versions (tenant_id, site_id, meter_binding_id)
  WHERE meter_binding_id IS NOT NULL;

CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_point_ratio_version()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.point_id IS NULL OR NEW.status <> 'RELEASED' THEN
    RETURN NEW;
  END IF;

  IF EXISTS (
    SELECT 1
    FROM core_registry.meter_ratio_versions existing
    WHERE existing.id <> NEW.id
      AND existing.tenant_id = NEW.tenant_id
      AND existing.site_id = NEW.site_id
      AND existing.point_id = NEW.point_id
      AND existing.status = 'RELEASED'
      AND tstzrange(existing.effective_from, COALESCE(existing.effective_to, 'infinity'::timestamptz), '[)')
       && tstzrange(NEW.effective_from, COALESCE(NEW.effective_to, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'overlapping released meter_ratio_versions window for point %', NEW.point_id
      USING ERRCODE = 'exclusion_violation';
  END IF;

  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS meter_ratio_versions_reject_point_overlap ON core_registry.meter_ratio_versions;
CREATE TRIGGER meter_ratio_versions_reject_point_overlap
BEFORE INSERT OR UPDATE OF tenant_id, site_id, point_id, effective_from, effective_to, status
ON core_registry.meter_ratio_versions
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_overlapping_point_ratio_version();

DROP POLICY IF EXISTS meter_ratio_versions_migrator_all ON core_registry.meter_ratio_versions;
CREATE POLICY meter_ratio_versions_migrator_all
  ON core_registry.meter_ratio_versions
  FOR ALL TO s1_core_migrator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS meter_ratio_versions_metric_engine_select ON core_registry.meter_ratio_versions;
CREATE POLICY meter_ratio_versions_metric_engine_select
  ON core_registry.meter_ratio_versions
  FOR SELECT TO metric_engine_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS meter_ratio_versions_settlement_select ON core_registry.meter_ratio_versions;
CREATE POLICY meter_ratio_versions_settlement_select
  ON core_registry.meter_ratio_versions
  FOR SELECT TO settlement_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS meter_ratio_versions_writer_all ON core_registry.meter_ratio_versions;
CREATE POLICY meter_ratio_versions_writer_all
  ON core_registry.meter_ratio_versions
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

-- 4. Debounced dirty window queue for energy & COP metric recalculation.
CREATE TABLE IF NOT EXISTS core_registry.energy_recalc_dirty_windows (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  granularity text NOT NULL CHECK (granularity IN ('HOUR','DAY','MONTH','ALL')),
  window_start timestamptz NOT NULL,
  window_end timestamptz NOT NULL,
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED')),
  available_at timestamptz NOT NULL DEFAULT now(),
  requested_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(requested_by)),
  requested_by_display text NOT NULL DEFAULT '',
  reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 1000),
  source text NOT NULL DEFAULT 'BACKFILL' CHECK (source IN ('BACKFILL','MANUAL','TARIFF_CHANGE','RATIO_CHANGE')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (window_end > window_start),
  CHECK (updated_at >= created_at)
);

COMMENT ON TABLE core_registry.energy_recalc_dirty_windows IS '能耗与指标重算脏窗口队列，承接历史回补、电价变更及变比变更后的防抖重算任务';

CREATE INDEX IF NOT EXISTS energy_recalc_dirty_windows_due_idx
  ON core_registry.energy_recalc_dirty_windows (status, available_at, tenant_id, site_id, granularity, window_start);

CREATE INDEX IF NOT EXISTS energy_recalc_dirty_windows_scope_idx
  ON core_registry.energy_recalc_dirty_windows (tenant_id, site_id, created_at DESC);

ALTER TABLE core_registry.energy_recalc_dirty_windows ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.energy_recalc_dirty_windows FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS energy_recalc_dirty_windows_migrator_all ON core_registry.energy_recalc_dirty_windows;
CREATE POLICY energy_recalc_dirty_windows_migrator_all
  ON core_registry.energy_recalc_dirty_windows
  FOR ALL TO s1_core_migrator
  USING (true)
  WITH CHECK (true);

DROP POLICY IF EXISTS energy_recalc_dirty_windows_runtime_rw ON core_registry.energy_recalc_dirty_windows;
CREATE POLICY energy_recalc_dirty_windows_runtime_rw
  ON core_registry.energy_recalc_dirty_windows
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS energy_recalc_dirty_windows_writer_rw ON core_registry.energy_recalc_dirty_windows;
CREATE POLICY energy_recalc_dirty_windows_writer_rw
  ON core_registry.energy_recalc_dirty_windows
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS energy_recalc_dirty_windows_metric_engine_rw ON core_registry.energy_recalc_dirty_windows;
CREATE POLICY energy_recalc_dirty_windows_metric_engine_rw
  ON core_registry.energy_recalc_dirty_windows
  FOR ALL TO metric_engine_runtime
  USING (
    core_registry.current_tenant_id() IS NULL
    OR (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  )
  WITH CHECK (
    core_registry.current_tenant_id() IS NULL
    OR (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  );

DROP POLICY IF EXISTS energy_recalc_dirty_windows_scheduler_rw ON core_registry.energy_recalc_dirty_windows;
CREATE POLICY energy_recalc_dirty_windows_scheduler_rw
  ON core_registry.energy_recalc_dirty_windows
  FOR ALL TO scheduler_runtime
  USING (
    core_registry.current_tenant_id() IS NULL
    OR (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  )
  WITH CHECK (
    core_registry.current_tenant_id() IS NULL
    OR (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  );

-- 5. Least-privilege role grants.
REVOKE ALL ON TABLE core_registry.site_calendar_holidays, core_registry.energy_recalc_dirty_windows FROM PUBLIC;
GRANT USAGE ON SCHEMA core_registry TO s1_core_runtime, s1_core_writer, metric_engine_runtime, scheduler_runtime, settlement_runtime, mv_runtime;

GRANT SELECT ON TABLE core_registry.site_calendar_holidays TO s1_core_runtime, settlement_runtime, mv_runtime;
GRANT SELECT, INSERT, UPDATE ON TABLE core_registry.site_calendar_holidays TO s1_core_writer;
GRANT ALL ON TABLE core_registry.site_calendar_holidays TO s1_core_migrator;

GRANT SELECT ON TABLE core_registry.meter_ratio_versions TO s1_core_runtime, metric_engine_runtime, settlement_runtime;
GRANT SELECT, INSERT, UPDATE ON TABLE core_registry.meter_ratio_versions TO s1_core_writer;
GRANT ALL ON TABLE core_registry.meter_ratio_versions TO s1_core_migrator;

GRANT SELECT ON TABLE core_registry.energy_recalc_dirty_windows TO s1_core_runtime;
GRANT SELECT, INSERT, UPDATE ON TABLE core_registry.energy_recalc_dirty_windows TO s1_core_writer, metric_engine_runtime, scheduler_runtime;
GRANT ALL ON TABLE core_registry.energy_recalc_dirty_windows TO s1_core_migrator;

RESET ROLE;

COMMIT;
