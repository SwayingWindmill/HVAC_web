import { useEffect } from 'react';
import { Outlet, createFileRoute, redirect } from '@tanstack/react-router';
import { z } from 'zod';
import { siteAssetModelEnvelopeQuery } from '@/api/site-asset-model';
import { RouteAccessError, requireSite } from '@/app/route-access';
import type { ShellRuntime } from '@/app/shell-runtime';
import { SiteRealtimeProvider } from '@/features/operations/realtime/site-realtime-context';

// Global workspace entries (sidebar navigation) operate on one Registry Site at a time.
// The Site travels in the `site` search parameter so links, history and the protected
// Site scope agree on which Site the page shows.
const workspaceSiteSearchSchema = z.object({
  site: z.string().optional().catch(undefined),
});

function defaultWorkspaceSiteId(runtime: ShellRuntime): string | undefined {
  const snapshot = runtime.current();
  return snapshot.protectedScope?.siteId ?? snapshot.lastSiteId ?? snapshot.sites?.items?.[0]?.id;
}

function SiteWorkspaceLayout() {
  const { site, principal, queryClient } = Route.useRouteContext();
  useEffect(() => {
    if (__HVAC_WEB_FRONTEND_REVIEW__) return;
    const capabilities = principal.authorization.capabilities;
    if (!capabilities.includes('asset.read') || !capabilities.includes('device.read')) return;
    void queryClient.prefetchQuery(siteAssetModelEnvelopeQuery(site.id));
  }, [principal.authorization.capabilities, queryClient, site.id]);
  return (
    <SiteRealtimeProvider>
      <Outlet />
    </SiteRealtimeProvider>
  );
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
  component: SiteWorkspaceLayout,
});
