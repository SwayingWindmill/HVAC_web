import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { createPlatformGatewayClient, type EnergySeriesQuery } from '../../../api/generated/platformGateway.gen';
import { periodWindow, summarizeEnergy, type EnergyPeriod } from './model';

const client = createPlatformGatewayClient();
const siteRoute = getRouteApi('/_app/_site');

/** Site electricity and cooling for a period, from the Energy analytics owner. */
export function useEnergySummary(period: EnergyPeriod) {
  const { site, principal } = siteRoute.useRouteContext();
  return useQuery({
    queryKey: ['energy-series', site.id, period],
    queryFn: async ({ signal }) => {
      const { granularity, from, to } = periodWindow(period, new Date(), site.timezone);
      const read = (energyType: EnergySeriesQuery['energyType']) => client.queryEnergySeries({
        tenantId: site.tenantId, siteId: site.id, energyType, granularity, timezone: site.timezone,
        from: from.toISOString(), to: to.toISOString(), qualityPolicy: 'VALID_ONLY',
      }, principal.session.csrfToken, { signal });
      const [electricity, cooling] = await Promise.all([read('electricity'), read('cooling')]);
      return summarizeEnergy(electricity.data, cooling.data, granularity, site.timezone);
    },
    refetchInterval: 60_000,
    retry: false,
  });
}
