SET LOCAL ROLE s1_core_migrator;

DROP POLICY IF EXISTS operating_schedules_optimization_scope ON core_registry.operating_schedules;
CREATE POLICY operating_schedules_optimization_scope ON core_registry.operating_schedules
  FOR SELECT TO optimization_runtime
  USING (
    tenant_id = core_registry.current_tenant_id()
    AND core_registry.is_authorized_site(site_id)
  );

DROP POLICY IF EXISTS operating_schedule_slots_optimization_scope ON core_registry.operating_schedule_slots;
CREATE POLICY operating_schedule_slots_optimization_scope ON core_registry.operating_schedule_slots
  FOR SELECT TO optimization_runtime
  USING (
    tenant_id = core_registry.current_tenant_id()
    AND core_registry.is_authorized_site(site_id)
  );

DROP POLICY IF EXISTS operating_exceptions_optimization_scope ON core_registry.operating_exceptions;
CREATE POLICY operating_exceptions_optimization_scope ON core_registry.operating_exceptions
  FOR SELECT TO optimization_runtime
  USING (
    tenant_id = core_registry.current_tenant_id()
    AND core_registry.is_authorized_site(site_id)
  );

GRANT SELECT (tenant_id, site_id, id, name, pre_cool_lead_minutes, coast_down_lag_minutes, revision)
  ON core_registry.operating_schedules TO optimization_runtime;

GRANT SELECT (tenant_id, site_id, id, schedule_id, schedule_revision, day_of_week, start_minute, end_minute, target_supply_temp_c)
  ON core_registry.operating_schedule_slots TO optimization_runtime;

GRANT SELECT (tenant_id, site_id, id, title, kind, start_date, end_date, replacement_slots, reason, status)
  ON core_registry.operating_exceptions TO optimization_runtime;

RESET ROLE;
