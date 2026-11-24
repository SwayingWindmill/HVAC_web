import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import type { AssetsDeviceRow } from '@/features/assets/model';
import { projectAssetsRealtimeRow, type AssetsRealtimeState } from '@/features/assets/realtime';
import { siteDevicesRealtimeShellState } from '@/features/assets/realtime-shell-status';
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';
import { useSiteDevicesRealtime } from '@/features/assets/useSiteDevicesRealtime';
import { buildPlantView } from './plant-model';
import { createRealtimeDemandController } from './realtime-demand';

const workspaceSiteRoute = getRouteApi('/_app/_site');

// The Snapshot is re-read on the plant's 30 s publish interval so the page stays
// current when the stream is unavailable; stream updates win by business revision.
const SNAPSHOT_POLL_MS = 30_000;
const REALTIME_HANDOFF_GRACE_MS = 250;

export type PlantLiveMode = 'live' | 'polling' | 'connecting';

export type RealtimePlantSnapshot = {
  readonly site: ReturnType<typeof workspaceSiteRoute.useRouteContext>['site'];
  readonly plant: ReturnType<typeof buildPlantView>;
  readonly mode: PlantLiveMode;
  readonly registry: ReturnType<typeof useSiteAssetsData>['registry'];
  readonly current: ReturnType<typeof useSiteAssetsData>['current'];
  readonly currentUnavailable: ReturnType<typeof useSiteAssetsData>['currentUnavailable'];
  readonly refresh: ReturnType<typeof useSiteAssetsData>['refresh'];
};

interface SiteRealtimeContextValue {
  readonly acquire: () => () => void;
  readonly value: RealtimePlantSnapshot;
}

const SiteRealtimeContext = createContext<SiteRealtimeContextValue | null>(null);

function SiteRealtimeHost({ children }: { readonly children: ReactNode }) {
  const { site, principal, runtime, publishRealtimeStatus } = workspaceSiteRoute.useRouteContext();
  const [active, setActive] = useState(false);

  const controller = useMemo(
    () => createRealtimeDemandController({ graceMs: REALTIME_HANDOFF_GRACE_MS, onChange: setActive }),
    [],
  );

  useEffect(() => () => controller.reset(), [controller]);

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
    enabled: active,
    currentRefetchIntervalMs: active ? SNAPSHOT_POLL_MS : false,
  });

  const realtime = useSiteDevicesRealtime({
    rows: assets.rows,
    allowed: active && canSubscribe && assets.current.isSuccess,
    protectedGeneration: assets.protectedGeneration,
    runtime: assets.telemetryRuntime,
    protectedRequestToken,
    registerProtectedResource,
  });

  // The header's realtime status follows this Site session; at most one host is active per Site.
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

  const projectionCacheRef = useRef(new Map<string, {
    readonly base: AssetsDeviceRow;
    readonly state: AssetsRealtimeState | null;
    readonly row: AssetsDeviceRow;
  }>());
  const rows = useMemo(() => {
    const cache = projectionCacheRef.current;
    const nextKeys = new Set<string>();
    const nextRows = assets.rows.map((baseRow) => {
      const deviceId = baseRow.device.id;
      const state = realtime.states.get(deviceId) ?? null;
      nextKeys.add(deviceId);
      const cached = cache.get(deviceId);
      if (cached && cached.base === baseRow && cached.state === state) {
        return cached.row;
      }
      const projected = projectAssetsRealtimeRow(baseRow, state).row;
      cache.set(deviceId, { base: baseRow, state, row: projected });
      return projected;
    });
    for (const key of cache.keys()) {
      if (!nextKeys.has(key)) cache.delete(key);
    }
    return nextRows;
  }, [assets.rows, realtime.states]);
  const plant = useMemo(
    () => buildPlantView(rows, assets.registry.data?.assetModel),
    [assets.registry.data?.assetModel, rows],
  );

  let liveCount = 0;
  for (const state of realtime.states.values()) {
    if (state.status === 'live') liveCount += 1;
  }
  const mode: PlantLiveMode = realtime.phase === 'active' && liveCount > 0
    ? 'live'
    : realtime.phase === 'opening' ? 'connecting' : 'polling';

  const acquire = useCallback(() => controller.acquire(), [controller]);
  const value = useMemo<RealtimePlantSnapshot>(
    () => ({
      site,
      plant,
      mode,
      registry: assets.registry,
      current: assets.current,
      currentUnavailable: assets.currentUnavailable,
      refresh: assets.refresh,
    }),
    [site, plant, mode, assets.registry, assets.current, assets.currentUnavailable, assets.refresh],
  );
  const contextValue = useMemo<SiteRealtimeContextValue>(
    () => ({ acquire, value }),
    [acquire, value],
  );

  return <SiteRealtimeContext.Provider value={contextValue}>{children}</SiteRealtimeContext.Provider>;
}

/**
 * Mounts one demand-driven realtime + snapshot coordinator for the active `/_app/_site` subtree,
 * keyed by Site and protected-scope generation so Site switches reset immediately.
 */
export function SiteRealtimeProvider({ children }: { readonly children: ReactNode }) {
  const { site, runtime } = workspaceSiteRoute.useRouteContext();
  const protectedGeneration = runtime.current().protectedScope?.generation ?? 0;
  return <SiteRealtimeHost key={`${protectedGeneration}:${site.id}`}>{children}</SiteRealtimeHost>;
}

export function useSiteRealtimeContext(): SiteRealtimeContextValue {
  const ctx = useContext(SiteRealtimeContext);
  if (!ctx) {
    throw new Error('useRealtimePlant must be used inside a SiteRealtimeProvider (/_app/_site route).');
  }
  return ctx;
}
