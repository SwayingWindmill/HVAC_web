import React, { StrictMode, useCallback, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { ConfigProvider } from 'antd';

import { AgentSessionWorkspace } from '@/features/operations/AgentSessionWorkspace';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import '@/global.css';

const site: Site = {
  id: '01910000-0001-7000-8000-000000000001',
  tenantId: '01910000-0000-7000-8000-000000000001',
  code: 'audit-site',
  displayName: 'Agent Audit Site',
  timezone: 'Asia/Shanghai',
  status: 'ACTIVE',
  revision: 1,
  createdAt: '2026-09-04T00:00:00.000Z',
  updatedAt: '2026-09-04T00:00:00.000Z',
};

const operator = {
  subject: 'agent-session-audit',
  issuer: 'https://identity.example.test',
  displayName: 'Agent Session Auditor',
  email: '',
  roles: ['operator'],
};

const principal: CurrentPrincipalResponse = {
  principal: operator,
  context: {
    initiatingPrincipal: operator,
    executingServicePrincipal: {
      service: 'platform-gateway',
      spiffeId: 'spiffe://hvac.local/platform-gateway',
    },
    tenantId: site.tenantId,
    audience: 'iam-service',
    policyRevision: 'agent-session-policy-1',
    delegationExpiresAt: '2026-09-05T00:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 11,
    policyRevision: 'agent-session-policy-1',
    capabilities: ['site.read'],
  },
  session: {
    id: 'agent-session-browser-audit',
    expiresAt: '2026-09-05T00:00:00.000Z',
    idleTimeoutMs: 1_800_000,
    csrfToken: '[REDACTED_SECRET]',
    revocationObjectiveMs: 30_000,
    lastAuditMessageId: 'agent-session-browser-audit-message',
  },
};

type Resource = Parameters<React.ComponentProps<typeof AgentSessionWorkspace>['registerProtectedResource']>[0];

function AuditApp() {
  const resourceRef = useRef<Resource | null>(null);
  const [purgeCount, setPurgeCount] = useState(0);
  const registerProtectedResource = useCallback((resource: Resource) => {
    resourceRef.current = resource;
    return () => {
      if (resourceRef.current === resource) resourceRef.current = null;
    };
  }, []);

  return (
    <ConfigProvider>
      <main style={{ height: '100vh', padding: 16 }}>
        <button
          id="agent-session-audit-purge"
          type="button"
          onClick={() => {
            void resourceRef.current?.purge('SITE_CHANGE');
            setPurgeCount((current) => current + 1);
          }}
        >
          Purge protected Agent resource
        </button>
        <output id="agent-session-audit-purge-count" aria-live="polite">{purgeCount}</output>
        <AgentSessionWorkspace
          site={site}
          principal={principal}
          registerProtectedResource={registerProtectedResource}
        />
      </main>
    </ConfigProvider>
  );
}

const root = document.getElementById('root');
if (!root) throw new Error('Agent Session browser fixture root is missing.');
createRoot(root).render(<StrictMode><AuditApp /></StrictMode>);
