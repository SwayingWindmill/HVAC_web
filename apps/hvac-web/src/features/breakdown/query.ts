import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { periodWindow, siteDayKey, type EnergyPeriod } from '@/features/energy-analysis/consumption/model';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import type { PlantDevice } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { energyBreakdown, EQUIPMENT_ORDER, type EquipmentCategory, type Machine } from './breakdown-model';

/** The plant's machines that use electricity, metered or not. */
export function plantMachines(devices: readonly PlantDevice[]): Machine[] {
  return devices.flatMap((device) => ((EQUIPMENT_ORDER as readonly string[]).includes(device.category)
    ? [{ deviceId: device.deviceId, name: device.name, category: device.category as EquipmentCategory }]
    : []));
}

/** The HVAC total meter and each machine's sub-meter over a period, from the Energy analytics owner. */
export function useEnergyBreakdown(period: Exclude<EnergyPeriod, 'year'>) {
  const { site, read } = useEnergyReader();
  const { plant, registry } = useRealtimePlant();
  // The plant's devices come from the Registry asset model; until it loads there is no machine list.
  const machines = useMemo(() => (registry.data ? plantMachines(plant.devices) : null), [registry.data, plant.devices]);
  const query = useQuery({
    queryKey: ['energy-breakdown', site.id, period, machines?.map((machine) => machine.deviceId).join(',')],
    enabled: machines !== null,
    queryFn: async ({ signal }) => {
      const { granularity, from, to } = periodWindow(period, new Date(), site.timezone);
      const data = await read('electricity', granularity, from, to, signal, 'device');
      const bucketKey = granularity === 'hour'
        ? (periodStart: string) => new Date(periodStart).toISOString()
        : (periodStart: string) => siteDayKey(new Date(periodStart), site.timezone);
      return energyBreakdown(machines!, data.points, data.devices ?? [], bucketKey);
    },
    refetchInterval: 5 * 60_000,
    retry: false,
  });
  return { machines, registry, query };
}
