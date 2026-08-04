import type {
  Device,
  PlatformGatewayClient,
  SiteAssetModel,
} from '../../api/generated/platformGateway.gen.ts';
import type {
  BatchObservationFailure,
  DeviceObservationSnapshot,
  S2TelemetryClient,
} from '../../api/generated/s2Telemetry.gen.ts';
import { parseSnapshot } from '../../platform/telemetry-live/contract.ts';
import { REAL_ASSETS_CATALOG_REVISION, listTelemetryKeys, resolveRealAssetsProfile } from './catalog.ts';
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
  readonly organizationId: string;
  readonly siteId: string;
  readonly signal: AbortSignal;
}

export interface LoadRealAssetsCurrentStateInput {
  readonly client: S2TelemetryClient;
  readonly devices: readonly Device[];
  readonly organizationId: string;
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

function validateRegistryScope<T extends { id: string; owningOrganizationId: string; siteId: string }>(
  items: readonly T[],
  organizationId: string,
  siteId: string,
  label: string,
): void {
  assertUnique(items.map((item) => item.id), label);
  if (items.some((item) => item.owningOrganizationId !== organizationId || item.siteId !== siteId)) {
    throw new Error(`${label} crossed the authenticated Organization or Site scope`);
  }
}

function validateAssetModel(model: SiteAssetModel, organizationId: string, siteId: string): void {
  if (model.schemaVersion !== 1 || model.siteId !== siteId) throw new Error('Asset Model envelope escaped the requested Site scope');
  validateRegistryScope(model.areas, organizationId, siteId, 'Area collection');
  validateRegistryScope(model.equipment, organizationId, siteId, 'Equipment collection');
  validateRegistryScope(model.devices, organizationId, siteId, 'Device Endpoint collection');
  validateRegistryScope(model.sensors, organizationId, siteId, 'Sensor collection');
  validateRegistryScope(model.telemetryPoints, organizationId, siteId, 'Telemetry Point collection');
  validateRegistryScope(model.relationships, organizationId, siteId, 'Asset relationship collection');
  assertUnique(model.calculatedPointInputs.map((input) => `${input.calculatedPointId}:${input.ordinal}`), 'Calculated Point input collection');
  if (model.calculatedPointInputs.some((input) => input.owningOrganizationId !== organizationId || input.siteId !== siteId)) {
    throw new Error('Calculated Point input collection crossed the authenticated Organization or Site scope');
  }

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
  for (const input of model.calculatedPointInputs) {
    const calculatedPoint = model.telemetryPoints.find((point) => point.id === input.calculatedPointId);
    if (calculatedPoint?.pointKind !== 'CALCULATED'
      || !ids.POINT.has(input.inputPointId)
      || input.formulaRevision !== calculatedPoint.formulaRevision) {
      throw new Error('Calculated Point input collection referenced an invalid Point or formula revision');
    }
  }

  const independentSensorDevices = new Set(model.relationships
    .filter((relationship) => relationship.fromType === 'SENSOR'
      && relationship.toType === 'DEVICE'
      && relationship.role === 'INDEPENDENT_DEVICE'
      && relationship.status === 'ACTIVE'
      && relationship.validTo === null)
    .map((relationship) => relationship.toId)).size;
  const expectedCounts = {
    areas: model.areas.length,
    equipment: model.equipment.length,
    deviceEndpoints: model.devices.length,
    sensors: model.sensors.length,
    telemetryPoints: model.telemetryPoints.length,
    calculatedPoints: model.telemetryPoints.filter((point) => point.pointKind === 'CALCULATED').length,
    independentSensorDevices,
  };
  if (Object.entries(expectedCounts).some(([key, value]) => model.counts[key as keyof typeof expectedCounts] !== value)) {
    throw new Error('Asset Model counts do not match the returned relationship graph');
  }
}

export async function loadRealAssetsRegistry(input: LoadRealAssetsRegistryInput): Promise<RealAssetsRegistryData> {
  const response = await input.client.getSiteAssetModel(input.siteId, { signal: input.signal });
  validateAssetModel(response.data, input.organizationId, input.siteId);
  return { assetModel: response.data, routePolicyRevision: response.routePolicyRevision };
}

function requestedKeys(device: Device): string[] {
  return [...listTelemetryKeys(resolveRealAssetsProfile(device.deviceType))];
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
  if (parsed.owningOrganizationId !== device.owningOrganizationId || parsed.siteId !== device.siteId) {
    throw new Error(`Device Snapshot scope or selected-key order drifted for ${device.id}`);
  }
  if (parsed.displayState !== expectedDisplayState(parsed)) {
    throw new Error(`Device Snapshot display-state evidence drifted for ${device.id}`);
  }
  return parsed;
}

function chunkDevices(devices: readonly Device[]): Device[][] {
  const chunks: Device[][] = [];
  for (let index = 0; index < devices.length; index += MAX_BATCH_DEVICES) {
    chunks.push(devices.slice(index, index + MAX_BATCH_DEVICES));
  }
  return chunks;
}

export async function loadRealAssetsCurrentState(input: LoadRealAssetsCurrentStateInput): Promise<RealAssetsCurrentStateData> {
  validateRegistryScope(input.devices, input.organizationId, input.siteId, 'Current-state Device selection');
  if (!input.csrfToken) throw new Error('Current-state batch requires the authenticated Session CSRF capability');
  const byDeviceId = new Map<string, RealAssetsSnapshotResult>();
  const chunks = chunkDevices(input.devices);
  let routePolicyRevision: string | null | undefined;

  for (let chunkIndex = 0; chunkIndex < chunks.length; chunkIndex += 1) {
    const chunk = chunks[chunkIndex];
    const expected = chunk.map((device, index) => ({
      device,
      requestId: `assets-${chunkIndex}-${index}-${device.id}`,
      keys: requestedKeys(device),
    }));
    const selectionCount = expected.reduce((total, item) => total + item.keys.length, 0);
    if (selectionCount > MAX_BATCH_KEY_SELECTIONS) throw new Error('Current-state batch exceeded the total key selection limit');

    const response = await input.client.batchGetDeviceObservationSnapshots({
      requests: expected.map((item) => ({ requestId: item.requestId, deviceId: item.device.id, keys: item.keys })),
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
  organizationId: string,
  siteId: string,
  routePolicyEpoch = 0,
): readonly unknown[] {
  return ['real-assets', generation, organizationId, siteId, 'asset-model', routePolicyEpoch] as const;
}

export function realAssetsCurrentStateQueryKey(
  generation: number,
  organizationId: string,
  siteId: string,
  devices: readonly Device[],
  routePolicyEpoch = 0,
): readonly unknown[] {
  return [
    'real-assets',
    generation,
    organizationId,
    siteId,
    'current-state',
    REAL_ASSETS_CATALOG_REVISION,
    routePolicyEpoch,
    devices.map((device) => `${device.id}:${device.revision}:${requestedKeys(device).join(',')}`).join('|'),
  ] as const;
}
