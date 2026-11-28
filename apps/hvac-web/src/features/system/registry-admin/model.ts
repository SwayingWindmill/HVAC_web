import type {
  Asset,
  Device,
  ImportPlan,
  MutationMeta,
  Site,
  SiteAssetModel,
  Space,
  TelemetryPoint,
} from '@/api/generated/platformGateway.gen';

export type RegistryAdminResourceType = 'SITE' | 'SPACE' | 'ASSET' | 'DEVICE' | 'POINT';

export interface RegistryExportV1 {
  schemaVersion: 1;
  exportedAt: string;
  site: Pick<Site, 'id' | 'tenantId' | 'code' | 'displayName' | 'timezone' | 'status' | 'revision'>;
  spaces: Array<Pick<Space, 'id' | 'siteId' | 'parentSpaceId' | 'code' | 'displayName' | 'spaceType' | 'status' | 'revision'>>;
  assets: Array<Pick<Asset, 'id' | 'siteId' | 'code' | 'displayName' | 'assetType' | 'status' | 'revision'>>;
  devices: Array<Pick<Device, 'id' | 'siteId' | 'code' | 'displayName' | 'deviceType' | 'status' | 'revision'>>;
  points: Array<Pick<TelemetryPoint, 'id' | 'siteId' | 'reportingDeviceId' | 'sensorId' | 'pointCode' | 'sourceKey' | 'displayName' | 'pointType' | 'valueType' | 'unit' | 'writable' | 'sampleIntervalMs' | 'publishIntervalMs' | 'staleAfterMs' | 'counterDecreaseMode' | 'counterRolloverModulus' | 'status' | 'revision'>>;
  relationships: SiteAssetModel['relationships'];
}

export function makeRegistryMutationMeta(expectedRevision: number, reason: string, idempotencyKey: string): MutationMeta {
  return { expectedRevision, reason: reason.trim(), idempotencyKey };
}

export function canCommitImportPlan(plan: ImportPlan | null): boolean {
  return Boolean(plan && plan.results.length > 0 && plan.results.every((row) => row.status === 'READY'));
}

export function buildRegistryExport(site: Site, model: SiteAssetModel, exportedAt: string): RegistryExportV1 {
  return {
    schemaVersion: 1,
    exportedAt,
    site: {
      id: site.id,
      tenantId: site.tenantId,
      code: site.code,
      displayName: site.displayName,
      timezone: site.timezone,
      status: site.status,
      revision: site.revision,
    },
    spaces: model.spaces.map((space) => ({
      id: space.id,
      siteId: space.siteId,
      parentSpaceId: space.parentSpaceId,
      code: space.code,
      displayName: space.displayName,
      spaceType: space.spaceType,
      status: space.status,
      revision: space.revision,
    })),
    assets: model.assets.map((asset) => ({
      id: asset.id,
      siteId: asset.siteId,
      code: asset.code,
      displayName: asset.displayName,
      assetType: asset.assetType,
      status: asset.status,
      revision: asset.revision,
    })),
    devices: model.devices.map((device) => ({
      id: device.id,
      siteId: device.siteId,
      code: device.code,
      displayName: device.displayName,
      deviceType: device.deviceType,
      status: device.status,
      revision: device.revision,
    })),
    points: model.telemetryPoints.map((point) => ({
      id: point.id,
      siteId: point.siteId,
      reportingDeviceId: point.reportingDeviceId,
      sensorId: point.sensorId,
      pointCode: point.pointCode,
      sourceKey: point.sourceKey,
      displayName: point.displayName,
      pointType: point.pointType,
      valueType: point.valueType,
      unit: point.unit,
      writable: point.writable,
      sampleIntervalMs: point.sampleIntervalMs,
      publishIntervalMs: point.publishIntervalMs,
      staleAfterMs: point.staleAfterMs,
      counterDecreaseMode: point.counterDecreaseMode,
      counterRolloverModulus: point.counterRolloverModulus,
      status: point.status,
      revision: point.revision,
    })),
    relationships: model.relationships,
  };
}

export function registryExportFileName(site: Site): string {
  return `registry-${site.code}-rev${site.revision}.json`;
}

export function newRegistryIdempotencyKey(action: string): string {
  return `${action}:${crypto.randomUUID()}`;
}
