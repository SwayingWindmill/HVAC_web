import { createFileRoute } from '@tanstack/react-router';
import { NotificationCenter } from '@/features/notifications/NotificationCenter';
import { requirePlatform } from '@/app/route-access';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';

export const Route = createFileRoute('/_app/notifications')({
  beforeLoad: ({ context }) => requirePlatform(context.runtime),
  staticData: {
    title: '通知',
    scope: 'platform',
    requiresPlatform: true,
    navigation: { id: 'notifications', label: '通知', group: 'system', order: 900 },
  },
  component: NotificationsRoute,
});

function NotificationsRoute() {
  return <NotificationCenter snapshot={useShellSnapshot()} />;
}
