import type {
  Alarm,
  AlarmOperation,
  AlarmSeverity,
  AlarmSourceType,
} from '../api/alarm-contract.ts';

export type AlarmBusinessState = 'ACTIVE' | 'ACKNOWLEDGED' | 'SUPPRESSED' | 'CLEARED';

export interface RealAlarmProjection {
  readonly businessState: AlarmBusinessState;
  readonly statusLabel: string;
  readonly severityLabel: string;
  readonly sourceLabel: string;
  readonly occurrenceLabel: string;
  readonly canMutate: true;
  readonly canAcknowledge: boolean;
  readonly canAssign: boolean;
  readonly canUnassign: boolean;
  readonly canSuppress: boolean;
  readonly canUnsuppress: boolean;
}

const stateLabels: Record<AlarmBusinessState, string> = {
  ACTIVE: '活动',
  ACKNOWLEDGED: '已确认',
  SUPPRESSED: '已抑制',
  CLEARED: '已恢复',
};

const severityLabels: Record<AlarmSeverity, string> = {
  INFO: '提示',
  WARNING: '警告',
  MINOR: '次要',
  MAJOR: '重要',
  CRITICAL: '严重',
};

const sourceLabels: Record<AlarmSourceType, string> = {
  DEVICE_RULE: '设备规则',
  SITE_RULE: 'Site 规则',
  EXTERNAL: '外部权威源',
};

const operationLabels: Record<AlarmOperation, string> = {
  PUBLISH: '发布',
  ACKNOWLEDGE: '确认',
  ASSIGN: '指派',
  UNASSIGN: '取消指派',
  SUPPRESS: '抑制',
  UNSUPPRESS: '解除抑制',
  CLEAR: '恢复',
};

export function projectRealAlarm(alarm: Alarm): RealAlarmProjection {
  const active = alarm.condition === 'ACTIVE';
  const businessState: AlarmBusinessState = !active
    ? 'CLEARED'
    : alarm.suppression
      ? 'SUPPRESSED'
      : alarm.acknowledgement
        ? 'ACKNOWLEDGED'
        : 'ACTIVE';
  return Object.freeze({
    businessState,
    statusLabel: stateLabels[businessState],
    severityLabel: severityLabels[alarm.currentSeverity],
    sourceLabel: sourceLabels[alarm.sourceType],
    occurrenceLabel: alarm.occurrenceCount === 1 ? '首次发生' : `累计 ${alarm.occurrenceCount} 次`,
    canMutate: true as const,
    canAcknowledge: !alarm.acknowledgement,
    canAssign: active,
    canUnassign: active && Boolean(alarm.assigneeId),
    canSuppress: active && !alarm.suppression,
    canUnsuppress: active && Boolean(alarm.suppression),
  });
}

export function alarmBusinessStateLabel(state: AlarmBusinessState): string {
  return stateLabels[state];
}

export function alarmOperationLabel(operation: AlarmOperation): string {
  return operationLabels[operation];
}
