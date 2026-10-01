import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { OperationsWorkspace } from '@/features/system-operations/OperationsWorkspace';

const operationsSearchSchema = z.object({
  view: fallback(z.enum(['systems', 'comfort']), 'systems').optional(),
  group: z.string().optional(),
  comfortView: fallback(z.enum(['thermal', 'air-quality']), 'thermal').optional(),
  q: z.string().optional(),
  area: z.string().optional(),
  data: fallback(z.enum(['all', 'available', 'issue']), 'all').optional(),
  inspect: z.string().optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/operations')({
  validateSearch: zodValidator(operationsSearchSchema),
  staticData: {
    title: '运行',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: { id: 'site-operations', label: '运行', group: 'operate', order: 20, siteLeaf: 'operations' },
  },
  component: OperationsRoute,
});

function OperationsRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();

  return (
    <section data-route-state="READY" data-site-id={site.id} data-site-route="operations" aria-label="运行">
      <Suspense fallback={<RouteLoading label="正在加载运行工作区" />}>
        <OperationsWorkspace
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
