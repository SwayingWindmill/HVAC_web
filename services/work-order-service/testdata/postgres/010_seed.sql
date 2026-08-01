BEGIN;

INSERT INTO work_order_runtime.work_order_current (
  work_order_id, organization_id, site_id, title, description, priority, status,
  assignee_id, team_id, scheduled_start, due_at,
  task_total, task_completed, task_blocked, note_count, attachment_count,
  version, created_at, updated_at
) VALUES
(
  '01920000-1000-7000-8000-000000000001',
  '01920000-0000-7000-8000-000000000001',
  '01920000-0001-7000-8000-000000000001',
  'Inspect AHU-1 fan vibration',
  'Verify the reported vibration and record the maintenance outcome.',
  'HIGH', 'OPEN', 'principal:operator-a', 'team:mechanical',
  '2026-08-02T01:00:00Z', '2026-08-02T04:00:00Z',
  2, 1, 0, 1, 1,
  1, '2026-08-01T09:00:00Z', '2026-08-01T10:00:00Z'
),
(
  '01920000-1000-7000-8000-000000000002',
  '01920000-0000-7000-8000-000000000001',
  '01920000-0001-7000-8000-000000000001',
  'Calibrate chilled-water sensor',
  'Complete the approved calibration procedure and preserve the investigation link.',
  'URGENT', 'IN_PROGRESS', 'principal:operator-b', 'team:controls',
  '2026-08-01T10:30:00Z', '2026-08-01T13:00:00Z',
  1, 0, 0, 0, 0,
  2, '2026-08-01T08:00:00Z', '2026-08-01T11:00:00Z'
),
(
  '01920000-1000-7000-8000-000000000003',
  '01920000-0000-7000-8000-000000000001',
  '01920000-0001-7000-8000-000000000002',
  'Replace return-air filter',
  'Replace the filter and capture completion evidence.',
  'MEDIUM', 'COMPLETED', NULL, 'team:mechanical',
  '2026-07-31T01:00:00Z', '2026-07-31T03:00:00Z',
  1, 1, 0, 0, 0,
  3, '2026-07-31T00:00:00Z', '2026-07-31T02:00:00Z'
),
(
  '01920000-1000-7000-8000-000000000004',
  '01920000-0000-7000-8000-000000000002',
  '01920000-0001-7000-8000-000000000003',
  'Inspect tenant heat complaint',
  'Validate the external request within the second Organization.',
  'LOW', 'OPEN', NULL, NULL,
  NULL, NULL,
  0, 0, 0, 0, 0,
  1, '2026-08-01T07:00:00Z', '2026-08-01T07:00:00Z'
);

INSERT INTO work_order_runtime.work_order_source_reference (
  organization_id, site_id, work_order_id, source_domain, source_resource_id, relationship, created_at
) VALUES
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000001', 'ALARM', '01920000-2000-7000-8000-000000000001', 'ORIGIN', '2026-08-01T09:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000002', '01920000-1000-7000-8000-000000000003', 'ALARM', '01920000-2000-7000-8000-000000000002', 'ORIGIN', '2026-07-31T00:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000002', 'MANUAL', 'manual:calibration-2026-08-01', 'ORIGIN', '2026-08-01T08:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000002', 'INVESTIGATION', '01920000-3000-7000-8000-000000000001', 'RELATED', '2026-08-01T08:00:00Z'),
('01920000-0000-7000-8000-000000000002', '01920000-0001-7000-8000-000000000003', '01920000-1000-7000-8000-000000000004', 'EXTERNAL', 'ticket:tenant-heat-44', 'ORIGIN', '2026-08-01T07:00:00Z');

INSERT INTO work_order_runtime.work_order_timeline (
  organization_id, site_id, work_order_id, version, operation, from_status, to_status,
  reason, actor_type, actor_id, assignee_id, team_id, policy_revision, correlation_id, occurred_at
) VALUES
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000001', 1, 'CREATE', NULL, 'OPEN', 'created from authoritative Alarm', 'PRINCIPAL', 'principal:operator-a', 'principal:operator-a', 'team:mechanical', 'work-order-policy-1', 'correlation-wo-1', '2026-08-01T09:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000002', 1, 'CREATE', NULL, 'OPEN', 'created by operator', 'PRINCIPAL', 'principal:operator-b', 'principal:operator-b', 'team:controls', 'work-order-policy-1', 'correlation-wo-2-create', '2026-08-01T08:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000002', 2, 'START', 'OPEN', 'IN_PROGRESS', 'calibration started', 'PRINCIPAL', 'principal:operator-b', NULL, NULL, 'work-order-policy-2', 'correlation-wo-2-start', '2026-08-01T11:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000002', '01920000-1000-7000-8000-000000000003', 1, 'CREATE', NULL, 'OPEN', 'created from authoritative Alarm', 'PRINCIPAL', 'principal:operator-c', NULL, 'team:mechanical', 'work-order-policy-1', 'correlation-wo-3-create', '2026-07-31T00:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000002', '01920000-1000-7000-8000-000000000003', 2, 'START', 'OPEN', 'IN_PROGRESS', 'filter replacement started', 'PRINCIPAL', 'principal:operator-c', NULL, NULL, 'work-order-policy-2', 'correlation-wo-3-start', '2026-07-31T01:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000002', '01920000-1000-7000-8000-000000000003', 3, 'COMPLETE', 'IN_PROGRESS', 'COMPLETED', 'filter replacement verified', 'PRINCIPAL', 'principal:operator-c', NULL, NULL, 'work-order-policy-3', 'correlation-wo-3-complete', '2026-07-31T02:00:00Z'),
('01920000-0000-7000-8000-000000000002', '01920000-0001-7000-8000-000000000003', '01920000-1000-7000-8000-000000000004', 1, 'CREATE', NULL, 'OPEN', 'created from external request', 'PRINCIPAL', 'principal:operator-d', NULL, NULL, 'work-order-policy-1', 'correlation-wo-4', '2026-08-01T07:00:00Z');

INSERT INTO work_order_runtime.work_order_task (
  organization_id, site_id, work_order_id, task_id, position, title, status, version, created_at, updated_at
) VALUES
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000001', '01920000-4000-7000-8000-000000000001', 0, 'Inspect fan assembly', 'COMPLETED', 1, '2026-08-01T09:00:00Z', '2026-08-01T09:30:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000001', '01920000-4000-7000-8000-000000000002', 1, 'Record vibration reading', 'OPEN', 1, '2026-08-01T09:00:00Z', '2026-08-01T09:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000002', '01920000-4000-7000-8000-000000000003', 0, 'Calibrate sensor', 'OPEN', 1, '2026-08-01T08:00:00Z', '2026-08-01T08:00:00Z'),
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000002', '01920000-1000-7000-8000-000000000003', '01920000-4000-7000-8000-000000000004', 0, 'Replace filter', 'COMPLETED', 1, '2026-07-31T00:00:00Z', '2026-07-31T01:30:00Z');

INSERT INTO work_order_runtime.work_order_note (
  organization_id, site_id, work_order_id, note_id, author_id, body, created_at
) VALUES
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000001', '01920000-5000-7000-8000-000000000001', 'principal:operator-a', 'Bearing housing requires follow-up inspection.', '2026-08-01T09:40:00Z');

INSERT INTO work_order_runtime.work_order_attachment_metadata (
  organization_id, site_id, work_order_id, attachment_id, object_reference, media_type, byte_size, sha256, created_by, created_at
) VALUES
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000001', '01920000-1000-7000-8000-000000000001', '01920000-6000-7000-8000-000000000001', 'object://work-orders/wo-1/vibration-photo', 'image/jpeg', 2048, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'principal:operator-a', '2026-08-01T09:45:00Z');

INSERT INTO work_order_runtime.work_order_completion_evidence (
  organization_id, site_id, work_order_id, kind, reference, captured_at, completion_version
) VALUES
('01920000-0000-7000-8000-000000000001', '01920000-0001-7000-8000-000000000002', '01920000-1000-7000-8000-000000000003', 'verification-report', 'evidence://work-orders/wo-3/verification', '2026-07-31T02:00:00Z', 3);

COMMIT;
