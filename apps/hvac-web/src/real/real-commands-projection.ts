import type { Command, CommandStatus } from '../api/command-contract.ts';

export type RealCommandBusinessState =
  | 'IN_PROGRESS'
  | 'AWAITING_APPROVAL'
  | 'SUCCEEDED'
  | 'FAILED'
  | 'REJECTED'
  | 'CANCELLED'
  | 'EXPIRED'
  | 'OUTCOME_UNKNOWN';

export interface RealCommandProjection {
  readonly businessState: RealCommandBusinessState;
  readonly statusLabel: string;
  readonly terminal: boolean;
  readonly canApprove: boolean;
  readonly outcomeWarning: string | null;
}

const STATUS_LABELS: Record<CommandStatus, string> = {
  SUBMITTED: '已提交',
  VALIDATING: '正在校验',
  AWAITING_APPROVAL: '等待审批',
  APPROVED: '已批准',
  QUEUED: '已排队',
  DISPATCHING: '正在下发',
  SUCCEEDED: '已验证成功',
  FAILED: '失败',
  REJECTED: '已拒绝',
  CANCELLED: '已取消',
  EXPIRED: '已过期',
  OUTCOME_UNKNOWN: '设备结果待确认',
};

const TERMINAL_STATUSES = new Set<CommandStatus>([
  'SUCCEEDED',
  'FAILED',
  'REJECTED',
  'CANCELLED',
  'EXPIRED',
  'OUTCOME_UNKNOWN',
]);

export function commandStatusLabel(status: CommandStatus): string {
  return STATUS_LABELS[status];
}

export function isTerminalCommandStatus(status: CommandStatus): boolean {
  return TERMINAL_STATUSES.has(status);
}

export function projectRealCommand(command: Command): RealCommandProjection {
  const canApprove = command.status === 'AWAITING_APPROVAL'
    && command.approvalCount < command.requiredApprovalCount;
  const businessState: RealCommandBusinessState = command.status === 'AWAITING_APPROVAL'
    ? 'AWAITING_APPROVAL'
    : command.status === 'SUCCEEDED'
      ? 'SUCCEEDED'
      : command.status === 'FAILED'
        ? 'FAILED'
        : command.status === 'REJECTED'
          ? 'REJECTED'
          : command.status === 'CANCELLED'
            ? 'CANCELLED'
            : command.status === 'EXPIRED'
              ? 'EXPIRED'
              : command.status === 'OUTCOME_UNKNOWN'
                ? 'OUTCOME_UNKNOWN'
                : 'IN_PROGRESS';

  return Object.freeze({
    businessState,
    statusLabel: commandStatusLabel(command.status),
    terminal: isTerminalCommandStatus(command.status),
    canApprove,
    outcomeWarning: command.status === 'OUTCOME_UNKNOWN'
      ? '设备结果待确认。系统不会自动重发；请先核对设备状态和审计证据。'
      : null,
  });
}
