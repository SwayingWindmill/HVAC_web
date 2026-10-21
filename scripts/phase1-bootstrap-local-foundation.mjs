import { spawnSync } from 'node:child_process';

import { centralPlantIdentity, localUUID, sqlLiteral } from './central-plant-local-contract.mjs';
import { localContainer } from './lib/local-environment.mjs';

const postgresContainer = process.env.PHASE1_POSTGRES_CONTAINER || localContainer('postgres');
const { tenantId, siteId } = centralPlantIdentity;
const policyIDs = {
  registry: localUUID(0x900000000001),
  telemetry: localUUID(0x900000000002),
  alarm: localUUID(0x900000000003),
  workOrder: localUUID(0x900000000004),
};
// Unattended workloads read owners as their own Workload Principal (ADR 0017), each with one
// Tenant-wide role holding only what it reads. Phase 1 runs the energy projector inside the
// Telemetry Runtime, so that identity resolves meter bindings and reads the import Tariffs it lays
// out as price windows (#575); report-service, the optimization service and M&V read the Sites'
// energy and its cost through the Telemetry Query Service (ADR 0023); the Gateway's workload reads
// the Site a WeChat alarm reminder names.
const workloads = [
  {
    subject: 'spiffe://hvac.local/telemetry-runtime-service', displayName: '能耗投影（遥测运行时）',
    roleKey: 'energy-projection', roleName: '能耗投影', capabilities: ['meter-binding.resolve', 'tariff.read'], ids: 0x900000000101,
  },
  {
    subject: 'spiffe://hvac.local/report-service', displayName: '能源报告',
    roleKey: 'report-energy-read', roleName: '报告读取能耗', capabilities: ['analytics.energy-series.read'], ids: 0x900000000111,
  },
  {
    subject: 'spiffe://hvac.local/optimization-service', displayName: '优化建议',
    roleKey: 'optimization-energy-read', roleName: '优化建议读取能耗', capabilities: ['analytics.energy-series.read'], ids: 0x900000000121,
  },
  {
    subject: 'spiffe://hvac.local/mv-service', displayName: '节能核验',
    roleKey: 'mv-energy-read', roleName: '节能核验读取能耗', capabilities: ['analytics.energy-series.read'], ids: 0x900000000131,
  },
  // WeChat alarm reminders name the Site and its time (docs/specs/miniprogram-wechat.md); the
  // reminder worker runs in the Gateway's workload.
  {
    subject: 'spiffe://hvac.local/platform-gateway', displayName: '微信告警提醒',
    roleKey: 'wechat-reminder-site-read', roleName: '微信提醒读取站点', capabilities: ['site.read'], ids: 0x900000000141,
  },
].map((workload) => ({
  ...workload,
  issuer: 'spiffe://hvac.local',
  principalId: localUUID(workload.ids),
  membershipId: localUUID(workload.ids + 1),
  roleTemplateId: localUUID(workload.ids + 2),
  roleBindingId: localUUID(workload.ids + 3),
}));

const workloadSql = (workload) => `
INSERT INTO iam.principals (id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(workload.principalId)}, ${sqlLiteral(workload.issuer)}, ${sqlLiteral(workload.subject)},
  ${sqlLiteral(workload.displayName)}, '', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (external_issuer, external_subject) DO UPDATE
SET status = 'ACTIVE', updated_at = clock_timestamp();

INSERT INTO iam.tenant_memberships (id, tenant_id, principal_id, status, valid_from, valid_to, revision, created_at, updated_at)
SELECT ${sqlLiteral(workload.membershipId)}, ${sqlLiteral(tenantId)}, principal.id, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
FROM iam.principals principal
WHERE principal.external_issuer = ${sqlLiteral(workload.issuer)} AND principal.external_subject = ${sqlLiteral(workload.subject)}
ON CONFLICT (tenant_id, principal_id) DO UPDATE
SET status = 'ACTIVE', valid_to = NULL, updated_at = clock_timestamp();

INSERT INTO iam.role_templates (id, tenant_id, role_key, display_name, capabilities, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(workload.roleTemplateId)}, ${sqlLiteral(tenantId)}, ${sqlLiteral(workload.roleKey)}, ${sqlLiteral(workload.roleName)},
  ARRAY[${workload.capabilities.map(sqlLiteral).join(', ')}]::text[], 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (tenant_id, role_key) DO UPDATE
SET capabilities = EXCLUDED.capabilities, status = 'ACTIVE', updated_at = clock_timestamp();

INSERT INTO iam.role_bindings (id, tenant_id, principal_id, role_template_id, status, valid_from, valid_to, revision, created_at, updated_at)
SELECT ${sqlLiteral(workload.roleBindingId)}, ${sqlLiteral(tenantId)}, principal.id, template.id, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
FROM iam.principals principal
JOIN iam.role_templates template ON template.tenant_id = ${sqlLiteral(tenantId)} AND template.role_key = ${sqlLiteral(workload.roleKey)}
WHERE principal.external_issuer = ${sqlLiteral(workload.issuer)} AND principal.external_subject = ${sqlLiteral(workload.subject)}
ON CONFLICT (tenant_id, principal_id, role_template_id, site_id) DO UPDATE
SET status = 'ACTIVE', valid_to = NULL, updated_at = clock_timestamp();
`;
const sql = `
BEGIN;
INSERT INTO iam.tenants (
  id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at
) VALUES (
  ${sqlLiteral(tenantId)}, 'central-plant-local', '中央机房本地验证租户', 'Asia/Shanghai', 'CNY', 'CN',
  'ACTIVE', 1, clock_timestamp(), clock_timestamp()
)
ON CONFLICT (id) DO UPDATE
SET display_name = EXCLUDED.display_name,
    timezone = EXCLUDED.timezone,
    currency = EXCLUDED.currency,
    country = EXCLUDED.country,
    status = 'ACTIVE',
    updated_at = clock_timestamp();

INSERT INTO core_registry.sites (
  id, tenant_id, code, display_name, timezone, status, revision, created_at, updated_at
) VALUES (
  ${sqlLiteral(siteId)}, ${sqlLiteral(tenantId)}, 'central-plant', '中央机房', 'Asia/Shanghai',
  'ACTIVE', 1, clock_timestamp(), clock_timestamp()
)
ON CONFLICT (id) DO UPDATE
SET tenant_id = EXCLUDED.tenant_id,
    display_name = EXCLUDED.display_name,
    timezone = EXCLUDED.timezone,
    status = 'ACTIVE',
    updated_at = clock_timestamp();

INSERT INTO iam.authorization_revisions (tenant_id, revision, updated_at)
VALUES (${sqlLiteral(tenantId)}, 1, clock_timestamp())
ON CONFLICT (tenant_id) DO NOTHING;

INSERT INTO iam.policies (
  id, tenant_id, policy_key, policy_revision, status, document, created_at, updated_at
) VALUES
  (${sqlLiteral(policyIDs.registry)}, ${sqlLiteral(tenantId)}, 'registry-read', 1, 'ACTIVE', '{}'::jsonb, clock_timestamp(), clock_timestamp()),
  (${sqlLiteral(policyIDs.telemetry)}, ${sqlLiteral(tenantId)}, 'telemetry-access', 1, 'ACTIVE', '{}'::jsonb, clock_timestamp(), clock_timestamp()),
  (${sqlLiteral(policyIDs.alarm)}, ${sqlLiteral(tenantId)}, 'alarm-access', 1, 'ACTIVE', '{}'::jsonb, clock_timestamp(), clock_timestamp()),
  (${sqlLiteral(policyIDs.workOrder)}, ${sqlLiteral(tenantId)}, 'work-order-access', 1, 'ACTIVE', '{}'::jsonb, clock_timestamp(), clock_timestamp())
ON CONFLICT (tenant_id, policy_key, policy_revision) DO UPDATE
SET status = 'ACTIVE',
    policy_revision = GREATEST(iam.policies.policy_revision, EXCLUDED.policy_revision),
    updated_at = clock_timestamp();

${workloads.map(workloadSql).join('')}COMMIT;
`;

const result = spawnSync('docker', [
  'exec', '-i', postgresContainer,
  'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1',
], {
  input: sql,
  stdio: ['pipe', 'inherit', 'inherit'],
});

if (result.error) throw result.error;
if (result.status !== 0) process.exit(result.status ?? 1);
console.log('Local Phase 1 Tenant/Site foundation applied.');
