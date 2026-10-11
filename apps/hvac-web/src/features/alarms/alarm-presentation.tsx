import { Badge } from '@/components/ui/badge';
import type { Alarm, AlarmOperation, AlarmSeverity } from './alarm-api';

export const SEVERITY_ORDER: readonly AlarmSeverity[] = ['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO'];

export const SEVERITY_LABELS: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '次要',
  WARNING: '预警',
  INFO: '提示',
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
  return formatSpan((to ? Date.parse(to) : now) - Date.parse(from));
}

/** A length of time in the units an operator reads: minutes, then hours and minutes, then days and hours. */
export function formatSpan(milliseconds: number): string {
  const minutes = Math.max(0, Math.round(milliseconds / 60_000));
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} 小时 ${minutes % 60} 分`;
  return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`;
}

export function severityRank(severity: AlarmSeverity): number {
  return SEVERITY_ORDER.indexOf(severity);
}

export const SEVERITY_COLORS: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: 'var(--severity-critical)',
  MAJOR: 'var(--severity-major)',
  MINOR: 'var(--severity-minor)',
  WARNING: 'var(--severity-warning)',
  INFO: 'var(--severity-info)',
};

export function SeverityBadge({ severity }: { readonly severity: AlarmSeverity }) {
  const color = SEVERITY_COLORS[severity];
  return (
    <Badge
      variant="outline"
      style={{ color, borderColor: `color-mix(in oklab, ${color} 35%, transparent)`, background: `color-mix(in oklab, ${color} 10%, transparent)` }}
    >
      {SEVERITY_LABELS[severity]}
    </Badge>
  );
}

/** Most severe first, then the most recent. */
export function sortAlarmsForOperators(alarms: readonly Alarm[]): Alarm[] {
  return [...alarms].sort((left, right) => severityRank(left.currentSeverity) - severityRank(right.currentSeverity)
    || Date.parse(right.lastOccurredAt) - Date.parse(left.lastOccurredAt));
}

