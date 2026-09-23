import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { OptimizationPlansConsole } from '@/features/optimization/OptimizationPlansConsole';

export const Route = createFileRoute('/_app/sites/$siteId/optimize')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '优化方案',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: OptimizeRoute,
});

function OptimizeRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载优化方案" />}>
      <OptimizationPlansConsole site={site} principal={principal} />
    </Suspense>
  );
}
