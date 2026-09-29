import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { EnergyAnalytics } from '@/features/energy/EnergyAnalytics';

export const Route = createFileRoute('/_app/sites/$siteId/energy/')({
  component: EnergyIndexRoute,
});

function EnergyIndexRoute() {
  const { site, principal } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <section data-testid="real-site-route-energy" data-route-state="READY" data-site-id={site.id} data-site-route="energy">
      <Suspense fallback={<RouteLoading label="正在加载能源分析" />}>
        <EnergyAnalytics
          site={site}
          principal={principal}
          initialPeriod="month"
          searchState={searchState}
          onWorkspaceChange={(next: any) => {
            void navigate({
              to: '/sites/$siteId/energy/$period',
              params: { siteId: site.id, period: next.period },
              search: {
                anchor: next.anchor,
                quality: next.qualityPolicy === 'VALID_ONLY' ? undefined : next.qualityPolicy,
              },
            });
          }}
        />
      </Suspense>
    </section>
  );
}
