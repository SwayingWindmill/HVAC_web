import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { createPlatformGatewayClient, type EnergySeriesQuery } from '../../../api/generated/platformGateway.gen';
import {
  compareHours,
  hourlyHeatmap,
  periodWindow,
  siteDays,
  summarizeEnergy,
  type EnergyPeriod,
  type Granularity,
} from './model';

const client = createPlatformGatewayClient();
const siteRoute = getRouteApi('/_app/_site');
const DAY_MS = 86_400_000;

function useEnergyReader() {
  const { site, principal } = siteRoute.useRouteContext();
  const read = (energyType: EnergySeriesQuery['energyType'], granularity: Granularity, from: Date, to: Date, signal: AbortSignal) =>
    client.queryEnergySeries({
      tenantId: site.tenantId, siteId: site.id, energyType, granularity, timezone: site.timezone,
      from: from.toISOString(), to: to.toISOString(), qualityPolicy: 'VALID_ONLY',
    }, principal.session.csrfToken, { signal }).then((response) => response.data);
  const summary = async (granularity: Granularity, from: Date, to: Date, signal: AbortSignal) => {
    const [electricity, cooling] = await Promise.all([
      read('electricity', granularity, from, to, signal),
      read('cooling', granularity, from, to, signal),
    ]);
    return summarizeEnergy(electricity, cooling, granularity, site.timezone);
  };
  return { site, read, summary };
}

/** Site electricity and cooling for a period, from the Energy analytics owner. */
export function useEnergySummary(period: EnergyPeriod) {
  const { site, summary } = useEnergyReader();
  return useQuery({
    queryKey: ['energy-series', site.id, period],
    queryFn: ({ signal }) => {
      const { granularity, from, to } = periodWindow(period, new Date(), site.timezone);
      return summary(granularity, from, to, signal);
    },
    refetchInterval: 60_000,
    retry: false,
  });
}

/** Today by Site-local hour beside the same hours yesterday, up to now. */
export function useTodayAgainstYesterday() {
  const { site, summary } = useEnergyReader();
  return useQuery({
    queryKey: ['energy-series', site.id, 'today-against-yesterday'],
    queryFn: async ({ signal }) => {
      const { from, to } = periodWindow('today', new Date(), site.timezone);
      const [today, yesterday] = await Promise.all([
        summary('hour', from, to, signal),
        summary('hour', new Date(from.getTime() - DAY_MS), new Date(to.getTime() - DAY_MS), signal),
      ]);
      return { today, yesterday, hours: compareHours(today, yesterday) };
    },
    refetchInterval: 60_000,
    retry: false,
  });
}

/** Hourly HVAC electricity over the last `days` Site-local days, as a day-by-hour grid. */
export function useHourlyHeatmap(days: number) {
  const { site, read } = useEnergyReader();
  return useQuery({
    queryKey: ['energy-series', site.id, 'hourly-heatmap', days],
    queryFn: async ({ signal }) => {
      const now = new Date();
      const dayKeys = siteDays(now, site.timezone, days);
      const { from } = periodWindow('today', new Date(now.getTime() - (days - 1) * DAY_MS), site.timezone);
      const electricity = await read('electricity', 'hour', from, now, signal);
      return hourlyHeatmap(electricity, site.timezone, dayKeys);
    },
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}
