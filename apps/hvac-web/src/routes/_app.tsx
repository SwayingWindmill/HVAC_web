import { Outlet, createFileRoute, useBlocker } from '@tanstack/react-router';
import { ShellChrome } from '@/app/ShellChrome';
import { requireCapabilities } from '@/app/route-access';
import { siteIdFromPathname } from '@/app/router-paths';
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
      const currentSiteId = siteIdFromPathname(current.pathname);
      const nextSiteId = siteIdFromPathname(next.pathname);
      if (!currentSiteId || !nextSiteId || currentSiteId === nextSiteId) return false;

      await runtime.requestSiteNavigation(next.pathname);
      return true;
    },
  });

  return (
    <ShellChrome>
      <Outlet />
    </ShellChrome>
  );
}
