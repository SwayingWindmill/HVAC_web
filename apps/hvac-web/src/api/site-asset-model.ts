import { queryOptions, type QueryClient } from '@tanstack/react-query';
import { ZodError } from 'zod';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type PlatformResponse,
  type SiteAssetModel,
} from './generated/platformGateway.gen.ts';

const client = createPlatformGatewayClient();

export const SITE_ASSET_MODEL_QUERY_ROOT = ['site-asset-model'] as const;

export interface SiteAssetModelQueryOptions {
  readonly enabled?: boolean;
  readonly staleTime?: number;
  readonly retry?: boolean | number | ((failureCount: number, error: Error) => boolean);
}

/** Canonical TanStack Query key for a site's asset model across all workspace and platform consumers. */
export function siteAssetModelQueryKey(siteId: string | null): readonly ['site-asset-model', string] {
  return ['site-asset-model', siteId ?? 'NONE'] as const;
}

function retrySiteAssetModelQuery(failureCount: number, error: Error): boolean {
  if (failureCount >= 1 || error instanceof ZodError) return false;
  if (!(error instanceof PlatformApiError)) return true;
  return error.problem.retryable;
}

/** Envelope query (includes `routePolicyRevision`) shared by `useSiteAssetsData` and `BenchmarkWorkspace`. */
export function siteAssetModelEnvelopeQuery(
  siteId: string | null,
  options: SiteAssetModelQueryOptions = {},
) {
  return queryOptions({
    queryKey: siteAssetModelQueryKey(siteId),
    queryFn: ({ signal }) => client.getSiteAssetModel(siteId!, { signal }),
    enabled: Boolean(siteId) && (options.enabled ?? true),
    staleTime: options.staleTime ?? 5 * 60_000,
    retry: options.retry ?? retrySiteAssetModelQuery,
  });
}

/** Standard `SiteAssetModel` query projecting `response.data` while sharing the exact envelope cache key. */
export function siteAssetModelQuery(
  siteId: string | null,
  options: SiteAssetModelQueryOptions = {},
) {
  return queryOptions({
    ...siteAssetModelEnvelopeQuery(siteId, options),
    select: (response: PlatformResponse<SiteAssetModel>) => response.data,
  });
}

/** Invalidates both the canonical asset-model cache and any protected site-assets entries derived from it. */
export async function invalidateSiteAssetModel(queryClient: QueryClient, siteId?: string | null): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: siteId ? siteAssetModelQueryKey(siteId) : SITE_ASSET_MODEL_QUERY_ROOT }),
    queryClient.invalidateQueries({ queryKey: ['real-assets'] }),
  ]);
}
