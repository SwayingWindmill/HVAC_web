import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { EnergyReviewWorkspace } from '@/features/energy-review/EnergyReviewWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/energy-review')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '能源评审',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-energy-review',
      label: '能源评审',
      group: 'energy',
      order: 40,
      siteLeaf: 'energy-review',
    },
  },
  component: EnergyReviewRoute,
});

function EnergyReviewRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载能源评审" />}>
      <EnergyReviewWorkspace siteId={site.id} />
    </Suspense>
  );
}
