BEGIN;

INSERT INTO alarm_runtime.alarm_current (
  alarm_id, organization_id, site_id, device_id, source_type, source_reference,
  title, summary, severity, status, assignee_id, suppressed_until, occurrence_count,
  first_occurred_at, last_occurred_at, evidence, transitions, version, created_at, updated_at
) VALUES (
  '01910000-1000-7000-8000-000000000001',
  '01910000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-2000-7000-8000-000000000001',
  'DEVICE_RULE',
  'rule:postgres-supply-temperature-drift:v1',
  'Postgres supply temperature drift',
  'Alarm Service published a durable PostgreSQL lifecycle fixture.',
  'MAJOR',
  'OPEN',
  NULL,
  NULL,
  2,
  '2026-07-31T09:00:00Z',
  '2026-07-31T09:05:00Z',
  '[{"kind":"telemetry-snapshot","reference":"snapshot:postgres-1","capturedAt":"2026-07-31T09:05:00Z"}]'::jsonb,
  '[{"toStatus":"OPEN","operation":"PUBLISH","reason":"ALARM_PUBLISHED","actorType":"WORKLOAD","occurredAt":"2026-07-31T09:00:00Z","version":1}]'::jsonb,
  1,
  '2026-07-31T09:00:00Z',
  '2026-07-31T09:05:00Z'
);

COMMIT;
