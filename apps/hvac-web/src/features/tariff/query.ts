import { queryOptions } from '@tanstack/react-query';
import { createPlatformGatewayClient, PlatformApiError, type SiteTariff } from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

/** The import Tariff in effect for a Site; null when none is assigned to its grid import. */
export function siteTariffQuery(siteId: string) {
  return queryOptions({
    queryKey: ['tariff', siteId],
    queryFn: async ({ signal }): Promise<SiteTariff | null> => {
      try {
        return (await client.getSiteTariff(siteId, { signal })).data;
      } catch (error) {
        if (error instanceof PlatformApiError && error.problem.status === 404) return null;
        throw error;
      }
    },
    // A Tariff Version changes a few times a year.
    staleTime: 10 * 60_000,
    retry: false,
  });
}
