import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { EfficiencyAnalyticsDashboard } from '@/features/efficiency/EfficiencyAnalyticsDashboard';

export const Route = createFileRoute('/_app/sites/$siteId/efficiency')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '能效',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: EfficiencyRoute,
});

function EfficiencyRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载能效" />}>
      <EfficiencyAnalyticsDashboard site={site} principal={principal} />
    </Suspense>
  );
}
