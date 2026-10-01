import { Suspense } from 'react';
import { createFileRoute, getRouteApi } from '@tanstack/react-router';

import { RouteLoading } from '@/app/RouteLoading';
import { IssuesWorkspace } from '@/features/issues/IssuesWorkspace';

const issuesRoute = getRouteApi('/_app/sites/$siteId/issues');

export const Route = createFileRoute('/_app/sites/$siteId/issues/')({
  component: IssuesIndexRoute,
});

function IssuesIndexRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = issuesRoute.useSearch();
  const navigate = issuesRoute.useNavigate();

  return (
    <section data-route-state="READY" data-site-id={site.id} data-site-route="issues" aria-label="告警与诊断">
      <Suspense fallback={<RouteLoading label="正在加载告警与诊断" />}>
        <IssuesWorkspace
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
