import { useEffect, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { siteAssetModelEnvelopeQuery, siteAssetModelQueryKey } from '@/api/site-asset-model';
import type { HvacRouterContext } from '@/app/router-context';
import {
  assetsCurrentStateQueryKey,
  assetsProtectedQueryRoot,
  assetsRegistryQueryKey,
  loadAssetsCurrentState,
  loadAssetsRegistry,
  type AssetsRegistryClient,
} from '@/features/assets/data';
import { buildAssetsHierarchy, buildAssetsRows } from '@/features/assets/model';
import { runAssetsProtectedRequest } from '@/features/assets/protected-request';
import { createAssetsTelemetryRuntime } from '@/features/assets/telemetry-runtime';

interface UseSiteAssetsDataInput {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  /** When false, holds queries and protected resource registration idle until needed. Defaults to true. */
  readonly enabled?: boolean;
  /** Re-read the current-state Snapshot on this interval; off by default. */
  readonly currentRefetchIntervalMs?: number | false;
}

export function useSiteAssetsData({
  site,
  principal,
  runtime,
  enabled = true,
  currentRefetchIntervalMs = false,
}: UseSiteAssetsDataInput) {
  const queryClient = useQueryClient();
  const sharedRegistryClient = useMemo<AssetsRegistryClient>(
    () => ({
      getSiteAssetModel: (requestedSiteId, init) => {
        if (init?.signal?.aborted) {
          return Promise.reject(new DOMException('The operation was aborted.', 'AbortError'));
        }
        return queryClient.fetchQuery(siteAssetModelEnvelopeQuery(requestedSiteId, { staleTime: 5 * 60_000 }));
      },
    }),
    [queryClient],
  );
  const telemetryRuntime = useMemo(() => createAssetsTelemetryRuntime(), []);
  const protectedGeneration = runtime.current().protectedScope?.generation ?? 0;
  const tenantId = site.tenantId;
  const queryRoot = useMemo(
    () => assetsProtectedQueryRoot(protectedGeneration, tenantId, site.id),
    [protectedGeneration, site.id, tenantId],
  );

  useEffect(() => {
    if (!enabled) return undefined;
    const scope = runtime.current().protectedScope;
    if (!scope || scope.state !== 'idle' || scope.siteId !== site.id || scope.generation !== protectedGeneration) {
      return undefined;
    }
    const assetModelRoot = siteAssetModelQueryKey(site.id);
    return runtime.registerProtectedResource({
      id: `site-assets:${protectedGeneration}:${site.id}`,
      kind: 'query-cache',
      purge: async () => {
        await Promise.all([
          queryClient.cancelQueries({ queryKey: queryRoot }),
          queryClient.cancelQueries({ queryKey: assetModelRoot }),
        ]);
        queryClient.removeQueries({ queryKey: queryRoot });
        queryClient.removeQueries({ queryKey: assetModelRoot });
      },
    });
  }, [enabled, protectedGeneration, queryClient, queryRoot, runtime, site.id]);

  const registry = useQuery({
    queryKey: assetsRegistryQueryKey(protectedGeneration, tenantId, site.id),
    queryFn: ({ signal }) => runAssetsProtectedRequest(
      runtime.protectedRequestToken(),
      signal,
      (protectedSignal) => loadAssetsRegistry({
        client: sharedRegistryClient,
        tenantId,
        siteId: site.id,
        signal: protectedSignal,
      }),
    ),
    enabled,
    staleTime: 5 * 60_000,
    retry: 1,
  });

  const devices = registry.data?.assetModel.devices ?? [];
  const telemetryPoints = registry.data?.assetModel.telemetryPoints ?? [];
  const canReadCurrent = principal.authorization.capabilities.includes('telemetry.batch.read');
  const currentEnabled = enabled && registry.isSuccess && devices.length > 0 && canReadCurrent;

  const current = useQuery({
    queryKey: assetsCurrentStateQueryKey(protectedGeneration, tenantId, site.id, devices, telemetryPoints),
    queryFn: ({ signal }) => runAssetsProtectedRequest(
      runtime.protectedRequestToken(),
      signal,
      () => loadAssetsCurrentState({
        client: telemetryRuntime.client,
        devices,
        telemetryPoints,
        tenantId,
        siteId: site.id,
        csrfToken: principal.session.csrfToken,
        currentRoutePolicyRevision: telemetryRuntime.currentRoutePolicyRevision,
        signal,
      }),
    ),
    enabled: currentEnabled,
    staleTime: 15_000,
    refetchInterval: enabled ? currentRefetchIntervalMs : false,
    retry: 1,
  });

  const rows = useMemo(
    () => registry.data
      ? buildAssetsRows({ assetModel: registry.data.assetModel, snapshots: current.data?.byDeviceId })
      : [],
    [current.data, registry.data],
  );
  const hierarchy = useMemo(
    () => registry.data ? buildAssetsHierarchy(registry.data.assetModel, site.displayName) : null,
    [registry.data, site.displayName],
  );

  const currentPending = currentEnabled && current.isPending;
  const currentUnavailable = registry.isSuccess
    && devices.length > 0
    && (!canReadCurrent || current.isError);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: siteAssetModelQueryKey(site.id), refetchType: 'none' });
    void registry.refetch();
    if (currentEnabled) void current.refetch();
  };

  return {
    registry,
    current,
    rows,
    hierarchy,
    currentPending,
    currentUnavailable,
    canReadCurrent,
    protectedGeneration,
    telemetryRuntime,
    refresh,
  } as const;
}
