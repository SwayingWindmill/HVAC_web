import { useMutation, useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import {
  acknowledgeAlarm,
  alarmKeys,
  assignAlarm,
  suppressAlarm,
  unassignAlarm,
  unsuppressAlarm,
  type Alarm,
} from './alarm-api';

const siteRoute = getRouteApi('/_app/_site');

/** Alarm actions for the signed-in operator; every alarm view on the Site refreshes after any mutation. */
export function useAlarmActions() {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const csrfToken = principal.session.csrfToken;
  const applyUpdate = (alarm: Alarm) => {
    queryClient.setQueryData(alarmKeys.detail(site.id, alarm.alarmId), alarm);
    void queryClient.invalidateQueries({ queryKey: alarmKeys.all(site.id) });
  };
  const acknowledge = useMutation({
    mutationFn: ({ alarm, comment }: { alarm: Alarm; comment: string }) => acknowledgeAlarm(alarm.alarmId, comment, csrfToken),
    onSuccess: applyUpdate,
  });
  const claim = useMutation({
    mutationFn: (alarm: Alarm) => assignAlarm(alarm, principal.principalId, csrfToken),
    onSuccess: applyUpdate,
  });
  const unassign = useMutation({
    mutationFn: ({ alarm, reason }: { alarm: Alarm; reason: string }) => unassignAlarm(alarm, reason, csrfToken),
    onSuccess: applyUpdate,
  });
  const suppress = useMutation({
    mutationFn: ({ alarm, suppressedUntil, reason }: { alarm: Alarm; suppressedUntil: string; reason: string }) =>
      suppressAlarm(alarm, suppressedUntil, reason, csrfToken),
    onSuccess: applyUpdate,
  });
  const unsuppress = useMutation({
    mutationFn: ({ alarm, reason }: { alarm: Alarm; reason: string }) => unsuppressAlarm(alarm, reason, csrfToken),
    onSuccess: applyUpdate,
  });
  const capabilities = principal.authorization.capabilities;
  return {
    acknowledge,
    claim,
    unassign,
    suppress,
    unsuppress,
    canClaim: capabilities.includes('alarm.assign'),
    canCreateWorkOrder: capabilities.includes('work-order.create'),
    myId: principal.principalId,
  };
}
