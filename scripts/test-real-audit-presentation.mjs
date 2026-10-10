import assert from 'node:assert/strict';
import test from 'node:test';
import { presentAuditRecord } from '../apps/hvac-web/src/features/system/audit/model.ts';

const currentUser = { subject: '01a1214f-312f-7898-986d-b1aef872f3ae', displayName: '本地管理员' };
const record = (overrides) => ({
  ledgerSequence: 1, messageId: '01a12370-0000-7000-8000-000000000001', tenantId: '018f3d00-0000-7000-8000-000000000001',
  actor: currentUser.subject, action: 'SESSION_CREATED', resourceType: 'bff-session', resourceId: '01a12370-0000-7000-8000-000000000002',
  outcome: 'SUCCEEDED', policyRevision: '7', correlationId: '01a12370-0000-7000-8000-000000000003', occurredAt: '2026-10-10T01:50:40Z',
  ...overrides,
});

test('audit rows show business facts, never identifiers or internal codes', () => {
  const row = presentAuditRecord(record({}), currentUser);
  assert.deepEqual({ actor: row.actor, action: row.action, resource: row.resource, outcome: row.outcome, tone: row.tone },
    { actor: '本地管理员', action: '登录', resource: '登录会话', outcome: '成功', tone: 'success' });
  const text = JSON.stringify({ actor: row.actor, action: row.action, resource: row.resource, outcome: row.outcome });
  assert.doesNotMatch(text, /[0-9a-f]{8}-[0-9a-f]{4}-|SESSION_|bff-|SUCCEEDED/);
});

test('other accounts and unrecognized codes stay unnamed instead of exposing raw values', () => {
  const row = presentAuditRecord(record({ actor: '01a12360-f4a7-76f6-a654-170eac9bc168', action: 'SOMETHING_NEW', resourceType: 'mystery', outcome: 'DENIED' }), currentUser);
  assert.deepEqual({ actor: row.actor, action: row.action, resource: row.resource, outcome: row.outcome, tone: row.tone },
    { actor: '其他账号', action: '未识别的操作', resource: '其他对象', outcome: '拒绝', tone: 'destructive' });
});
