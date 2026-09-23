import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { ControlOptimizationConsole } from '@/features/control/ControlOptimizationConsole';

const controlSearchSchema = z.object({
  target: z.string().max(256).optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/control')({
  validateSearch: zodValidator(controlSearchSchema),
  staticData: {
    title: '控制中心',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: ControlRoute,
});

function ControlRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();

  return (
    <section className="real-route-surface" data-route-state="READY" data-site-id={site.id} data-site-route="control" aria-label="控制中心">
      <Suspense fallback={<RouteLoading label="正在加载控制中心" />}>
        <ControlOptimizationConsole
          site={site}
          principal={principal}
          runtime={runtime}
          searchState={searchState}
          onSearchChange={(patch: any) => {
            void navigate({
              search: (previous: any) => ({ ...previous, ...(patch as object) }),
              replace: true,
            });
          }}
        />
      </Suspense>
    </section>
  );
}
