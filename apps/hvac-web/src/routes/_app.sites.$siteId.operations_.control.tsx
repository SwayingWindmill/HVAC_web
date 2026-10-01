import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { ControlCenter } from '@/features/control/ControlCenter';

const controlSearchSchema = z.object({
  target: z.string().max(256).optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/operations_/control')({
  validateSearch: zodValidator(controlSearchSchema),
  staticData: {
    title: '即时控制',
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
    <section className="real-route-surface" data-route-state="READY" data-site-id={site.id} data-site-route="operations-control" aria-label="即时控制">
      <Suspense fallback={<RouteLoading label="正在加载即时控制" />}>
        <ControlCenter
          site={site}
          principal={principal}
          runtime={runtime}
          searchState={searchState}
          onSearchChange={(patch) => {
            void navigate({
              search: (previous) => ({ ...previous, ...patch }),
              replace: true,
            });
          }}
        />
      </Suspense>
    </section>
  );
}
