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
    // Leaving the active Site scope, for another Site or a platform page, goes through the runtime: it
    // purges the scope before navigating.
    shouldBlockFn: async ({ next }) => {
      const activeSiteId = runtime.current().protectedScope?.siteId;
      const nextSiteId = siteIdFromLocation(defaultStringifySearch(next.search));
      if (!activeSiteId || nextSiteId === activeSiteId) return false;

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
