import { useQueries, useQuery } from '@tanstack/react-query';
import { useCallback, useEffect, useState } from 'react';
import type { TelemetryKey } from './generated/s2Telemetry.gen';
import { API_MODE, USE_MOCK } from './config';
import { http, unwrap } from './http';
import {
  MOCK_DEVICES,
  mockGetBatch,
  mockGetLatest,
  mockGetTimeseries,
  onMockPush,
  startMockPush,
} from './mock';
import { createTelemetryCurrentRuntime } from './telemetry-current';
import type { DeviceSnapshot, Range, TelemetryPoint, TimeseriesMap } from './types';
import type { TelemetryLiveSession, TelemetryLiveState } from '@/platform/telemetry-live';

const compatibilityRuntime = createTelemetryCurrentRuntime();

function currentStateRetired(surface: string): Promise<never> {
  return Promise.reject(
    new Error(`${surface} is retired in real mode; use the S2 telemetry-current Snapshot/live client`),
  );
}

const realGetTimeseries = (deviceId: string, keys: string[], range: Range) =>
  unwrap<TimeseriesMap>(
    http.get(`/telemetry/devices/${deviceId}/timeseries`, { params: { keys: keys.join(','), range } }),
  );

function useS2TelemetryLive(deviceIds: string[], keys: string[]) {
  const [states, setStates] = useState<ReadonlyMap<string, TelemetryLiveState>>(new Map());
  const [error, setError] = useState<Error | null>(null);
  const deviceSignature = deviceIds.join('|');
  const keySignature = keys.join('|');

  useEffect(() => {
    if (API_MODE !== 'real' || deviceIds.length === 0 || keys.length === 0) {
      setStates(new Map());
      setError(null);
      return undefined;
    }

    let active = true;
    let session: TelemetryLiveSession | null = null;
    let unsubscribe: (() => void) | null = null;
    const controller = new AbortController();
    const targets = deviceIds.map((deviceId) => ({
      clientSubscriptionId: `compatibility-${deviceId}`,
      deviceId,
      keys: [...keys] as TelemetryKey[],
    }));

    setStates(new Map());
    setError(null);
    compatibilityRuntime.live.open(targets, { signal: controller.signal }).then((opened) => {
      if (!active) {
        opened.close();
        return;
      }
      session = opened;
      const publish = () => {
        const next = new Map<string, TelemetryLiveState>();
        for (const state of opened.getStates()) {
          if (state.status === 'revoked') compatibilityRuntime.live.purge();
          next.set(state.deviceId, state);
        }
        setStates(next);
      };
      publish();
      unsubscribe = opened.subscribe(publish);
    }).catch((cause: unknown) => {
      if (!active || controller.signal.aborted) return;
      setError(cause instanceof Error ? cause : new Error(String(cause)));
    });

    return () => {
      active = false;
      controller.abort();
      unsubscribe?.();
      session?.close();
    };
  }, [deviceSignature, keySignature]);

  const get = useCallback((deviceId: string, key: string): number | undefined => {
    const value = states.get(deviceId)?.snapshot?.values.find((entry) => entry.key === key);
    return value?.state === 'PRESENT' && typeof value.value === 'number' ? value.value : undefined;
  }, [states]);

  const loading = API_MODE === 'real' && deviceIds.length > 0 && !error && (
    states.size < deviceIds.length || [...states.values()].some((state) => state.status === 'initializing')
  );
  return { get, loading, error };
}

function useMockTelemetryPush() {
  const [values, setValues] = useState<ReadonlyMap<string, number>>(new Map());

  useEffect(() => {
    if (!USE_MOCK) return undefined;
    const offPush = onMockPush((deviceId, key, value) => {
      setValues((current) => {
        const next = new Map(current);
        next.set(`${deviceId}:${key}`, value);
        return next;
      });
    });
    const stop = startMockPush();
    return () => {
      offPush();
      stop();
    };
  }, []);

  return values;
}

/**
 * Legacy-shaped hook retained for mock-only pages. Real mode fails closed and
 * never calls Legacy latest, batch, ThingsBoard or Socket.IO current-state paths.
 */
export function useLatest(deviceId: string, keys: string[], enabled = true) {
  return useQuery({
    queryKey: ['telemetry-retired', 'latest', deviceId, keys.join(',')],
    queryFn: () => (USE_MOCK ? mockGetLatest(deviceId, keys) : currentStateRetired('Legacy latest telemetry')),
    staleTime: 30_000,
    enabled,
    retry: false,
  });
}

/** Historical time-series remains the explicit compatibility boundary. */
export function useTimeseries(deviceId: string, keys: string[], range: Range = '24h', enabled = true) {
  return useQuery({
    queryKey: ['telemetry', 'ts', deviceId, keys.join(','), range],
    queryFn: () => (USE_MOCK ? mockGetTimeseries(deviceId, keys, range) : realGetTimeseries(deviceId, keys, range)),
    staleTime: 60_000,
    enabled,
  });
}

/** Mock-only batch compatibility hook; real current state uses telemetry-current. */
export function useBatch(deviceIds: string[], keys: string[], enabled = true) {
  return useQuery({
    queryKey: ['telemetry-retired', 'batch', deviceIds.join(','), keys.join(',')],
    queryFn: () => (USE_MOCK ? mockGetBatch(deviceIds, keys) : currentStateRetired('Legacy batch telemetry')),
    staleTime: 30_000,
    enabled,
    retry: false,
  });
}

export function useBuildingTimeseries(range: Range = 'day', deviceIds: string[] = MOCK_DEVICES, key = 'power') {
  const queries = useQueries({
    queries: deviceIds.map((id) => ({
      queryKey: ['telemetry', 'ts', id, key, range],
      queryFn: () => (USE_MOCK ? mockGetTimeseries(id, [key], range) : realGetTimeseries(id, [key], range)),
      staleTime: 60_000,
    })),
  });
  const isLoading = queries.some((query) => query.isLoading);
  const isError = queries.some((query) => query.isError);
  const refetch = () => Promise.all(queries.map((query) => query.refetch()));
  const data: TelemetryPoint[] = [];
  if (!isLoading) {
    const count = Math.max(0, ...queries.map((query) => query.data?.[key]?.length ?? 0));
    for (let index = 0; index < count; index += 1) {
      let sum = 0;
      let timestamp = 0;
      let found = false;
      queries.forEach((query) => {
        const point = query.data?.[key]?.[index];
        if (point) {
          sum += point.value;
          timestamp = point.ts;
          found = true;
        }
      });
      if (found) data.push({ ts: timestamp, value: Math.round(sum) });
    }
  }
  return { data, isLoading, isError, refetch };
}

/**
 * Compatibility facade for pages not yet migrated to device-detail state. Real
 * mode opens one S2 Snapshot/live session for the requested devices; Mock mode
 * keeps the existing simulator behavior without retaining Socket.IO code.
 */
export function useTelemetryLive(deviceIds: string[], keys: string[]) {
  const s2 = useS2TelemetryLive(deviceIds, keys);
  const mockPush = useMockTelemetryPush();
  const { data: batch, isLoading } = useBatch(deviceIds, keys, USE_MOCK);

  if (!USE_MOCK) return s2;
  const get = (deviceId: string, key: string): number | undefined =>
    mockPush.get(`${deviceId}:${key}`)
    ?? (batch as DeviceSnapshot[] | undefined)?.find((device) => device.deviceId === deviceId)?.latest[key]?.value;
  return { get, loading: isLoading || !batch, error: null };
}
