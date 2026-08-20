import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { API_MODE } from './config';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  siteDashboardSummaryDeltaSchema,
  type SiteDashboardSummary,
  uuidV7Schema,
} from './generated/platformGateway.gen';
import { startSiteDashboardLive } from './site-dashboard-live';

const client = createPlatformGatewayClient();

export const siteDashboardSummaryQueryKey = (tenantId: string, siteId: string, authorizationScope: string) =>
  ['presentation', 'site-dashboard-summary', tenantId, siteId, authorizationScope] as const;

export async function readSiteDashboardSummary(siteId: string, signal?: AbortSignal): Promise<SiteDashboardSummary> {
  const response = await client.getSiteDashboardSummary(uuidV7Schema.parse(siteId), { signal });
  return response.data;
}

export function useSiteDashboardSummary(tenantId: string, siteId: string, authorizationScope: string, enabled = true) {
  const queryClient = useQueryClient();
  const queryKey = siteDashboardSummaryQueryKey(tenantId, siteId, authorizationScope);
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => readSiteDashboardSummary(siteId, signal),
    enabled: API_MODE === 'real' && enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: true,
    retry: (failureCount, error) => failureCount < 1 && (!(error instanceof PlatformApiError) || error.problem.retryable),
  });

  useEffect(() => {
    if (API_MODE !== 'real' || !enabled || !query.isSuccess || !query.data) return undefined;
    const session = startSiteDashboardLive(tenantId, siteId, query.data, {
      readSummary: () => readSiteDashboardSummary(siteId),
      getSummary: () => queryClient.getQueryData<SiteDashboardSummary>(queryKey),
      setSummary: (summary) => queryClient.setQueryData(queryKey, summary),
      parseDelta: (raw) => siteDashboardSummaryDeltaSchema.parse(JSON.parse(raw) as unknown),
      openEventSource: (url) => {
        const source = new EventSource(url);
        return {
          addSummaryListener: (listener) => source.addEventListener('dashboard-summary', (event) => listener((event as MessageEvent<string>).data)),
          setErrorHandler: (listener) => { source.onerror = () => listener(); },
          close: () => source.close(),
        };
      },
      schedule: (callback, delayMs) => window.setTimeout(callback, delayMs),
      cancelSchedule: (handle) => window.clearTimeout(handle),
    });
    return () => session.close();
  }, [authorizationScope, enabled, query.isSuccess, queryClient, siteId, tenantId]);

  return query;
}

export function presentSiteDashboardError(error: unknown): { title: string; description: string; retryable: boolean } {
  if (error instanceof PlatformApiError) {
    if (error.problem.code === 'RESOURCE_NOT_FOUND') {
      return { title: '站点不可见或不存在', description: error.problem.detail, retryable: false };
    }
    return { title: error.problem.title, description: error.problem.detail, retryable: error.problem.retryable };
  }
  return { title: '站点运营摘要不可用', description: error instanceof Error ? error.message : '无法读取权威 SiteDashboardSummary。', retryable: true };
}
