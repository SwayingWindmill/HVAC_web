import type { Alarm, AlarmSeverity } from '@/api/alarms';

export interface DashboardPriorityAlarmIdentity {
  title: string;
  severity: AlarmSeverity;
}

export function selectDashboardAlarmCandidate(
  priorityAlarm: Readonly<DashboardPriorityAlarmIdentity>,
  alarms: readonly Alarm[],
): Alarm | null {
  const titleMatches = alarms.filter((candidate) => candidate.title === priorityAlarm.title);
  if (titleMatches.length === 1) return titleMatches[0];
  if (titleMatches.length === 0) return null;

  const severityMatches = titleMatches.filter((candidate) => candidate.currentSeverity === priorityAlarm.severity);
  return severityMatches.length === 1 ? severityMatches[0] : null;
}
