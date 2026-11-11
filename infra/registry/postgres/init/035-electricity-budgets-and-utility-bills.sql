-- Monthly electricity budgets and two-part utility bills (#785): each Site stores monthly
-- electricity budget targets (natural month YYYY-MM) and utility bills with actual reading windows
-- (supporting cross-month billing cycles), CHILLER_PLANT vs WHOLE_BUILDING meter scope, four-part
-- payable identity, negative/positive power-factor adjustments, and DRAFT -> CONFIRMED locking.
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE core_registry.monthly_electricity_budgets (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  billing_month text NOT NULL CHECK (billing_month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
  budget_amount_cny numeric(14,2) NOT NULL CHECK (budget_amount_cny >= 0),
  note text NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
  revision bigint NOT NULL CHECK (revision >= 1),
  updated_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(updated_by)),
  updated_by_display text NOT NULL CHECK (length(btrim(updated_by_display)) BETWEEN 1 AND 256),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, billing_month),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE INDEX monthly_electricity_budgets_site_month_idx
  ON core_registry.monthly_electricity_budgets (tenant_id, site_id, billing_month DESC);

CREATE TABLE core_registry.utility_bills (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  billing_month text NOT NULL CHECK (billing_month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
  reading_start_date date NOT NULL,
  reading_end_date date NOT NULL CHECK (reading_end_date >= reading_start_date),
  meter_scope text NOT NULL CHECK (meter_scope IN ('CHILLER_PLANT', 'WHOLE_BUILDING')),
  billed_energy_kwh double precision NOT NULL CHECK (billed_energy_kwh >= 0),
  tou_breakdown jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(tou_breakdown) = 'object'),
  energy_charge_cny numeric(14,2) NOT NULL CHECK (energy_charge_cny >= 0),
  basic_charge_mode text NOT NULL CHECK (basic_charge_mode IN ('NONE', 'TRANSFORMER_CAPACITY_KVA', 'MAX_DEMAND_KW')),
  basic_charge_quantity double precision CHECK (basic_charge_quantity IS NULL OR basic_charge_quantity >= 0),
  basic_charge_cny numeric(14,2) NOT NULL CHECK (basic_charge_cny >= 0),
  standard_power_factor numeric(4,2) CHECK (standard_power_factor IS NULL OR (standard_power_factor >= 0.70 AND standard_power_factor <= 0.99)),
  actual_power_factor numeric(4,3) CHECK (actual_power_factor IS NULL OR (actual_power_factor >= 0 AND actual_power_factor <= 1)),
  power_factor_adjustment_cny numeric(14,2) NOT NULL,
  other_charges_cny numeric(14,2) NOT NULL DEFAULT 0,
  total_payable_cny numeric(14,2) NOT NULL CHECK (
    total_payable_cny >= 0
    AND total_payable_cny = energy_charge_cny + basic_charge_cny + power_factor_adjustment_cny + other_charges_cny
  ),
  chiller_share_override_ratio double precision CHECK (
    chiller_share_override_ratio IS NULL OR (chiller_share_override_ratio > 0 AND chiller_share_override_ratio <= 1)
  ),
  utility_account_no text NOT NULL DEFAULT '' CHECK (length(utility_account_no) <= 100),
  invoice_no text NOT NULL DEFAULT '' CHECK (length(invoice_no) <= 100),
  note text NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
  status text NOT NULL CHECK (status IN ('DRAFT', 'CONFIRMED')),
  confirmed_by uuid CHECK (confirmed_by IS NULL OR core_registry.is_uuid_v7(confirmed_by)),
  confirmed_by_display text CHECK (confirmed_by_display IS NULL OR length(btrim(confirmed_by_display)) BETWEEN 1 AND 256),
  confirmed_at timestamptz,
  updated_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(updated_by)),
  updated_by_display text NOT NULL CHECK (length(btrim(updated_by_display)) BETWEEN 1 AND 256),
  revision bigint NOT NULL CHECK (revision >= 1),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (
    (basic_charge_mode = 'NONE' AND basic_charge_quantity IS NULL AND basic_charge_cny = 0)
    OR (basic_charge_mode <> 'NONE' AND basic_charge_quantity IS NOT NULL AND basic_charge_quantity > 0)
  ),
  CHECK (meter_scope = 'WHOLE_BUILDING' OR chiller_share_override_ratio IS NULL),
  CHECK ((status = 'CONFIRMED') = (confirmed_at IS NOT NULL AND confirmed_by IS NOT NULL AND confirmed_by_display IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE INDEX utility_bills_site_month_idx
  ON core_registry.utility_bills (tenant_id, site_id, billing_month DESC, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION core_registry.reject_duplicate_confirmed_utility_bill()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status <> 'CONFIRMED' THEN
    RETURN NEW;
  END IF;
  PERFORM pg_advisory_xact_lock(
    hashtextextended(
      NEW.tenant_id::text || '/' || NEW.site_id::text || '/utility-bills/' || NEW.billing_month || '/' || NEW.meter_scope,
      0
    )
  );
  IF EXISTS (
    SELECT 1 FROM core_registry.utility_bills AS other
    WHERE other.tenant_id = NEW.tenant_id
      AND other.site_id = NEW.site_id
      AND other.id <> NEW.id
      AND other.billing_month = NEW.billing_month
      AND other.meter_scope = NEW.meter_scope
      AND other.status = 'CONFIRMED'
  ) THEN
    RAISE EXCEPTION 'confirmed utility bill already exists for this Site, billing month, and meter scope' USING ERRCODE = '23P01';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER utility_bills_reject_duplicate_confirmed
BEFORE INSERT OR UPDATE ON core_registry.utility_bills
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_duplicate_confirmed_utility_bill();

ALTER TABLE core_registry.registry_audit_facts
  DROP CONSTRAINT IF EXISTS registry_audit_facts_resource_type_check;
ALTER TABLE core_registry.registry_audit_facts
  ADD CONSTRAINT registry_audit_facts_resource_type_check
  CHECK (resource_type IN (
    'SITE', 'SPACE', 'ASSET', 'DEVICE', 'SENSOR', 'POINT', 'BINDING',
    'TEMPLATE_REVISION', 'TEMPLATE_ASSIGNMENT', 'IMPORT', 'RETIREMENT',
    'OPERATING_SCHEDULE', 'OPERATING_EXCEPTION',
    'MONTHLY_ELECTRICITY_BUDGET', 'UTILITY_BILL'
  ));

ALTER TABLE core_registry.monthly_electricity_budgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.monthly_electricity_budgets FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.utility_bills ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.utility_bills FORCE ROW LEVEL SECURITY;

CREATE POLICY monthly_electricity_budgets_migrator_all ON core_registry.monthly_electricity_budgets
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
CREATE POLICY utility_bills_migrator_all ON core_registry.utility_bills
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

CREATE POLICY monthly_electricity_budgets_runtime_scope ON core_registry.monthly_electricity_budgets
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY utility_bills_runtime_scope ON core_registry.utility_bills
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY monthly_electricity_budgets_writer_scope ON core_registry.monthly_electricity_budgets
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY utility_bills_writer_scope ON core_registry.utility_bills
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.monthly_electricity_budgets, core_registry.utility_bills FROM PUBLIC;
GRANT SELECT ON core_registry.monthly_electricity_budgets, core_registry.utility_bills TO s1_core_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.monthly_electricity_budgets, core_registry.utility_bills TO s1_core_writer;

RESET ROLE;
SET LOCAL ROLE s1_iam_migrator;

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
SELECT 10, 'electricity-billing-v1', capabilities || ARRAY['billing.manage']::text[], 'ACTIVE', '2026-11-12T00:00:00Z'
FROM iam.capability_catalog_revisions
WHERE revision = 9;

UPDATE iam.role_templates
SET capabilities = array_append(capabilities, 'billing.manage'),
    revision = revision + 1,
    updated_at = '2026-11-12T00:00:00Z'
WHERE tenant_id IS NULL
  AND role_key IN ('site-manager', 'admin')
  AND NOT capabilities @> ARRAY['billing.manage']::text[];

RESET ROLE;
COMMIT;
