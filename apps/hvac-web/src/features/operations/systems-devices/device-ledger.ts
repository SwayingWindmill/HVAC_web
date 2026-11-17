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

/** Merged `key:value` tag tokens from the Device and its bound Asset(s). */
export function deviceTagTokens(device: PlantDevice): string[] {
  const tokens = new Set<string>();
  const addTags = (tags?: Readonly<Record<string, string>>) => {
    if (!tags) return;
    for (const [key, value] of Object.entries(tags)) {
      tokens.add(`${key}:${value}`);
    }
  };
  addTags(device.row.device.tags);
  if (device.row.binding.state === 'bound') {
    addTags(device.row.binding.asset.tags);
  } else if (device.row.binding.state === 'multi-bound') {
    for (const item of device.row.binding.bindings) {
      addTags(item.asset.tags);
    }
  }
  return Array.from(tokens).sort((left, right) => left.localeCompare(right));
}
