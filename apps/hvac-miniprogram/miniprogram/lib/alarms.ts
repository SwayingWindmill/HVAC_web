// What the 通知 tab shows: the Site's alarms themselves, what is happening and what cleared lately, and
// one alarm with what an operator needs to acknowledge it. Severity names,
// order and durations are the web's (apps/hvac-web/src/features/alarms/alarm-presentation.tsx).

import { siteDateTime, siteWhen } from './today';

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

/** Rows of alarms, most severe first unless `bySeverity` is off (cleared ones keep the order given). */
export function presentAlarmRows(alarms: readonly Alarm[], filter: { readonly unacknowledgedOnly: boolean }, todayStart: string | null, order = { bySeverity: true }) {
  const shown = alarms.filter((alarm) => !filter.unacknowledgedOnly || alarm.acknowledgement == null);
  return (order.bySeverity ? shown.sort(bySeverity) : shown)
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

// The latest cleared alarms the page lists; the web's 告警 page has them all.
const RECENTLY_CLEARED = 20;

const EMPTY = {
  active: { all: '现在没有正在发生的告警。', unacknowledged: '正在发生的告警都有人确认了。' },
  cleared: { all: '最近没有恢复的告警。', unacknowledged: '最近恢复的告警都有人确认过。' },
} as const;

/** Happening with no one on them yet: what the 通知 tab's badge counts. */
export const waitingCount = (active: readonly Alarm[]) => active.filter((alarm) => alarm.acknowledgement == null).length;

/** The 通知 page: what is happening, most severe first, then what cleared lately, newest first. */
export function presentFeed(active: readonly Alarm[], cleared: readonly Alarm[], todayStart: string | null, filter: { readonly unacknowledgedOnly: boolean }) {
  const unacknowledged = waitingCount(active);
  const which = filter.unacknowledgedOnly ? 'unacknowledged' : 'all';
  const activeRows = presentAlarmRows(active, filter, todayStart);
  const clearedRows = presentAlarmRows(
    [...cleared].sort((left, right) => Date.parse(right.clearedAt ?? right.lastOccurredAt) - Date.parse(left.clearedAt ?? left.lastOccurredAt)).slice(0, RECENTLY_CLEARED),
    filter,
    todayStart,
    { bySeverity: false },
  );
  return {
    sections: [
      { title: '正在发生', caption: active.length === 0 ? '' : `${active.length} 条 · ${unacknowledged === 0 ? '都有人确认了' : `${unacknowledged} 条没人确认`}`, rows: activeRows, empty: activeRows.length > 0 ? '' : EMPTY.active[which] },
      { title: '最近恢复', caption: '', rows: clearedRows, empty: clearedRows.length > 0 ? '' : EMPTY.cleared[which] },
    ],
    needsAttention: unacknowledged,
  };
}
