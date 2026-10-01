import type { Alarm, AlarmOperation, AlarmSeverity } from './alarm-api';

export const SEVERITY_ORDER: readonly AlarmSeverity[] = ['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO'];

export const SEVERITY_LABELS: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '次要',
  WARNING: '预警',
  INFO: '提示',
};

export const SEVERITY_CLASSES: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/50 dark:text-red-300',
  MAJOR: 'border-orange-200 bg-orange-50 text-orange-700 dark:border-orange-900 dark:bg-orange-950/50 dark:text-orange-300',
  MINOR: 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/50 dark:text-amber-300',
  WARNING: 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900 dark:bg-sky-950/50 dark:text-sky-300',
  INFO: 'border-border bg-muted text-muted-foreground',
};

const OPERATION_LABELS: Readonly<Record<AlarmOperation, string>> = {
  PUBLISH: '触发',
  ACKNOWLEDGE: '确认',
  ASSIGN: '认领',
  UNASSIGN: '取消认领',
  SUPPRESS: '抑制',
  UNSUPPRESS: '解除抑制',
  CLEAR: '恢复',
};

export function operationLabel(operation: AlarmOperation): string {
  return OPERATION_LABELS[operation];
}

/** Condition and acknowledgement shown together, as an operator reads them. */
export function alarmStatusLabel(alarm: Alarm): string {
  const condition = alarm.condition === 'ACTIVE' ? '活动' : '已恢复';
  return `${condition} · ${alarm.acknowledgement ? '已确认' : '未确认'}`;
}

export function formatDuration(from: string, to: string | undefined, now: number): string {
  const minutes = Math.max(0, Math.round(((to ? Date.parse(to) : now) - Date.parse(from)) / 60_000));
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} 小时 ${minutes % 60} 分`;
  return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`;
}

export function severityRank(severity: AlarmSeverity): number {
  return SEVERITY_ORDER.indexOf(severity);
}
