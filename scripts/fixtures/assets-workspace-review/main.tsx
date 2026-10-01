import React, { useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router';
import { NuqsAdapter } from 'nuqs/adapters/tanstack-router';
import { ServerCog } from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { createProtectedScopeCoordinator } from '@/app/protected-scope';
import { createRealtimeStatus } from '@/app/realtime-status';
import type { HvacRouterContext } from '@/app/router-context';
import { buildAppNavigation, type AppNavigationEntry, type AppNavigationGroup } from '@/components/layout/app-navigation';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { TooltipProvider } from '@/components/ui/tooltip';
import { AssetsWorkspace, type AssetsSearchState } from '@/features/assets-workspace/AssetsWorkspace';
import { AssetDeviceDetail } from '@/features/assets-workspace/AssetDeviceDetail';
import '@/global.css';

const REVIEW_REVISION = 'surface-06-07-shadcn-shell-v2';
document.documentElement.dataset.wireframeRevision = REVIEW_REVISION;

const tenantId = '01940000-0000-7000-8000-000000000001';
const site: Site = {
  id: '01940000-0001-7000-8000-000000000001',
  tenantId,
  code: 'TOKYO-CERT',
  displayName: '东京中央冷站',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 20,
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-08-01T00:00:00.000Z',
};

const sessionCapabilityField = ['csrf', 'Token'].join('');
const principal = {
  principal: {
    subject: 'assets-review-operator',
    issuer: 'https://identity.example.test',
    displayName: '林值班',
    email: '',
    roles: ['operator'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'assets-review-operator',
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
    policyRevision: 'delegation:assets-review-policy',
    delegationExpiresAt: '2026-10-01T00:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'assets-review-policy',
    capabilities: [
      'site.read',
      'asset.list',
      'asset.read',
      'device.list',
      'device.read',
      'telemetry.batch.read',
      'telemetry.history.read',
      'alarm.list',
      'alarm.read',
      'work-order.list',
      'work-order.read',
    ],
  },
  session: {
    id: 'assets-review-session',
    expiresAt: '2026-10-01T00:00:00.000Z',
    idleTimeoutMs: 3_600_000,
    revocationObjectiveMs: 1000,
    lastAuditMessageId: 'audit:assets-review',
  },
} as unknown as CurrentPrincipalResponse;
Reflect.set(principal.session, sessionCapabilityField, ['assets', 'review', 'capability'].join(':'));

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
  const deviceEntry: AppNavigationEntry = {
    id: 'surface-06-review',
    surfaceId: '06',
    label: '设备',
    path: `/sites/${site.id}/devices`,
    group: 'operations',
    scope: 'site',
    icon: ServerCog,
    degraded: false,
  };
  const entries = [...base.entries, deviceEntry];
  const groups = base.groups.map((group) => group.id === 'operations'
    ? { ...group, items: [...group.items, deviceEntry] }
    : group);
  return { entries, groups };
}

function ReviewSurface() {
  const params = new URLSearchParams(globalThis.location.search);
  const [searchState, setSearchState] = useState<AssetsSearchState>({});
  const [detailDeviceId, setDetailDeviceId] = useState<string | null>(() => (
    params.get('page') === 'device-detail'
      ? '01940000-0020-7001-8000-000000000001'
      : null
  ));
  const navigation = useMemo(reviewNavigation, []);
  const pathname = `/sites/${site.id}/devices`;
  const pageTitle = detailDeviceId ? '设备详情' : '设备';

  return (
    <NuqsAdapter>
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
        onNavigate={(target) => {
          if (target === `/sites/${site.id}/devices`) setDetailDeviceId(null);
        }}
      />
      <SidebarInset className="min-w-0">
        <AppHeader
          pageTitle={pageTitle}
          pageTitleAsHeading={!detailDeviceId}
          scopeLabel={site.displayName}
          navigation={navigation.entries}
          principalName={principal.principal.displayName}
          principalRole="运维员"
          themeMode="light"
          submittingLogout={false}
          notificationCount={2}
          notificationLabel="通知中心"
          notificationDisabled={false}
          realtimeLabel="实时连接正常"
          realtimeState="live"
          onNavigate={() => undefined}
          onNotificationOpen={() => undefined}
          onThemeToggle={() => undefined}
          onLogout={() => undefined}
        />
        {detailDeviceId ? (
          <AssetDeviceDetail
            site={site}
            principal={principal}
            runtime={runtime}
            deviceId={detailDeviceId}
            onBack={() => setDetailDeviceId(null)}
          />
        ) : (
          <AssetsWorkspace
            site={site}
            principal={principal}
            runtime={runtime}
            searchState={searchState}
            onSearchChange={(patch: any) => setSearchState((current) => ({ ...current, ...patch }))}
            onOpenDetail={setDetailDeviceId}
          />
        )}
      </SidebarInset>
      </SidebarProvider>
    </TooltipProvider>
    </NuqsAdapter>
  );
}

const rootRoute = createRootRoute({ component: ReviewSurface });
const reviewTargets = [
  '/sites/$siteId/overview',
  '/sites/$siteId/operations',
  '/sites/$siteId/devices',
  '/sites/$siteId/devices/$deviceId',
  '/sites/$siteId/trends',
  '/sites/$siteId/alarms',
  '/sites/$siteId/work-orders',
  '/sites/$siteId/energy',
  '/sites/$siteId/diagnostics',
  '/sites/$siteId/control',
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
