import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useMemo } from 'react';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { plantMachinesFromAssetModel } from '@/features/breakdown/plant-machines';
import type { Machine } from '@/features/breakdown/breakdown-model';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';

const siteRoute = getRouteApi('/_app/_site');

// A sub-metered machine reports electricity of its own; a week is long enough to see one that is idle.
const METERED_LOOKBACK_MS = 7 * 24 * 3_600_000;

/**
 * The plant's machines, named as the plant page names them, from the Site Asset Model cache.
 */
export function usePlantMachines() {
  const { site } = siteRoute.useRouteContext();
  const assetModel = useQuery(siteAssetModelQuery(site.id));
  const machines = useMemo(() => (assetModel.data ? plantMachinesFromAssetModel(assetModel.data) : null), [assetModel.data]);
  const names = useMemo(() => {
    if (!machines) return null;
    const entries: [string, string][] = [];
    for (const machine of machines) {
      for (const deviceId of machine.deviceIds ?? [machine.deviceId]) {
        entries.push([deviceId, machine.name]);
      }
    }
    return new Map(entries);
  }, [machines]);
  return { machines, names };
}

/**
 * The machines a measure can be bounded by: those with a sub-meter, which the Energy analytics
 * owner reports electricity for, as the breakdown page counts them.
 */
export function useMeteredMachines(machines: readonly Machine[] | null, enabled: boolean) {
  const { site, read } = useEnergyReader();
  const metered = useQuery({
    queryKey: ['savings-metered-machines', site.id],
    enabled,
    queryFn: async ({ signal }) => {
      const to = new Date();
      const data = await read('electricity', 'day', new Date(to.getTime() - METERED_LOOKBACK_MS), to, signal, 'device');
      return new Set((data.devices ?? []).filter((device) => device.points.length > 0).map((device) => device.deviceId));
    },
    staleTime: 5 * 60_000,
    retry: false,
  });
  return {
    machines: machines && metered.data
      ? machines.flatMap((machine) => {
        const matchedId = (machine.deviceIds ?? [machine.deviceId]).find((id) => metered.data!.has(id));
        return matchedId ? [{ ...machine, deviceId: matchedId }] : [];
      })
      : null,
    isPending: machines === null || metered.isPending,
    isError: metered.isError,
  };
}
