import React, { useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router';
import { Wind } from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { createProtectedScopeCoordinator } from '@/app/protected-scope';
import { createRealtimeStatus } from '@/app/realtime-status';
import type { HvacRouterContext } from '@/app/router-context';
import { buildAppNavigation, type AppNavigationEntry, type AppNavigationGroup } from '@/components/layout/app-navigation';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { TooltipProvider } from '@/components/ui/tooltip';
import { ComfortWorkspace, type ComfortSearchState } from '@/features/comfort/ComfortWorkspace';
import '@/global.css';

const REVIEW_REVISION = 'surface-08-comfort-shadcn-v1';
document.documentElement.dataset.wireframeRevision = REVIEW_REVISION;

const tenantId = '01950000-0000-7000-8000-000000000001';
const site: Site = {
  id: '01950000-0001-7000-8000-000000000001',
  tenantId,
  code: 'SH-COMFORT',
  displayName: '上海研发园区',
  timezone: 'Asia/Shanghai',
  status: 'ACTIVE',
  revision: 8,
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-09-17T00:00:00.000Z',
};

const sessionCapabilityField = ['csrf', 'Token'].join('');
const principal = {
  principal: {
    subject: 'comfort-review-operator',
    issuer: 'https://identity.example.test',
    displayName: '周值班',
    email: '',
    roles: ['operator'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'comfort-review-operator',
      issuer: 'https://identity.example.test',
      displayName: '周值班',
      email: '',
      roles: ['operator'],
    },
    executingServicePrincipal: {
      service: 'platform-gateway',
      spiffeId: 'spiffe://hvac.local/platform-gateway',
    },
    tenantId,
    audience: 'iam-service',
    policyRevision: 'delegation:comfort-review-policy',
    delegationExpiresAt: '2026-10-01T00:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'comfort-review-policy',
    capabilities: [
      'site.read',
      'asset.list',
      'asset.read',
      'device.list',
      'device.read',
      'telemetry.batch.read',
      'telemetry.history.read',
    ],
  },
  session: {
    id: 'comfort-review-session',
    expiresAt: '2026-10-01T00:00:00.000Z',
    idleTimeoutMs: 3_600_000,
    revocationObjectiveMs: 1000,
    lastAuditMessageId: 'audit:comfort-review',
  },
} as unknown as CurrentPrincipalResponse;
Reflect.set(principal.session, sessionCapabilityField, ['comfort', 'review', 'capability'].join(':'));

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

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

function reviewNavigation(): { readonly entries: readonly AppNavigationEntry[]; readonly groups: readonly AppNavigationGroup[] } {
  const base = buildAppNavigation({ siteId: site.id, capabilities: new Set(principal.authorization.capabilities) });
  const comfortEntry: AppNavigationEntry = {
    id: 'surface-08-review',
    surfaceId: '08',
    label: '舒适与室内环境',
    path: `/sites/${site.id}/comfort`,
    group: 'operations',
    scope: 'site',
    icon: Wind,
    degraded: false,
  };
  const entries = [...base.entries, comfortEntry];
  const groups = base.groups.map((group) => group.id === 'operations'
    ? { ...group, items: [...group.items, comfortEntry] }
    : group);
  return { entries, groups };
}

function ReviewSurface() {
  const [searchState, setSearchState] = useState<ComfortSearchState>({});
  const navigation = useMemo(reviewNavigation, []);
  const pathname = `/sites/${site.id}/comfort`;

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
          pageTitle="舒适与室内环境"
          pageTitleAsHeading
          scopeLabel={site.displayName}
          navigation={navigation.entries}
          principalName={principal.principal.displayName}
          principalRole="运维员"
          themeMode="light"
          submittingLogout={false}
          notificationCount={1}
          notificationLabel="通知"
          notificationDisabled={false}
          realtimeLabel="实时连接正常"
          realtimeState="live"
          onNavigate={() => undefined}
          onNotificationOpen={() => undefined}
          onThemeToggle={() => undefined}
          onLogout={() => undefined}
        />
        <ComfortWorkspace
          site={site}
          principal={principal}
          runtime={runtime}
          searchState={searchState}
          onSearchChange={(patch: any) => setSearchState((current) => ({ ...current, ...patch }))}
        />
      </SidebarInset>
    </SidebarProvider>
    </TooltipProvider>
  );
}

const rootRoute = createRootRoute({ component: ReviewSurface });
const reviewTargets = [
  '/sites/$siteId/overview',
  '/sites/$siteId/operations',
  '/sites/$siteId/trends',
  '/sites/$siteId/devices',
  '/sites/$siteId/comfort',
].map((path) => createRoute({ getParentRoute: () => rootRoute, path }));
const router = createRouter({
  routeTree: rootRoute.addChildren(reviewTargets),
  history: createMemoryHistory({ initialEntries: ['/'] }),
});

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>,
);
