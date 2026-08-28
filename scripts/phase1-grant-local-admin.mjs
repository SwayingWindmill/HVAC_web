import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const reconcilePath = process.env.PHASE1_IDENTITY_RECONCILE_FILE
  || path.join(repoRoot, 'deploy', 'platform', 'phase1', 'runtime', 'identity-reconcile.json');
const issuer = process.env.PHASE1_LOCAL_IDENTITY_ISSUER || 'https://localhost:8443/identity';
const postgresContainer = process.env.PHASE1_POSTGRES_CONTAINER || 'hvac-phase1-local-postgres-1';

const reconcile = JSON.parse(readFileSync(reconcilePath, 'utf8'));
const subject = reconcile.userId;
const tenantId = reconcile.seed?.tenantId;
const siteId = reconcile.seed?.siteBindings?.[0]?.siteId;
if (!subject || !tenantId || !siteId) {
  throw new Error('Local admin grant requires userId, tenantId, and at least one site binding in identity-reconcile.json');
}

const sql = String.raw`
BEGIN;

INSERT INTO iam.site_bindings(
  id, tenant_id, site_id, principal_id, actions, effect,
  valid_from, valid_to, revision, created_at, updated_at
)
SELECT
  '01a006a0-0030-7000-8000-000000000001'::uuid,
  :'tenant_id'::uuid,
  :'site_id'::uuid,
  p.id,
  ARRAY['analytics.energy-series.read']::text[],
  'ALLOW', now(), NULL, 1, now(), now()
FROM iam.principals p
WHERE p.external_issuer = :'admin_issuer'
  AND p.external_subject = :'admin_subject'
ON CONFLICT (tenant_id, site_id, principal_id) DO UPDATE
SET actions = (
      SELECT array_agg(action ORDER BY action)
      FROM (
        SELECT DISTINCT unnest(iam.site_bindings.actions || EXCLUDED.actions) AS action
      ) merged
    ),
    effect = 'ALLOW',
    valid_to = NULL,
    revision = iam.site_bindings.revision + 1,
    updated_at = now()
WHERE NOT EXCLUDED.actions <@ iam.site_bindings.actions
   OR iam.site_bindings.effect IS DISTINCT FROM 'ALLOW'
   OR iam.site_bindings.valid_to IS NOT NULL;

INSERT INTO iam.telemetry_scope_bindings(
  id, tenant_id, principal_id, site_id, device_id, actions, effect, status,
  valid_from, valid_to, revision, created_at, updated_at
)
SELECT
  '01a006a0-0000-7000-8000-000000000001'::uuid,
  :'tenant_id'::uuid,
  p.id,
  :'site_id'::uuid,
  NULL,
  ARRAY[
    'telemetry.batch.read',
    'telemetry.history.read',
    'telemetry.recovery.checkpoint',
    'telemetry.recovery.use',
    'telemetry.resubscribe',
    'telemetry.snapshot.read',
    'telemetry.subscribe'
  ]::text[],
  'ALLOW', 'ACTIVE', now(), NULL, 1, now(), now()
FROM iam.principals p
WHERE p.external_issuer = :'admin_issuer'
  AND p.external_subject = :'admin_subject'
ON CONFLICT (id) DO UPDATE
SET tenant_id = EXCLUDED.tenant_id,
    principal_id = EXCLUDED.principal_id,
    site_id = EXCLUDED.site_id,
    device_id = NULL,
    actions = EXCLUDED.actions,
    effect = 'ALLOW',
    status = 'ACTIVE',
    valid_to = NULL,
    revision = iam.telemetry_scope_bindings.revision + 1,
    updated_at = now()
WHERE iam.telemetry_scope_bindings.tenant_id IS DISTINCT FROM EXCLUDED.tenant_id
   OR iam.telemetry_scope_bindings.principal_id IS DISTINCT FROM EXCLUDED.principal_id
   OR iam.telemetry_scope_bindings.site_id IS DISTINCT FROM EXCLUDED.site_id
   OR iam.telemetry_scope_bindings.device_id IS NOT NULL
   OR iam.telemetry_scope_bindings.actions IS DISTINCT FROM EXCLUDED.actions
   OR iam.telemetry_scope_bindings.effect IS DISTINCT FROM 'ALLOW'
   OR iam.telemetry_scope_bindings.status IS DISTINCT FROM 'ACTIVE'
   OR iam.telemetry_scope_bindings.valid_to IS NOT NULL;

INSERT INTO iam.alarm_permissions(
  id, principal_id, tenant_id, site_id, action, effect, status,
  valid_from, valid_to, revision, created_at, updated_at
)
SELECT ids.id, p.id, :'tenant_id'::uuid, :'site_id'::uuid, ids.action,
       'ALLOW', 'ACTIVE', now(), NULL, 1, now(), now()
FROM iam.principals p
CROSS JOIN (VALUES
  ('01a006a0-0001-7000-8000-000000000001'::uuid, 'alarm:read'),
  ('01a006a0-0002-7000-8000-000000000001'::uuid, 'alarm:ack')
) AS ids(id, action)
WHERE p.external_issuer = :'admin_issuer'
  AND p.external_subject = :'admin_subject'
ON CONFLICT (principal_id, tenant_id, site_id, action, effect) DO UPDATE
SET status = 'ACTIVE',
    valid_to = NULL,
    revision = iam.alarm_permissions.revision + 1,
    updated_at = now()
WHERE iam.alarm_permissions.status IS DISTINCT FROM 'ACTIVE'
   OR iam.alarm_permissions.valid_to IS NOT NULL;

INSERT INTO iam.work_order_permissions(
  id, principal_id, tenant_id, site_id, action, effect, status,
  valid_from, valid_to, revision, created_at, updated_at
)
SELECT ids.id, p.id, :'tenant_id'::uuid, :'site_id'::uuid, ids.action,
       'ALLOW', 'ACTIVE', now(), NULL, 1, now(), now()
FROM iam.principals p
CROSS JOIN (VALUES
  ('01a006a0-0010-7000-8000-000000000001'::uuid, 'work-order:list'),
  ('01a006a0-0011-7000-8000-000000000001'::uuid, 'work-order:read'),
  ('01a006a0-0012-7000-8000-000000000001'::uuid, 'work-order:create'),
  ('01a006a0-0013-7000-8000-000000000001'::uuid, 'work-order:assign'),
  ('01a006a0-0014-7000-8000-000000000001'::uuid, 'work-order:plan'),
  ('01a006a0-0015-7000-8000-000000000001'::uuid, 'work-order:start'),
  ('01a006a0-0016-7000-8000-000000000001'::uuid, 'work-order:block'),
  ('01a006a0-0017-7000-8000-000000000001'::uuid, 'work-order:resume'),
  ('01a006a0-0018-7000-8000-000000000001'::uuid, 'work-order:complete'),
  ('01a006a0-0019-7000-8000-000000000001'::uuid, 'work-order:cancel'),
  ('01a006a0-0020-7000-8000-000000000001'::uuid, 'work-order:reopen')
) AS ids(id, action)
WHERE p.external_issuer = :'admin_issuer'
  AND p.external_subject = :'admin_subject'
ON CONFLICT (principal_id, tenant_id, site_id, action, effect) DO UPDATE
SET status = 'ACTIVE',
    valid_to = NULL,
    revision = iam.work_order_permissions.revision + 1,
    updated_at = now()
WHERE iam.work_order_permissions.status IS DISTINCT FROM 'ACTIVE'
   OR iam.work_order_permissions.valid_to IS NOT NULL;

COMMIT;
`;

const result = spawnSync('docker', [
  'exec',
  '-i',
  postgresContainer,
  'psql',
  '-U', 's1_iam_migrator',
  '-d', 'hvac_s1',
  '-v', 'ON_ERROR_STOP=1',
  '-v', `admin_issuer=${issuer}`,
  '-v', `admin_subject=${subject}`,
  '-v', `tenant_id=${tenantId}`,
  '-v', `site_id=${siteId}`,
], {
  cwd: repoRoot,
  input: sql,
  stdio: ['pipe', 'inherit', 'inherit'],
});

if (result.error) throw result.error;
if (result.status !== 0) process.exit(result.status ?? 1);
console.log('Local platform-admin grants applied.');
