import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import {
  energyRetryDelay,
  periodWindow,
  previousWindow,
  retryEnergyQuery,
  siteDayKey,
  type EnergyPeriod,
  type Granularity,
} from '@/features/energy-analysis/consumption/model';
import { energyQueryNow } from '@/lib/site-time';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import type { EnergySeriesResponse } from '@/api/generated/platformGateway.gen';
import { energyBreakdown, type Breakdown, type Machine } from './breakdown-model';
import { plantMachines, plantMachinesFromAssetModel } from './plant-machines';

export { plantMachines };

function buildBreakdownFromSeries(
  machines: readonly Machine[] | null,
  data: EnergySeriesResponse | undefined,
  granularity: Granularity,
  timeZone: string,
): Breakdown | undefined {
  if (!machines || !data) return undefined;
  const bucketKey = granularity === 'hour'
    ? (periodStart: string) => new Date(periodStart).toISOString()
    : (periodStart: string) => siteDayKey(new Date(periodStart), timeZone);
  const isMonth = granularity === 'month';
  const devices = (data.devices ?? []).map((device) => ({
    deviceId: device.deviceId,
    points: device.points,
    crossGapKWh: isMonth ? 0 : (device.crossGapKWh ?? 0),
  }));
  const siteCrossGapKWh = isMonth ? 0 : (data.crossGap?.energyKWh ?? 0);
  return energyBreakdown(machines, data.points, devices, bucketKey, siteCrossGapKWh);
}

/**
 * The HVAC total meter and each machine's sub-meter over a period and over the same span before
 * it, from the Energy analytics owner.
 */
export function useEnergyBreakdown(period: EnergyPeriod) {
  const { site, read } = useEnergyReader();
  const registry = useQuery(siteAssetModelQuery(site.id));
  const machines = useMemo(() => (registry.data ? plantMachinesFromAssetModel(registry.data) : null), [registry.data]);
  const dayKey = siteDayKey(energyQueryNow(site.timezone), site.timezone);

  const readRawBreakdown = (window: (period: EnergyPeriod, now: Date, timeZone: string) => { granularity: Granularity; from: Date; to: Date }) =>
    async ({ signal }: { signal: AbortSignal }) => {
      const { granularity, from, to } = window(period, energyQueryNow(site.timezone), site.timezone);
      const data = await read('electricity', granularity, from, to, signal, 'device');
      return { data, granularity };
    };

  const rawCurrent = useQuery({
    queryKey: ['energy-breakdown', site.id, period, 'current'],
    queryFn: readRawBreakdown(periodWindow),
    refetchInterval: 5 * 60_000,
    staleTime: 30_000,
    retry: retryEnergyQuery,
    retryDelay: energyRetryDelay,
  });
  const rawPrevious = useQuery({
    queryKey: ['energy-breakdown', site.id, period, 'previous', dayKey],
    queryFn: readRawBreakdown(previousWindow),
    staleTime: 5 * 60_000,
    retry: retryEnergyQuery,
    retryDelay: energyRetryDelay,
  });

  const currentData = useMemo(
    () => buildBreakdownFromSeries(machines, rawCurrent.data?.data, rawCurrent.data?.granularity ?? periodWindow(period, new Date(), site.timezone).granularity, site.timezone),
    [machines, period, rawCurrent.data, site.timezone],
  );
  const previousData = useMemo(
    () => buildBreakdownFromSeries(machines, rawPrevious.data?.data, rawPrevious.data?.granularity ?? previousWindow(period, new Date(), site.timezone).granularity, site.timezone),
    [machines, period, rawPrevious.data, site.timezone],
  );

  const query = useMemo(() => ({
    data: currentData,
    isPending: rawCurrent.isPending,
    isError: rawCurrent.isError || registry.isError,
    refetch: () => Promise.all([
      ...(registry.isError ? [registry.refetch()] : []),
      rawCurrent.refetch(),
    ]),
  }), [currentData, rawCurrent, registry]);

  const previous = useMemo(() => ({
    data: previousData,
    isPending: rawPrevious.isPending,
    isError: rawPrevious.isError,
    refetch: () => rawPrevious.refetch(),
  }), [previousData, rawPrevious]);

  return { machines, registry, query, previous };
}
