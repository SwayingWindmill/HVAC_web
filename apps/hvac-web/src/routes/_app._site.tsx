import { Outlet, createFileRoute, redirect } from '@tanstack/react-router';
import { z } from 'zod';
import { RouteAccessError, requireSite } from '@/app/route-access';
import type { ShellRuntime } from '@/app/shell-runtime';

// Global workspace entries (sidebar navigation) operate on one Registry Site at a time.
// The Site travels in the `site` search parameter so links, history and the protected
// Site scope agree on which Site the page shows.
const workspaceSiteSearchSchema = z.object({
  site: z.string().optional(),
});

function defaultWorkspaceSiteId(runtime: ShellRuntime): string | undefined {
  const snapshot = runtime.current();
  return snapshot.protectedScope?.siteId ?? snapshot.sites?.items?.[0]?.id;
}

export const Route = createFileRoute('/_app/_site')({
  validateSearch: workspaceSiteSearchSchema,
  beforeLoad: ({ context, search, location }) => {
    if (!search.site) {
      const siteId = defaultWorkspaceSiteId(context.runtime);
      if (!siteId) throw new RouteAccessError('SITE_NOT_VISIBLE');
      const params = new URLSearchParams(location.searchStr);
      params.set('site', siteId);
      throw redirect({ href: `${location.pathname}?${params.toString()}`, replace: true });
    }
    return requireSite(context.runtime, search.site);
  },
  component: Outlet,
});
