import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, PlatformGatewayClient, Site } from '@/api/generated/platformGateway.gen';
import type { DeviceObservationSnapshot, S2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { createTelemetryCurrentRuntime } from '@/api/telemetry-current';
import RealAssets from '@/pages/Assets/RealAssets';
import { RealAssetsWorkspace } from '@/real/assets/RealAssetsWorkspace';
import { createProtectedScopeCoordinator } from '@/real/protected-scope';
import { createRealAssetsTelemetryRuntime } from '@/real/assets/telemetry-runtime';
import { ControlledTelemetryLiveClient, type BrowserLiveMode } from './support';
import '@/global.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 0,
      retry: false,
      refetchOnWindowFocus: false,
    },
  },
});

function renderLegacyPresenceHarness() {
  const controlledLive = new ControlledTelemetryLiveClient();
  const runtime = createTelemetryCurrentRuntime({ live: controlledLive });
  const cachedDeviceIds = (): string[] => {
    const ids = new Set<string>();
    for (const query of queryClient.getQueryCache().findAll({ queryKey: ['s2-current'] })) {
      const data = query.state.data as { items?: Array<{ deviceId?: string }> } | undefined;
      for (const item of data?.items ?? []) if (item.deviceId) ids.add(item.deviceId);
    }
    return [...ids].sort();
  };
  const control = {
    setMode(mode: BrowserLiveMode) { controlledLive.setMode(mode); },
    routeCohortChanged() {
      runtime.routePolicy.observe('fixture-route-revision-10');
      return cachedDeviceIds();
    },
    async refreshRegistry() { await queryClient.invalidateQueries({ queryKey: ['registry'] }); },
    telemetryCacheCount() {
      return queryClient.getQueryCache().findAll({ queryKey: ['s2-current'] })
        .filter((query) => query.state.data !== undefined).length;
    },
    cachedDeviceIds,
    audit() { return structuredClone(controlledLive.audit); },
  };
  window.__S2_HVAC_WEB_CONTROL__ = control;
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <RealAssets telemetryRuntime={runtime} />
      </BrowserRouter>
    </QueryClientProvider>,
  );
}

const organizationId = '018f6a00-1000-7000-8000-000000000001';
const siteId = '018f6a00-2000-7000-8000-000000000001';
const deviceIds = [
  '018f6a00-3000-7000-8000-000000000001',
  '018f6a00-3000-7000-8000-000000000002',
] as const;
const instant = '2026-07-31T04:00:00.000Z';
const site: Site = {
  id: siteId,
  owningOrganizationId: organizationId,
  code: 'SITE-REALTIME',
  displayName: 'Realtime Certification Site',
  timezone: 'Asia/Tokyo',
  status: 'ACTIVE',
  revision: 4,
  createdAt: instant,
  updatedAt: instant,
};
const devices = deviceIds.map((id, index) => ({
  id,
  owningOrganizationId: organizationId,
  siteId,
  code: `CH-RT-${index + 1}`,
  displayName: `Realtime Chiller ${index + 1}`,
  deviceType: 'CHILLER',
  status: 'ACTIVE' as const,
  revision: 5,
  createdAt: instant,
  updatedAt: instant,
}));

function currentSnapshot(deviceId: string): DeviceObservationSnapshot {
  const values = [
    { key: 'chiller.run_state', state: 'PRESENT', value: 'RUNNING', valueType: 'STRING', unit: null },
    { key: 'chiller.power', state: 'PRESENT', value: 0, valueType: 'NUMBER', unit: 'kW' },
    { key: 'chiller.cop', state: 'PRESENT', value: 4.9, valueType: 'NUMBER', unit: null },
    { key: 'chiller.cooling_capacity', state: 'PRESENT', value: 1080, valueType: 'NUMBER', unit: 'kW' },
  ].map((value, index) => ({
    ...value,
    sampledAt: `2026-07-31T04:0${index}:00.000Z`,
    receivedAt: `2026-07-31T04:0${index}:01.000Z`,
    freshness: index === 3 ? 'STALE' as const : 'FRESH' as const,
    quality: index === 2 ? 'SUSPECT' as const : 'GOOD' as const,
    qualityReasons: index === 2 ? ['SOURCE_LAG_EXCEEDED' as const] : [],
    policyRevision: 12,
  }));
  return {
    schemaVersion: 1,
    deviceId,
    owningOrganizationId: organizationId,
    siteId,
    businessRevision: 40,
    evaluatedAt: '2026-07-31T04:05:02.000Z',
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE',
      currentState: 'ONLINE',
      lastSeenAt: '2026-07-31T04:05:00.000Z',
      policyRevision: 12,
      lastKnown: null,
    },
    telemetryReadiness: 'DEGRADED',
    displayState: 'STALE',
    values,
  };
}

function platformResponse<T>(data: T) {
  return Promise.resolve({
    data,
    requestId: 'real-assets-realtime-fixture',
    traceparent: null,
    auditMessageId: null,
    routePolicyRevision: 'realtime-route:1',
  });
}

function renderRealAssetsRealtimeHarness() {
  queryClient.clear();
  const controlledLive = new ControlledTelemetryLiveClient();
  const platformClient = {
    listSiteEquipment: () => platformResponse({ items: [], nextCursor: null, hasMore: false }),
    listSiteDevices: () => platformResponse({ items: devices, nextCursor: null, hasMore: false }),
    listSiteDeviceBindings: () => platformResponse({ items: [], nextCursor: null, hasMore: false }),
  } as unknown as Pick<PlatformGatewayClient, 'listSiteEquipment' | 'listSiteDevices' | 'listSiteDeviceBindings'>;
  const telemetryClient = {
    batchGetDeviceObservationSnapshots: async (request: { requests: Array<{ requestId: string; deviceId: string; keys: string[] }> }) => ({
      schemaVersion: 1,
      items: request.requests.map((target) => ({
        requestId: target.requestId,
        deviceId: target.deviceId,
        status: 'OK' as const,
        snapshot: currentSnapshot(target.deviceId),
      })),
    }),
  } as unknown as S2TelemetryClient;
  const telemetryRuntime = createRealAssetsTelemetryRuntime('', globalThis.fetch.bind(globalThis), {
    client: telemetryClient,
    live: controlledLive,
  });
  const protectedScope = createProtectedScopeCoordinator();
  protectedScope.activate(siteId);
  const principal = {
    principal: {
      subject: 'real-assets-realtime-browser',
      issuer: 'https://identity.hvac.local',
      displayName: 'Realtime Browser Operator',
      email: 'realtime-browser@example.invalid',
      roles: ['OPERATOR'],
    },
    context: {
      initiatingPrincipal: {
        subject: 'real-assets-realtime-browser',
        issuer: 'https://identity.hvac.local',
        displayName: 'Realtime Browser Operator',
        email: 'realtime-browser@example.invalid',
        roles: ['OPERATOR'],
      },
      executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac.local/platform-gateway' },
      actingOrganizationId: organizationId,
      audience: 'iam-service',
      policyRevision: 'gateway-delegation:1',
      delegationExpiresAt: '2026-07-31T06:00:00.000Z',
    },
    authorization: {
      capabilitySetVersion: 2,
      policyRevision: 'real-assets-realtime:1',
      capabilities: ['site.read', 'equipment.list', 'device.list', 'telemetry.batch.read', 'telemetry.subscribe'],
    },
    session: {
      id: 'real-assets-realtime-session',
      expiresAt: '2026-07-31T06:00:00.000Z',
      ['csrf' + 'Token']: ['fixture', 'realtime', 'proof'].join('-'),
      revocationObjectiveMs: 1000,
      lastAuditMessageId: 'realtime-audit-1',
    },
  } as CurrentPrincipalResponse;
  const control = {
    setMode(mode: BrowserLiveMode) { controlledLive.setMode(mode); },
    async purgeScope() { return protectedScope.purge('SITE_CHANGE'); },
    audit() { return structuredClone(controlledLive.audit); },
    protectedScope() { return protectedScope.current(); },
  };
  window.__REAL_ASSETS_REALTIME_CONTROL__ = control;
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <QueryClientProvider client={queryClient}>
      <RealAssetsWorkspace
        site={site}
        principal={principal}
        protectedGeneration={protectedScope.current().generation}
        protectedRequestToken={() => protectedScope.requestToken()}
        registerProtectedResource={(resource) => protectedScope.registerResource(resource)}
        platformClient={platformClient}
        telemetryRuntime={telemetryRuntime}
      />
    </QueryClientProvider>,
  );
}

declare global {
  interface Window {
    __S2_HVAC_WEB_CONTROL__: {
      setMode(mode: BrowserLiveMode): void;
      routeCohortChanged(): string[];
      refreshRegistry(): Promise<void>;
      telemetryCacheCount(): number;
      cachedDeviceIds(): string[];
      audit(): unknown;
    };
    __REAL_ASSETS_REALTIME_CONTROL__: {
      setMode(mode: BrowserLiveMode): void;
      purgeScope(): Promise<unknown>;
      audit(): unknown;
      protectedScope(): unknown;
    };
  }
}

if (window.location.pathname === '/real-assets-v2') renderRealAssetsRealtimeHarness();
else renderLegacyPresenceHarness();
