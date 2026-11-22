import type { SiteAssetModel } from '../../api/generated/platformGateway.gen.ts';
import { buildAssetsRows } from '../assets/model.ts';
import { buildPlantView, type PlantDevice } from '../operations/realtime/plant-model.ts';
import { isOneOf } from '../../lib/typed.ts';
import { EQUIPMENT_ORDER, type Machine } from './breakdown-model.ts';

/** The plant's machines that use electricity, metered or not. */
export function plantMachines(devices: readonly PlantDevice[]): Machine[] {
  return devices.flatMap((device) => (isOneOf(EQUIPMENT_ORDER, device.category)
    ? [{ deviceId: device.deviceId, name: device.name, category: device.category }]
    : []));
}

/** Derives ordered electricity-consuming machines from the Site Asset Model without snapshot/stream sessions. */
export function plantMachinesFromAssetModel(assetModel: SiteAssetModel): Machine[] {
  const rows = buildAssetsRows({ assetModel });
  return plantMachines(buildPlantView(rows, assetModel).devices);
}
