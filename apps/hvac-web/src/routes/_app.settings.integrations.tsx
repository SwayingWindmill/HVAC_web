import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { IntegrationsWorkspace } from '@/features/integrations/IntegrationsWorkspace';

export const Route = createFileRoute('/_app/settings/integrations')({
  staticData: {
    title: '集成管理',
    scope: 'platform',
    navigation: {
      id: 'settings-integrations',
      label: '集成管理',
      group: 'system',
      order: 85,
    },
  },
  component: IntegrationsRoute,
});

function IntegrationsRoute() {
  return (
    <Suspense fallback={<RouteLoading label="正在加载集成管理" />}>
      <IntegrationsWorkspace />
    </Suspense>
  );
}
