import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useMemo } from 'react';
import type { SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { periodWindow, siteDayKey, type EnergyPeriod } from '@/features/energy-analysis/consumption/model';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import type { PlantDevice } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { energyBreakdown, EQUIPMENT_ORDER, powerBuckets, type EquipmentCategory } from './breakdown-model';

const client = createS2TelemetryClient();
const siteRoute = getRouteApi('/_app/_site');

const POWER_KEY: Readonly<Record<EquipmentCategory, string>> = {
  CHILLER: 'chiller.power',
  CHILLED_WATER_PUMP: 'chwp.power',
  COOLING_WATER_PUMP: 'cwp.power',
  COOLING_TOWER: 'cooling_tower.power',
};

export interface MeteredMachine {
  readonly deviceId: string;
  readonly name: string;
  readonly category: EquipmentCategory;
  readonly pointCode: string;
}

/** The plant's machines with the power point each reports. */
export function meteredMachines(devices: readonly PlantDevice[], model: Pick<SiteAssetModel, 'telemetryPoints'>): MeteredMachine[] {
  return devices.flatMap((device) => {
    if (!(EQUIPMENT_ORDER as readonly string[]).includes(device.category)) return [];
    const category = device.category as EquipmentCategory;
    const point = model.telemetryPoints.find((candidate) =>
      candidate.reportingDeviceId === device.deviceId && candidate.sourceKey === POWER_KEY[category] && candidate.status === 'ACTIVE');
    return point ? [{ deviceId: device.deviceId, name: device.name, category, pointCode: point.pointCode }] : [];
  });
}

/** Hourly machine power and metered energy over a period, split by machine. */
export function useEnergyBreakdown(period: Exclude<EnergyPeriod, 'year'>) {
  const { site, principal } = siteRoute.useRouteContext();
  const { read } = useEnergyReader();
  const { plant, registry } = useRealtimePlant();
  const machines = useMemo(
    () => (registry.data ? meteredMachines(plant.devices, registry.data.assetModel) : null),
    [registry.data, plant.devices],
  );
  const query = useQuery({
    queryKey: ['energy-breakdown', site.id, period, machines?.map((machine) => machine.deviceId).join(',')],
    enabled: machines !== null,
    queryFn: async ({ signal }) => {
      const { from, to } = periodWindow(period, new Date(), site.timezone);
      const [metered, ...series] = await Promise.all([
        read('electricity', 'hour', from, to, signal),
        ...machines!.map((machine) => client.queryDeviceHistoryAggregate({
          deviceId: machine.deviceId,
          keys: [machine.pointCode],
          from: from.toISOString(),
          to: to.toISOString(),
          granularity: 'HOUR',
          timezone: site.timezone,
          qualityPolicy: 'USABLE',
        }, { csrfToken: principal.session.csrfToken, signal })),
      ]);
      const bucketKey = period === 'today'
        ? (periodStart: string) => new Date(periodStart).toISOString()
        : (periodStart: string) => siteDayKey(new Date(periodStart), site.timezone);
      return energyBreakdown(
        machines!.map((machine, index) => ({ ...machine, buckets: powerBuckets(series[index].buckets) })),
        metered.points,
        bucketKey,
      );
    },
    refetchInterval: 5 * 60_000,
    retry: false,
  });
  return { machines, registry, query };
}
