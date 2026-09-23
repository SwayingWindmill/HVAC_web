import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { DistributedEnergyWorkspace } from '@/features/der/DistributedEnergyWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/der')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '分布式能源与柔性',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: DERRoute,
});

function DERRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载分布式能源与柔性" />}>
      <DistributedEnergyWorkspace siteId={site.id} />
    </Suspense>
  );
}
