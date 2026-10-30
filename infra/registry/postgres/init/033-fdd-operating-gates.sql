-- FDD judges the chilled-water low-delta-T rule only while the plant operates (#NNN): a rule
-- revision carries its operating gates, and FDD reads the Site's chillers and chilled-water pumps
-- with their rated power and capacity and the devices bound to them to evaluate those gates.
-- Existing revisions take the default gates: a chiller above 10% of its rated power, a load ratio
-- of at least 15%, and 70% of the window for both operating and low minutes (spec
-- docs/specs/fdd-rule-definition.md).
BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.fdd_rule_revisions
  ADD COLUMN operating_gates jsonb NOT NULL
    DEFAULT '{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10,"chillerRunning":{"minimumRatedPowerFraction":0.1},"loadRatio":{"minimum":0.15},"chilledWaterPumpRunning":{}}'::jsonb
    CHECK (jsonb_typeof(operating_gates) = 'object');

CREATE POLICY assets_fdd_scope ON core_registry.assets FOR SELECT TO fdd_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT (tenant_id, site_id, id, asset_type, status, rated_power, rated_capacity) ON core_registry.assets TO fdd_runtime;

CREATE POLICY device_bindings_fdd_scope ON core_registry.device_bindings FOR SELECT TO fdd_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT (tenant_id, site_id, asset_id, device_id, status, valid_from, valid_to) ON core_registry.device_bindings TO fdd_runtime;

RESET ROLE;
COMMIT;
