import type { Alarm, AlarmSeverity } from '@/api/alarms';

export type AlarmCenterView = 'active' | 'history' | 'suppressed' | 'performance';
export type AlarmAcknowledgementFilter = 'all' | 'unacknowledged' | 'acknowledged';
export type AlarmOwnershipFilter = 'all' | 'unassigned' | 'assigned';
export type AlarmWorkflowFilter = 'all' | 'unacknowledged' | 'acknowledged' | 'processing';
export type AlarmWorkflowState = Exclude<AlarmWorkflowFilter, 'all'> | 'recovered';
export type AlarmLifecycleStageKey = 'triggered' | 'acknowledged' | 'processing' | 'awaiting-recovery' | 'recovered';
export type AlarmLifecycleStageState = 'done' | 'current' | 'pending' | 'unavailable';
export interface AlarmLifecycleStage {
  key: AlarmLifecycleStageKey;
  state: AlarmLifecycleStageState;
}
export type AlarmSuppressionFilter = 'all' | 'suppressed' | 'normal';
export type AlarmSourceFilter = 'all' | 'DEVICE_RULE' | 'SITE_RULE' | 'EXTERNAL';

export interface AlarmRow {
  alarm: Alarm;
  workflow: AlarmWorkflowState;
  severityLabel: string;
  statusLabel: string;
  deviceLabel: string;
  locationLabel: string;
}

export const severityRank: Readonly<Record<AlarmSeverity, number>> = Object.freeze({
  CRITICAL: 5,
  MAJOR: 4,
  MINOR: 3,
  WARNING: 2,
  INFO: 1,
});

export const severityLabel: Readonly<Record<AlarmSeverity, string>> = Object.freeze({
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '一般',
  WARNING: '警告',
  INFO: '提示',
});

export const severityColor: Readonly<Record<AlarmSeverity, string>> = Object.freeze({
  CRITICAL: 'red',
  MAJOR: 'orange',
  MINOR: 'gold',
  WARNING: 'gold',
  INFO: 'blue',
});

export function formatInstant(value: string | undefined, timeZone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

export function durationMs(from: string, to: string | undefined): number {
  const end = to ? Date.parse(to) : Date.now();
  return Math.max(0, end - Date.parse(from));
}

export function formatDurationMs(milliseconds: number): string {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时 ${Math.floor((seconds % 3600) / 60)} 分`;
  return `${Math.floor(seconds / 86400)} 天 ${Math.floor((seconds % 86400) / 3600)} 小时`;
}

export function formatDuration(from: string, to: string | undefined): string {
  return formatDurationMs(durationMs(from, to));
}

export function localDateKey(value: string | number | Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(value));
}

export function alarmWorkflow(alarm: Alarm): AlarmWorkflowState {
  if (alarm.condition === 'CLEARED') return 'recovered';
  if (alarm.assigneeId) return 'processing';
  if (alarm.acknowledgement) return 'acknowledged';
  return 'unacknowledged';
}

export function alarmLifecycleStages(alarm: Alarm): readonly AlarmLifecycleStage[] {
  const active = alarm.condition === 'ACTIVE';
  const acknowledged = Boolean(alarm.acknowledgement);
  const assigned = Boolean(alarm.assigneeId);

  return Object.freeze([
    { key: 'triggered', state: 'done' },
    {
      key: 'acknowledged',
      state: acknowledged ? 'done' : active ? 'current' : 'pending',
    },
    {
      key: 'processing',
      state: assigned ? 'done' : active && acknowledged ? 'current' : 'pending',
    },
    {
      key: 'awaiting-recovery',
      state: !active ? 'done' : assigned ? 'current' : 'pending',
    },
    {
      key: 'recovered',
      state: !active ? 'done' : 'pending',
    },
  ] satisfies AlarmLifecycleStage[]);
}

export function workflowLabel(workflow: AlarmWorkflowState): string {
  switch (workflow) {
    case 'unacknowledged': return '未确认';
    case 'acknowledged': return '已确认';
    case 'processing': return '处理中';
    case 'recovered': return '已恢复';
  }
}

export function workflowTagColor(workflow: AlarmWorkflowState): string {
  switch (workflow) {
    case 'unacknowledged': return 'red';
    case 'acknowledged': return 'blue';
    case 'processing': return 'processing';
    case 'recovered': return 'green';
  }
}

export function linkedWorkOrderIds(alarm: Alarm): string[] {
  return alarm.links.filter((link) => link.kind === 'WORK_ORDER').map((link) => link.targetId);
}

export function recoveryReason(alarm: Alarm): string {
  const clearEntry = alarm.timeline.slice().reverse().find((entry) => entry.operation === 'CLEAR');
  return clearEntry?.reason || '未提供恢复原因';
}
