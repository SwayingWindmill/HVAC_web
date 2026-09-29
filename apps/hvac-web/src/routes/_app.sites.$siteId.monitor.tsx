import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { HvacMonitorPage } from '@/features/monitor/HvacMonitorPage';

const monitorSearchSchema = z.object({
  page: fallback(z.enum(['overview', 'plant', 'terminal', 'analysis', 'modes']), 'overview').optional(),
  view: fallback(z.enum(['topology', 'anomaly', 'energy']), 'topology').optional(),
  flow: fallback(z.enum(['cooling', 'power', 'hydraulic']), 'cooling').optional(),
  device: z.string().optional(),
  analysisDevice: z.string().optional(),
  alarm: z.string().optional(),
  building: z.string().optional(),
  floor: z.string().optional(),
  zone: z.string().optional(),
  opportunity: z.string().optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/monitor')({
  validateSearch: zodValidator(monitorSearchSchema),
  staticData: {
    title: '运行监控',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: MonitorRoute,
});

function MonitorRoute() {
  const { site, principal } = Route.useRouteContext();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <Suspense fallback={<RouteLoading label="正在加载运行监控" />}>
      <HvacMonitorPage
        site={site}
        principal={principal}
        search={search}
        onSearchChange={(patch) => {
          void navigate({
            search: (previous) => ({ ...previous, ...patch }),
            replace: true,
          });
        }}
      />
    </Suspense>
  );
}
