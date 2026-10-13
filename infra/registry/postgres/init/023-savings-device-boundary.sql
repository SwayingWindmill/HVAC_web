-- Savings verification, Option B (#484): a measure's measurement boundary is either the plant's
-- total meter or one device's sub-meter, and each verified day records the plant's electricity
-- over the same hours so a Site's savings can be read against the whole plant.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- NULL is the plant's total meter (Option C); a device is that device's sub-meter (Option B).
ALTER TABLE core_registry.savings_measures
  ADD COLUMN IF NOT EXISTS boundary_device_id uuid;
ALTER TABLE core_registry.savings_measures
  DROP CONSTRAINT IF EXISTS savings_measures_boundary_device_fkey;
ALTER TABLE core_registry.savings_measures
  ADD CONSTRAINT savings_measures_boundary_device_fkey
  FOREIGN KEY (tenant_id, site_id, boundary_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id);

ALTER TABLE core_registry.savings_daily_results
  ADD COLUMN IF NOT EXISTS plant_kwh double precision NOT NULL;

-- Reporting periods overlap only within one boundary: two measures on the same device, or any
-- measure beside one on the plant's total meter, which already holds every sub-meter.
CREATE OR REPLACE FUNCTION core_registry.reject_overlapping_savings_reporting()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.implemented_at IS NULL THEN
    RETURN NEW;
  END IF;
  -- Writers on one Site take turns, so two measures implemented at once cannot both pass the
  -- check below; it then reads with a fresh snapshot that includes the other's commit.
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id::text || '/' || NEW.site_id::text, 0));
  IF EXISTS (
    SELECT 1 FROM core_registry.savings_measures AS other
    WHERE other.tenant_id = NEW.tenant_id AND other.site_id = NEW.site_id AND other.id <> NEW.id
      AND other.implemented_at IS NOT NULL
      AND (other.boundary_device_id IS NULL OR NEW.boundary_device_id IS NULL OR other.boundary_device_id = NEW.boundary_device_id)
      AND tstzrange(other.implemented_at, coalesce(other.ended_at, 'infinity'::timestamptz), '[)')
          && tstzrange(NEW.implemented_at, coalesce(NEW.ended_at, 'infinity'::timestamptz), '[)')
  ) THEN
    RAISE EXCEPTION 'savings measures on one measurement boundary cannot report over the same period' USING ERRCODE = '23P01';
  END IF;
  RETURN NEW;
END
$$;

RESET ROLE;
COMMIT;
