import type { AuditSearchRecord } from '../../../api/generated/platformGateway.gen';
import type { StatusTone } from '../../../components/common/StatusBadge';

export interface AuditRow {
  readonly key: string;
  readonly occurredAt: string;
  readonly actor: string;
  readonly action: string;
  readonly resource: string;
  readonly outcome: string;
  readonly tone: StatusTone;
}

export interface AuditViewer {
  readonly subject: string;
  readonly displayName: string;
}

const ACTIONS: Readonly<Record<string, string>> = {
  SESSION_CREATED: '登录',
  SESSION_REVOKED: '撤销会话',
};

const RESOURCES: Readonly<Record<string, string>> = {
  'bff-session': '登录会话',
};

const OUTCOMES: Readonly<Record<string, { label: string; tone: StatusTone }>> = {
  SUCCEEDED: { label: '成功', tone: 'success' },
  FAILED: { label: '失败', tone: 'destructive' },
  DENIED: { label: '拒绝', tone: 'destructive' },
};

// Operators see business facts only: no identifiers and no internal codes. With no user
// directory yet, only the viewer's own account can be named.
export function presentAuditRecord(record: AuditSearchRecord, viewer: AuditViewer): AuditRow {
  const outcome = OUTCOMES[record.outcome] ?? { label: '其他结果', tone: 'neutral' as const };
  return {
    key: String(record.ledgerSequence),
    occurredAt: record.occurredAt,
    actor: record.actor === viewer.subject ? viewer.displayName : '其他账号',
    action: ACTIONS[record.action] ?? '未识别的操作',
    resource: RESOURCES[record.resourceType] ?? '其他对象',
    outcome: outcome.label,
    tone: outcome.tone,
  };
}
