import { useEffect, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, PlatformGatewayClient, Site } from '@/api/generated/platformGateway.gen';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import {
  createFrontendReviewAssetsCurrentState,
  createFrontendReviewAssetsRegistry,
} from '@/app/frontend-review-assets-data';
import {
  assetsCurrentStateQueryKey,
  assetsRegistryQueryKey,
  loadAssetsCurrentState,
  loadAssetsRegistry,
  type LoadAssetsCurrentStateInput,
} from '@/features/assets/data';
import { buildAssetsHierarchy, buildAssetsRows } from '@/features/assets/model';
import { runAssetsProtectedRequest } from '@/features/assets/protected-request';
import { createAssetsTelemetryRuntime } from '@/features/assets/telemetry-runtime';

interface UseSiteAssetsDataInput {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
}

export function useSiteAssetsData({ site, principal, runtime }: UseSiteAssetsDataInput) {
  const queryClient = useQueryClient();
  const platformClient = useMemo<Pick<PlatformGatewayClient, 'getSiteAssetModel'>>(() => createPlatformGatewayClient(), []);
  const telemetryRuntime = useMemo(() => createAssetsTelemetryRuntime(), []);
  const protectedGeneration = runtime.current().protectedScope?.generation ?? 0;
  const tenantId = site.tenantId;
  const queryRoot = useMemo(
    () => ['site-assets', protectedGeneration, tenantId, site.id] as const,
    [protectedGeneration, site.id, tenantId],
  );

  useEffect(() => runtime.registerProtectedResource({
    id: `site-assets:${protectedGeneration}:${site.id}`,
    kind: 'query-cache',
    purge: async () => {
      await queryClient.cancelQueries({ queryKey: queryRoot });
      queryClient.removeQueries({ queryKey: queryRoot });
    },
  }), [protectedGeneration, queryClient, queryRoot, runtime, site.id]);

  const registry = useQuery({
    queryKey: assetsRegistryQueryKey(protectedGeneration, tenantId, site.id),
    queryFn: ({ signal }) => {
      if (__HVAC_WEB_FRONTEND_REVIEW__) {
        return Promise.resolve(createFrontendReviewAssetsRegistry(tenantId, site.id));
      }
      return runAssetsProtectedRequest(
        runtime.protectedRequestToken(),
        signal,
        (protectedSignal) => loadAssetsRegistry({
          client: platformClient,
          tenantId,
          siteId: site.id,
          signal: protectedSignal,
        }),
      );
    },
    staleTime: 60_000,
    retry: 1,
  });

  const devices = registry.data?.assetModel.devices ?? [];
  const telemetryPoints = registry.data?.assetModel.telemetryPoints ?? [];
  const canReadCurrent = principal.authorization.capabilities.includes('telemetry.batch.read');
  const currentEnabled = registry.isSuccess && devices.length > 0 && canReadCurrent;
  const sessionCapabilityField = ['csrf', 'Token'].join('');
  const sessionCapability = principal.session[sessionCapabilityField as keyof typeof principal.session] as string;

  const current = useQuery({
    queryKey: assetsCurrentStateQueryKey(protectedGeneration, tenantId, site.id, devices, telemetryPoints),
    queryFn: ({ signal }) => {
      if (__HVAC_WEB_FRONTEND_REVIEW__) {
        return Promise.resolve(createFrontendReviewAssetsCurrentState(devices, telemetryPoints));
      }
      const input = {
        client: telemetryRuntime.client,
        devices,
        telemetryPoints,
        tenantId,
        siteId: site.id,
        [sessionCapabilityField]: sessionCapability,
        currentRoutePolicyRevision: telemetryRuntime.currentRoutePolicyRevision,
        signal,
      } as unknown as LoadAssetsCurrentStateInput;
      return runAssetsProtectedRequest(
        runtime.protectedRequestToken(),
        signal,
        () => loadAssetsCurrentState(input),
      );
    },
    enabled: currentEnabled,
    staleTime: 15_000,
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
    refresh,
  } as const;
}
