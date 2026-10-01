import { useCallback, useMemo } from 'react';
import { getRouteApi, useLocation } from '@tanstack/react-router';
import type { Site } from '@/api/generated/platformGateway.gen';
import { siteIdFromLocation, workspaceLocationForSite } from '@/app/router-paths';
import { useShellRuntime, useShellSnapshot } from '@/app/ShellRuntimeContext';

export interface ScopeItem {
  /** Stable query-key scope for the Site, `site:<siteId>`. */
  readonly id: string;
  readonly type: 'site';
  readonly siteId: string;
  readonly name: string;
  readonly timezone: string;
}

const NO_SITES: readonly Readonly<Site>[] = [];

function siteScope(site: Readonly<Site>): ScopeItem {
  return {
    id: `site:${site.id}`,
    type: 'site',
    siteId: site.id,
    name: site.displayName,
    timezone: site.timezone,
  };
}

/** Registry Sites visible to the principal and the Site the current location is scoped to. */
export function useScope() {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const location = useLocation();
  const sites = snapshot.sites?.items ?? NO_SITES;
  const siteId = siteIdFromLocation(location.searchStr) ?? snapshot.protectedScope?.siteId;
  const availableScopes = useMemo(() => sites.map(siteScope), [sites]);
  const currentScope = availableScopes.find((scope) => scope.siteId === siteId) ?? null;

  const setScope = useCallback((nextSiteId: string) => {
    if (nextSiteId === currentScope?.siteId) return;
    void runtime.requestSiteNavigation(workspaceLocationForSite(location.pathname, nextSiteId));
  }, [currentScope?.siteId, location.pathname, runtime]);

  return { currentScope, availableScopes, setScope };
}

const workspaceSiteRoute = getRouteApi('/_app/_site');

/** The Site of a global workspace page, resolved and authorized by the `_site` layout route. */
export function useWorkspaceScope() {
  const { site } = workspaceSiteRoute.useRouteContext();
  const currentScope = useMemo(() => siteScope(site), [site]);
  return { currentScope, site };
}
