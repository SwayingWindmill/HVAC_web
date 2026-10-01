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
    site: pick(site, ['id', 'tenantId', 'code', 'displayName', 'timezone', 'status', 'revision']),
    spaces: model.spaces.map((space) => pick(space, ['id', 'siteId', 'parentSpaceId', 'code', 'displayName', 'spaceType', 'status', 'revision'])),
    assets: model.assets.map((asset) => pick(asset, ['id', 'siteId', 'code', 'displayName', 'assetType', 'status', 'revision'])),
    devices: model.devices.map((device) => pick(device, ['id', 'siteId', 'code', 'displayName', 'deviceType', 'status', 'revision'])),
    points: model.telemetryPoints.map((point) => pick(point, [
      'id', 'siteId', 'reportingDeviceId', 'sensorId', 'pointCode', 'sourceKey', 'displayName', 'pointType', 'valueType', 'unit',
      'writable', 'sampleIntervalMs', 'publishIntervalMs', 'staleAfterMs', 'counterDecreaseMode', 'counterRolloverModulus', 'status', 'revision',
    ])),
    relationships: model.relationships,
  };
}

export function registryExportFileName(site: Site): string {
  return `registry-${site.code}-rev${site.revision}.json`;
}

export function newRegistryIdempotencyKey(action: string): string {
  return `${action}:${crypto.randomUUID()}`;
}

function pick<T extends object, K extends keyof T>(value: T, keys: readonly K[]): Pick<T, K> {
  const result = {} as Pick<T, K>;
  for (const key of keys) result[key] = value[key];
  return result;
}
