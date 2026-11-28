import { queryOptions } from '@tanstack/react-query';
import { platformGatewayClient } from '@/api/platform-client';

export const dataGapsKeys = {
  site: (siteId: string) => ['site-data-gaps', siteId] as const,
};

export function siteDataGapsQuery(siteId: string) {
  return queryOptions({
    queryKey: dataGapsKeys.site(siteId),
    queryFn: ({ signal }) => platformGatewayClient.listSiteDataGaps(siteId, { signal }),
    select: (response) => response.data.items,
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}
