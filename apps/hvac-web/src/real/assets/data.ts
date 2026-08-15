import type {
  Device,
  PlatformGatewayClient,
  SiteAssetModel,
  TelemetryPoint,
} from '../../api/generated/platformGateway.gen.ts';
import type {
  BatchObservationFailure,
  DeviceObservationSnapshot,
  S2TelemetryClient,
} from '../../api/generated/s2Telemetry.gen.ts';
import { parseSnapshot } from '../../platform/telemetry-live/contract.ts';
import { REAL_ASSETS_CATALOG_REVISION } from './catalog.ts';
import type { RealAssetsSnapshotResult } from './model.ts';

const MAX_BATCH_DEVICES = 100;
const MAX_BATCH_KEY_SELECTIONS = 2048;

export interface RealAssetsRegistryClient {
  getSiteAssetModel: PlatformGatewayClient['getSiteAssetModel'];
}

export interface RealAssetsRegistryData {
  readonly assetModel: SiteAssetModel;
  readonly routePolicyRevision: string | null;
}

export interface LoadRealAssetsRegistryInput {
  readonly client: RealAssetsRegistryClient;
  readonly tenantId: string;
  readonly siteId: string;
  readonly signal: AbortSignal;
}

export interface LoadRealAssetsCurrentStateInput {
  readonly client: S2TelemetryClient;
  readonly devices: readonly Device[];
  readonly telemetryPoints: readonly TelemetryPoint[];
  readonly tenantId: string;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly currentRoutePolicyRevision: () => string | null;
  readonly signal: AbortSignal;
}

export interface RealAssetsCurrentStateData {
  readonly byDeviceId: ReadonlyMap<string, RealAssetsSnapshotResult>;
  readonly partial: boolean;
  readonly requestCount: number;
  readonly routePolicyRevision: string | null;
}

function assertUnique(values: readonly string[], label: string): void {
  if (new Set(values).size !== values.length) throw new Error(`${label} contains duplicate identities`);
}

function validateRegistryScope<T extends { id: string; tenantId: string; siteId: string }>(
  items: readonly T[],
  tenantId: string,
  siteId: string,
  label: string,
): void {
  assertUnique(items.map((item) => item.id), label);
  if (items.some((item) => item.tenantId !== tenantId || item.siteId !== siteId)) {
    throw new Error(`${label} crossed the Tenant or Site scope`);
  }
}

function validateAssetModel(model: SiteAssetModel, tenantId: string, siteId: string): void {
  if (model.schemaVersion !== 2 || model.tenantId !== tenantId || model.siteId !== siteId) {
    throw new Error('Asset Model envelope escaped the requested Tenant or Site scope');
  }
  validateRegistryScope(model.areas, tenantId, siteId, 'Area collection');
  validateRegistryScope(model.equipment, tenantId, siteId, 'Equipment collection');
  validateRegistryScope(model.devices, tenantId, siteId, 'Device Endpoint collection');
  validateRegistryScope(model.sensors, tenantId, siteId, 'Sensor collection');
  validateRegistryScope(model.telemetryPoints, tenantId, siteId, 'Telemetry Point collection');
  validateRegistryScope(model.relationships, tenantId, siteId, 'Asset relationship collection');

  const ids = {
    SITE: new Set([siteId]),
    AREA: new Set(model.areas.map((item) => item.id)),
    EQUIPMENT: new Set(model.equipment.map((item) => item.id)),
    DEVICE: new Set(model.devices.map((item) => item.id)),
    SENSOR: new Set(model.sensors.map((item) => item.id)),
    POINT: new Set(model.telemetryPoints.map((item) => item.id)),
  } as const;
  for (const area of model.areas) {
    if (area.parentAreaId && !ids.AREA.has(area.parentAreaId)) throw new Error('Area collection referenced an invisible parent Area');
  }
  for (const relationship of model.relationships) {
    if (!ids[relationship.fromType].has(relationship.fromId) || !ids[relationship.toType].has(relationship.toId)) {
      throw new Error('Asset relationship collection referenced a resource outside the visible Site model');
    }
  }
  for (const point of model.telemetryPoints) {
    if (!ids.DEVICE.has(point.reportingDeviceId) || (point.sensorId && !ids.SENSOR.has(point.sensorId))) {
      throw new Error('Telemetry Point collection referenced an invisible Device Endpoint or Sensor');
    }
  }

  const expectedCounts = {
    areas: model.areas.length,
    equipment: model.equipment.length,
    deviceEndpoints: model.devices.length,
    physicalSensors: model.sensors.length,
    points: model.telemetryPoints.length,
  };
  if (Object.entries(expectedCounts).some(([key, value]) => model.counts[key as keyof typeof expectedCounts] !== value)) {
    throw new Error('Asset Model counts do not match the returned relationship graph');
  }
}

export async function loadRealAssetsRegistry(input: LoadRealAssetsRegistryInput): Promise<RealAssetsRegistryData> {
  const response = await input.client.getSiteAssetModel(input.siteId, { signal: input.signal });
  validateAssetModel(response.data, input.tenantId, input.siteId);
  return { assetModel: response.data, routePolicyRevision: response.routePolicyRevision };
}

function pointCodesByDevice(
  devices: readonly Device[],
  telemetryPoints: readonly TelemetryPoint[],
): ReadonlyMap<string, readonly string[]> {
  const visibleDeviceIds = new Set(devices.map((device) => device.id));
  const keysByDevice = new Map<string, string[]>();
  for (const point of telemetryPoints) {
    if (!visibleDeviceIds.has(point.reportingDeviceId)) {
      throw new Error('Current-state Telemetry Point selection referenced an invisible Device Endpoint');
    }
    const keys = keysByDevice.get(point.reportingDeviceId) ?? [];
    if (keys.includes(point.pointCode)) {
      throw new Error(`Current-state Telemetry Point selection duplicated ${point.pointCode} for ${point.reportingDeviceId}`);
    }
    keys.push(point.pointCode);
    keysByDevice.set(point.reportingDeviceId, keys);
  }
  return new Map(devices.map((device) => [
    device.id,
    Object.freeze([...(keysByDevice.get(device.id) ?? [])].sort((left, right) => left.localeCompare(right))),
  ]));
}

function expectedDisplayState(snapshot: DeviceObservationSnapshot): DeviceObservationSnapshot['displayState'] {
  if (snapshot.presence.applicability === 'NOT_APPLICABLE') return null;
  if (snapshot.evaluationAvailability === 'UNAVAILABLE') return 'UNAVAILABLE';
  if (snapshot.presence.currentState === 'OFFLINE') return 'OFFLINE';
  if (snapshot.presence.currentState === null
    || snapshot.presence.currentState === 'UNKNOWN'
    || snapshot.telemetryReadiness === 'INCOMPLETE') return 'UNKNOWN';
  if (snapshot.presence.currentState === 'ONLINE' && snapshot.telemetryReadiness === 'DEGRADED') return 'STALE';
  if (snapshot.presence.currentState === 'ONLINE'
    && (snapshot.telemetryReadiness === 'CURRENT' || snapshot.telemetryReadiness === 'NOT_APPLICABLE')) return 'ONLINE';
  return 'UNKNOWN';
}

function validateSnapshot(snapshot: DeviceObservationSnapshot, device: Device, keys: readonly string[]): DeviceObservationSnapshot {
  const parsed = parseSnapshot(snapshot, device.id, keys);
  if (parsed.tenantId !== device.tenantId || parsed.siteId !== device.siteId) {
    throw new Error(`Device Snapshot scope or selected-key order drifted for ${device.id}`);
  }
  if (parsed.displayState !== expectedDisplayState(parsed)) {
    throw new Error(`Device Snapshot display-state evidence drifted for ${device.id}`);
  }
  return parsed;
}

interface CurrentStateSelection {
  readonly device: Device;
  readonly keys: readonly string[];
}

function chunkSelections(selections: readonly CurrentStateSelection[]): CurrentStateSelection[][] {
  const chunks: CurrentStateSelection[][] = [];
  let current: CurrentStateSelection[] = [];
  let keyCount = 0;
  for (const selection of selections) {
    if (selection.keys.length > MAX_BATCH_KEY_SELECTIONS) {
      throw new Error(`Current-state Device ${selection.device.id} exceeded the key selection limit`);
    }
    if (current.length > 0
      && (current.length >= MAX_BATCH_DEVICES || keyCount + selection.keys.length > MAX_BATCH_KEY_SELECTIONS)) {
      chunks.push(current);
      current = [];
      keyCount = 0;
    }
    current.push(selection);
    keyCount += selection.keys.length;
  }
  if (current.length > 0) chunks.push(current);
  return chunks;
}

export async function loadRealAssetsCurrentState(input: LoadRealAssetsCurrentStateInput): Promise<RealAssetsCurrentStateData> {
  validateRegistryScope(input.devices, input.tenantId, input.siteId, 'Current-state Device selection');
  validateRegistryScope(input.telemetryPoints, input.tenantId, input.siteId, 'Current-state Telemetry Point selection');
  if (!input.csrfToken) throw new Error('Current-state batch requires the authenticated Session CSRF capability');
  const byDeviceId = new Map<string, RealAssetsSnapshotResult>();
  const keysByDevice = pointCodesByDevice(input.devices, input.telemetryPoints);
  const chunks = chunkSelections(input.devices.map((device) => ({
    device,
    keys: keysByDevice.get(device.id) ?? [],
  })));
  let routePolicyRevision: string | null | undefined;

  for (let chunkIndex = 0; chunkIndex < chunks.length; chunkIndex += 1) {
    const expected = chunks[chunkIndex].map((selection, index) => ({
      ...selection,
      requestId: `assets-${chunkIndex}-${index}-${selection.device.id}`,
    }));

    const response = await input.client.batchGetDeviceObservationSnapshots({
      requests: expected.map((item) => ({ requestId: item.requestId, deviceId: item.device.id, keys: [...item.keys] })),
    }, {
      csrfToken: input.csrfToken,
      signal: input.signal,
      requestId: `real-assets-${chunkIndex}`,
    });
    const responseRoutePolicyRevision = input.currentRoutePolicyRevision();
    if (routePolicyRevision === undefined) {
      routePolicyRevision = responseRoutePolicyRevision;
    } else if (routePolicyRevision !== responseRoutePolicyRevision) {
      throw new Error('Current-state route-policy revision changed during bounded batch loading');
    }
    if (response.schemaVersion !== 1 || response.items.length !== expected.length) {
      throw new Error('Current-state batch returned an unsupported or incomplete envelope');
    }
    response.items.forEach((item, index) => {
      const target = expected[index];
      if (item.requestId !== target.requestId || item.deviceId !== target.device.id) {
        throw new Error('Current-state batch response order or Device scope drifted');
      }
      if (item.status === 'OK') {
        byDeviceId.set(item.deviceId, { status: 'ok', snapshot: validateSnapshot(item.snapshot, target.device, target.keys) });
      } else {
        byDeviceId.set(item.deviceId, { status: 'error', problem: (item as BatchObservationFailure).problem });
      }
    });
  }

  return {
    byDeviceId,
    partial: [...byDeviceId.values()].some((result) => result.status === 'error'),
    requestCount: chunks.length,
    routePolicyRevision: routePolicyRevision ?? null,
  };
}

export function realAssetsRegistryQueryKey(
  generation: number,
  tenantId: string,
  siteId: string,
  routePolicyEpoch = 0,
): readonly unknown[] {
  return ['real-assets', generation, tenantId, siteId, 'asset-model', routePolicyEpoch] as const;
}

export function realAssetsCurrentStateQueryKey(
  generation: number,
  tenantId: string,
  siteId: string,
  devices: readonly Device[],
  telemetryPoints: readonly TelemetryPoint[],
  routePolicyEpoch = 0,
): readonly unknown[] {
  const keysByDevice = pointCodesByDevice(devices, telemetryPoints);
  return [
    'real-assets',
    generation,
    tenantId,
    siteId,
    'current-state',
    REAL_ASSETS_CATALOG_REVISION,
    routePolicyEpoch,
    devices.map((device) => `${device.id}:${device.revision}:${(keysByDevice.get(device.id) ?? []).join(',')}`).join('|'),
    telemetryPoints.map((point) => `${point.id}:${point.revision}:${point.reportingDeviceId}:${point.pointCode}`).sort().join('|'),
  ] as const;
}
