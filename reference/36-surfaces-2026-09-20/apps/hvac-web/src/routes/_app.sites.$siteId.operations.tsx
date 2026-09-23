import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { OperationsConsole } from '@/features/system-operations/OperationsConsole';

const operationsSearchSchema = z.object({
  group: z.string().optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/operations')({
  validateSearch: zodValidator(operationsSearchSchema),
  staticData: {
    title: '系统运行',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: { id: 'site-operations', label: '系统运行', group: 'operate', order: 20, siteLeaf: 'operations' },
  },
  component: OperationsRoute,
});

function OperationsRoute() {
  const { site, principal } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();

  return (
    <section data-route-state="READY" data-site-id={site.id} data-site-route="operations" aria-label="系统运行">
      <Suspense fallback={<RouteLoading label="正在加载系统运行" />}>
        <OperationsConsole
          site={site}
          principal={principal}
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
