import { formatDecimal } from '../../lib/operator-format.ts';
import type { Command, CommandApprovalPolicy, CommandPriorityTier, CommandRisk, CommandSource, CommandStatus } from './command-api';
import type { ActionCapability, CommandLabel } from './control-model';

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
  CANCELLED: { label: '已撤回', variant: 'default' },
  EXPIRED: { label: '已过期', variant: 'default' },
  OUTCOME_UNKNOWN: { label: '待现场确认', variant: 'warning' },
};

export const RISK_LABEL: Readonly<Record<CommandRisk, string>> = { LOW: '低风险', MEDIUM: '中风险', HIGH: '高风险', CRITICAL: '极高风险' };

export const APPROVAL_LABEL: Readonly<Record<CommandApprovalPolicy, string>> = {
  NONE: '无需审批',
  SINGLE_APPROVER: '需一人审批',
  TWO_PERSON: '需两人审批',
};

export const PRIORITY_LABEL: Readonly<Record<CommandPriorityTier, string>> = {
  SAFETY_INTERLOCK: '安全联锁',
  MANUAL_OVERRIDE: '人工干预',
  PROTECTION_RULE: '保护规则',
  AI_OPTIMIZATION: 'AI 优化',
  SCHEDULE: '定时日程',
};

export const COMMAND_SOURCE_LABEL: Readonly<Record<CommandSource, string>> = {
  SAFETY_INTERLOCK: '安全联锁保护',
  OPERATOR: '值班人员操作',
  PROTECTION_RULE: '本地保护规则',
  OPTIMIZATION: '自动节能策略',
  SCHEDULE: '运行日程控制',
};

export function transitionReasonNote(reason: string): string | null {
  switch (reason) {
    case 'DEVICE_PRIORITY_LOCKED':
      return '设备正由更高优先级指令锁定';
    case 'PRIORITY_PREEMPTED':
      return '已被更高优先级指令抢占撤回';
    default:
      return null;
  }
}

/** The verb on a button and in a command's title, e.g. 一键停机. */
export const ACTION_VERB: Readonly<Record<ActionCapability, string>> = {
  START: '启动',
  STOP: '停止',
  START_UNIT_SEQUENCE: '一键启动',
  STOP_UNIT_SEQUENCE: '一键停机',
  RESET_FAULT: '复位故障',
};

/**
 * Starting or stopping plant equipment is high risk at its base, so the Gateway asks for the
 * authenticator code at submit; settings are assessed per change and may ask for it on refusal.
 */
export const CODE_AT_SUBMIT: ReadonlySet<ActionCapability> = new Set(['START', 'STOP', 'START_UNIT_SEQUENCE', 'STOP_UNIT_SEQUENCE']);

/** Moving toward the plant: submitted but neither settled nor waiting on a person. */
export const IN_FLIGHT: ReadonlySet<CommandStatus> = new Set(['SUBMITTED', 'VALIDATING', 'APPROVED', 'QUEUED', 'DISPATCHING']);

/** A command as the operator issued it: the point and the value it was set to, on which asset. */
export function commandSummary(command: Pick<Command, 'pointId' | 'parameters'>, labels: ReadonlyMap<string, CommandLabel>): { title: string; target: string } {
  const label = labels.get(command.pointId);
  const value = Object.values(command.parameters)[0];
  const setTo = value === undefined ? '' : ` → ${formatDecimal(value, 1)}${label?.unit ? ` ${label.unit}` : ''}`;
  return { title: `${label?.label ?? '已移除的控制点'}${setTo}`, target: label?.assetName ?? '未登记设备' };
}

/** Waiting on a person: an approval, or a site check after the plant did not answer. */
export const needsPerson = (command: Pick<Command, 'status'>) => command.status === 'AWAITING_APPROVAL' || command.status === 'OUTCOME_UNKNOWN';
