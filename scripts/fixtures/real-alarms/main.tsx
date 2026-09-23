import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/app/protected-scope';
import { buildAppNavigation } from '@/components/layout/app-navigation';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { TooltipProvider } from '@/components/ui/tooltip';
import { AlarmCenterWorkbench, type AlarmCenterSearchState } from '@/features/alarms/AlarmCenterWorkbench';
import '@/global.css';

const ALARM_REVIEW_REVISION = 'surface-09-wireframe-v1';
document.documentElement.dataset.wireframeRevision = ALARM_REVIEW_REVISION;

const tenantId = '01910000-0000-7000-8000-000000000001';
const siteA: Site = {
  id: '01910000-0001-7000-8000-000000000001', tenantId,
  code: 'TOKYO-ALARM', displayName: '东京中央冷站', timezone: 'Asia/Tokyo', status: 'ACTIVE', revision: 1,
  createdAt: '2026-07-31T00:00:00.000Z', updatedAt: '2026-07-31T00:00:00.000Z',
};
const siteB: Site = {
  id: '01910000-0002-7000-8000-000000000002', tenantId,
  code: 'OSAKA-ALARM', displayName: '大阪南区冷站', timezone: 'Asia/Tokyo', status: 'ACTIVE', revision: 1,
  createdAt: '2026-07-31T00:00:00.000Z', updatedAt: '2026-07-31T00:00:00.000Z',
};
const principal = {
  principal: { subject: 'real-alarm-audit', issuer: 'https://identity.example.test', displayName: '林值班', email: '', roles: ['operator'] },
  context: {
    initiatingPrincipal: { subject: 'real-alarm-audit', issuer: 'https://identity.example.test', displayName: '林值班', email: '', roles: ['operator'] },
    executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac.local/platform-gateway' },
    tenantId, audience: 'iam-service', policyRevision: 'alarm-policy-1', delegationExpiresAt: '2026-10-01T00:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'alarm-policy-1',
    capabilities: ['site.read', 'alarm.list', 'alarm.read', 'alarm.assign', 'asset.list', 'device.list', 'work-order.list', 'work-order.create'],
  },
  session: { id: 'alarm-audit-session', expiresAt: '2026-10-01T00:00:00.000Z', idleTimeoutMs: 30 * 60 * 1000, revocationObjectiveMs: 30000, lastAuditMessageId: 'alarm-audit-message' },
} as unknown as CurrentPrincipalResponse;
Reflect.set(principal.session, ['csrf', 'Token'].join(''), 'fixture-capability');

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
let registeredResource: ProtectedScopeResource | null = null;
let registeredDraft: ProtectedScopeDraft | null = null;

function readSearchState(): AlarmCenterSearchState {
  const params = new URLSearchParams(globalThis.location.search);
  const alarmView = params.get('alarmView');
  const severity = params.get('severity');
  const ack = params.get('ack');
  const owner = params.get('owner');
  const sourceType = params.get('sourceType');
  return {
    alarmView: alarmView === 'active' || alarmView === 'history' || alarmView === 'suppressed' || alarmView === 'performance' ? alarmView : undefined,
    q: params.get('q') || undefined,
    severity: severity === 'CRITICAL' || severity === 'MAJOR' || severity === 'MINOR' || severity === 'WARNING' || severity === 'INFO' ? severity : undefined,
    ack: ack === 'unacknowledged' || ack === 'acknowledged' ? ack : undefined,
    owner: owner === 'unassigned' || owner === 'assigned' ? owner : undefined,
    sourceType: sourceType === 'DEVICE_RULE' || sourceType === 'SITE_RULE' || sourceType === 'EXTERNAL' ? sourceType : undefined,
    selected: params.get('selected') || undefined,
    source: params.get('source') || undefined,
    device: params.get('device') || undefined,
    deviceId: params.get('deviceId') || undefined,
  };
}

function writeSearchState(state: AlarmCenterSearchState): void {
  const params = new URLSearchParams();
  if (state.alarmView && state.alarmView !== 'active') params.set('alarmView', state.alarmView);
  if (state.q) params.set('q', state.q);
  if (state.severity) params.set('severity', state.severity);
  if (state.ack) params.set('ack', state.ack);
  if (state.owner) params.set('owner', state.owner);
  if (state.sourceType) params.set('sourceType', state.sourceType);
  if (state.selected) params.set('selected', state.selected);
  if (state.source) params.set('source', state.source);
  if (state.device) params.set('device', state.device);
  if (state.deviceId) params.set('deviceId', state.deviceId);
  const query = params.toString();
  globalThis.history.replaceState(null, '', query ? `/?${query}` : '/');
}

function AlarmReviewSurface() {
  const [site, setSite] = useState<Site>(siteA);
  const [alarmAccess, setAlarmAccess] = useState(true);
  const [searchState, setSearchState] = useState<AlarmCenterSearchState>(() => readSearchState());
  const effectivePrincipal = {
    ...principal,
    authorization: {
      ...principal.authorization,
      policyRevision: alarmAccess ? 'alarm-policy-1' : 'alarm-policy-denied',
      capabilities: alarmAccess ? principal.authorization.capabilities : ['site.read'],
    },
  } as CurrentPrincipalResponse;
  const navigation = buildAppNavigation({ siteId: site.id, capabilities: new Set(effectivePrincipal.authorization.capabilities) });

  useEffect(() => {
    const onPopState = () => setSearchState(readSearchState());
    globalThis.addEventListener('popstate', onPopState);
    return () => globalThis.removeEventListener('popstate', onPopState);
  }, []);

  useEffect(() => {
    Reflect.set(globalThis, '__REAL_ALARMS_AUDIT__', {
      cacheCount: () => queryClient.getQueryCache().findAll({ queryKey: ['surface-09'] }).length,
      cacheKeys: () => queryClient.getQueryCache().findAll({ queryKey: ['surface-09'] }).map((query) => query.queryKey),
      purge: async () => registeredResource?.purge(),
      draftDirty: () => registeredDraft?.isDirty() ?? false,
      switchSite: () => {
        setSearchState({});
        writeSearchState({});
        setSite(siteB);
      },
      denyAlarm: () => setAlarmAccess(false),
      siteId: () => site.id,
    });
  }, [alarmAccess, site]);

  const pathname = `/sites/${site.id}/issues`;
  return (
    <TooltipProvider>
      <SidebarProvider>
      <AppSidebar
        title="泉来禾智慧能源"
        pathname={pathname}
        groups={navigation.groups}
        siteId={site.id}
        siteLabel={site.displayName}
        siteOptions={[{ value: site.id, label: site.displayName }, { value: siteB.id, label: siteB.displayName }]}
        onSiteChange={(nextSiteId) => {
          setSearchState({});
          writeSearchState({});
          setSite(nextSiteId === siteB.id ? siteB : siteA);
        }}
        onNavigate={() => undefined}
      />
      <SidebarInset className="min-w-0">
        <AppHeader
          pageTitle="告警"
          scopeLabel={site.displayName}
          navigation={navigation.entries}
          principalName={effectivePrincipal.principal.displayName}
          principalRole="运维员"
          themeMode="light"
          submittingLogout={false}
          notificationCount={3}
          notificationLabel="通知"
          notificationDisabled={false}
          realtimeLabel="实时连接正常"
          realtimeState="live"
          onNavigate={() => undefined}
          onNotificationOpen={() => undefined}
          onThemeToggle={() => undefined}
          onLogout={() => undefined}
        />
        <AlarmCenterWorkbench
          site={site}
          principal={effectivePrincipal}
          searchState={searchState}
          onSearchChange={(patch: any) => {
            setSearchState((previous) => {
              const next = { ...previous, ...patch };
              writeSearchState(next);
              return next;
            });
          }}
          registerUnsavedDraft={(draft: any) => {
            registeredDraft = draft;
            return () => { if (registeredDraft?.id === draft.id) registeredDraft = null; };
          }}
          registerProtectedResource={(resource: any) => {
            registeredResource = resource;
            return () => { if (registeredResource?.id === resource.id) registeredResource = null; };
          }}
        />
      </SidebarInset>
    </SidebarProvider>
    </TooltipProvider>
  );
}

const rootRoute = createRootRoute({ component: AlarmReviewSurface });
const reviewTargets = [
  '/sites/$siteId/overview',
  '/sites/$siteId/operations',
  '/sites/$siteId/issues',
  '/sites/$siteId/assets/$deviceId',
  '/sites/$siteId/work-orders',
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
