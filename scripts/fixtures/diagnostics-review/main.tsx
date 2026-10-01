import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { createProtectedScopeCoordinator } from '@/app/protected-scope';
import { createRealtimeStatus } from '@/app/realtime-status';
import type { HvacRouterContext } from '@/app/router-context';
import { DiagnosticsWorkspace, type DiagnosticsSearchState } from '@/features/diagnostics/DiagnosticsWorkspace';
import '@/global.css';

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
    subject: 'diagnostics-review-operator',
    issuer: 'https://identity.example.test',
    displayName: '诊断复审操作员',
    email: '',
    roles: ['operator'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'diagnostics-review-operator',
      issuer: 'https://identity.example.test',
      displayName: '诊断复审操作员',
      email: '',
      roles: ['operator'],
    },
    executingServicePrincipal: {
      service: 'platform-gateway',
      spiffeId: 'spiffe://hvac.local/platform-gateway',
    },
    tenantId,
    audience: 'iam-service',
    policyRevision: 'delegation:diagnostics-review-policy',
    delegationExpiresAt: '2026-09-14T12:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'diagnostics-review-policy',
    capabilities: ['site.read', 'asset.list', 'device.list'],
  },
  session: {
    id: 'diagnostics-review-session',
    expiresAt: '2026-09-14T12:00:00.000Z',
    idleTimeoutMs: 3_600_000,
    revocationObjectiveMs: 1000,
    lastAuditMessageId: 'audit:diagnostics-review',
  },
} as unknown as CurrentPrincipalResponse;
Reflect.set(principal.session, sessionCapabilityField, ['diagnostics', 'review', 'capability'].join(':'));

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

function readSearch(): DiagnosticsSearchState {
  const params = new URLSearchParams(location.search);
  return {
    diagnosis: params.get('diagnosis') || undefined,
    q: params.get('q') || undefined,
    source: params.get('source') || undefined,
    alarm: params.get('alarm') || undefined,
  };
}

function writeSearch(next: DiagnosticsSearchState) {
  const params = new URLSearchParams();
  if (next.diagnosis) params.set('diagnosis', next.diagnosis);
  if (next.q) params.set('q', next.q);
  if (next.source) params.set('source', next.source);
  if (next.alarm) params.set('alarm', next.alarm);
  const query = params.toString();
  history.replaceState(null, '', query ? `/?${query}` : '/');
}

function ReviewApp() {
  const [searchState, setSearchState] = useState<DiagnosticsSearchState>(() => readSearch());

  useEffect(() => {
    const sync = () => setSearchState(readSearch());
    addEventListener('popstate', sync);
    return () => removeEventListener('popstate', sync);
  }, []);

  return (
    <DiagnosticsWorkspace
      site={site}
      principal={principal}
      runtime={runtime}
      searchState={searchState}
      onSearchChange={(patch: any) => {
        setSearchState((current) => {
          const next = { ...current, ...patch };
          writeSearch(next);
          return next;
        });
      }}
    />
  );
}

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <div className="min-h-screen bg-background p-6 text-foreground">
        <ReviewApp />
      </div>
    </QueryClientProvider>
  </React.StrictMode>,
);
