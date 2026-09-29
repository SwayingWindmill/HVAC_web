import { useQuery } from '@tanstack/react-query';
import { listNotifications } from '@/api/notifications';

export function useShellNotifications(enabled: boolean, principalSubject: string) {
  const query = useQuery({
    queryKey: ['shell', 'notifications', principalSubject],
    queryFn: ({ signal }) => listNotifications(signal),
    enabled: enabled && !__HVAC_WEB_FRONTEND_REVIEW__,
    staleTime: 30_000,
    refetchInterval: 60_000,
  });

  return {
    count: query.data?.filter((item) => item.status === 'UNREAD').length ?? 0,
  };
}
