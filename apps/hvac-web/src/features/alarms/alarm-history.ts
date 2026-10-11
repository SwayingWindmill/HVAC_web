import type { Alarm, AlarmSeverity } from './alarm-api';

// What the Site's alarm history says at a glance: how many were raised each day and where.

export type DailyAlarmCounts = { readonly day: string; readonly label: string } & Readonly<Record<AlarmSeverity, number>>;

const siteDay = (instant: number, timeZone: string) =>
  new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(instant);

/** Alarms raised per Site-local day for the last `days` days (today included), by the severity they were raised at. */
export function alarmDailyCounts(alarms: readonly Alarm[], timeZone: string, days: number, now: number): DailyAlarmCounts[] {
  const rows = new Map<string, Record<AlarmSeverity, number>>();
  for (let offset = days - 1; offset >= 0; offset -= 1) {
    rows.set(siteDay(now - offset * 86_400_000, timeZone), { CRITICAL: 0, MAJOR: 0, MINOR: 0, WARNING: 0, INFO: 0 });
  }
  for (const alarm of alarms) {
    const counts = rows.get(siteDay(Date.parse(alarm.firstOccurredAt), timeZone));
    const raisedAt = alarm.timeline.find((entry) => entry.operation === 'PUBLISH')?.currentSeverity ?? alarm.peakSeverity;
    if (counts) counts[raisedAt] += 1;
  }
  return [...rows.entries()].map(([day, counts]) => ({ day, label: `${Number(day.slice(5, 7))}/${Number(day.slice(8, 10))}`, ...counts }));
}

/** Devices by how many alarms they raised, most first; Site-level alarms count under no device. */
export function alarmsByDevice(alarms: readonly Alarm[]): { readonly deviceId: string; readonly count: number }[] {
  const counts = new Map<string, number>();
  for (const alarm of alarms) if (alarm.deviceId) counts.set(alarm.deviceId, (counts.get(alarm.deviceId) ?? 0) + 1);
  return [...counts.entries()].map(([deviceId, count]) => ({ deviceId, count })).sort((left, right) => right.count - left.count);
}

/** Mean time from raise to clear over the alarms that have cleared, in milliseconds; null when none have. */
export function meanTimeToClear(alarms: readonly Alarm[]): number | null {
  const durations = alarms
    .filter((alarm) => alarm.condition === 'CLEARED' && alarm.clearedAt)
    .map((alarm) => Date.parse(alarm.clearedAt!) - Date.parse(alarm.firstOccurredAt));
  return durations.length === 0 ? null : durations.reduce((sum, value) => sum + value, 0) / durations.length;
}

/** Alarms raised on the last `days` Site-local days, today included: the window the trend, ranking and recovery time share. */
export function alarmsInWindow(alarms: readonly Alarm[], timeZone: string, days: number, now: number): Alarm[] {
  const first = siteDay(now - (days - 1) * 86_400_000, timeZone);
  return alarms.filter((alarm) => siteDay(Date.parse(alarm.firstOccurredAt), timeZone) >= first);
}
