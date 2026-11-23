import type { PlantDevice, PlantUnit } from '../realtime/plant-model';

/** Offline, partially offline across bound devices, in fault, or reporting stale data. */
export function needsAttention(device: PlantUnit | PlantDevice): boolean {
  const partialOffline = 'offlineMemberCount' in device && device.offlineMemberCount > 0;
  return device.connection === 'OFFLINE' || partialOffline || device.runState === 'FAULT' || device.hasStaleData;
}

export function deviceLocation(device: PlantUnit | PlantDevice): string {
  const members = 'members' in device ? device.members : [device];
  for (const member of members) {
    if (member.row.space.state === 'bound') return member.row.space.space.displayName;
  }
  return '未登记';
}

/** The Asset the unit is bound to; work orders about equipment reference it. */
export function boundAssetId(device: PlantUnit | PlantDevice): string | null {
  return device.assetId ?? (device.row.binding.state === 'bound' ? device.row.binding.asset.id : null);
}

/** Merged `key:value` tag tokens from all bound Devices and their bound Asset(s). */
export function deviceTagTokens(device: PlantUnit | PlantDevice): string[] {
  const tokens = new Set<string>();
  const addTags = (tags?: Readonly<Record<string, string>>) => {
    if (!tags) return;
    for (const [key, value] of Object.entries(tags)) {
      tokens.add(`${key}:${value}`);
    }
  };
  const members = 'members' in device ? device.members : [device];
  for (const member of members) {
    addTags(member.row.device.tags);
    if (member.row.binding.state === 'bound') {
      addTags(member.row.binding.asset.tags);
    } else if (member.row.binding.state === 'multi-bound') {
      for (const item of member.row.binding.bindings) {
        addTags(item.asset.tags);
      }
    }
  }
  return Array.from(tokens).sort((left, right) => left.localeCompare(right));
}
