import { useQuery } from '@tanstack/react-query';
import { activeAlarmsQuery } from './alarm-api';

/** How many of the Site's alarms are happening now with nobody acknowledging them; 0 without a Site. */
export function useWaitingAlarmCount(siteId: string | undefined): number {
  const active = useQuery({ ...activeAlarmsQuery(siteId ?? ''), enabled: siteId !== undefined });
  return siteId === undefined ? 0 : active.data?.items.filter((alarm) => !alarm.acknowledgement).length ?? 0;
}
