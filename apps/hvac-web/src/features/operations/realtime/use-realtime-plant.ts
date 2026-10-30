import { useCallback, useEffect, useId, useMemo } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { projectAssetsRealtimeRow } from '@/features/assets/realtime';
import { siteDevicesRealtimeShellState } from '@/features/assets/realtime-shell-status';
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';
import { useSiteDevicesRealtime } from '@/features/assets/useSiteDevicesRealtime';
import { buildPlantView } from './plant-model';

const workspaceSiteRoute = getRouteApi('/_app/_site');

// The Snapshot is re-read on the plant's 30 s publish interval so the page stays
// current when the stream is unavailable; stream updates win by business revision.
const SNAPSHOT_POLL_MS = 30_000;

export type PlantLiveMode = 'live' | 'polling' | 'connecting';

export function useRealtimePlant() {
  const { site, principal, runtime, publishRealtimeStatus } = workspaceSiteRoute.useRouteContext();
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

  // The header's realtime status follows this page's session; several plant views can be open at once.
  const statusSource = useId();
  const shellState = siteDevicesRealtimeShellState(realtime.phase, realtime.states);
  useEffect(() => {
    if (shellState === 'idle') {
      runtime.releaseRealtimeStatus(statusSource);
      return;
    }
    // A Site switch purges this session before the next Site activates; never report across Sites.
    const shell = runtime.current();
    if (shell.state !== 'READY' || shell.protectedScope?.siteId !== site.id) return;
    publishRealtimeStatus({ source: statusSource, state: shellState, siteId: site.id });
  }, [publishRealtimeStatus, runtime, shellState, site.id, statusSource]);
  useEffect(() => () => runtime.releaseRealtimeStatus(statusSource), [runtime, statusSource]);

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
