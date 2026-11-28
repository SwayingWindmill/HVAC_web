import { queryOptions } from '@tanstack/react-query';
import { PlatformApiError, type SiteTariff } from './generated/platformGateway.gen.ts';
import { platformGatewayClient } from './platform-client.ts';

export const siteTariffQueryKey = (siteId: string) => ['tariff', siteId] as const;

/** The import Tariff in effect for a Site; null when none is assigned to its grid import. */
export function siteTariffQuery(siteId: string) {
  return queryOptions({
    queryKey: siteTariffQueryKey(siteId),
    queryFn: async ({ signal }): Promise<SiteTariff | null> => {
      try {
        return (await platformGatewayClient.getSiteTariff(siteId, { signal })).data;
      } catch (error) {
        if (error instanceof PlatformApiError && error.problem.status === 404) return null;
        throw error;
      }
    },
    staleTime: 5 * 60_000,
    retry: (failureCount, error) => {
      if (error instanceof PlatformApiError && (error.problem.status === 404 || error.problem.status === 403)) {
        return false;
      }
      return failureCount < 1;
    },
  });
}
