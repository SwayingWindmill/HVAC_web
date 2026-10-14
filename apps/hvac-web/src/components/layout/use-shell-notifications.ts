import { queryOptions, useQuery } from '@tanstack/react-query';
import { listNotifications } from '@/api/notifications';

/** The principal's inbox, shared by the header bell and the notification center. */
export function shellNotificationsQuery(principalSubject: string) {
  return queryOptions({
    queryKey: ['shell', 'notifications', principalSubject],
    queryFn: ({ signal }) => listNotifications(signal),
    enabled: !__HVAC_WEB_FRONTEND_REVIEW__,
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}

export function useShellNotifications(enabled: boolean, principalSubject: string) {
  const query = useQuery({ ...shellNotificationsQuery(principalSubject), enabled: enabled && !__HVAC_WEB_FRONTEND_REVIEW__ });

  return {
    count: query.data?.filter((item) => item.status === 'UNREAD').length ?? 0,
  };
}
