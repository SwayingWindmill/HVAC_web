import { Suspense } from 'react';
import { createFileRoute, notFound } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { EnergyAnalytics } from '@/features/energy/EnergyAnalytics';
import type { EnergyWorkspacePeriod } from '@/features/energy/workspace';

const PERIODS = new Set<EnergyWorkspacePeriod>(['day', 'week', 'month', 'year']);

export const Route = createFileRoute('/_app/sites/$siteId/energy/$period')({
  beforeLoad: ({ params }) => {
    if (!PERIODS.has(params.period as EnergyWorkspacePeriod)) throw notFound();
    return { energyPeriod: params.period as EnergyWorkspacePeriod };
  },
  component: EnergyPeriodRoute,
});

function EnergyPeriodRoute() {
  const { site, principal, energyPeriod } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <section data-testid="real-site-route-energy" data-route-state="READY" data-site-id={site.id} data-site-route="energy">
      <Suspense fallback={<RouteLoading label="正在加载能源分析" />}>
        <EnergyAnalytics
          site={site}
          principal={principal}
          initialPeriod={energyPeriod}
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
