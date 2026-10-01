import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { OpportunitiesWorkspace } from '@/features/opportunities/OpportunitiesWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/opportunities')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '节能机会',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: OpportunitiesRoute,
});

function OpportunitiesRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载节能机会" />}>
      <OpportunitiesWorkspace site={site} principal={principal} />
    </Suspense>
  );
}
