BEGIN;
SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.assets
  ADD COLUMN IF NOT EXISTS tags jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE core_registry.assets
  DROP CONSTRAINT IF EXISTS assets_tags_object_check;
ALTER TABLE core_registry.assets
  ADD CONSTRAINT assets_tags_object_check CHECK (jsonb_typeof(tags) = 'object' AND octet_length(tags::text) <= 8192);
CREATE INDEX IF NOT EXISTS assets_tags_gin_idx ON core_registry.assets USING gin (tags);
COMMENT ON COLUMN core_registry.assets.tags IS '资产多维标签键值映射，用于跨站点检索与分组过滤';

ALTER TABLE core_registry.devices
  ADD COLUMN IF NOT EXISTS tags jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE core_registry.devices
  DROP CONSTRAINT IF EXISTS devices_tags_object_check;
ALTER TABLE core_registry.devices
  ADD CONSTRAINT devices_tags_object_check CHECK (jsonb_typeof(tags) = 'object' AND octet_length(tags::text) <= 8192);
CREATE INDEX IF NOT EXISTS devices_tags_gin_idx ON core_registry.devices USING gin (tags);
COMMENT ON COLUMN core_registry.devices.tags IS '设备多维标签键值映射，用于跨站点检索与分组过滤';

GRANT SELECT ON
  core_registry.registry_templates,
  core_registry.registry_template_revisions,
  core_registry.registry_template_assignments
TO s1_core_runtime;

DROP POLICY IF EXISTS registry_templates_runtime_read ON core_registry.registry_templates;
CREATE POLICY registry_templates_runtime_read ON core_registry.registry_templates
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS registry_template_revisions_runtime_read ON core_registry.registry_template_revisions;
CREATE POLICY registry_template_revisions_runtime_read ON core_registry.registry_template_revisions
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id());

DROP POLICY IF EXISTS registry_template_assignments_runtime_read ON core_registry.registry_template_assignments;
CREATE POLICY registry_template_assignments_runtime_read ON core_registry.registry_template_assignments
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

COMMIT;
