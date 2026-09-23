import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { SiteBenchmarkingWorkspace } from '@/features/benchmarking/SiteBenchmarkingWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/benchmarking')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '站点对标',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-benchmarking',
      label: '站点对标',
      group: 'management',
      order: 20,
      siteLeaf: 'benchmarking',
    },
  },
  component: BenchmarkingRoute,
});

function BenchmarkingRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载站点对标" />}>
      <SiteBenchmarkingWorkspace siteId={site.id} />
    </Suspense>
  );
}
