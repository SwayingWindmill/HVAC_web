import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { createPlatformGatewayClient, type EnergySeriesQuery } from '../../../api/generated/platformGateway.gen';
import {
  comparePeriods,
  completedComparisonTotals,
  hourlyHeatmap,
  periodWindow,
  previousWindow,
  siteDays,
  summarizeEnergy,
  type EnergyPeriod,
  type Granularity,
} from './model';

const client = createPlatformGatewayClient();
const siteRoute = getRouteApi('/_app/_site');
const DAY_MS = 86_400_000;

/** Reads Site energy series from the Energy analytics owner for the current Site. */
export function useEnergyReader() {
  const { site, principal } = siteRoute.useRouteContext();
  const read = (
    energyType: EnergySeriesQuery['energyType'], granularity: Granularity, from: Date, to: Date, signal: AbortSignal,
    breakdown?: EnergySeriesQuery['breakdown'],
  ) =>
    client.queryEnergySeries({
      tenantId: site.tenantId, siteId: site.id, energyType, granularity, timezone: site.timezone,
      from: from.toISOString(), to: to.toISOString(), qualityPolicy: 'VALID_ONLY', ...(breakdown ? { breakdown } : {}),
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

/**
 * Site electricity and cooling for a period beside the same span of the period before, from the
 * Energy analytics owner; `buckets` lines the two up for a chart or a table.
 */
export function useEnergyComparison(period: EnergyPeriod) {
  const { site, summary } = useEnergyReader();
  return useQuery({
    queryKey: ['energy-series', site.id, 'comparison', period],
    queryFn: async ({ signal }) => {
      const now = new Date();
      const current = periodWindow(period, now, site.timezone);
      const before = previousWindow(period, now, site.timezone);
      const [currentSummary, previousSummary] = await Promise.all([
        summary(current.granularity, current.from, current.to, signal),
        summary(before.granularity, before.from, before.to, signal),
      ]);
      return {
        current: currentSummary,
        previous: previousSummary,
        buckets: comparePeriods(currentSummary, previousSummary, { from: current.from, previousFrom: before.from, now, timeZone: site.timezone }),
      };
    },
    refetchInterval: 60_000,
    staleTime: 30_000,
    retry: false,
  });
}

/** Today's cumulative Site-local hour energy, shared between overview and digital twin. */
export function useTodayEnergy() {
  const { site, summary } = useEnergyReader();
  return useQuery({
    queryKey: ['energy-series', site.id, 'today-summary'],
    queryFn: async ({ signal }) => {
      const now = new Date();
      const { from, to } = periodWindow('today', now, site.timezone);
      const today = await summary('hour', from, to, signal);
      return { today, from, now };
    },
    refetchInterval: 60_000,
    staleTime: 30_000,
    retry: false,
  });
}

function useWholeYesterdayEnergy() {
  const { site, summary } = useEnergyReader();
  const yesterdayKey = siteDays(new Date(), site.timezone, 2)[0];
  return useQuery({
    queryKey: ['energy-series', site.id, 'whole-yesterday', yesterdayKey],
    queryFn: async ({ signal }) => {
      const now = new Date();
      const { from: todayStart } = periodWindow('today', now, site.timezone);
      const before = previousWindow('today', now, site.timezone);
      const wholeYesterday = await summary('hour', before.from, todayStart, signal);
      return { wholeYesterday, from: before.from };
    },
    staleTime: 30 * 60_000,
    gcTime: 60 * 60_000,
    retry: false,
  });
}

/**
 * Today by Site-local hour against yesterday: `completed` aligns complete hours for a like-for-like
 * delta without extra mid-hour queries, and `hours` draws the whole of yesterday as the reference line.
 */
export function useTodayAgainstYesterday() {
  const { site } = useEnergyReader();
  const todayQuery = useTodayEnergy();
  const yesterdayQuery = useWholeYesterdayEnergy();
  const todayData = todayQuery.data;
  const yesterdayData = yesterdayQuery.data;
  const data = useMemo(() => {
    if (!todayData || !yesterdayData) return undefined;
    const hours = comparePeriods(todayData.today, yesterdayData.wholeYesterday, {
      from: todayData.from,
      previousFrom: yesterdayData.from,
      now: todayData.now,
      timeZone: site.timezone,
    });
    return {
      today: todayData.today,
      wholeYesterday: yesterdayData.wholeYesterday,
      hours,
      completed: completedComparisonTotals(hours),
    };
  }, [site.timezone, todayData, yesterdayData]);

  const refetch = () => Promise.all([todayQuery.refetch(), yesterdayQuery.refetch()]);

  if (todayQuery.isError || yesterdayQuery.isError) {
    return { data: undefined, isPending: false as const, isError: true as const, refetch };
  }
  if (todayQuery.isPending || yesterdayQuery.isPending || !data) {
    return { data: undefined, isPending: true as const, isError: false as const, refetch };
  }
  return { data, isPending: false as const, isError: false as const, refetch };
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
    staleTime: 30_000,
    retry: false,
  });
}
