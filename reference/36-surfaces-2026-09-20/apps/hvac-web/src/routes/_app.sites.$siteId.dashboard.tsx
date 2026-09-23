import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { Overview } from '@/features/overview/Overview';
import { RouteLoading } from '@/app/RouteLoading';

export const Route = createFileRoute('/_app/sites/$siteId/dashboard')({
  staticData: {
    title: '站点总览',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: DashboardRoute,
});

function DashboardRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <section className="real-route-surface real-route-surface--dashboard" data-route-state="READY" data-site-id={site.id} data-site-route="dashboard">
      <Suspense fallback={<RouteLoading label="正在加载站点总览" />}>
        <Overview site={site} principal={principal} />
      </Suspense>
    </section>
  );
}
