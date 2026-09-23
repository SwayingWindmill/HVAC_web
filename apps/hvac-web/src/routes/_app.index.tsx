import { Navigate, createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { ForbiddenSurface, NoAuthorizedSiteSurface, SiteChooserSurface, SiteDiscoveryUnavailableSurface } from '@/app/RouteSurfaces';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';

export const Route = createFileRoute('/_app/')({
  staticData: {
    title: '站点入口',
    scope: 'platform',
  },
  component: SiteEntryRoute,
});

function SiteEntryRoute() {
  const snapshot = useShellSnapshot();
  const sites = snapshot.sites;
  if (!sites || sites.state === 'checking') {
    return <RouteLoading label="正在加载授权站点" testId="real-site-discovery-checking" routeState="SITE_DISCOVERY_CHECKING" />;
  }
  if (sites.state === 'forbidden') return <ForbiddenSurface />;
  if (sites.state === 'unavailable') return <SiteDiscoveryUnavailableSurface />;
  const items = sites.items ?? [];
  if (items.length === 0) return <NoAuthorizedSiteSurface />;
  if (items.length === 1) return <Navigate to="/sites/$siteId/overview" params={{ siteId: items[0].id }} replace />;
  return <SiteChooserSurface sites={items} />;
}
