import type { SiteAssetModel } from '../../api/generated/platformGateway.gen.ts';
import { buildAssetsRows } from '../assets/model.ts';
import { buildPlantView, type PlantDevice, type PlantUnit } from '../operations/realtime/plant-model.ts';
import { isOneOf } from '../../lib/typed.ts';
import { EQUIPMENT_ORDER, type Machine } from './breakdown-model.ts';

const METER_DEVICE_TYPES = new Set(['HVAC_POWER_METER', 'POWER_METER']);

function preferredEnergyDeviceId(unit: PlantUnit | PlantDevice): string {
  const members = 'members' in unit ? unit.members : [unit];
  const withCounter = members.find((member) =>
    member.row.telemetryPoints.some((point) => point.status === 'ACTIVE' && point.pointType === 'COUNTER'));
  if (withCounter) return withCounter.deviceId;
  const withMeter = members.find((member) => METER_DEVICE_TYPES.has(member.row.device.deviceType));
  return withMeter?.deviceId ?? unit.deviceId;
}

/** The plant's physical machines that use electricity, metered or not. */
export function plantMachines(units: readonly (PlantUnit | PlantDevice)[]): Machine[] {
  return units.flatMap((unit) => {
    if (!isOneOf(EQUIPMENT_ORDER, unit.category)) return [];
    const deviceIds = 'deviceIds' in unit ? unit.deviceIds : [unit.deviceId];
    return [{
      deviceId: preferredEnergyDeviceId(unit),
      ...(deviceIds.length > 1 ? { deviceIds } : {}),
      name: unit.name,
      category: unit.category,
    }];
  });
}

/** Derives ordered electricity-consuming machines from the Site Asset Model without snapshot/stream sessions. */
export function plantMachinesFromAssetModel(assetModel: SiteAssetModel): Machine[] {
  const rows = buildAssetsRows({ assetModel });
  return plantMachines(buildPlantView(rows, assetModel).units);
}
