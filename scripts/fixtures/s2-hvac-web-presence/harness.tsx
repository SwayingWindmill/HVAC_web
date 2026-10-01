import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, PlatformGatewayClient, Site, SiteAssetModel } from '@/api/generated/platformGateway.gen';
import type { DeviceObservationSnapshot, S2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { AssetsWorkspace } from '@/features/assets/AssetsWorkspace';
import { parseAssetsDetailPath } from '@/features/assets/detail';
import { createProtectedScopeCoordinator } from '@/app/protected-scope';
import { createAssetsTelemetryRuntime } from '@/features/assets/telemetry-runtime';
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

const tenantId = '018f6a00-1000-7000-8000-000000000001';
const siteId = '018f6a00-2000-7000-8000-000000000001';
const deviceIds = [
  '018f6a00-3000-7000-8000-000000000001',
  '018f6a00-3000-7000-8000-000000000002',
] as const;
const instant = '2026-07-31T04:00:00.000Z';
const site: Site = {
  id: siteId,
  tenantId: tenantId,
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
  tenantId,
  siteId,
  code: `CH-RT-${index + 1}`,
  displayName: `Realtime Chiller ${index + 1}`,
  deviceType: 'CHILLER',
  status: 'ACTIVE' as const,
  revision: 5,
  createdAt: instant,
  updatedAt: instant,
}));

const pointCodes = [
  'chiller.run_state',
  'chiller.power',
  'chiller.cop',
  'chiller.cooling_capacity',
] as const;

const telemetryPoints = devices.flatMap((device, deviceIndex) => pointCodes.map((pointCode, pointIndex) => ({
  id: `018f6a00-400${deviceIndex}-7000-8000-00000000000${pointIndex + 1}`,
  tenantId,
  siteId,
  reportingDeviceId: device.id,
  sensorId: null,
  pointCode,
  sourceKey: pointCode,
  displayName: pointCode,
  pointType: pointCode.endsWith('run_state') ? 'STATE' as const : 'TELEMETRY' as const,
  valueType: pointCode.endsWith('run_state') ? 'STRING' as const : 'NUMBER' as const,
  unit: pointCode.endsWith('power') || pointCode.endsWith('cooling_capacity') ? 'kW' : null,
  writable: false,
  sampleIntervalMs: 1000,
  publishIntervalMs: 1000,
  staleAfterMs: 5000,
  counterDecreaseMode: null,
  counterRolloverModulus: null,
  sourceMetadata: {},
  status: 'ACTIVE' as const,
  revision: 1,
  createdAt: instant,
  updatedAt: instant,
})));

const assetModel: SiteAssetModel = {
  schemaVersion: 2,
  tenantId,
  siteId,
  spaces: [],
  assets: [],
  devices,
  sensors: [],
  telemetryPoints,
  relationships: [],
  counts: {
    spaces: 0,
    assets: 0,
    deviceEndpoints: devices.length,
    physicalSensors: 0,
    points: telemetryPoints.length,
  },
};

function currentSnapshot(deviceId: string, keys: readonly string[]): DeviceObservationSnapshot {
  const valuesByKey = new Map([
    ['chiller.run_state', { value: 'RUNNING', valueType: 'STRING' as const, unit: null }],
    ['chiller.power', { value: 0, valueType: 'NUMBER' as const, unit: 'kW' }],
    ['chiller.cop', { value: 4.9, valueType: 'NUMBER' as const, unit: null }],
    ['chiller.cooling_capacity', { value: 1080, valueType: 'NUMBER' as const, unit: 'kW' }],
  ]);
  const values = keys.map((key, index) => {
    const value = valuesByKey.get(key);
    if (!value) throw new Error(`Unexpected current-state key ${key}`);
    return {
      key,
      state: 'PRESENT' as const,
      ...value,
      sampledAt: `2026-07-31T04:0${index}:00.000Z`,
      receivedAt: `2026-07-31T04:0${index}:01.000Z`,
      freshness: key === 'chiller.cooling_capacity' ? 'STALE' as const : 'FRESH' as const,
      quality: key === 'chiller.cop' ? 'PARTIAL' as const : 'GOOD' as const,
      qualityReasons: key === 'chiller.cop' ? ['SOURCE_LAG_EXCEEDED' as const] : [],
      policyRevision: 12,
    };
  });
  return {
    schemaVersion: 1,
    deviceId,
    tenantId: tenantId,
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
    requestId: 'assets-realtime-fixture',
    traceparent: null,
    auditMessageId: null,
    routePolicyRevision: 'realtime-route:1',
    location: null,
  });
}

const controlledLive = new ControlledTelemetryLiveClient();
const platformClient = {
  getSiteAssetModel: () => platformResponse(assetModel),
} as Pick<PlatformGatewayClient, 'getSiteAssetModel'>;
const telemetryClient = {
  batchGetDeviceObservationSnapshots: async (request: { requests: Array<{ requestId: string; deviceId: string; keys: string[] }> }) => ({
    schemaVersion: 1,
    items: request.requests.map((target) => ({
      requestId: target.requestId,
      deviceId: target.deviceId,
      status: 'OK' as const,
      snapshot: currentSnapshot(target.deviceId, target.keys),
    })),
  }),
} as unknown as S2TelemetryClient;
const telemetryRuntime = createAssetsTelemetryRuntime('', globalThis.fetch.bind(globalThis), {
  client: telemetryClient,
  live: controlledLive,
});
const protectedScope = createProtectedScopeCoordinator();
protectedScope.activate(siteId);
const principal = {
  principal: {
    subject: 'assets-realtime-browser',
    issuer: 'https://identity.hvac.local',
    displayName: 'Realtime Browser Operator',
    email: 'realtime-browser@example.invalid',
    roles: ['OPERATOR'],
  },
  context: {
    initiatingPrincipal: {
      subject: 'assets-realtime-browser',
      issuer: 'https://identity.hvac.local',
      displayName: 'Realtime Browser Operator',
      email: 'realtime-browser@example.invalid',
      roles: ['OPERATOR'],
    },
    executingServicePrincipal: { service: 'platform-gateway', spiffeId: 'spiffe://hvac.local/platform-gateway' },
    tenantId,
    audience: 'iam-service',
    policyRevision: 'gateway-delegation:1',
    delegationExpiresAt: '2026-07-31T06:00:00.000Z',
  },
  authorization: {
    capabilitySetVersion: 12,
    policyRevision: 'assets-realtime:1',
    capabilities: ['site.read', 'asset.list', 'device.list', 'device.read', 'telemetry.batch.read', 'telemetry.subscribe'],
  },
  session: {
    id: 'assets-realtime-session',
    expiresAt: '2026-07-31T06:00:00.000Z',
    idleTimeoutMs: 30 * 60 * 1000,
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

function AssetsRealtimeHarness() {
  const location = useLocation();
  const parsed = parseAssetsDetailPath(location.pathname, site.id);
  React.useEffect(() => {
    window.__ASSETS_REALTIME_CONTROL__ = control;
    return () => { delete window.__ASSETS_REALTIME_CONTROL__; };
  }, []);
  return (
    <AssetsWorkspace
      site={site}
      principal={principal}
      requestedDetail={parsed.state === 'detail' ? parsed.target : undefined}
      protectedGeneration={protectedScope.current().generation}
      protectedRequestToken={() => protectedScope.requestToken()}
      registerProtectedResource={(resource) => protectedScope.registerResource(resource)}
      publishRealtimeStatus={() => undefined}
      platformClient={platformClient}
      telemetryRuntime={telemetryRuntime}
    />
  );
}

declare global {
  interface Window {
    __ASSETS_REALTIME_CONTROL__?: {
      setMode(mode: BrowserLiveMode): void;
      purgeScope(): Promise<unknown>;
      audit(): unknown;
      protectedScope(): unknown;
    };
  }
}

queryClient.clear();
ReactDOM.createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <BrowserRouter>
      <AssetsRealtimeHarness />
    </BrowserRouter>
  </QueryClientProvider>,
);
