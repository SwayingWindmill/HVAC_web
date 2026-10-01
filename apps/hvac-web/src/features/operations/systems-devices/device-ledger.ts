import type { PlantDevice } from '../realtime/plant-model';

/** Offline, in fault, or reporting stale data: someone should look at it. */
export function needsAttention(device: PlantDevice): boolean {
  return device.connection === 'OFFLINE' || device.runState === 'FAULT' || device.hasStaleData;
}

export function deviceLocation(device: PlantDevice): string {
  return device.row.space.state === 'bound' ? device.row.space.space.displayName : '未登记';
}

/** The Asset the device is bound to; work orders about equipment reference it. */
export function boundAssetId(device: PlantDevice): string | null {
  return device.row.binding.state === 'bound' ? device.row.binding.asset.id : null;
}
