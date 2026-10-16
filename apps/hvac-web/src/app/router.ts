import { createRouter } from '@tanstack/react-router';
import type { HvacRouterContext } from './router-context';
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
    scrollRestoration: ({ location }) => {
      const samePage = location.pathname === renderedPath;
      renderedPath = location.pathname;
      return !samePage;
    },
  });
}

export type HvacRouter = ReturnType<typeof createHvacRouter>;

declare module '@tanstack/react-router' {
  interface Register {
    router: HvacRouter;
  }
}
