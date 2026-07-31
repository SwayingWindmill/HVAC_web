import type {
  Device,
  DeviceBinding,
  DeviceBindingCollection,
  DeviceCollection,
  Equipment,
  EquipmentCollection,
  PlatformGatewayClient,
  RegistryListParams,
} from '../../api/generated/platformGateway.gen.ts';
import type {
  BatchObservationFailure,
  DeviceObservationSnapshot,
  S2TelemetryClient,
} from '../../api/generated/s2Telemetry.gen.ts';
import { parseSnapshot } from '../../platform/telemetry-live/contract.ts';
import { REAL_ASSETS_CATALOG_REVISION, listTelemetryKeys, resolveRealAssetsProfile } from './catalog.ts';
import type { RealAssetsSnapshotResult } from './model.ts';

const REGISTRY_PAGE_SIZE = 200;
const MAX_REGISTRY_PAGES = 12;
const MAX_BATCH_DEVICES = 100;
const MAX_BATCH_KEY_SELECTIONS = 2048;

export interface RealAssetsRegistryClient {
  listSiteEquipment: PlatformGatewayClient['listSiteEquipment'];
  listSiteDevices: PlatformGatewayClient['listSiteDevices'];
  listSiteDeviceBindings: PlatformGatewayClient['listSiteDeviceBindings'];
}

export interface RealAssetsRegistryData {
  readonly equipment: readonly Equipment[];
  readonly devices: readonly Device[];
  readonly bindings: readonly DeviceBinding[];
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

type RegistryCollection = EquipmentCollection | DeviceCollection | DeviceBindingCollection;
type RegistryPageResponse = {
  readonly data: RegistryCollection;
  readonly routePolicyRevision: string | null;
};
type CollectedRegistryCollection<T> = {
  readonly items: T[];
  readonly routePolicyRevision: string | null;
};

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

async function collectRegistryCollection<T>(
  fetchPage: (params: RegistryListParams, init: RequestInit) => Promise<RegistryPageResponse>,
  signal: AbortSignal,
): Promise<CollectedRegistryCollection<T>> {
  const items: T[] = [];
  let cursor: string | undefined;
  let routePolicyRevision: string | null | undefined;
  for (let page = 0; page < MAX_REGISTRY_PAGES; page += 1) {
    const response = await fetchPage({ limit: REGISTRY_PAGE_SIZE, cursor }, { signal });
    if (routePolicyRevision === undefined) {
      routePolicyRevision = response.routePolicyRevision;
    } else if (response.routePolicyRevision !== routePolicyRevision) {
      throw new Error('Registry route-policy revision changed during pagination');
    }
    items.push(...response.data.items as T[]);
    if (!response.data.hasMore) return { items, routePolicyRevision: routePolicyRevision ?? null };
    if (!response.data.nextCursor) throw new Error('Registry collection omitted the next cursor');
    cursor = response.data.nextCursor;
  }
  throw new Error('Registry collection exceeded the bounded page budget');
}

export async function loadRealAssetsRegistry(input: LoadRealAssetsRegistryInput): Promise<RealAssetsRegistryData> {
  const [equipmentCollection, deviceCollection, bindingCollection] = await Promise.all([
    collectRegistryCollection<Equipment>((params, init) => input.client.listSiteEquipment(input.siteId, params, init), input.signal),
    collectRegistryCollection<Device>((params, init) => input.client.listSiteDevices(input.siteId, params, init), input.signal),
    collectRegistryCollection<DeviceBinding>((params, init) => input.client.listSiteDeviceBindings(input.siteId, params, init), input.signal),
  ]);
  const equipment = equipmentCollection.items;
  const devices = deviceCollection.items;
  const bindings = bindingCollection.items;
  validateRegistryScope(equipment, input.organizationId, input.siteId, 'Equipment collection');
  validateRegistryScope(devices, input.organizationId, input.siteId, 'Device collection');
  validateRegistryScope(bindings, input.organizationId, input.siteId, 'DeviceBinding collection');

  const routePolicyRevisions = [
    equipmentCollection.routePolicyRevision,
    deviceCollection.routePolicyRevision,
    bindingCollection.routePolicyRevision,
  ];
  if (new Set(routePolicyRevisions).size > 1) {
    throw new Error('Registry collections were read under different route-policy revisions');
  }

  const equipmentIds = new Set(equipment.map((item) => item.id));
  const deviceIds = new Set(devices.map((item) => item.id));
  if (bindings.some((binding) => !equipmentIds.has(binding.equipmentId) || !deviceIds.has(binding.deviceId))) {
    throw new Error('DeviceBinding collection referenced a resource outside the visible Site inventory');
  }
  return { equipment, devices, bindings, routePolicyRevision: routePolicyRevisions[0] ?? null };
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
  return ['real-assets', generation, organizationId, siteId, 'registry', routePolicyEpoch] as const;
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
