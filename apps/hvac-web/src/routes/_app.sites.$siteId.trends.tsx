import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { TrendAnalysis } from '@/features/trends/TrendAnalysis';

const trendSearchSchema = z.object({
  timeStart: z.string().datetime({ offset: true }).optional(),
  timeEnd: z.string().datetime({ offset: true }).optional(),
  series: z.string().max(2048).optional(),
  eventTypes: z.literal('alarm').optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/trends')({
  validateSearch: zodValidator(trendSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read', 'asset.list', 'device.list', 'telemetry.history.read']);
  },
  staticData: {
    title: '趋势分析',
    scope: 'site',
    requiredCapabilities: ['site.read', 'asset.list', 'device.list', 'telemetry.history.read'],
  },
  component: TrendAnalysisRoute,
});

function TrendAnalysisRoute() {
  const { site, principal } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <section data-route-state="READY" data-site-id={site.id} data-site-route="trends" aria-label="趋势分析">
      <Suspense fallback={<RouteLoading label="正在加载趋势分析" />}>
        <TrendAnalysis
          site={site}
          principal={principal}
          searchState={searchState}
          onSearchChange={(patch: any) => {
            void navigate({
              search: (previous: any) => ({ ...previous, ...patch }),
              replace: true,
            });
          }}
        />
      </Suspense>
    </section>
  );
}
