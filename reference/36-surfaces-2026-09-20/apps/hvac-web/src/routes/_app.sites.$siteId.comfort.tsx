import { createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { requireCapabilities } from '@/app/route-access';
import { ComfortDashboard } from '@/features/comfort/ComfortDashboard';

const comfortSearchSchema = z.object({
  view: fallback(z.enum(['attention', 'thermal', 'air-quality']), 'attention').optional(),
  q: z.string().optional(),
  area: z.string().optional(),
  data: fallback(z.enum(['all', 'available', 'issue']), 'all').optional(),
  inspect: z.string().optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/comfort')({
  validateSearch: zodValidator(comfortSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read', 'asset.list', 'device.list']);
  },
  staticData: {
    title: '舒适与室内环境',
    scope: 'site',
    requiredCapabilities: ['site.read', 'asset.list', 'device.list'],
  },
  component: ComfortRoute,
});

function ComfortRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = Route.useSearch();
  const navigate = Route.useNavigate();

  return (
    <section data-route-state="READY" data-site-id={site.id} data-site-route="comfort" aria-label="舒适与室内环境">
      <ComfortDashboard
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
    </section>
  );
}
