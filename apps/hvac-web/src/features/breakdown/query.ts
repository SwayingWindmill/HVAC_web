import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useMemo } from 'react';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import { periodWindow, siteDayKey, type EnergyPeriod } from '@/features/energy-analysis/consumption/model';
import type { PlantDevice } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { energyBreakdown, EQUIPMENT_ORDER, type EquipmentCategory, type Machine } from './breakdown-model';

const client = createPlatformGatewayClient();
const siteRoute = getRouteApi('/_app/_site');

/** The plant's machines that use electricity, metered or not. */
export function plantMachines(devices: readonly PlantDevice[]): Machine[] {
  return devices.flatMap((device) => ((EQUIPMENT_ORDER as readonly string[]).includes(device.category)
    ? [{ deviceId: device.deviceId, name: device.name, category: device.category as EquipmentCategory }]
    : []));
}

/** The HVAC total meter and each machine's sub-meter over a period, from the Energy analytics owner. */
export function useEnergyBreakdown(period: Exclude<EnergyPeriod, 'year'>) {
  const { site, principal } = siteRoute.useRouteContext();
  const { plant, registry } = useRealtimePlant();
  const machines = useMemo(() => (registry.data ? plantMachines(plant.devices) : null), [registry.data, plant.devices]);
  const query = useQuery({
    queryKey: ['energy-breakdown', site.id, period, machines?.map((machine) => machine.deviceId).join(',')],
    enabled: machines !== null,
    queryFn: async ({ signal }) => {
      const { granularity, from, to } = periodWindow(period, new Date(), site.timezone);
      const { data } = await client.queryEnergySeries({
        tenantId: site.tenantId, siteId: site.id, energyType: 'electricity', granularity, timezone: site.timezone,
        from: from.toISOString(), to: to.toISOString(), qualityPolicy: 'VALID_ONLY', breakdown: 'device',
      }, principal.session.csrfToken, { signal });
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
