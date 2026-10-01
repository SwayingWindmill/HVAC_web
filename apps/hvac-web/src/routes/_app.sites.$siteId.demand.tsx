import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { DemandAnalyticsDashboard } from '@/features/demand/DemandAnalyticsDashboard';

export const Route = createFileRoute('/_app/sites/$siteId/demand')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '需求与负荷分析',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: DemandRoute,
});

function DemandRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载需求与负荷分析" />}>
      <DemandAnalyticsDashboard site={site} principal={principal} />
    </Suspense>
  );
}
