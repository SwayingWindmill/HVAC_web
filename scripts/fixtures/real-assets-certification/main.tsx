import React, { useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { DeviceObservationSnapshot } from '@/api/generated/s2Telemetry.gen';
import { createProtectedScopeCoordinator, type ProtectedScopePurgeReason } from '@/real/protected-scope';
import { RealAssetsWorkspace } from '@/real/assets/RealAssetsWorkspace';
import { parseRealAssetsDetailPath } from '@/real/assets/detail';
import type { RealAssetsRealtimeState, RealAssetsRealtimeTarget } from '@/real/assets/realtime';
import { createRealAssetsTelemetryRuntime, type RealAssetsTelemetryLiveClient, type RealAssetsTelemetryLiveSession } from '@/real/assets/telemetry-runtime';
import '@/real/real-shell.css';

const organizationId = '01940000-0000-7000-8000-000000000001';
const siteA: Site = {
  id: '01940000-0001-7000-8000-000000000001', owningOrganizationId: organizationId,
  code: 'TOKYO-CERT', displayName: 'Tokyo 200 Device Certification Site', timezone: 'Asia/Tokyo', status: 'ACTIVE', revision: 20,
  createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z',
};
const siteB: Site = {
  id: '01940000-0002-7000-8000-000000000002', owningOrganizationId: organizationId,
  code: 'OSAKA-CERT', displayName: 'Osaka Scope Purge Site', timezone: 'Asia/Tokyo', status: 'ACTIVE', revision: 2,
  createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z',
};
const sessionCapabilityField = ['csrf', 'Token'].join('');
const sessionCapabilityValue = ['real-assets', 'certification', 'capability'].join(':');
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const protectedScope = createProtectedScopeCoordinator();

function principal(sessionId: string, policyRevision: string): CurrentPrincipalResponse {
  const response = {
    principal: {
      subject: 'real-assets-certification-operator', issuer: 'https://identity.example.test',
      displayName: 'Real Assets Certification Operator', email: '', roles: ['operator'],
    },
    context: {
      initiatingPrincipal: {
        subject: 'real-assets-certification-operator', issuer: 'https://identity.example.test',
        displayName: 'Real Assets Certification Operator', email: '', roles: ['operator'],
      },
      executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac.local/platform-gateway' },
      actingOrganizationId: organizationId,
      audience: 'iam-service',
      policyRevision: `delegation:${policyRevision}`,
      delegationExpiresAt: '2026-08-01T12:00:00.000Z',
    },
    authorization: {
      capabilitySetVersion: 2,
      policyRevision,
      capabilities: [
        'equipment.list', 'device.list', 'device.read',
        'telemetry.batch.read', 'telemetry.history.read', 'telemetry.subscribe',
      ],
    },
    session: {
      id: sessionId,
      expiresAt: '2026-08-01T12:00:00.000Z',
      revocationObjectiveMs: 1000,
      lastAuditMessageId: `audit:${sessionId}`,
    },
  } as unknown as CurrentPrincipalResponse;
  Reflect.set(response.session, sessionCapabilityField, sessionCapabilityValue);
  return response;
}

function valueForKey(key: string, revision: number) {
  if (key.endsWith('run_state')) return { value: 'RUNNING', valueType: 'STRING' as const, unit: null };
  if (key.endsWith('cop')) return { value: 4.8 + ((revision % 3) / 10), valueType: 'NUMBER' as const, unit: null };
  if (key.includes('capacity')) return { value: 520 + revision, valueType: 'NUMBER' as const, unit: 'kW' };
  return { value: 18 + revision, valueType: 'NUMBER' as const, unit: 'kW' };
}

function liveSnapshot(target: RealAssetsRealtimeTarget, siteId: string, revision: number): DeviceObservationSnapshot {
  return {
    schemaVersion: 1,
    deviceId: target.deviceId,
    owningOrganizationId: organizationId,
    siteId,
    businessRevision: revision,
    evaluatedAt: '2026-08-01T08:00:00.000Z',
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-08-01T07:59:59.000Z',
      policyRevision: 14, lastKnown: null,
    },
    telemetryReadiness: 'CURRENT',
    displayState: 'ONLINE',
    values: target.keys.map((key) => {
      const projected = valueForKey(key, revision);
      return {
        key, state: 'PRESENT' as const, ...projected,
        sampledAt: '2026-08-01T07:59:58.000Z', receivedAt: '2026-08-01T07:59:59.000Z',
        freshness: 'FRESH' as const, quality: 'GOOD' as const, qualityReasons: [], policyRevision: 14,
      };
    }),
  };
}

class CertificationLiveSession implements RealAssetsTelemetryLiveSession {
  private readonly owner: CertificationLiveClient;
  private readonly targets: readonly RealAssetsRealtimeTarget[];
  private readonly states = new Map<string, RealAssetsRealtimeState>();
  private readonly listeners = new Set<(states: ReadonlyArray<RealAssetsRealtimeState>) => void>();
  private closedFlag = false;
  private revision = 30;

  constructor(owner: CertificationLiveClient, targets: readonly RealAssetsRealtimeTarget[], siteId: string) {
    this.owner = owner;
    this.targets = targets;
    for (const target of targets) {
      this.states.set(target.clientSubscriptionId, {
        ...target,
        status: 'live',
        snapshot: liveSnapshot(target, siteId, this.revision),
        recovered: false,
        updatedAt: new Date().toISOString(),
      });
    }
  }

  getState(clientSubscriptionId: string) { return this.states.get(clientSubscriptionId); }
  getStates() { return [...this.states.values()]; }
  subscribe(listener: (states: ReadonlyArray<RealAssetsRealtimeState>) => void) {
    this.listeners.add(listener);
    listener(this.getStates());
    return () => this.listeners.delete(listener);
  }
  async refresh() { this.transition('gap'); }
  async checkpoint() { return undefined; }
  close() {
    if (this.closedFlag) return;
    this.closedFlag = true;
    this.listeners.clear();
    this.owner.closed(this);
  }

  transition(kind: string) {
    if (this.closedFlag) return;
    this.revision += kind === 'gap' ? 2 : 1;
    for (const target of this.targets) {
      const current = this.states.get(target.clientSubscriptionId);
      const snapshot = current?.snapshot ?? liveSnapshot(target, this.owner.currentSiteId(), this.revision);
      let next: RealAssetsRealtimeState;
      if (kind === 'reconnecting') {
        next = { ...target, status: 'snapshot', snapshot, reason: 'reconnecting', updatedAt: new Date().toISOString() };
      } else if (kind === 'degraded') {
        next = { ...target, status: 'unavailable', snapshot, reason: 'transport-unavailable', retryable: true, updatedAt: new Date().toISOString() };
      } else if (kind === 'revoked') {
        next = { ...target, status: 'revoked', snapshot: null, updatedAt: new Date().toISOString() };
      } else if (kind === 'gap') {
        next = { ...target, status: 'snapshot', snapshot: liveSnapshot(target, this.owner.currentSiteId(), this.revision), reason: 'authoritative-snapshot', updatedAt: new Date().toISOString() };
      } else {
        next = {
          ...target, status: 'live', snapshot: liveSnapshot(target, this.owner.currentSiteId(), this.revision),
          recovered: kind === 'recovered', updatedAt: new Date().toISOString(),
        };
      }
      this.states.set(target.clientSubscriptionId, next);
    }
    const states = this.getStates();
    for (const listener of this.listeners) listener(states);
  }
}

class CertificationLiveClient implements RealAssetsTelemetryLiveClient {
  private readonly sessions = new Set<CertificationLiveSession>();
  private siteId = siteA.id;
  readonly openedTargets: RealAssetsRealtimeTarget[][] = [];
  openCount = 0;
  closeCount = 0;
  purgeCount = 0;
  maximumActive = 0;

  currentSiteId() { return this.siteId; }
  setSiteId(siteId: string) { this.siteId = siteId; }
  activeSubscriptionCount() { return [...this.sessions].reduce((count, session) => count + session.getStates().length, 0); }
  async open(targets: ReadonlyArray<RealAssetsRealtimeTarget>, options?: { signal?: AbortSignal }) {
    if (options?.signal?.aborted) throw options.signal.reason;
    this.openCount += 1;
    this.openedTargets.push(targets.map((target) => ({ ...target, keys: [...target.keys] })));
    const session = new CertificationLiveSession(this, targets, this.siteId);
    this.sessions.add(session);
    this.maximumActive = Math.max(this.maximumActive, this.activeSubscriptionCount());
    options?.signal?.addEventListener('abort', () => session.close(), { once: true });
    return session;
  }
  closed(session: CertificationLiveSession) {
    if (this.sessions.delete(session)) this.closeCount += 1;
  }
  purge() {
    this.purgeCount += 1;
    for (const session of [...this.sessions]) session.close();
  }
  emit(kind: string) { for (const session of this.sessions) session.transition(kind); }
  snapshot() {
    return {
      openCount: this.openCount,
      closeCount: this.closeCount,
      purgeCount: this.purgeCount,
      activeSubscriptions: this.activeSubscriptionCount(),
      maximumActive: this.maximumActive,
      openedTargets: this.openedTargets,
    };
  }
}

const liveClient = new CertificationLiveClient();
const telemetryRuntime = createRealAssetsTelemetryRuntime('', globalThis.fetch.bind(globalThis), { live: liveClient });
const clipboardValues: string[] = [];
Object.defineProperty(navigator, 'clipboard', {
  configurable: true,
  value: { writeText: async (value: string) => { clipboardValues.push(value); } },
});

interface AppState {
  site: Site;
  sessionId: string;
  policyRevision: string;
}

function selectedDevice(site: Site): string | undefined {
  const parsed = parseRealAssetsDetailPath(location.pathname, site.id);
  return parsed.state === 'detail' ? parsed.deviceId : undefined;
}

function App() {
  const [state, setState] = useState<AppState>(() => {
    const initialSite = location.pathname.includes(siteB.id) ? siteB : siteA;
    protectedScope.activate(initialSite.id);
    liveClient.setSiteId(initialSite.id);
    return { site: initialSite, sessionId: 'certification-session-1', policyRevision: 'certification-policy-1' };
  });
  const currentPrincipal = useMemo(() => principal(state.sessionId, state.policyRevision), [state.policyRevision, state.sessionId]);
  const generation = protectedScope.current().generation;

  useEffect(() => {
    const transition = async (reason: ProtectedScopePurgeReason, next: Partial<AppState>) => {
      await protectedScope.purge(reason);
      const nextSite = next.site ?? state.site;
      protectedScope.activate(nextSite.id);
      liveClient.setSiteId(nextSite.id);
      history.replaceState(null, '', `/sites/${nextSite.id}/assets`);
      setState((current) => ({ ...current, ...next, site: nextSite }));
      await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      return protectedScope.current();
    };
    Reflect.set(globalThis, '__REAL_ASSETS_CERTIFICATION__', {
      state: () => ({
        siteId: state.site.id,
        sessionId: state.sessionId,
        policyRevision: state.policyRevision,
        protectedScope: protectedScope.current(),
        cacheKeys: queryClient.getQueryCache().getAll().map((query) => query.queryKey),
        clipboardValues: [...clipboardValues],
        realtime: liveClient.snapshot(),
      }),
      realtime: (kind: string) => liveClient.emit(kind),
      switchSite: () => transition('SITE_CHANGE', { site: state.site.id === siteA.id ? siteB : siteA }),
      switchSession: () => transition('SESSION_LOSS', { sessionId: `${state.sessionId}-next` }),
      switchPolicy: () => transition('POLICY_CHANGE', { policyRevision: `${state.policyRevision}-next` }),
      purge: (reason: ProtectedScopePurgeReason = 'DISPOSE') => protectedScope.purge(reason),
      sites: { siteA, siteB },
    });
  }, [state]);

  return (
    <main className="real-route-surface" data-testid="real-assets-certification-root">
      <RealAssetsWorkspace
        key={`${state.site.id}:${state.sessionId}:${state.policyRevision}:${generation}`}
        site={state.site}
        principal={currentPrincipal}
        requestedDeviceId={selectedDevice(state.site)}
        protectedGeneration={generation}
        protectedRequestToken={() => protectedScope.requestToken()}
        registerProtectedResource={(resource) => protectedScope.registerResource(resource)}
        telemetryRuntime={telemetryRuntime}
      />
    </main>
  );
}

declare global {
  var __REAL_ASSETS_CERTIFICATION__: Record<string, unknown> | undefined;
}

createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}><App /></QueryClientProvider>,
);
