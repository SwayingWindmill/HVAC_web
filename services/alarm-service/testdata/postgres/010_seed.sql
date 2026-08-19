BEGIN;

SELECT set_config('app.tenant_id', '0190f000-0000-7000-8000-000000000001', true);

INSERT INTO alarm_runtime.events (
  event_id, tenant_id, site_id, device_id, point_id,
  event_type, severity, message, start_time, end_time, status
) VALUES (
  '01910000-3000-7000-8000-000000000001',
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-2000-7000-8000-000000000001',
  '01910000-4000-7000-8000-000000000001',
  'SUPPLY_TEMPERATURE_DRIFT',
  'MAJOR',
  'Supply temperature drift event used by the Alarm provenance fixture.',
  '2026-07-31T09:00:00Z',
  NULL,
  'ACTIVE'
);

INSERT INTO alarm_runtime.alarm_current (
  alarm_id, tenant_id, site_id, device_id, event_id, point_id, alarm_type, fingerprint,
  incident_correlation_id, source_type, source_reference, rule_revision, title, summary,
  condition, current_severity, peak_severity, occurrence_count, first_occurred_at, last_occurred_at,
  evidence, links, version, created_at, updated_at
) VALUES (
  '01910000-1000-7000-8000-000000000001',
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-2000-7000-8000-000000000001',
  '01910000-3000-7000-8000-000000000001',
  '01910000-4000-7000-8000-000000000001',
  'SUPPLY_TEMPERATURE_DRIFT',
  'cc6e61d78e5065f64413e6689c706356d3e9decfc4a91f8ab9f3275a0d6f3f63',
  '01910000-1000-7000-8000-000000000002',
  'DEVICE_RULE',
  'rule:postgres-supply-temperature-drift:v1',
  'alarm-policy-postgres-1',
  'Postgres supply temperature drift',
  'Alarm Service published a durable PostgreSQL lifecycle fixture.',
  'ACTIVE',
  'MAJOR',
  'MAJOR',
  1,
  '2026-07-31T09:00:00Z',
  '2026-07-31T09:00:00Z',
  '[{"kind":"telemetry-snapshot","reference":"snapshot:postgres-1","capturedAt":"2026-07-31T09:00:00Z"}]'::jsonb,
  '[{"kind":"DEVICE","targetId":"01910000-2000-7000-8000-000000000001"},{"kind":"EVENT","targetId":"01910000-3000-7000-8000-000000000001"},{"kind":"POINT","targetId":"01910000-4000-7000-8000-000000000001"}]'::jsonb,
  1,
  '2026-07-31T09:00:00Z',
  '2026-07-31T09:00:00Z'
);

INSERT INTO alarm_runtime.alarm_timeline (
  tenant_id, site_id, alarm_id, version, operation, condition, reason, actor_type, actor_id,
  assignee_id, suppression, current_severity, policy_revision, correlation_id, occurred_at
) VALUES (
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-1000-7000-8000-000000000001',
  1,
  'PUBLISH',
  'ACTIVE',
  'ALARM_PUBLISHED',
  'WORKLOAD',
  'alarm-evaluator',
  NULL,
  NULL,
  'MAJOR',
  'alarm-policy-postgres-1',
  '01910000-1000-7000-8000-000000000002',
  '2026-07-31T09:00:00Z'
);

COMMIT;
