import { useQuery } from '@tanstack/react-query';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { activeAlarmsQuery } from './alarm-api';

/** How many of the Site's alarms are happening now with nobody acknowledging them; 0 without a Site or permission. */
export function useWaitingAlarmCount(siteId: string | undefined): number {
  const snapshot = useShellSnapshot();
  const canListAlarms = snapshot.principal?.authorization.capabilities.includes('alarm.list') ?? false;
  const enabled = siteId !== undefined && canListAlarms;
  const active = useQuery({ ...activeAlarmsQuery(siteId ?? ''), enabled });
  return enabled ? active.data?.items.filter((alarm) => !alarm.acknowledgement).length ?? 0 : 0;
}
