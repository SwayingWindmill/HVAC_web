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

INSERT INTO alarm_runtime.alarm_policy_revision (
  tenant_id, site_id, policy_id, policy_revision_id, revision, schema_version, digest, policy, released_at, released_by
) VALUES
(
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-5000-7000-8000-000000000001',
  '01910000-5000-7000-8000-000000000002',
  1,
  1,
  '5915c63b4f29523c69a0038c24d66adf97a23e46b27ed3c54690df3927ea29d4',
  '{"schemaVersion":1,"policyId":"01910000-5000-7000-8000-000000000001","policyRevisionId":"01910000-5000-7000-8000-000000000002","revision":1,"digest":"5915c63b4f29523c69a0038c24d66adf97a23e46b27ed3c54690df3927ea29d4","alarmType":"S14_DURATION_SUPPLY_TEMPERATURE","sourceType":"SITE_RULE","sourceReference":"alarm-policy:postgres-s14-duration","title":"S14 duration supply temperature","summary":"S14 durable evaluator integration fixture.","severity":"MAJOR","qualityPolicy":"VALID_ONLY","freshnessSeconds":900,"triggerMode":"DURATION","durationSeconds":5,"raise":{"kind":"COMPARE","input":"supplyTemp","operator":"GT","value":{"type":"NUMBER","number":30}},"clear":{"kind":"COMPARE","input":"supplyTemp","operator":"LTE","value":{"type":"NUMBER","number":28}}}'::jsonb,
  '2026-08-19T11:00:00Z',
  'test:migrator'
),
(
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-5000-7000-8000-000000000011',
  '01910000-5000-7000-8000-000000000012',
  1,
  1,
  'd5c8217ac38c4ec8b1fbf2486ca73c865da889e8679254f2267f06e976b9632f',
  '{"schemaVersion":1,"policyId":"01910000-5000-7000-8000-000000000011","policyRevisionId":"01910000-5000-7000-8000-000000000012","revision":1,"digest":"d5c8217ac38c4ec8b1fbf2486ca73c865da889e8679254f2267f06e976b9632f","alarmType":"S14_SUPERSEDE_SUPPLY_TEMPERATURE","sourceType":"SITE_RULE","sourceReference":"alarm-policy:postgres-s14-supersede","title":"S14 supersede supply temperature","summary":"S14 claimed timer supersession fixture.","severity":"MAJOR","qualityPolicy":"VALID_ONLY","freshnessSeconds":900,"triggerMode":"DURATION","durationSeconds":5,"raise":{"kind":"COMPARE","input":"supplyTemp","operator":"GT","value":{"type":"NUMBER","number":30}},"clear":{"kind":"COMPARE","input":"supplyTemp","operator":"LTE","value":{"type":"NUMBER","number":28}}}'::jsonb,
  '2026-08-19T11:00:00Z',
  'test:migrator'
),
(
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-5000-7000-8000-000000000021',
  '01910000-5000-7000-8000-000000000022',
  1,
  1,
  '4364dfcb7a7dd35aa9a79bd80eb8a06fedaa39f9c45dca2d64835c606f59dfc8',
  '{"schemaVersion":1,"policyId":"01910000-5000-7000-8000-000000000021","policyRevisionId":"01910000-5000-7000-8000-000000000022","revision":1,"digest":"4364dfcb7a7dd35aa9a79bd80eb8a06fedaa39f9c45dca2d64835c606f59dfc8","alarmType":"S14_LEASE_SUPPLY_TEMPERATURE","sourceType":"SITE_RULE","sourceReference":"alarm-policy:postgres-s14-lease","title":"S14 lease supply temperature","summary":"S14 lease expiry and reclaim fixture.","severity":"MAJOR","qualityPolicy":"VALID_ONLY","freshnessSeconds":900,"triggerMode":"DURATION","durationSeconds":5,"raise":{"kind":"COMPARE","input":"supplyTemp","operator":"GT","value":{"type":"NUMBER","number":30}},"clear":{"kind":"COMPARE","input":"supplyTemp","operator":"LTE","value":{"type":"NUMBER","number":28}}}'::jsonb,
  '2026-08-19T11:00:00Z',
  'test:migrator'
);

INSERT INTO alarm_runtime.alarm_policy_assignment (
  tenant_id, site_id, assignment_id, assignment_revision, policy_revision_id, subject_type, subject_id, assigned_at, assigned_by
) VALUES
(
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-7000-7000-8000-000000000001',
  1,
  '01910000-5000-7000-8000-000000000002',
  'SITE',
  '01910000-0001-7000-8000-000000000001',
  '2026-08-19T11:01:00Z',
  'test:migrator'
),
(
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-7000-7000-8000-000000000002',
  1,
  '01910000-5000-7000-8000-000000000012',
  'SITE',
  '01910000-0001-7000-8000-000000000001',
  '2026-08-19T11:02:00Z',
  'test:migrator'
),
(
  '0190f000-0000-7000-8000-000000000001',
  '01910000-0001-7000-8000-000000000001',
  '01910000-7000-7000-8000-000000000003',
  1,
  '01910000-5000-7000-8000-000000000022',
  'SITE',
  '01910000-0001-7000-8000-000000000001',
  '2026-08-19T11:03:00Z',
  'test:migrator'
);

COMMIT;
