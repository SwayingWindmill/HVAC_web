import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { CarbonEmissionsDashboard } from '@/features/carbon/CarbonEmissionsDashboard';

export const Route = createFileRoute('/_app/sites/$siteId/carbon')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '碳排放',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: CarbonRoute,
});

function CarbonRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载碳排放与绿电分析" />}>
      <CarbonEmissionsDashboard siteId={site.id} />
    </Suspense>
  );
}
