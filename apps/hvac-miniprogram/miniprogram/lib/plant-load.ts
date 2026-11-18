// Reads the plant from its owners: the Registry asset model through the gateway, then every device's
// current values in as few batches as the gateway allows.

import {
  buildPlant,
  deviceName,
  fetchPlantDevice,
  readAssetModel,
  snapshotBatches,
  type Plant,
  type PlantDevice,
  type SnapshotsResponse,
} from './plant';
import { gateway } from './platform';
import type { Site } from './site';

/** The plant now; `fresh` also re-reads the registered equipment (a pull-down refresh). */
export async function loadPlant(site: Site, fresh = false): Promise<Plant> {
  const model = await readAssetModel(gateway, site.id, fresh);
  const responses = await Promise.all(snapshotBatches(model).map((requests) =>
    gateway.post<SnapshotsResponse>('/api/v1/telemetry/observation-snapshots:batchGet', { requests })));
  return buildPlant(model, { items: responses.flatMap((response) => response.items) }, new Date());
}

/** One device's current readings via the single-device observation-snapshot endpoint. */
export function loadPlantDevice(site: Site, deviceId: string, fresh = false): Promise<PlantDevice | null> {
  return fetchPlantDevice(gateway, site.id, deviceId, fresh);
}

/** The name operators know a device by, from the Site's registered equipment. */
export async function siteDeviceName(site: Site, deviceId: string): Promise<string | null> {
  return deviceName(await readAssetModel(gateway, site.id, false), deviceId, new Date());
}
