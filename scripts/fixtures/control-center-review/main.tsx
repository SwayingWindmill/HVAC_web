import { useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { createProtectedScopeCoordinator } from '@/app/protected-scope';
import { createRealtimeStatus } from '@/app/realtime-status';
import type { HvacRouterContext } from '@/app/router-context';
import { buildAppNavigation } from '@/components/layout/app-navigation';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { TooltipProvider } from '@/components/ui/tooltip';
import { ControlCenter, type ControlCenterSearchState } from '@/features/control/ControlCenter';
import '@/global.css';

const REVIEW_REVISION = 'surface-25-control-center-v1';
document.documentElement.dataset.wireframeRevision = REVIEW_REVISION;

const tenantId = '01970000-0000-7000-8000-000000000001';
const site: Site = {
  id: '01970000-0001-7000-8000-000000000001',
  tenantId,
  code: 'TOKYO-CONTROL',
  displayName: '东京中央冷站',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 9,
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-09-16T00:00:00.000Z',
};

const principal = {
  principal: {
    subject: 'control-review-operator',
    issuer: 'https://identity.example.test',
    displayName: '林值班',
    email: '',
    roles: ['operator'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'control-review-operator',
      issuer: 'https://identity.example.test',
      displayName: '林值班',
      email: '',
      roles: ['operator'],
    },
    executingServicePrincipal: {
      service: 'platform-gateway',
      spiffeId: 'spiffe://hvac.local/platform-gateway',
    },
    tenantId,
    audience: 'iam-service',
    policyRevision: 'control-review-policy',
    delegationExpiresAt: '2026-10-01T00:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'control-review-policy',
    capabilities: ['site.read', 'asset.list', 'device.list', 'telemetry.snapshot.read'],
  },
  session: {
    id: 'control-review-session',
    expiresAt: '2026-10-01T00:00:00.000Z',
    revocationObjectiveMs: 1000,
    lastAuditMessageId: 'audit:control-review',
  },
} as unknown as CurrentPrincipalResponse;
Reflect.set(principal.session, ['csrf', 'Token'].join(''), 'control-review-csrf');

const protectedScope = createProtectedScopeCoordinator();
protectedScope.activate(site.id);

const runtime = {
  current: () => ({
    state: 'READY' as const,
    principal,
    platform: { state: 'available' as const },
    sites: { state: 'available' as const, items: [site] },
    protectedScope: protectedScope.current(),
    realtime: createRealtimeStatus({ state: 'live', siteId: site.id }),
    logout: { status: 'idle' as const },
  }),
  subscribe: () => () => undefined,
  bootstrap: async () => undefined,
  retry: async () => undefined,
  beginLogin: () => undefined,
  activateSiteScope: () => undefined,
  registerUnsavedDraft: (draft) => protectedScope.registerDraft(draft),
  registerProtectedResource: (resource) => protectedScope.registerResource(resource),
  protectedRequestToken: () => protectedScope.requestToken(),
  publishRealtimeStatus: () => undefined,
  requestSiteNavigation: async () => 'navigated' as const,
  confirmSiteNavigation: async () => 'navigated' as const,
  cancelSiteNavigation: () => undefined,
  handlePolicyRevision: async () => 'unchanged' as const,
  logout: async () => 'completed' as const,
  purge: () => undefined,
  dispose: () => undefined,
} satisfies HvacRouterContext['runtime'];

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});

function ReviewSurface() {
  const [searchState, setSearchState] = useState<ControlCenterSearchState>(() => {
    const target = new URLSearchParams(globalThis.location.search).get('target');
    return target ? { target } : {};
  });
  const navigation = useMemo(() => buildAppNavigation({
    siteId: site.id,
    capabilities: new Set(principal.authorization.capabilities),
  }), []);
  const pathname = `/sites/${site.id}/control`;

  const updateSearch = (patch: Partial<ControlCenterSearchState>) => {
    setSearchState((current) => {
      const next = { ...current, ...patch };
      const params = new URLSearchParams(globalThis.location.search);
      if (next.target) params.set('target', next.target);
      else params.delete('target');
      globalThis.history.replaceState(null, '', `${globalThis.location.pathname}?${params.toString()}`);
      return next;
    });
  };

  return (
    <TooltipProvider>
      <SidebarProvider>
      <AppSidebar
        title="泉来禾智慧能源"
        pathname={pathname}
        groups={navigation.groups}
        siteId={site.id}
        siteLabel={site.displayName}
        siteOptions={[{ value: site.id, label: site.displayName }]}
        onSiteChange={() => undefined}
        onNavigate={() => undefined}
      />
      <SidebarInset className="min-w-0">
        <AppHeader
          pageTitle="控制"
          scopeLabel={site.displayName}
          navigation={navigation.entries}
          principalName={principal.principal.displayName}
          principalRole="运维员"
          themeMode="light"
          submittingLogout={false}
          notificationCount={2}
          notificationLabel="通知"
          notificationDisabled={false}
          realtimeLabel="实时连接正常"
          realtimeState="live"
          onNavigate={() => undefined}
          onNotificationOpen={() => undefined}
          onThemeToggle={() => undefined}
          onLogout={() => undefined}
        />
        <div className="p-4 md:p-6">
          <ControlCenter
            site={site}
            principal={principal}
            runtime={runtime}
            searchState={searchState}
            onSearchChange={updateSearch}
          />
        </div>
      </SidebarInset>
    </SidebarProvider>
    </TooltipProvider>
  );
}

const rootRoute = createRootRoute({ component: ReviewSurface });
const routes = [
  '/sites/$siteId/overview',
  '/sites/$siteId/operations',
  '/sites/$siteId/alarms',
  '/sites/$siteId/energy',
  '/sites/$siteId/diagnostics',
  '/sites/$siteId/control',
  '/sites/$siteId/assets',
].map((path) => createRoute({ getParentRoute: () => rootRoute, path }));
const router = createRouter({
  routeTree: rootRoute.addChildren(routes),
  history: createMemoryHistory({ initialEntries: ['/'] }),
});

createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <RouterProvider router={router} />
  </QueryClientProvider>,
);
