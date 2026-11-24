import { createRouter } from '@tanstack/react-router';
import type { HvacRouterContext } from './router-context';
import { DefaultRoutePending } from './RouteLoading';
import { RouteErrorSurface } from './RouteSurfaces';
import { routeTree } from '../routeTree.gen';

export function createHvacRouter(context: HvacRouterContext) {
  // A new page starts at the top (or where it was left, going back); a tab, period or side sheet
  // changed on the same page only changes the search and keeps the reader's place.
  let renderedPath: string | null = null;
  return createRouter({
    routeTree,
    context,
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0,
    defaultErrorComponent: RouteErrorSurface,
    defaultPendingComponent: DefaultRoutePending,
    defaultPendingMs: 250,
    scrollRestoration: ({ location }) => {
      const samePage = location.pathname === renderedPath;
      renderedPath = location.pathname;
      return !samePage;
    },
  });
}

export type HvacRouter = ReturnType<typeof createHvacRouter>;

/** Preload route code chunks for a pathname without running route loaders or guards. */
export function preloadPathChunks(
  router: Pick<HvacRouter, 'getMatchedRoutes' | 'loadRouteChunk'>,
  targetHrefOrPath: string,
): void {
  const pathname = targetHrefOrPath.split(/[?#]/)[0] || '/';
  const [matchedRoutes] = router.getMatchedRoutes(pathname);
  for (const route of matchedRoutes) {
    void Promise.resolve(router.loadRouteChunk(route)).catch(() => undefined);
  }
}

declare module '@tanstack/react-router' {
  interface Register {
    router: HvacRouter;
  }
}
