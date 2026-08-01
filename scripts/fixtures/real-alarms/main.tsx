import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/real/protected-scope';
import { RealAlarms } from '@/real/RealAlarms';
import '@/real/real-shell.css';

const organizationId = '01910000-0000-7000-8000-000000000001';
const siteA: Site = {
  id: '01910000-0001-7000-8000-000000000001', owningOrganizationId: organizationId,
  code: 'TOKYO-ALARM', displayName: 'Tokyo Alarm Site', timezone: 'Asia/Tokyo', status: 'ACTIVE', revision: 1,
  createdAt: '2026-07-31T00:00:00.000Z', updatedAt: '2026-07-31T00:00:00.000Z',
};
const siteB: Site = {
  id: '01910000-0002-7000-8000-000000000002', owningOrganizationId: organizationId,
  code: 'OSAKA-ALARM', displayName: 'Osaka Alarm Site', timezone: 'Asia/Tokyo', status: 'ACTIVE', revision: 1,
  createdAt: '2026-07-31T00:00:00.000Z', updatedAt: '2026-07-31T00:00:00.000Z',
};
const principal = {
  principal: { subject: 'real-alarm-audit', issuer: 'https://identity.example.test', displayName: 'Alarm Auditor', email: '', roles: ['operator'] },
  context: {
    initiatingPrincipal: { subject: 'real-alarm-audit', issuer: 'https://identity.example.test', displayName: 'Alarm Auditor', email: '', roles: ['operator'] },
    executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac.local/platform-gateway' },
    actingOrganizationId: organizationId, audience: 'iam-service', policyRevision: 'alarm-policy-1', delegationExpiresAt: '2026-08-01T00:00:00.000Z',
  },
  authorization: { capabilitySetVersion: 4, policyRevision: 'alarm-policy-1', capabilities: ['site.read', 'alarm.list', 'alarm.read'] },
  session: { id: 'alarm-audit-session', expiresAt: '2026-08-01T00:00:00.000Z', revocationObjectiveMs: 30000, lastAuditMessageId: 'alarm-audit-message' },
} as unknown as CurrentPrincipalResponse;
Reflect.set(principal.session, ['csrf', 'Token'].join(''), 'fixture-capability');

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
let registeredResource: ProtectedScopeResource | null = null;
let registeredDraft: ProtectedScopeDraft | null = null;

function App() {
  const [site, setSite] = useState<Site>(siteA);
  const [alarmAccess, setAlarmAccess] = useState(true);
  const effectivePrincipal = {
    ...principal,
    authorization: {
      ...principal.authorization,
      policyRevision: alarmAccess ? 'alarm-policy-1' : 'alarm-policy-denied',
      capabilities: alarmAccess ? ['site.read', 'alarm.list', 'alarm.read'] : ['site.read'],
    },
  } as CurrentPrincipalResponse;
  useEffect(() => {
    Reflect.set(globalThis, '__REAL_ALARMS_AUDIT__', {
      cacheCount: () => queryClient.getQueryCache().findAll({ queryKey: ['real-alarms'] }).length,
      cacheKeys: () => queryClient.getQueryCache().findAll({ queryKey: ['real-alarms'] }).map((query) => query.queryKey),
      purge: async () => registeredResource?.purge(),
      draftDirty: () => registeredDraft?.isDirty() ?? false,
      switchSite: () => {
        globalThis.history.replaceState(null, '', '/');
        setSite(siteB);
      },
      denyAlarm: () => setAlarmAccess(false),
      siteId: () => site.id,
    });
  }, [alarmAccess, site]);
  return (
    <main className="real-route-surface" data-testid="real-alarms-audit-root">
      <RealAlarms
        site={site}
        principal={effectivePrincipal}
        registerUnsavedDraft={(draft) => {
          registeredDraft = draft;
          return () => {
            if (registeredDraft?.id === draft.id) registeredDraft = null;
          };
        }}
        registerProtectedResource={(resource) => {
          registeredResource = resource;
          return () => {
            if (registeredResource?.id === resource.id) registeredResource = null;
          };
        }}
      />
    </main>
  );
}

createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}><App /></QueryClientProvider>,
);
