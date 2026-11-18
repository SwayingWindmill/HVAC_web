// Reads the plant from its owners: the Registry asset model through the gateway (with conditional
// ETag revalidation per tenant and site), then every device's current values in as few batches as allowed.

import {
  ASSET_MODEL_STORAGE_PREFIX,
  createAssetModelStore,
  type AssetModelStorage,
  type RawSiteAssetModel,
} from './asset-model-cache';
import type { Gateway } from './gateway';
import {
  buildPlant,
  clearAssetModelCache,
  deviceName,
  fetchPlantDevice,
  snapshotBatches,
  type Plant,
  type PlantDevice,
  type SnapshotsResponse,
} from './plant';
import { gateway, registerSignOutHandler, sessionToken } from './platform';
import type { Site } from './site';

export type { RawAssetRelationshipRecord, RawSiteAssetModel, RawTelemetryPointRecord } from './asset-model-cache';

const wxAssetModelStorage: AssetModelStorage = {
  get: (key) => {
    try {
      return wx.getStorageSync(key);
    } catch {
      return undefined;
    }
  },
  set: (key, value) => {
    try {
      wx.setStorageSync(key, value);
    } catch {
      // Ignore storage quota errors; in-memory cache holds the model for this session.
    }
  },
  remove: (key) => {
    try {
      wx.removeStorageSync(key);
    } catch {
      // Ignore missing key cleanup errors.
    }
  },
  listKeys: () => {
    try {
      return wx.getStorageInfoSync().keys.filter((key) => key.startsWith(ASSET_MODEL_STORAGE_PREFIX));
    } catch {
      return [];
    }
  },
};

const assetModelStore = createAssetModelStore({
  client: gateway,
  storage: wxAssetModelStorage,
  sessionToken,
});

export function clearPlantCache(): void {
  clearAssetModelCache();
  assetModelStore.clear();
}

registerSignOutHandler(clearPlantCache);

/** Reads the Site's asset model including control metadata, backed by per-tenant/site ETag cache. */
export function loadSiteAssetModel(site: Pick<Site, 'id' | 'tenantId'>, fresh = false): Promise<RawSiteAssetModel> {
  return assetModelStore.read(site.tenantId, site.id, fresh);
}

/** The plant now; `fresh` revalidates the registered equipment (with If-None-Match) and reloads snapshots. */
export async function loadPlant(site: Site, fresh = false): Promise<Plant> {
  const model = await loadSiteAssetModel(site, fresh);
  const responses = await Promise.all(snapshotBatches(model).map((requests) =>
    gateway.post<SnapshotsResponse>('/api/v1/telemetry/observation-snapshots:batchGet', { requests })));
  return buildPlant(model, { items: responses.flatMap((response) => response.items) }, new Date());
}

/** One device's current readings via the single-device observation-snapshot endpoint. */
export function loadPlantDevice(site: Site, deviceId: string, fresh = false): Promise<PlantDevice | null> {
  const assetModelPath = `/api/v1/sites/${site.id}/asset-model`;
  const client: Pick<Gateway, 'get'> = {
    get: <T>(path: string) => (
      path === assetModelPath
        ? (loadSiteAssetModel(site, fresh) as Promise<T>)
        : gateway.get<T>(path)
    ),
  };
  return fetchPlantDevice(client, site.id, deviceId, fresh);
}

/** The name operators know a device by, from the Site's registered equipment. */
export async function siteDeviceName(site: Site, deviceId: string): Promise<string | null> {
  return deviceName(await loadSiteAssetModel(site, false), deviceId, new Date());
}
