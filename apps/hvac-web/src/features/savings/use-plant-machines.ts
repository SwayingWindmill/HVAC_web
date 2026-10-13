import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useMemo } from 'react';
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';
import { plantMachines } from '@/features/breakdown/query';
import type { Machine } from '@/features/breakdown/breakdown-model';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import { buildPlantView } from '@/features/operations/realtime/plant-model';

const siteRoute = getRouteApi('/_app/_site');

// A sub-metered machine reports electricity of its own; a week is long enough to see one that is idle.
const METERED_LOOKBACK_MS = 7 * 24 * 3_600_000;

/**
 * The plant's machines, named as the plant page names them, from the Registry asset model. The
 * asset model is a protected resource registered once per page, so a page calls this once.
 */
export function usePlantMachines() {
  const { site, principal, runtime } = siteRoute.useRouteContext();
  const assets = useSiteAssetsData({ site, principal, runtime });
  const machines = useMemo(() => (assets.registry.data ? plantMachines(buildPlantView(assets.rows).devices) : null), [assets.registry.data, assets.rows]);
  const names = useMemo(() => (machines ? new Map(machines.map((machine) => [machine.deviceId, machine.name])) : null), [machines]);
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
    machines: machines && metered.data ? machines.filter((machine) => metered.data.has(machine.deviceId)) : null,
    isPending: machines === null || metered.isPending,
    isError: metered.isError,
  };
}
