// Reads the plant from its owners: the Registry asset model through the gateway, then every device's
// current values in as few batches as the gateway allows.

import { buildPlant, snapshotBatches, type AssetModel, type Plant, type SnapshotsResponse } from './plant';
import { gateway } from './platform';
import type { Site } from './site';

// The asset model changes only when an administrator registers equipment; the current values change
// every few seconds. Keep the last Site's model while the mini program runs and re-read it on request.
let cached: { readonly siteId: string; readonly model: AssetModel } | null = null;

async function assetModel(site: Site, fresh: boolean): Promise<AssetModel> {
  if (!fresh && cached?.siteId === site.id) return cached.model;
  const model = await gateway.get<AssetModel>(`/api/v1/sites/${site.id}/asset-model`);
  cached = { siteId: site.id, model };
  return model;
}

/** The plant now; `fresh` also re-reads the registered equipment (a pull-down refresh). */
export async function loadPlant(site: Site, fresh = false): Promise<Plant> {
  const model = await assetModel(site, fresh);
  const responses = await Promise.all(snapshotBatches(model).map((requests) =>
    gateway.post<SnapshotsResponse>('/api/v1/telemetry/observation-snapshots:batchGet', { requests })));
  return buildPlant(model, { items: responses.flatMap((response) => response.items) }, new Date());
}
