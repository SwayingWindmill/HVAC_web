import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { StrategiesConsole } from '@/features/strategies/StrategiesConsole';

export const Route = createFileRoute('/_app/sites/$siteId/strategies')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '策略',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: StrategiesRoute,
});

function StrategiesRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载策略" />}>
      <StrategiesConsole siteId={site.id} />
    </Suspense>
  );
}
