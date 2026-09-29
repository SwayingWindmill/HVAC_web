import { useEffect } from 'react';
import { NuqsAdapter } from 'nuqs/adapters/tanstack-router';
import { Outlet, createRootRouteWithContext, useLocation } from '@tanstack/react-router';
import type { HvacRouterContext } from '@/app/router-context';
import { NotFoundSurface, RouteErrorSurface } from '@/app/RouteSurfaces';
import { useObservability } from '@/app/Observability';

export const Route = createRootRouteWithContext<HvacRouterContext>()({
  component: RootRoute,
  notFoundComponent: NotFoundSurface,
  errorComponent: RouteErrorSurface,
});

function RootRoute() {
  const location = useLocation();
  const observability = useObservability();
  useEffect(() => {
    observability.record({ name: 'route_change', fields: { path: location.pathname } });
  }, [location.pathname, location.searchStr, observability]);
  return (
    <NuqsAdapter>
      <Outlet />
    </NuqsAdapter>
  );
}
