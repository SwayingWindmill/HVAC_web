import assert from 'node:assert/strict';
import test from 'node:test';
import { presentAuditRecord } from '../apps/hvac-web/src/features/system/audit/model.ts';

type AuditRecordInput = Parameters<typeof presentAuditRecord>[0];
type PresentedAuditRow = ReturnType<typeof presentAuditRecord>;

const currentUser = { subject: '01a1214f-312f-7898-986d-b1aef872f3ae', displayName: '本地管理员' };
const record = (overrides: Record<string, unknown>): AuditRecordInput => ({
  ledgerSequence: 1,
  messageId: '01a12370-0000-7000-8000-000000000001',
  tenantId: '018f3d00-0000-7000-8000-000000000001',
  actor: currentUser.subject,
  action: 'SESSION_CREATED',
  resourceType: 'bff-session',
  resourceId: '01a12370-0000-7000-8000-000000000002',
  outcome: 'SUCCEEDED',
  policyRevision: '7',
  correlationId: '01a12370-0000-7000-8000-000000000003',
  occurredAt: '2026-10-10T01:50:40Z',
  ...overrides,
} as AuditRecordInput);

test('audit rows show business facts, never identifiers or internal codes', () => {
  const row = presentAuditRecord(record({}), currentUser);
  assert.deepEqual(
    { actor: row.actor, action: row.action, resource: row.resource, target: row.target, clientKind: row.clientKind, summary: row.summary, outcome: row.outcome, tone: row.tone },
    { actor: '本地管理员', action: '登录', resource: '登录会话', target: '登录会话', clientKind: '—', summary: '—', outcome: '成功', tone: 'success' },
  );
  const text = JSON.stringify(row);
  assert.doesNotMatch(text, /[0-9a-f]{8}-[0-9a-f]{4}-|SESSION_|bff-|SUCCEEDED/);
});

test('other accounts and unrecognized codes stay unnamed instead of exposing raw values', () => {
  const row = presentAuditRecord(record({
    actor: '01a12360-f4a7-76f6-a654-170eac9bc168',
    actorDisplay: '01a12360-f4a7-76f6-a654-170eac9bc168',
    targetLabel: '01a12360-f4a7-76f6-a654-170eac9bc169',
    action: 'SOMETHING_NEW',
    resourceType: 'mystery',
    outcome: 'DENIED',
  }), currentUser);
  assert.deepEqual(
    { actor: row.actor, action: row.action, resource: row.resource, target: row.target, outcome: row.outcome, tone: row.tone, summary: row.summary },
    { actor: '其他账号', action: '未识别的操作', resource: '其他对象', target: '其他对象', outcome: '拒绝', tone: 'destructive', summary: '权限校验未通过' },
  );
});

test('domain operations format terminal badges, targets, and Chinese parameter diffs cleanly', () => {
  const cases: Array<[Record<string, unknown>, Partial<PresentedAuditRow>]> = [
    [{ actor: '01a12360-f4a7-76f6-a654-170eac9bc168', actorDisplay: '王值班', clientKind: 'MINI_PROGRAM', action: 'COMMAND_SUBMITTED', resourceType: 'remote-command', targetLabel: '1号冷水机组温度调节', changeSummary: { parameters: { setpointC: 23.5 } } }, { clientKind: '小程序', actor: '王值班', action: '下发控制指令', target: '1号冷水机组温度调节', summary: '温度设定 23.5℃' }],
    [{ actorDisplay: '张主管', clientKind: 'BROWSER', action: 'SITE_CONTROL_MODE_UPDATED', resourceType: 'control-lock', targetLabel: '松雅轩冷站', changeSummary: { afterMode: 'MANUAL_OVERRIDE', cancelledCount: 2, reason: '现场维保接管' } }, { clientKind: '网页', summary: '模式：本地手动；取消待发指令 2 条；原因：现场维保接管' }],
    [{ actorDisplay: '系统管理员', clientKind: 'BROWSER', action: 'IAM_MEMBER_STATUS_UPDATED', resourceType: 'iam-admin', targetLabel: '李值班', changeSummary: { beforeEnabled: true, afterEnabled: false } }, { action: '变更成员状态', summary: '状态：启用 → 停用' }],
    [{ actorDisplay: '王值班', clientKind: 'MINI_PROGRAM', action: 'REGISTRY_ASSET_MUTATED', resourceType: 'registry-mutation', targetLabel: '2号离心机', changeSummary: { changedFields: ['displayName', 'parentSpaceId', 'pointCode', 'sourceKey', 'tags', 'templateKey'], reason: '补充标准点位登记' } }, { summary: '变更字段：名称、上级空间、点位编码、采集源标识、标签、模板标识；原因：补充标准点位登记' }],
    [{ actorDisplay: '系统管理员', clientKind: 'BROWSER', action: 'IAM_MEMBER_PASSWORD_RESET', resourceType: 'iam-admin', targetLabel: '李值班', changeSummary: { passwordChanged: true } }, { action: '重置成员密码', summary: '已完成密码更新' }],
    [{ actorDisplay: '张主管', clientKind: 'BROWSER', action: 'OPERATING_SCHEDULE_UPDATED', resourceType: 'operating-schedule', targetLabel: '站点运行计划（夏季制冷计划）', changeSummary: { scheduleName: '夏季制冷计划', entriesCount: 5, preCoolLeadMinutes: 45, coastDownLagMinutes: 30, reason: '迎峰度夏调整' } }, { summary: '计划：夏季制冷计划；时段 5 条；预冷提前 45 分钟；停机滞后 30 分钟；原因：迎峰度夏调整' }],
    [{ actorDisplay: 'AI 自动优化', clientKind: 'SYSTEM', action: 'COMMAND_SUBMITTED', resourceType: 'remote-command', targetLabel: '风速设定指令', changeSummary: { parameters: { fanSpeedPct: 65 } } }, { clientKind: '系统', target: '风速设定指令', summary: '风速 65%' }],
  ];
  for (const [over, expectFields] of cases) {
    const row = presentAuditRecord(record(over), currentUser);
    for (const [key, want] of Object.entries(expectFields)) {
      assert.equal(row[key as keyof PresentedAuditRow], want);
    }
  }
});
