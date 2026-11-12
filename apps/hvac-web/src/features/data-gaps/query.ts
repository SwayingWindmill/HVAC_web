import { queryOptions } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';

const dataGapsGatewayClient = createPlatformGatewayClient();

export const dataGapsKeys = {
  site: (siteId: string) => ['site-data-gaps', siteId] as const,
};

export function siteDataGapsQuery(siteId: string) {
  return queryOptions({
    queryKey: dataGapsKeys.site(siteId),
    queryFn: ({ signal }) => dataGapsGatewayClient.listSiteDataGaps(siteId, { signal }),
    select: (response) => response.data.items,
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}
