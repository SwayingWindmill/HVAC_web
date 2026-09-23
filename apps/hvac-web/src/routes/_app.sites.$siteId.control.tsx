import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { ControlCenter } from '@/features/control/ControlCenter';

const controlSearchSchema = z.object({
  target: z.string().max(256).optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/control')({
  validateSearch: zodValidator(controlSearchSchema),
  staticData: {
    title: '控制',
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
    <section className="real-route-surface" data-route-state="READY" data-site-id={site.id} data-site-route="control" aria-label="控制">
      <Suspense fallback={<RouteLoading label="正在加载控制" />}>
        <ControlCenter
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
