import { createFileRoute, getRouteApi } from '@tanstack/react-router';
import { AssetsWorkspace } from '@/features/assets-workspace/AssetsWorkspace';

const devicesRoute = getRouteApi('/_app/sites/$siteId/devices');

export const Route = createFileRoute('/_app/sites/$siteId/devices/')({
  component: DevicesIndexRoute,
});

function DevicesIndexRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const searchState = devicesRoute.useSearch();
  const navigate = devicesRoute.useNavigate();

  return (
    <section className="real-route-surface" data-route-state="READY" data-site-id={site.id} data-site-route="devices">
      <AssetsWorkspace
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
        onOpenDetail={(deviceId) => {
          void navigate({
            to: '/sites/$siteId/devices/$deviceId',
            params: { siteId: site.id, deviceId },
            search: ({ inspect: _inspect, ...previous }) => previous,
          });
        }}
      />
    </section>
  );
}
