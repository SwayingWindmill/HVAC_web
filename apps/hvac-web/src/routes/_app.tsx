import { Outlet, createFileRoute, defaultStringifySearch, useBlocker } from '@tanstack/react-router';
import { ShellChrome } from '@/app/ShellChrome';
import { requireCapabilities } from '@/app/route-access';
import { siteIdFromLocation } from '@/app/router-paths';
import { RouteErrorSurface } from '@/app/RouteSurfaces';

export const Route = createFileRoute('/_app')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, []);
  },
  component: AppLayout,
  errorComponent: AppRouteError,
});

function AppRouteError({ error }: { error: unknown }) {
  return (
    <ShellChrome>
      <RouteErrorSurface error={error} />
    </ShellChrome>
  );
}

function AppLayout() {
  const { runtime } = Route.useRouteContext();

  useBlocker({
    enableBeforeUnload: false,
    shouldBlockFn: async ({ current, next }) => {
      const currentSiteId = siteIdFromLocation(defaultStringifySearch(current.search));
      const nextSiteId = siteIdFromLocation(defaultStringifySearch(next.search));
      if (!currentSiteId || !nextSiteId || currentSiteId === nextSiteId) return false;

      await runtime.requestSiteNavigation(`${next.pathname}${defaultStringifySearch(next.search)}`);
      return true;
    },
  });

  return (
    <ShellChrome>
      <Outlet />
    </ShellChrome>
  );
}
