// What the 通知 tab shows: this Site's alarm news from the notification inbox, the alarms themselves
// (active or cleared), and one alarm with what an operator needs to acknowledge it. Severity names,
// order and durations are the web's (apps/hvac-web/src/features/alarms/alarm-presentation.tsx).

import { siteDateTime, siteDay, siteWhen } from './today';

export type AlarmSeverity = 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';

/** The Alarm owner's alarm, as the Gateway returns it (the fields the mini program reads). */
export interface Alarm {
  readonly alarmId: string;
  readonly siteId: string;
  readonly deviceId?: string;
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
}

/** One item of the person's notification inbox (the web's api/notifications.ts; the fields the mini program reads). */
export interface InboxItem {
  readonly inboxItemId: string;
  readonly siteId: string;
  readonly alarmId: string;
  readonly sourceAction: 'CREATED' | 'SEVERITY_CHANGED' | 'ACKNOWLEDGED' | 'CLEARED';
  readonly severity: AlarmSeverity;
  readonly subject: string;
  readonly body: string;
  readonly status: 'UNREAD' | 'READ' | 'ACKED';
  readonly createdAt: string;
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
const level = (severity: AlarmSeverity) => severity.toLowerCase();

/** A count of alarms read, with + when there were more than were read. */
export const countText = (list: { readonly alarms: readonly unknown[]; readonly complete: boolean }) => `${list.alarms.length}${list.complete ? '' : '+'}`;

/** Happening or cleared, and whether someone is on it: 正在发生 · 未确认. */
const stateOf = (alarm: Alarm) => `${alarm.condition === 'ACTIVE' ? '正在发生' : '已恢复'} · ${alarm.acknowledgement == null ? '未确认' : '已确认'}`;

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
      const acknowledged = alarm.acknowledgement == null ? '未确认' : '已确认';
      // Without the Site's midnight there is no time to give, only the state.
      const detail = todayStart === null ? stateOf(alarm)
        : alarm.condition === 'ACTIVE' ? `${when(alarm.firstOccurredAt, todayStart)} 开始 · ${acknowledged}`
        : `${when(alarm.clearedAt ?? alarm.lastOccurredAt, todayStart)} 恢复 · ${acknowledged}`;
      return { alarmId: alarm.alarmId, title: alarm.title, level: level(alarm.currentSeverity), severity: SEVERITY_LABELS[alarm.currentSeverity], detail };
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
    level: level(alarm.currentSeverity),
    severity: SEVERITY_LABELS[alarm.currentSeverity],
    state: stateOf(alarm),
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
    acknowledgeNote: activeNow ? '确认表示有人在处理，告警不会因此消失；读数恢复后它自己恢复。' : '这条告警已经恢复了，确认只是记下有人看过。',
  };
}

/**
 * What 问助手 asks, as the web's 解释 does: the alarm by its title, when it began in Site time, whether
 * it has cleared, and its device, so the assistant reads the one meant among alarms of the same name.
 */
export function explainAlarmQuestion(alarm: Alarm, deviceName: string | null, todayStart: string | null): string {
  const began = todayStart === null ? '' : `${siteDateTime(alarm.firstOccurredAt, todayStart)} 开始`;
  const cleared = alarm.condition === 'CLEARED' ? (began ? '、已恢复' : '已恢复') : '';
  return `${began}${cleared}的告警「${alarm.title}」${deviceName ? `（${deviceName}）` : ''}是怎么回事？`;
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
      level: level(item.severity),
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
