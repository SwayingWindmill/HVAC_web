import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { Overview } from '@/features/overview/Overview';

export const Route = createFileRoute('/_app/sites/$siteId/overview')({
  staticData: {
    title: '站点总览',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: { id: 'site-overview', label: '站点总览', group: 'operate', order: 10, siteLeaf: 'overview' },
  },
  component: OverviewRoute,
});

function OverviewRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <section data-route-state="READY" data-site-id={site.id} data-site-route="overview">
      <Suspense fallback={<RouteLoading label="正在加载站点总览" />}>
        <Overview site={site} principal={principal} />
      </Suspense>
    </section>
  );
}
