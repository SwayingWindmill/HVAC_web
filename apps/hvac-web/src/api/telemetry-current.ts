import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { API_MODE } from './config';
import {
  createPlatformGatewayClient,
  type Device,
  type PlatformGatewayClient,
} from './generated/platformGateway.gen';
import {
  createS2TelemetryClient,
  S2TelemetryClientError,
  type BatchObservationFailure,
  type DeviceObservationSnapshot,
  type ProblemDetails,
  type S2TelemetryClient,
  type TelemetryKey,
} from './generated/s2Telemetry.gen';
import {
  createTelemetryLiveClient,
  type TelemetryLiveClient,
  type TelemetryLiveSession,
  type TelemetryLiveState,
} from '@/platform/telemetry-live';

export const DEVICE_DETAIL_TELEMETRY_KEYS = [
  'temperature',
  'humidity',
  'setpoint',
  'power',
] as const satisfies ReadonlyArray<TelemetryKey>;

const PRESENCE_QUERY_ROOT = ['s2-current', 'presence'] as const;
const MAX_BATCH_DEVICES = 100;

export interface PresenceBatchSuccess {
  status: 'ok';
  deviceId: string;
  snapshot: DeviceObservationSnapshot;
}

export interface PresenceBatchError {
  status: 'error';
  deviceId: string;
  problem: ProblemDetails;
}

export type PresenceBatchItem = PresenceBatchSuccess | PresenceBatchError;

export interface PresenceBatchResult {
  items: PresenceBatchItem[];
  byDeviceId: ReadonlyMap<string, PresenceBatchItem>;
  partial: boolean;
}

export interface DeviceLiveResult {
  state: TelemetryLiveState | null;
  pending: boolean;
  error: Error | null;
}

export interface TelemetryRoutePolicyTracker {
  currentRevision(): string | null;
  observe(revision: string | null): void;
  subscribe(listener: (previousRevision: string, nextRevision: string) => void): () => void;
}

class DefaultTelemetryRoutePolicyTracker implements TelemetryRoutePolicyTracker {
  private revision: string | null = null;
  private readonly listeners = new Set<(previousRevision: string, nextRevision: string) => void>();

  currentRevision(): string | null {
    return this.revision;
  }

  observe(revision: string | null): void {
    if (!revision || revision === this.revision) return;
    const previousRevision = this.revision;
    this.revision = revision;
    if (!previousRevision) return;
    for (const listener of this.listeners) listener(previousRevision, revision);
  }

  subscribe(listener: (previousRevision: string, nextRevision: string) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}

export interface TelemetryCurrentRuntime {
  telemetry: S2TelemetryClient;
  platform: Pick<PlatformGatewayClient, 'getCurrentPrincipal'>;
  live: TelemetryLiveClient;
  routePolicy: TelemetryRoutePolicyTracker;
}

function createRouteAwareFetch(routePolicy: TelemetryRoutePolicyTracker): typeof fetch {
  const fetchImplementation = globalThis.fetch.bind(globalThis);
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    const response = await fetchImplementation(input, init);
    const revision = response.headers.get('x-route-policy-revision');
    if (revision) globalThis.setTimeout(() => routePolicy.observe(revision), 0);
    return response;
  }) as typeof fetch;
}

export function createTelemetryCurrentRuntime(input: Partial<TelemetryCurrentRuntime> = {}): TelemetryCurrentRuntime {
  const routePolicy = input.routePolicy ?? new DefaultTelemetryRoutePolicyTracker();
  const platform = input.platform ?? createPlatformGatewayClient();
  const telemetry = input.telemetry ?? createS2TelemetryClient('', createRouteAwareFetch(routePolicy));
  return {
    telemetry,
    platform,
    live: input.live ?? createTelemetryLiveClient({ telemetry, platform }),
    routePolicy,
  };
}

const defaultRuntime = createTelemetryCurrentRuntime();

function requestId(deviceId: string, index: number): string {
  return `visible-${index}-${deviceId}`;
}

function validatePresenceSnapshot(
  snapshot: DeviceObservationSnapshot,
  device: Device,
): DeviceObservationSnapshot {
  if (snapshot.schemaVersion !== 1
    || snapshot.deviceId !== device.id
    || snapshot.owningOrganizationId !== device.owningOrganizationId
    || snapshot.siteId !== device.siteId
    || snapshot.values.length !== 0) {
    throw new Error(`Presence-only Snapshot scope drifted for Device ${device.id}`);
  }
  return snapshot;
}

async function csrf(
  runtime: TelemetryCurrentRuntime,
  organizationId: string,
  signal?: AbortSignal,
): Promise<string> {
  const principal = await runtime.platform.getCurrentPrincipal({ signal });
  if (principal.data.context.actingOrganizationId !== organizationId) {
    throw new Error('Authenticated Organization changed during telemetry request');
  }
  const value = principal.data.session.csrfToken;
  if (!value) throw new Error('Authenticated Session omitted CSRF capability');
  return value;
}

export async function readVisibleDevicePresence(
  devices: ReadonlyArray<Device>,
  organizationId: string,
  siteId: string,
  runtime: TelemetryCurrentRuntime = defaultRuntime,
  signal?: AbortSignal,
): Promise<PresenceBatchResult> {
  if (devices.length > MAX_BATCH_DEVICES) throw new Error('Visible Device batch exceeds 100');
  if (devices.some((device) => device.owningOrganizationId !== organizationId || device.siteId !== siteId)) {
    throw new Error('Visible Device batch crosses Organization or Site scope');
  }
  if (new Set(devices.map((device) => device.id)).size !== devices.length) {
    throw new Error('Visible Device batch contains duplicate Device IDs');
  }
  if (devices.length === 0) return { items: [], byDeviceId: new Map(), partial: false };

  const csrfToken = await csrf(runtime, organizationId, signal);
  const expected = devices.map((device, index) => ({
    requestId: requestId(device.id, index),
    device,
  }));
  const response = await runtime.telemetry.batchGetDeviceObservationSnapshots({
    requests: expected.map((item) => ({ requestId: item.requestId, deviceId: item.device.id, keys: [] })),
  }, { csrfToken, signal });
  if (response.schemaVersion !== 1 || response.items.length !== expected.length) {
    throw new Error('Presence batch returned a partial or unsupported envelope');
  }

  const items = response.items.map((item, index): PresenceBatchItem => {
    const target = expected[index];
    if (item.requestId !== target.requestId || item.deviceId !== target.device.id) {
      throw new Error('Presence batch order or Device scope drifted');
    }
    if (item.status === 'OK') {
      return { status: 'ok', deviceId: item.deviceId, snapshot: validatePresenceSnapshot(item.snapshot, target.device) };
    }
    return { status: 'error', deviceId: item.deviceId, problem: (item as BatchObservationFailure).problem };
  });
  return {
    items,
    byDeviceId: new Map(items.map((item) => [item.deviceId, item])),
    partial: items.some((item) => item.status === 'error'),
  };
}

export function useVisibleDevicePresence(
  devices: ReadonlyArray<Device>,
  organizationId: string | null,
  siteId: string | null,
  runtime: TelemetryCurrentRuntime = defaultRuntime,
) {
  const queryClient = useQueryClient();
  useEffect(
    () => runtime.routePolicy.subscribe(() => purgeTelemetryCurrentState(queryClient, runtime)),
    [queryClient, runtime],
  );
  const stableDevices = useMemo(
    () => [...devices],
    [devices],
  );
  const deviceIds = stableDevices.map((device) => device.id).join('|');
  return useQuery({
    queryKey: [...PRESENCE_QUERY_ROOT, organizationId, siteId, deviceIds],
    queryFn: ({ signal }) => readVisibleDevicePresence(stableDevices, organizationId!, siteId!, runtime, signal),
    enabled: API_MODE === 'real' && Boolean(organizationId && siteId && stableDevices.length > 0),
    staleTime: 10_000,
    refetchInterval: 30_000,
    retry: (failureCount, error) => failureCount < 1 && !(error instanceof S2TelemetryClientError && !error.problem.retryable),
  });
}

function liveSubscriptionId(deviceId: string): string {
  return `assets-device-${deviceId}`;
}

export function useDeviceTelemetryLive(
  device: Device | null,
  runtime: TelemetryCurrentRuntime = defaultRuntime,
  keys: ReadonlyArray<TelemetryKey> = DEVICE_DETAIL_TELEMETRY_KEYS,
): DeviceLiveResult {
  const queryClient = useQueryClient();
  const [result, setResult] = useState<DeviceLiveResult>({ state: null, pending: Boolean(device), error: null });
  const keySignature = keys.join('|');

  useEffect(() => {
    if (API_MODE !== 'real' || !device) {
      setResult({ state: null, pending: false, error: null });
      return undefined;
    }
    let active = true;
    let session: TelemetryLiveSession | null = null;
    let unsubscribe: (() => void) | null = null;
    const controller = new AbortController();
    setResult({ state: null, pending: true, error: null });
    runtime.live.open([{
      clientSubscriptionId: liveSubscriptionId(device.id),
      deviceId: device.id,
      keys: [...keys],
    }], { signal: controller.signal }).then((opened) => {
      if (!active) {
        opened.close();
        return;
      }
      session = opened;
      const publish = () => {
        const state = opened.getState(liveSubscriptionId(device.id)) ?? null;
        if (state?.status === 'revoked') purgeTelemetryCurrentState(queryClient, runtime);
        setResult({ state, pending: state?.status === 'initializing', error: null });
      };
      publish();
      unsubscribe = opened.subscribe(publish);
    }).catch((error: unknown) => {
      if (!active || controller.signal.aborted) return;
      if (error instanceof S2TelemetryClientError && error.problem.code === 'RESOURCE_NOT_FOUND') runtime.live.purge();
      setResult({ state: null, pending: false, error: error instanceof Error ? error : new Error(String(error)) });
    });
    return () => {
      active = false;
      controller.abort();
      unsubscribe?.();
      session?.close();
    };
  }, [device?.id, device?.owningOrganizationId, device?.siteId, keySignature, queryClient, runtime]);

  return result;
}

export function purgeTelemetryCurrentState(queryClient?: QueryClient, runtime: TelemetryCurrentRuntime = defaultRuntime): void {
  runtime.live.purge();
  queryClient?.removeQueries({ queryKey: ['s2-current'] });
}

export function presentTelemetryError(error: unknown): { title: string; description: string; retryable: boolean } {
  if (error instanceof S2TelemetryClientError) {
    return {
      title: error.problem.code === 'RESOURCE_NOT_FOUND' ? 'Device 不可见或已撤权' : '设备状态暂不可用',
      description: error.problem.detail,
      retryable: error.problem.retryable,
    };
  }
  return {
    title: '设备状态连接失败',
    description: '真实模式不会回退到 Legacy、ThingsBoard、Socket.IO 或 Mock 状态。',
    retryable: true,
  };
}
