import { createRouter } from '@tanstack/react-router';
import type { HvacRouterContext } from './router-context';
import { routeTree } from '../routeTree.gen';

export function createHvacRouter(context: HvacRouterContext) {
  return createRouter({
    routeTree,
    context,
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0,
  });
}

export type HvacRouter = ReturnType<typeof createHvacRouter>;

declare module '@tanstack/react-router' {
  interface Register {
    router: HvacRouter;
  }
}
