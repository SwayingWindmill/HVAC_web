// What the 通知 tab shows: this Site's alarm news from the notification inbox, the alarms themselves
// (active or cleared), and one alarm with what an operator needs to acknowledge it. Severity names,
// order and durations are the web's (apps/hvac-web/src/features/alarms/alarm-presentation.tsx).

import { siteDay, siteWhen } from './today';

export type AlarmSeverity = 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';

/** The Alarm owner's alarm, as the Gateway returns it (the fields the mini program reads). */
export interface Alarm {
  readonly alarmId: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly deviceId?: string;
  readonly incidentCorrelationId: string;
  readonly title: string;
  readonly summary: string;
  readonly condition: 'ACTIVE' | 'CLEARED';
  readonly currentSeverity: AlarmSeverity;
  /** null (or absent) until someone acknowledges it. */
  readonly acknowledgement?: { readonly acknowledgedAt: string; readonly acknowledgedBy: string; readonly comment?: string } | null;
  readonly occurrenceCount: number;
  readonly firstOccurredAt: string;
  readonly lastOccurredAt: string;
  readonly clearedAt?: string | null;
  readonly version: number;
}

/** One item of the person's notification inbox (the web's api/notifications.ts). */
export interface InboxItem {
  readonly inboxItemId: string;
  readonly intentId: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly principalId: string;
  readonly alarmId: string;
  readonly incidentCorrelationId: string;
  readonly sourceAction: 'CREATED' | 'SEVERITY_CHANGED' | 'ACKNOWLEDGED' | 'CLEARED';
  readonly severity: AlarmSeverity;
  readonly subject: string;
  readonly body: string;
  readonly status: 'UNREAD' | 'READ' | 'ACKED';
  readonly createdAt: string;
  readonly readAt?: string;
}

const SEVERITY_ORDER: readonly AlarmSeverity[] = ['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO'];

export const SEVERITY_LABELS: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '次要',
  WARNING: '预警',
  INFO: '提示',
};

/** The shape each severity is drawn with (assets/severity/<level>.svg), so it reads without colour too. */
const LEVEL: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: 'critical',
  MAJOR: 'major',
  MINOR: 'minor',
  WARNING: 'warning',
  INFO: 'info',
};

/** A length of time in the units an operator reads: minutes, then hours and minutes, then days and hours. */
function formatSpan(milliseconds: number): string {
  const minutes = Math.max(0, Math.round(milliseconds / 60_000));
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} 小时 ${minutes % 60} 分`;
  return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`;
}

const when = (at: string, todayStart: string | null) => (todayStart === null ? '' : siteWhen(at, todayStart));

/** Most severe first, then the most recent (the web's sortAlarmsForOperators). */
const bySeverity = (left: Alarm, right: Alarm) =>
  SEVERITY_ORDER.indexOf(left.currentSeverity) - SEVERITY_ORDER.indexOf(right.currentSeverity)
  || Date.parse(right.lastOccurredAt) - Date.parse(left.lastOccurredAt);

export function presentAlarmRows(alarms: readonly Alarm[], filter: { readonly unacknowledgedOnly: boolean }, todayStart: string | null) {
  return alarms
    .filter((alarm) => !filter.unacknowledgedOnly || alarm.acknowledgement == null)
    .sort(bySeverity)
    .map((alarm) => {
      const moment = alarm.condition === 'ACTIVE' ? `${when(alarm.firstOccurredAt, todayStart)} 开始` : `${when(alarm.clearedAt ?? alarm.lastOccurredAt, todayStart)} 恢复`;
      return {
        alarmId: alarm.alarmId,
        title: alarm.title,
        level: LEVEL[alarm.currentSeverity],
        severity: SEVERITY_LABELS[alarm.currentSeverity],
        detail: `${moment.trim()} · ${alarm.acknowledgement ? '已确认' : '未确认'}`,
      };
    });
}

export function presentAlarm(alarm: Alarm, context: { readonly deviceName: string | null; readonly now: Date; readonly todayStart: string | null; readonly me: string }) {
  const { deviceName, now, todayStart, me } = context;
  const activeNow = alarm.condition === 'ACTIVE';
  const end = activeNow ? now.getTime() : Date.parse(alarm.clearedAt ?? alarm.lastOccurredAt);
  const facts = [
    ...(deviceName === null ? [] : [{ label: '设备', value: deviceName }]),
    ...(todayStart === null ? [] : [{ label: '开始', value: when(alarm.firstOccurredAt, todayStart) }]),
    ...(todayStart === null || activeNow ? [] : [{ label: '恢复', value: when(alarm.clearedAt ?? alarm.lastOccurredAt, todayStart) }]),
    { label: activeNow ? '已经持续' : '持续了', value: formatSpan(end - Date.parse(alarm.firstOccurredAt)) },
    { label: '报过', value: `${alarm.occurrenceCount} 次` },
  ];
  const acknowledgement = alarm.acknowledgement ?? null;
  return {
    level: LEVEL[alarm.currentSeverity],
    severity: SEVERITY_LABELS[alarm.currentSeverity],
    state: `${activeNow ? '正在发生' : '已恢复'} · ${acknowledgement ? '已确认' : '未确认'}`,
    title: alarm.title,
    summary: alarm.summary,
    facts,
    // People are named relative to the reader until there is a user directory, as on the web.
    acknowledgement: acknowledgement === null ? null : {
      who: acknowledgement.acknowledgedBy === me ? '我' : '其他人员',
      when: when(acknowledgement.acknowledgedAt, todayStart),
      comment: acknowledgement.comment ?? '',
    },
    canAcknowledge: acknowledgement === null,
  };
}

const ACTIONS: Readonly<Record<InboxItem['sourceAction'], string>> = {
  CREATED: '新告警',
  SEVERITY_CHANGED: '级别变了',
  ACKNOWLEDGED: '有人确认了',
  CLEARED: '已恢复',
};

export type InboxView = 'unread' | 'all';

/** How many of this Site's notifications are unread (the 通知 tab's badge). */
export const unreadOn = (items: readonly InboxItem[], siteId: string) =>
  items.filter((item) => item.siteId === siteId && item.status === 'UNREAD').length;

/** This Site's notifications by Site day, newest first; times need the Site's midnight. */
export function presentInbox(items: readonly InboxItem[], siteId: string, todayStart: string | null, view: InboxView) {
  const here = items.filter((item) => item.siteId === siteId);
  const shown = here
    .filter((item) => view === 'all' || item.status === 'UNREAD')
    .sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt));
  const groups: { day: string; items: ReturnType<typeof entry>[] }[] = [];
  function entry(item: InboxItem) {
    const recovered = item.sourceAction === 'CLEARED';
    return {
      id: item.inboxItemId,
      alarmId: item.alarmId,
      unread: item.status === 'UNREAD',
      recovered,
      level: LEVEL[item.severity],
      tag: recovered ? ACTIONS.CLEARED : `${SEVERITY_LABELS[item.severity]} · ${ACTIONS[item.sourceAction]}`,
      time: todayStart === null ? '' : siteDay(item.createdAt, todayStart).time,
      subject: item.subject,
      body: item.body,
    };
  }
  for (const item of shown) {
    const day = todayStart === null ? '' : siteDay(item.createdAt, todayStart).day;
    const last = groups[groups.length - 1];
    if (last !== undefined && last.day === day) last.items.push(entry(item));
    else groups.push({ day, items: [entry(item)] });
  }
  return {
    unread: unreadOn(items, siteId),
    groups,
    empty: shown.length > 0 ? '' : view === 'unread' ? '没有未读的通知。' : '这个站点还没有通知。',
  };
}
