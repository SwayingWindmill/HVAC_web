-- Equipment nameplates (chillers, pumps, cooling towers) live on the asset.
-- rated_power and rated_capacity stay the columns FDD already reads; nameplate holds
-- the rest (current, refrigerant, year, efficiency, role) and which fields are not
-- confirmed yet. A NULL nameplate is an asset with no plate recorded.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.assets
  ADD COLUMN IF NOT EXISTS nameplate jsonb;
ALTER TABLE core_registry.assets
  DROP CONSTRAINT IF EXISTS assets_nameplate_document_check;
ALTER TABLE core_registry.assets
  ADD CONSTRAINT assets_nameplate_document_check
  CHECK (nameplate IS NULL OR (jsonb_typeof(nameplate) = 'object' AND octet_length(nameplate::text) <= 8192));

RESET ROLE;
COMMIT;
