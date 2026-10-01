import { useCallback, useMemo } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { projectAssetsRealtimeRow } from '@/features/assets/realtime';
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';
import { useSiteDevicesRealtime } from '@/features/assets/useSiteDevicesRealtime';
import { buildPlantView } from './plant-model';

const workspaceSiteRoute = getRouteApi('/_app/_site');

// The Snapshot is re-read on the plant's 30 s publish interval so the page stays
// current when the stream is unavailable; stream updates win by business revision.
const SNAPSHOT_POLL_MS = 30_000;

export type PlantLiveMode = 'live' | 'polling' | 'connecting';

export function useRealtimePlant() {
  const { site, principal, runtime } = workspaceSiteRoute.useRouteContext();
  const capabilities = principal.authorization.capabilities;
  const canSubscribe = capabilities.includes('telemetry.subscribe');
  const protectedRequestToken = useCallback(() => runtime.protectedRequestToken(), [runtime]);
  const registerProtectedResource = useCallback(
    (resource: Parameters<typeof runtime.registerProtectedResource>[0]) => runtime.registerProtectedResource(resource),
    [runtime],
  );

  const assets = useSiteAssetsData({
    site,
    principal,
    runtime,
    currentRefetchIntervalMs: SNAPSHOT_POLL_MS,
  });

  const realtime = useSiteDevicesRealtime({
    rows: assets.rows,
    allowed: canSubscribe && assets.current.isSuccess,
    protectedGeneration: assets.protectedGeneration,
    runtime: assets.telemetryRuntime,
    protectedRequestToken,
    registerProtectedResource,
  });

  const rows = useMemo(
    () => assets.rows.map((row) => projectAssetsRealtimeRow(row, realtime.states.get(row.device.id) ?? null).row),
    [assets.rows, realtime.states],
  );
  const plant = useMemo(() => buildPlantView(rows), [rows]);

  const liveCount = [...realtime.states.values()].filter((state) => state.status === 'live').length;
  const mode: PlantLiveMode = realtime.phase === 'active' && liveCount > 0
    ? 'live'
    : realtime.phase === 'opening' ? 'connecting' : 'polling';

  return {
    site,
    plant,
    mode,
    registry: assets.registry,
    current: assets.current,
    currentUnavailable: assets.currentUnavailable,
    refresh: assets.refresh,
  } as const;
}
