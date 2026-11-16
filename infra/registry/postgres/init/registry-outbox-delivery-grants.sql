\set ON_ERROR_STOP on
BEGIN;

SET LOCAL ROLE s1_core_migrator;

GRANT SELECT, INSERT, UPDATE ON core_registry.domain_event_deliveries TO s1_core_writer;
GRANT SELECT, INSERT, UPDATE ON core_registry.domain_consumer_inbox TO s1_core_writer;

DROP POLICY IF EXISTS domain_event_deliveries_writer_scope ON core_registry.domain_event_deliveries;
CREATE POLICY domain_event_deliveries_writer_scope ON core_registry.domain_event_deliveries
  FOR ALL TO s1_core_writer
  USING (
    EXISTS (
      SELECT 1
      FROM core_registry.domain_outbox_events e
      WHERE e.id = event_id
        AND e.tenant_id = core_registry.current_tenant_id()
        AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM core_registry.domain_outbox_events e
      WHERE e.id = event_id
        AND e.tenant_id = core_registry.current_tenant_id()
        AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
    )
  );

DROP POLICY IF EXISTS domain_consumer_inbox_writer_scope ON core_registry.domain_consumer_inbox;
CREATE POLICY domain_consumer_inbox_writer_scope ON core_registry.domain_consumer_inbox
  FOR ALL TO s1_core_writer
  USING (
    EXISTS (
      SELECT 1
      FROM core_registry.domain_outbox_events e
      WHERE e.id = event_id
        AND e.tenant_id = core_registry.current_tenant_id()
        AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM core_registry.domain_outbox_events e
      WHERE e.id = event_id
        AND e.tenant_id = core_registry.current_tenant_id()
        AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
    )
  );

RESET ROLE;

GRANT USAGE ON SCHEMA iam TO s1_core_writer;
GRANT SELECT ON iam.tenants TO s1_core_writer;

DROP POLICY IF EXISTS tenants_core_writer_relay ON iam.tenants;
CREATE POLICY tenants_core_writer_relay ON iam.tenants
  FOR SELECT TO s1_core_writer
  USING (true);

COMMIT;
