import type { Alarm, AlarmSeverity, AlarmSourceType, AlarmStatus } from '../api/alarm-contract.ts';

export type AlarmBusinessState = 'ACTIVE' | 'ACKNOWLEDGED' | 'SUPPRESSED' | 'CLOSED';

export interface RealAlarmProjection {
  readonly businessState: AlarmBusinessState;
  readonly statusLabel: string;
  readonly severityLabel: string;
  readonly sourceLabel: string;
  readonly occurrenceLabel: string;
  readonly canMutate: false;
}

const statusLabels: Record<AlarmStatus, string> = {
  OPEN: '未处理',
  ACKNOWLEDGED: '已确认',
  SUPPRESSED: '已抑制',
  CLOSED: '已关闭',
};

const severityLabels: Record<AlarmSeverity, string> = {
  INFO: '提示',
  WARNING: '警告',
  MAJOR: '重要',
  CRITICAL: '严重',
};

const sourceLabels: Record<AlarmSourceType, string> = {
  DEVICE_RULE: '设备规则',
  SITE_RULE: 'Site 规则',
  EXTERNAL: '外部权威源',
};

export function projectRealAlarm(alarm: Alarm): RealAlarmProjection {
  const businessState: AlarmBusinessState = alarm.status === 'OPEN'
    ? 'ACTIVE'
    : alarm.status;
  return Object.freeze({
    businessState,
    statusLabel: statusLabels[alarm.status],
    severityLabel: severityLabels[alarm.severity],
    sourceLabel: sourceLabels[alarm.sourceType],
    occurrenceLabel: alarm.occurrenceCount === 1 ? '首次发生' : `累计 ${alarm.occurrenceCount} 次`,
    canMutate: false as const,
  });
}

export function alarmStatusLabel(status: AlarmStatus): string {
  return statusLabels[status];
}
