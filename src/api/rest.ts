import { useQuery, useQueries } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { USE_MOCK } from './config';
import { http, unwrap } from './http';
import { mockGetLatest, mockGetTimeseries, mockGetBatch, MOCK_DEVICES } from './mock';
import { telemetry } from './telemetry';
import type { LatestMap, TimeseriesMap, DeviceSnapshot, Range, TelemetryPoint } from './types';

const realGetLatest = (deviceId: string, keys: string[]) =>
  unwrap<LatestMap>(http.get(`/telemetry/devices/${deviceId}/latest`, { params: { keys: keys.join(',') } }));

const realGetTimeseries = (deviceId: string, keys: string[], range: Range) =>
  unwrap<TimeseriesMap>(
    http.get(`/telemetry/devices/${deviceId}/timeseries`, { params: { keys: keys.join(','), range } }),
  );

const realGetBatch = (deviceIds: string[], keys: string[]) =>
  unwrap<DeviceSnapshot[]>(http.post('/telemetry/latest/batch', { deviceIds, keys }));

/** Latest snapshot of one device's keys. Mount-time initial value for the cockpit. */
export function useLatest(deviceId: string, keys: string[], enabled = true) {
  return useQuery({
    queryKey: ['telemetry', 'latest', deviceId, keys.join(',')],
    queryFn: () => (USE_MOCK ? mockGetLatest(deviceId, keys) : realGetLatest(deviceId, keys)),
    staleTime: 30_000,
    enabled,
  });
}

/** Historical time-series for one device's keys (charts). */
export function useTimeseries(deviceId: string, keys: string[], range: Range = '24h', enabled = true) {
  return useQuery({
    queryKey: ['telemetry', 'ts', deviceId, keys.join(','), range],
    queryFn: () => (USE_MOCK ? mockGetTimeseries(deviceId, keys, range) : realGetTimeseries(deviceId, keys, range)),
    staleTime: 60_000,
    enabled,
  });
}

/** Multi-device latest snapshot (fleet view on mount). */
export function useBatch(deviceIds: string[], keys: string[], enabled = true) {
  return useQuery({
    queryKey: ['telemetry', 'batch', deviceIds.join(','), keys.join(',')],
    queryFn: () => (USE_MOCK ? mockGetBatch(deviceIds, keys) : realGetBatch(deviceIds, keys)),
    staleTime: 30_000,
    enabled,
  });
}

/**
 * Building-level historical trend: fetch each device's time-series via React Query
 * (cached) and sum them index-aligned into a single series. Backend has no aggregate
 * endpoint yet (#4 gap), so front-end aggregation is the chosen path per #8 spec.
 */
export function useBuildingTimeseries(range: Range = 'day', deviceIds: string[] = MOCK_DEVICES, key = 'power') {
  const queries = useQueries({
    queries: deviceIds.map((id) => ({
      queryKey: ['telemetry', 'ts', id, key, range],
      queryFn: () => (USE_MOCK ? mockGetTimeseries(id, [key], range) : realGetTimeseries(id, [key], range)),
      staleTime: 60_000,
    })),
  });
  const isLoading = queries.some((q) => q.isLoading);
  const data: TelemetryPoint[] = [];
  if (!isLoading) {
    const n = Math.max(0, ...queries.map((q) => q.data?.[key]?.length ?? 0));
    for (let i = 0; i < n; i++) {
      let sum = 0;
      let ts = 0;
      let has = false;
      queries.forEach((q) => {
        const p = q.data?.[key]?.[i];
        if (p) {
          sum += p.value;
          ts = p.ts;
          has = true;
        }
      });
      if (has) data.push({ ts, value: Math.round(sum) });
    }
  }
  return { data, isLoading };
}

/**
 * Live building values: initial snapshot from React Query (cached) + realtime pushes
 * from TelemetryClient. This is the core #8/#11 pattern — REST for history/initial,
 * WebSocket for live updates. `get(deviceId, key)` returns the freshest value.
 */
export function useTelemetryLive(deviceIds: string[], keys: string[]) {
  const { data: batch } = useBatch(deviceIds, keys);
  const live = useRef<Record<string, number>>({});
  const [, force] = useState(0);

  useEffect(() => {
    const cb = (deviceId: string, key: string, value: number) => {
      live.current[`${deviceId}:${key}`] = value;
      force((v) => v + 1);
    };
    deviceIds.forEach((id) => telemetry.subscribe(id, keys, cb));
    return () => deviceIds.forEach((id) => telemetry.unsubscribe(id, keys, cb));
  }, [deviceIds.join(','), keys.join(',')]);

  const get = (deviceId: string, key: string): number | undefined => {
    const k = `${deviceId}:${key}`;
    if (k in live.current) return live.current[k];
    return batch?.find((d) => d.deviceId === deviceId)?.latest[key]?.value;
  };

  return { get, loading: !batch };
}
