import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { buildAppNavigation } from '@/components/layout/app-navigation';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { TooltipProvider } from '@/components/ui/tooltip';
import { EnergyAnalytics, type EnergyAnalysisSearchState } from '@/features/energy/EnergyAnalytics';
import type { EnergyWorkspacePeriod } from '@/features/energy/workspace';
import '@/global.css';

const REVIEW_REVISION = 'surface-14-energy-analysis-v1';
document.documentElement.dataset.wireframeRevision = REVIEW_REVISION;

const tenantId = '01960000-0000-7000-8000-000000000001';
const site: Site = {
  id: '01960000-0001-7000-8000-000000000001',
  tenantId,
  code: 'TOKYO-ENERGY',
  displayName: '东京中央冷站',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 1,
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-09-15T00:00:00.000Z',
};

const principal = {
  principal: {
    subject: 'energy-review-operator',
    issuer: 'https://identity.example.test',
    displayName: '林值班',
    email: '',
    roles: ['operator'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'energy-review-operator',
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
    policyRevision: 'energy-review-policy',
    delegationExpiresAt: '2026-10-01T00:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'energy-review-policy',
    capabilities: ['site.read', 'alarm.list', 'alarm.read'],
  },
  session: {
    id: 'energy-review-session',
    expiresAt: '2026-10-01T00:00:00.000Z',
    revocationObjectiveMs: 1000,
    lastAuditMessageId: 'audit:energy-review',
  },
} as unknown as CurrentPrincipalResponse;
Reflect.set(principal.session, ['csrf', 'Token'].join(''), 'energy-review-csrf');

const nativeFetch = globalThis.fetch.bind(globalThis);
const fetchTarget = globalThis as typeof globalThis & { __energyFetchCalls?: string[] };
fetchTarget.__energyFetchCalls = [];
globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
  const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
  if (url.includes('/api/v1/analytics/energy-series')) fetchTarget.__energyFetchCalls?.push(url);
  return nativeFetch(input, init);
}) as typeof globalThis.fetch;

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false },
  },
});

function ReviewSurface() {
  const [period, setPeriod] = useState<EnergyWorkspacePeriod>('month');
  const [searchState, setSearchState] = useState<EnergyAnalysisSearchState>({ anchor: '2026-08-01' });
  const navigation = buildAppNavigation({
    siteId: site.id,
    capabilities: new Set(principal.authorization.capabilities),
  });
  const pathname = `/sites/${site.id}/energy`;

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
          pageTitle="能源分析"
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
        <EnergyAnalytics
          site={site}
          principal={principal}
          initialPeriod={period}
          searchState={searchState}
          onWorkspaceChange={(next: any) => {
            setPeriod(next.period);
            setSearchState({
              anchor: next.anchor,
              quality: next.qualityPolicy === 'VALID_ONLY' ? undefined : next.qualityPolicy,
            });
          }}
        />
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
