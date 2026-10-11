import type { CommandApprovalPolicy, CommandRisk, CommandStatus } from './command-api';

type StatusVariant = 'default' | 'success' | 'error' | 'warning' | 'info';

export const STATUS_PRESENTATION: Readonly<Record<CommandStatus, { label: string; variant: StatusVariant }>> = {
  SUBMITTED: { label: '已提交', variant: 'info' },
  VALIDATING: { label: '校验中', variant: 'info' },
  AWAITING_APPROVAL: { label: '待审批', variant: 'warning' },
  APPROVED: { label: '已批准', variant: 'info' },
  QUEUED: { label: '排队下发', variant: 'info' },
  DISPATCHING: { label: '下发中', variant: 'info' },
  SUCCEEDED: { label: '已执行', variant: 'success' },
  FAILED: { label: '执行失败', variant: 'error' },
  REJECTED: { label: '已拒绝', variant: 'error' },
  CANCELLED: { label: '已取消', variant: 'default' },
  EXPIRED: { label: '已过期', variant: 'default' },
  OUTCOME_UNKNOWN: { label: '待核对', variant: 'warning' },
};

export const RISK_LABEL: Readonly<Record<CommandRisk, string>> = { LOW: '低风险', MEDIUM: '中风险', HIGH: '高风险' };

export const APPROVAL_LABEL: Readonly<Record<CommandApprovalPolicy, string>> = {
  NONE: '无需审批',
  SINGLE_APPROVER: '需一人审批',
  TWO_PERSON: '需两人审批',
};

/** Moving toward the plant: submitted but neither settled nor waiting on a person. */
export const IN_FLIGHT: ReadonlySet<CommandStatus> = new Set(['SUBMITTED', 'VALIDATING', 'APPROVED', 'QUEUED', 'DISPATCHING']);
