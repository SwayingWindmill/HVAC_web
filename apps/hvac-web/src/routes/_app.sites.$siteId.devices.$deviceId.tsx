import { createFileRoute, getRouteApi } from '@tanstack/react-router';
import { AssetDeviceDetail } from '@/features/assets-workspace/AssetDeviceDetail';

const devicesRoute = getRouteApi('/_app/sites/$siteId/devices');

export const Route = createFileRoute('/_app/sites/$siteId/devices/$deviceId')({
  component: DeviceDetailRoute,
});

function DeviceDetailRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const { deviceId } = Route.useParams();
  const search = devicesRoute.useSearch();
  const navigate = devicesRoute.useNavigate();

  return (
    <section className="real-route-surface" data-route-state="READY" data-site-id={site.id} data-site-route="device-detail">
      <AssetDeviceDetail
        site={site}
        principal={principal}
        runtime={runtime}
        deviceId={deviceId}
        onBack={() => {
          void navigate({
            to: '/sites/$siteId/devices',
            params: { siteId: site.id },
            search,
          });
        }}
      />
    </section>
  );
}
