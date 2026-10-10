import { useMutation, useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { acknowledgeAlarm, alarmKeys, assignAlarm, type Alarm } from './alarm-api';

const siteRoute = getRouteApi('/_app/_site');

/** Acknowledge and claim, as the signed-in operator; every alarm view on the Site refreshes after either. */
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
  const capabilities = principal.authorization.capabilities;
  return {
    acknowledge,
    claim,
    canClaim: capabilities.includes('alarm.assign'),
    canCreateWorkOrder: capabilities.includes('work-order.create'),
    myId: principal.principalId,
  };
}
