BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- SE-ARCH-004 V2.1.2 durable business-event and cross-store publication boundary.
-- Durability follows business consequence, not process topology.

CREATE TABLE IF NOT EXISTS core_registry.domain_outbox_events (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid,
  event_type text NOT NULL CHECK (length(btrim(event_type)) BETWEEN 1 AND 128),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  subject_type text NOT NULL CHECK (length(btrim(subject_type)) BETWEEN 1 AND 64),
  subject_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(subject_id)),
  aggregate_type text NOT NULL CHECK (length(btrim(aggregate_type)) BETWEEN 1 AND 64),
  aggregate_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(aggregate_id)),
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  occurred_at timestamptz NOT NULL,
  published_at timestamptz,
  trace_id text,
  correlation_id text,
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, aggregate_type, aggregate_id, aggregate_version, event_type),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS core_registry.domain_event_deliveries (
  event_id uuid NOT NULL REFERENCES core_registry.domain_outbox_events(id) ON DELETE CASCADE,
  consumer_name text NOT NULL CHECK (length(btrim(consumer_name)) BETWEEN 1 AND 128),
  status text NOT NULL CHECK (status IN ('PENDING','LEASED','DELIVERED','FAILED')),
  lease_owner text,
  lease_until timestamptz,
  attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  next_retry_at timestamptz,
  delivered_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (event_id, consumer_name),
  CHECK ((status = 'LEASED') = (lease_owner IS NOT NULL AND lease_until IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.domain_consumer_inbox (
  event_id uuid NOT NULL REFERENCES core_registry.domain_outbox_events(id) ON DELETE RESTRICT,
  consumer_name text NOT NULL CHECK (length(btrim(consumer_name)) BETWEEN 1 AND 128),
  aggregate_type text NOT NULL,
  aggregate_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(aggregate_id)),
  aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
  applied_at timestamptz NOT NULL,
  PRIMARY KEY (event_id, consumer_name),
  UNIQUE (consumer_name, aggregate_type, aggregate_id, aggregate_version)
);

CREATE INDEX IF NOT EXISTS domain_event_delivery_ready_idx
  ON core_registry.domain_event_deliveries (status, next_retry_at, created_at, event_id);
CREATE INDEX IF NOT EXISTS domain_outbox_aggregate_order_idx
  ON core_registry.domain_outbox_events (tenant_id, aggregate_type, aggregate_id, aggregate_version);

CREATE TABLE IF NOT EXISTS core_registry.cross_store_publications (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  producer text NOT NULL CHECK (producer IN ('METRIC','FORECAST','OPTIMIZATION')),
  run_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(run_id)),
  result_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(result_id)),
  target_store text NOT NULL CHECK (target_store = 'CLICKHOUSE'),
  target_dataset text NOT NULL CHECK (length(btrim(target_dataset)) BETWEEN 1 AND 128),
  publication_evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(publication_evidence) = 'object'),
  status text NOT NULL CHECK (status IN ('PERSISTING','PERSISTED','FAILED')),
  started_at timestamptz NOT NULL,
  persisted_at timestamptz,
  reconciled_at timestamptz,
  last_error text,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, producer, run_id),
  UNIQUE (tenant_id, producer, result_id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK ((status = 'PERSISTED') = (persisted_at IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS cross_store_publication_reconcile_idx
  ON core_registry.cross_store_publications (producer, status, updated_at)
  WHERE status = 'PERSISTING';

ALTER TABLE core_registry.domain_outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.domain_outbox_events FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.domain_event_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.domain_event_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.domain_consumer_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.domain_consumer_inbox FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.cross_store_publications ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.cross_store_publications FORCE ROW LEVEL SECURITY;

CREATE POLICY domain_outbox_events_runtime_scope ON core_registry.domain_outbox_events
  FOR ALL TO metric_engine_runtime, settlement_runtime, forecast_runtime, optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND (site_id IS NULL OR core_registry.is_authorized_site(site_id)));
CREATE POLICY domain_event_deliveries_runtime_scope ON core_registry.domain_event_deliveries
  FOR ALL TO metric_engine_runtime, settlement_runtime, forecast_runtime, optimization_runtime
  USING (EXISTS (
    SELECT 1 FROM core_registry.domain_outbox_events e
    WHERE e.id = event_id AND e.tenant_id = core_registry.current_tenant_id()
      AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM core_registry.domain_outbox_events e
    WHERE e.id = event_id AND e.tenant_id = core_registry.current_tenant_id()
      AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
  ));
CREATE POLICY domain_consumer_inbox_runtime_scope ON core_registry.domain_consumer_inbox
  FOR ALL TO metric_engine_runtime, settlement_runtime, forecast_runtime, optimization_runtime
  USING (EXISTS (
    SELECT 1 FROM core_registry.domain_outbox_events e
    WHERE e.id = event_id AND e.tenant_id = core_registry.current_tenant_id()
      AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM core_registry.domain_outbox_events e
    WHERE e.id = event_id AND e.tenant_id = core_registry.current_tenant_id()
      AND (e.site_id IS NULL OR core_registry.is_authorized_site(e.site_id))
  ));
CREATE POLICY cross_store_publications_runtime_scope ON core_registry.cross_store_publications
  FOR ALL TO metric_engine_runtime, forecast_runtime, optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

REVOKE ALL ON core_registry.domain_outbox_events, core_registry.domain_event_deliveries,
  core_registry.domain_consumer_inbox, core_registry.cross_store_publications FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON core_registry.domain_outbox_events, core_registry.domain_event_deliveries,
  core_registry.domain_consumer_inbox, core_registry.cross_store_publications TO metric_engine_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.domain_outbox_events, core_registry.domain_event_deliveries,
  core_registry.domain_consumer_inbox TO settlement_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.domain_outbox_events, core_registry.domain_event_deliveries,
  core_registry.domain_consumer_inbox, core_registry.cross_store_publications TO forecast_runtime, optimization_runtime;

COMMIT;
