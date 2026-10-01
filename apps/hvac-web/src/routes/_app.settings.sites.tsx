import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { SiteSystemSettingsWorkspace } from '@/features/system-settings/SiteSystemSettingsWorkspace';

export const Route = createFileRoute('/_app/settings/sites')({
  staticData: {
    title: '站点与系统配置',
    scope: 'platform',
    navigation: {
      id: 'settings-sites',
      label: '站点与系统配置',
      group: 'system',
      order: 90,
    },
  },
  component: SiteSettingsRoute,
});

function SiteSettingsRoute() {
  return (
    <Suspense fallback={<RouteLoading label="正在加载站点配置" />}>
      <SiteSystemSettingsWorkspace />
    </Suspense>
  );
}
