import React from 'react';
import { createRoot } from 'react-dom/client';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { ProtectedScopeResource } from '@/real/protected-scope';
import { OperationsInvestigation } from '@/real/OperationsInvestigation';
import '@/real/real-shell.css';

const organizationId = '01910000-0000-7000-8000-000000000001';
const site: Site = {
  id: '01910000-0001-7000-8000-000000000001',
  owningOrganizationId: organizationId,
  code: 'TOKYO-OPERATIONS',
  displayName: 'Tokyo Operations Site',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 1,
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-08-01T00:00:00.000Z',
};
const principal = {
  principal: {
    subject: 'operations-reconnect-audit',
    issuer: 'https://identity.example.test',
    displayName: 'Operations Auditor',
    email: '',
    roles: ['operator'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'operations-reconnect-audit',
      issuer: 'https://identity.example.test',
      displayName: 'Operations Auditor',
      email: '',
      roles: ['operator'],
    },
    executingServicePrincipal: {
      service: 'platform-gateway',
      spiffeId: 'spiffe://hvac.local/platform-gateway',
    },
    actingOrganizationId: organizationId,
    audience: 'iam-service',
    policyRevision: 'operations-policy-1',
    delegationExpiresAt: '2026-08-02T00:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 1,
    policyRevision: 'operations-policy-1',
    capabilities: ['site.read'],
  },
  session: {
    id: 'operations-reconnect-session',
    expiresAt: '2026-08-02T00:00:00.000Z',
    revocationObjectiveMs: 30_000,
    lastAuditMessageId: 'operations-reconnect-audit-message',
  },
} as unknown as CurrentPrincipalResponse;

let protectedResource: ProtectedScopeResource | null = null;
Reflect.set(globalThis, '__OPERATIONS_RECONNECT_AUDIT__', {
  protectedResourceId: () => protectedResource?.id ?? null,
  purge: async () => protectedResource?.purge(),
});

createRoot(document.getElementById('root')!).render(
  <main className="real-route-surface" data-testid="operations-reconnect-audit-root">
    <OperationsInvestigation
      site={site}
      principal={principal}
      registerProtectedResource={(resource) => {
        protectedResource = resource;
        return () => {
          if (protectedResource?.id === resource.id) protectedResource = null;
        };
      }}
    />
  </main>,
);
