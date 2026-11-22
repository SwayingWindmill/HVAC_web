import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { periodWindow, previousWindow, siteDayKey, type EnergyPeriod } from '@/features/energy-analysis/consumption/model';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import { energyBreakdown } from './breakdown-model';
import { plantMachines, plantMachinesFromAssetModel } from './plant-machines';

export { plantMachines };

/**
 * The HVAC total meter and each machine's sub-meter over a period and over the same span before
 * it, from the Energy analytics owner.
 */
export function useEnergyBreakdown(period: EnergyPeriod) {
  const { site, read } = useEnergyReader();
  const registry = useQuery(siteAssetModelQuery(site.id));
  // The plant's devices come from the Registry asset model; until it loads there is no machine list.
  const machines = useMemo(() => (registry.data ? plantMachinesFromAssetModel(registry.data) : null), [registry.data]);
  const machineKey = machines?.map((machine) => machine.deviceId).join(',');
  const breakdownOf = (window: (period: EnergyPeriod, now: Date, timeZone: string) => { granularity: 'hour' | 'day' | 'month'; from: Date; to: Date }) =>
    async ({ signal }: { signal: AbortSignal }) => {
      const { granularity, from, to } = window(period, new Date(), site.timezone);
      const data = await read('electricity', granularity, from, to, signal, 'device');
      const bucketKey = granularity === 'hour'
        ? (periodStart: string) => new Date(periodStart).toISOString()
        : (periodStart: string) => siteDayKey(new Date(periodStart), site.timezone);
      const isMonth = granularity === 'month';
      const devices = (data.devices ?? []).map((device) => ({
        deviceId: device.deviceId,
        points: device.points,
        crossGapKWh: isMonth ? 0 : (device.crossGapKWh ?? 0),
      }));
      const siteCrossGapKWh = isMonth ? 0 : (data.crossGap?.energyKWh ?? 0);
      return energyBreakdown(machines!, data.points, devices, bucketKey, siteCrossGapKWh);
    };
  const query = useQuery({
    queryKey: ['energy-breakdown', site.id, period, machineKey],
    enabled: machines !== null,
    queryFn: breakdownOf(periodWindow),
    refetchInterval: 5 * 60_000,
    retry: false,
  });
  const previous = useQuery({
    queryKey: ['energy-breakdown', site.id, period, 'previous', machineKey],
    enabled: machines !== null,
    queryFn: breakdownOf(previousWindow),
    staleTime: 30 * 60_000,
    retry: false,
  });
  return { machines, registry, query, previous };
}
