import { formatTelemetryDisplayValue, formatTelemetryUnit, telemetryPointDefinition } from '../shared/centralPlantTelemetry';
import {
  aggregatePlantUnits,
  buildParallelBranchGroups,
  cleanBranchCode,
  classifyPlantCategory,
  EQUIPMENT_CATEGORIES,
  findFaultCodePoint,
  findLoopDifferentialPressure,
  findRunStatePoint,
  inferHydraulicAssetIndex,
  isPlantDeviceRunning,
  parseRunState,
  parseWaterLoop,
  PLANT_CATEGORY_LABELS as CATEGORY_LABELS,
  PLANT_CATEGORY_ORDER as CATEGORY_ORDER,
  WATER_LOOP_LABELS,
  WATER_LOOP_ORDER,
  type AggregatedPlantUnitMeta,
  type ConnectionState,
  type HydraulicAssetMeta,
  type PlantCategory,
  type RunState,
  type WaterLoopKind,
} from '../shared/plantTopology';
import { GatewayError, type Gateway } from './gateway';

// The plant as the web's 实时运行 and 设备 pages read it (apps/hvac-web/src/features/operations/realtime/plant-model.ts
// over shared/plantTopology.ts): identical equipment roles, run states, hydraulic branches and readings.
// test/plant-parity.test.ts runs both over the same recorded data.

export {
  CATEGORY_LABELS,
  EQUIPMENT_CATEGORIES,
  WATER_LOOP_LABELS,
  WATER_LOOP_ORDER,
  type ConnectionState,
  type PlantCategory,
  type RunState,
  type WaterLoopKind,
};

export interface AssetModel {
  readonly assets: readonly {
    readonly id: string;
    readonly displayName: string;
    readonly assetType: string;
    readonly waterLoop?: WaterLoopKind | string | null;
    readonly branchCode?: string | null;
  }[];
  readonly devices: readonly { readonly id: string; readonly displayName: string; readonly deviceType: string }[];
  readonly telemetryPoints: readonly TelemetryPoint[];
  readonly relationships: readonly Relationship[];
}

interface TelemetryPoint {
  readonly reportingDeviceId: string;
  readonly pointCode: string;
  readonly sourceKey: string;
  readonly displayName: string;
  readonly pointType: string;
  readonly valueType: string;
  readonly unit?: string | null;
  readonly status: string;
}

interface Relationship {
  readonly id: string;
  readonly fromType: string;
  readonly fromId: string;
  readonly toType: string;
  readonly toId: string;
  readonly role?: string;
  readonly status: string;
  readonly validFrom: string;
  readonly validTo?: string | null;
  readonly revision: number;
}

interface ValueState {
  readonly key: string;
  readonly state: 'PRESENT' | 'MISSING';
  readonly value?: unknown;
  readonly unit?: string | null;
  readonly sampledAt?: string;
  readonly freshness?: 'FRESH' | 'STALE';
  readonly quality?: string;
  readonly qualityReasons?: readonly string[];
  readonly measurement?: {
    readonly relative: number;
    readonly low: number;
    readonly high: number;
    readonly lowConfidence: boolean;
    readonly copRelative?: number;
  };
  readonly probeOffsets?: readonly { readonly pairCode: string; readonly offsetK: number; readonly suggested: boolean; readonly applied: boolean; readonly alarm?: string }[];
}

export interface Snapshot {
  readonly evaluationAvailability: 'AVAILABLE' | 'UNAVAILABLE';
  readonly presence: { readonly applicability: string; readonly currentState?: string | null };
  readonly values: readonly ValueState[];
}

export interface SnapshotsResponse {
  readonly items: readonly { readonly deviceId: string; readonly status: string; readonly snapshot?: Snapshot }[];
}

export type Connection = ConnectionState;

export interface PlantReading {
  readonly sourceKey: string;
  readonly label: string;
  /** '—' when there is no current value. */
  readonly display: string;
  readonly unit: string | null;
  readonly numeric: number | null;
  /** Present, fresh and of good quality: safe to use for derived figures. */
  readonly current: boolean;
  /** Fresh and present, and the only doubt is LOW_CONFIDENCE: shown as a number labelled 低可信. */
  readonly lowConfidence: boolean;
  readonly stale: boolean;
  readonly sampledAt: string | null;
  readonly measurement?: ValueState['measurement'];
  readonly probeOffsets?: NonNullable<ValueState['probeOffsets']>;
}

export interface PlantDevice {
  readonly deviceId: string;
  readonly name: string;
  readonly category: PlantCategory;
  readonly assetId: string | null;
  readonly assetType: string | null;
  readonly waterLoop: WaterLoopKind | null;
  readonly branchCode: string | null;
  readonly connection: Connection;
  readonly runState: RunState;
  readonly faultCode: string | null;
  readonly latestSampleAt: string | null;
  readonly hasStaleData: boolean;
  /** Every observed point, in the asset model's order. */
  readonly readings: readonly PlantReading[];
  reading(sourceKey: string): PlantReading | null;
}

/** Physical plant machine (or standalone unbound device) aggregating all devices bound to the same Asset. */
export interface PlantUnit extends PlantDevice {
  readonly unitId: string;
  readonly members: readonly PlantDevice[];
  readonly deviceIds: readonly string[];
  readonly offlineMemberCount: number;
}

export interface PlantGroup {
  readonly category: PlantCategory;
  readonly label: string;
  readonly devices: readonly PlantDevice[];
}

export interface ParallelBranch {
  readonly key: string;
  readonly waterLoop: WaterLoopKind | null;
  readonly loopLabel: string;
  readonly branchCode: string;
  readonly devices: readonly PlantUnit[];
  readonly equipmentCount: number;
  readonly runningCount: number;
  readonly hasAlert: boolean;
}

export interface Plant {
  readonly groups: readonly PlantGroup[];
  /** Raw communication devices (excluding GATEWAY): used for online counts and header reads. */
  readonly devices: readonly PlantDevice[];
  /** Asset-grouped physical plant units: used for equipment counts, device wall, and branches. */
  readonly units: readonly PlantUnit[];
  readonly branches: readonly ParallelBranch[];
  readonly loopBranches: Readonly<Partial<Record<WaterLoopKind, number>>>;
  readonly onlineCount: number;
  readonly latestSampleAt: string | null;
  readonly totalPower: PlantReading | null;
  readonly coolingCapacity: PlantReading | null;
  readonly plantCop: number | null;
  /** The COP comes from a 低可信 cooling load: shown with its interval, never averaged. */
  readonly plantCopLowConfidence: boolean;
}

function isCurrentRelationship(relationship: Relationship, now: Date): boolean {
  if (relationship.status !== 'ACTIVE') return false;
  const from = Date.parse(relationship.validFrom);
  const to = relationship.validTo ? Date.parse(relationship.validTo) : Number.POSITIVE_INFINITY;
  return Number.isFinite(from) && from <= now.getTime() && now.getTime() < to;
}

/** The one Asset a device is currently bound to; none when it is unbound or bound to several. */
function boundAsset(model: AssetModel, deviceId: string, now: Date) {
  const assetIds = new Set(model.relationships
    .filter((relationship) => relationship.fromType === 'DEVICE' && relationship.fromId === deviceId
      && relationship.toType === 'ASSET' && isCurrentRelationship(relationship, now))
    .map((relationship) => relationship.toId));
  if (assetIds.size !== 1) return null;
  const [assetId] = [...assetIds];
  return model.assets.find((asset) => asset.id === assetId) ?? null;
}

/** A device as operators name it: its bound Asset's name, else its own; null for a device not registered. */
export function deviceName(model: AssetModel, deviceId: string, now: Date): string | null {
  const device = model.devices.find((candidate) => candidate.id === deviceId);
  if (device === undefined) return null;
  return boundAsset(model, deviceId, now)?.displayName ?? device.displayName;
}

function readingOf(point: TelemetryPoint, value: ValueState | undefined, available: boolean): PlantReading {
  const definition = telemetryPointDefinition(point.sourceKey);
  const label = definition.label === point.sourceKey ? point.displayName : definition.label;
  if (!available || !value || value.state !== 'PRESENT') {
    return { sourceKey: point.sourceKey, label, display: '—', unit: null, numeric: null, current: false, lowConfidence: false, stale: false, sampledAt: null };
  }
  return {
    sourceKey: point.sourceKey,
    label,
    display: formatTelemetryDisplayValue(value.value, definition.precision ?? 3),
    unit: formatTelemetryUnit(value.unit ?? point.unit ?? definition.defaultUnit),
    numeric: typeof value.value === 'number' && Number.isFinite(value.value) ? value.value : null,
    current: value.freshness === 'FRESH' && value.quality === 'GOOD',
    lowConfidence: value.freshness === 'FRESH' && value.quality === 'PARTIAL'
      && (value.qualityReasons?.length ?? 0) > 0 && value.qualityReasons!.every((reason) => reason === 'LOW_CONFIDENCE'),
    stale: value.freshness === 'STALE',
    sampledAt: value.sampledAt ?? null,
    measurement: value.measurement,
    probeOffsets: value.probeOffsets,
  };
}

function latest(values: readonly (string | null)[]): string | null {
  let result: string | null = null;
  for (const value of values) {
    if (value && (!result || Date.parse(value) > Date.parse(result))) result = value;
  }
  return result;
}

function presentValue(snapshot: Snapshot | undefined, available: boolean, point: TelemetryPoint | undefined): unknown {
  if (!snapshot || !available || !point) return undefined;
  const value = snapshot.values.find((candidate) => candidate.key === point.pointCode);
  return value && value.state === 'PRESENT' ? value.value : undefined;
}

function buildHydraulicAssetIndex(model: AssetModel, now: Date): Map<string, HydraulicAssetMeta> {
  return inferHydraulicAssetIndex(
    model.assets,
    model.relationships.filter((rel) => isCurrentRelationship(rel, now)),
  );
}

function toDevice(
  model: AssetModel,
  device: AssetModel['devices'][number],
  snapshot: Snapshot | undefined,
  now: Date,
  hydraulicByAsset?: ReadonlyMap<string, HydraulicAssetMeta>,
): PlantDevice {
  const asset = boundAsset(model, device.id, now);
  const points = model.telemetryPoints.filter((point) => point.reportingDeviceId === device.id);
  const active = points.filter((point) => point.status === 'ACTIVE');
  const available = snapshot?.evaluationAvailability === 'AVAILABLE';
  const valueByKey = new Map((snapshot?.values ?? []).map((value) => [value.key, value]));
  // Commands are written, never observed.
  const readings = active.filter((point) => point.pointType !== 'COMMAND')
    .map((point) => readingOf(point, valueByKey.get(point.pointCode), available));

  const category = classifyPlantCategory({
    assetType: asset?.assetType ?? null,
    sourceKeys: points.map((point) => point.sourceKey),
    deviceType: device.deviceType,
  });

  const runValue = presentValue(snapshot, available, findRunStatePoint(points, category));
  const faultValue = presentValue(snapshot, available, findFaultCodePoint(points));
  const presence = available && snapshot && snapshot.presence.applicability === 'APPLICABLE' ? snapshot.presence.currentState : null;
  const meta = asset ? (hydraulicByAsset?.get(asset.id) ?? {
    waterLoop: parseWaterLoop(asset.waterLoop),
    branchCode: cleanBranchCode(asset.branchCode),
  }) : null;

  return {
    deviceId: device.id,
    name: deviceName(model, device.id, now) ?? device.displayName,
    category,
    assetId: asset?.id ?? null,
    assetType: asset?.assetType ?? null,
    waterLoop: meta?.waterLoop ?? null,
    branchCode: meta?.branchCode ?? null,
    connection: presence === 'ONLINE' || presence === 'SLEEPING' || presence === 'OFFLINE' ? presence : 'UNKNOWN',
    runState: parseRunState(runValue),
    faultCode: typeof faultValue === 'string' && faultValue.trim() ? faultValue.trim() : null,
    latestSampleAt: latest(readings.map((reading) => reading.sampledAt)),
    hasStaleData: readings.some((reading) => reading.stale),
    readings,
    reading: (sourceKey) => readings.find((reading) => reading.sourceKey === sourceKey) ?? null,
  };
}

function firstReading(devices: readonly PlantDevice[], sourceKey: string): PlantReading | null {
  for (const device of devices) {
    const reading = device.reading(sourceKey);
    if (reading) return reading;
  }
  return null;
}

export function buildPlantDevice(model: AssetModel, deviceId: string, snapshot: Snapshot | undefined, now: Date): PlantDevice | null {
  const device = model.devices.find((candidate) => candidate.id === deviceId);
  if (device === undefined || device.deviceType === 'GATEWAY') return null;
  return toDevice(model, device, snapshot, now, buildHydraulicAssetIndex(model, now));
}

function mergeDeviceSnapshots(snapshots: readonly Snapshot[]): Snapshot | undefined {
  if (snapshots.length === 0) return undefined;
  if (snapshots.length === 1) return snapshots[0];
  return {
    evaluationAvailability: snapshots.every((snapshot) => snapshot.evaluationAvailability === 'AVAILABLE') ? 'AVAILABLE' : 'UNAVAILABLE',
    presence: snapshots[0].presence,
    values: snapshots.flatMap((snapshot) => snapshot.values),
  };
}

function mergeUnitReadings(
  members: readonly PlantDevice[],
  pickReading: (sourceKey: string) => PlantReading | null,
): PlantReading[] {
  if (members.length === 1) return [...members[0].readings];
  const seen = new Set<string>();
  const merged: PlantReading[] = [];
  for (const member of members) {
    for (const reading of member.readings) {
      if (seen.has(reading.sourceKey)) continue;
      seen.add(reading.sourceKey);
      merged.push(pickReading(reading.sourceKey) ?? reading);
    }
  }
  return merged;
}

function toPlantUnit(meta: AggregatedPlantUnitMeta<PlantDevice, PlantReading>): PlantUnit {
  const { primary, members, deviceIds, unitId, offlineMemberCount, connection, runState, faultCode, latestSampleAt, hasStaleData, pickReading } = meta;
  return {
    ...primary,
    unitId,
    members,
    deviceIds,
    offlineMemberCount,
    connection: connection === 'ONLINE' || connection === 'SLEEPING' || connection === 'OFFLINE' ? connection : 'UNKNOWN',
    runState,
    faultCode,
    latestSampleAt,
    hasStaleData,
    readings: mergeUnitReadings(members, pickReading),
    reading: (sourceKey) => pickReading(sourceKey),
  };
}

export function toStandaloneUnit(device: PlantDevice): PlantUnit {
  return {
    ...device,
    unitId: device.assetId ?? device.deviceId,
    members: [device],
    deviceIds: [device.deviceId],
    offlineMemberCount: device.connection === 'OFFLINE' ? 1 : 0,
  };
}

export function buildPlant(model: AssetModel, snapshots: SnapshotsResponse, now: Date): Plant {
  const snapshotByDevice = new Map(snapshots.items
    .filter((item) => item.status === 'OK' && item.snapshot)
    .map((item) => [item.deviceId, item.snapshot]));
  const hydraulicByAsset = buildHydraulicAssetIndex(model, now);
  // A gateway is how the plant reaches the platform, not plant equipment.
  const all = model.devices.filter((device) => device.deviceType !== 'GATEWAY')
    .map((device) => toDevice(model, device, snapshotByDevice.get(device.id), now, hydraulicByAsset));
  const groups = CATEGORY_ORDER
    .map((category) => ({
      category,
      label: CATEGORY_LABELS[category],
      // Sorted as the web sorts them. Only the order of names depends on the phone's collation; no number does.
      devices: all.filter((device) => device.category === category).sort((left, right) => left.name.localeCompare(right.name, 'zh-CN')),
    }))
    .filter((group) => group.devices.length > 0);
  const devices = groups.flatMap((group) => group.devices);
  const units = aggregatePlantUnits<PlantDevice, PlantReading>(devices, { observedPointCount: (device) => device.readings.length })
    .map(toPlantUnit);
  const { branches, loopBranches } = buildParallelBranchGroups(units);
  const totalPower = firstReading(devices, 'hvac_meter.active_power');
  const coolingCapacity = firstReading(devices, 'btu_meter.instant_cooling_capacity');
  const plantCop = totalPower?.current && (coolingCapacity?.current || coolingCapacity?.lowConfidence)
    && totalPower.numeric !== null && coolingCapacity.numeric !== null && totalPower.numeric > 0
    ? coolingCapacity.numeric / totalPower.numeric
    : null;
  return {
    groups,
    devices,
    units,
    branches,
    loopBranches,
    onlineCount: devices.filter((device) => device.connection === 'ONLINE').length,
    latestSampleAt: latest(devices.map((device) => device.latestSampleAt)),
    totalPower,
    coolingCapacity,
    plantCop,
    plantCopLowConfidence: plantCop !== null && !coolingCapacity!.current,
  };
}

/** Running means reporting RUNNING while not offline: an offline device is offline whatever it last reported. */
export function isRunning(device: PlantDevice): boolean {
  return isPlantDeviceRunning(device);
}

/** The first device of the plant that reports this point. */
export function plantReading(plant: Plant, sourceKey: string): PlantReading | null {
  return firstReading(plant.devices, sourceKey);
}

export function loopDifferentialPressure(plant: Plant, waterLoop: WaterLoopKind): PlantReading | null {
  return findLoopDifferentialPressure<PlantReading>(plant.devices, waterLoop);
}

export interface SnapshotRequest {
  readonly requestId: string;
  readonly deviceId: string;
  readonly keys: readonly string[];
}

// The gateway's limits for current-value requests (matching telemetryauth.MaximumKeysPerTarget = 64 for single-device).
const MAX_BATCH_DEVICES = 100;
const MAX_BATCH_KEYS = 2048;
export const MAX_SINGLE_SNAPSHOT_KEYS = 64;

function deviceObservedKeys(model: AssetModel, deviceId: string): string[] {
  const seen = new Set<string>();
  for (const point of model.telemetryPoints) {
    if (point.reportingDeviceId === deviceId && point.status === 'ACTIVE' && point.pointType !== 'COMMAND') {
      seen.add(point.pointCode);
    }
  }
  return [...seen].sort();
}

/** Active non-COMMAND point codes for one device, chunked into <=64-key batches for GET observation-snapshot. */
export function deviceSnapshotKeyBatches(model: AssetModel, deviceId: string): string[][] {
  const keys = deviceObservedKeys(model, deviceId);
  if (keys.length === 0) return [[]];
  const batches: string[][] = [];
  for (let offset = 0; offset < keys.length; offset += MAX_SINGLE_SNAPSHOT_KEYS) {
    batches.push(keys.slice(offset, offset + MAX_SINGLE_SNAPSHOT_KEYS));
  }
  return batches;
}

/** Every device's observed points, packed into as few POST /api/v1/telemetry/observation-snapshots:batchGet requests as the limits allow. */
export function snapshotBatches(model: AssetModel): SnapshotRequest[][] {
  const batches: SnapshotRequest[][] = [];
  let batch: SnapshotRequest[] = [];
  let keyCount = 0;
  for (const device of model.devices) {
    const keys = deviceObservedKeys(model, device.id);
    if (batch.length > 0 && (batch.length >= MAX_BATCH_DEVICES || keyCount + keys.length > MAX_BATCH_KEYS)) {
      batches.push(batch);
      batch = [];
      keyCount = 0;
    }
    batch.push({ requestId: `${batches.length}-${batch.length}`, deviceId: device.id, keys });
    keyCount += keys.length;
  }
  if (batch.length > 0) batches.push(batch);
  return batches;
}

type ReadClient = Pick<Gateway, 'get'>;

let cachedModel: { readonly siteId: string; readonly client: ReadClient; readonly model: AssetModel } | null = null;

export function clearAssetModelCache(): void {
  cachedModel = null;
}

export async function readAssetModel(client: ReadClient, siteId: string, fresh: boolean): Promise<AssetModel> {
  if (!fresh && cachedModel?.siteId === siteId && cachedModel.client === client) return cachedModel.model;
  const model = await client.get<AssetModel>(`/api/v1/sites/${siteId}/asset-model`);
  cachedModel = { siteId, client, model };
  return model;
}

/**
 * Fetches one device's readings via GET /api/v1/devices/{deviceId}/observation-snapshot
 * (batching >64 keys); treats 404 as no snapshot yet for newly registered devices.
 */
export async function fetchPlantDevice(
  client: ReadClient,
  siteId: string,
  deviceId: string,
  fresh = false,
  now: Date = new Date(),
  preloadedModel?: AssetModel,
): Promise<PlantUnit | null> {
  const model = preloadedModel ?? await readAssetModel(client, siteId, fresh);
  const device = model.devices.find((candidate) => candidate.id === deviceId);
  if (device === undefined || device.deviceType === 'GATEWAY') return null;

  const asset = boundAsset(model, deviceId, now);
  const peerIds = asset
    ? model.devices
      .filter((candidate) => candidate.deviceType !== 'GATEWAY'
        && (candidate.id === deviceId || boundAsset(model, candidate.id, now)?.id === asset.id))
      .map((candidate) => candidate.id)
    : [deviceId];

  const peerDevices = (await Promise.all(
    peerIds.map(async (peerId) => {
      const batches = deviceSnapshotKeyBatches(model, peerId);
      const basePath = `/api/v1/devices/${encodeURIComponent(peerId)}/observation-snapshot`;
      const snapshots = await Promise.all(
        batches.map(async (keys): Promise<Snapshot | null> => {
          const path = keys.length > 0 ? `${basePath}?keys=${encodeURIComponent(keys.join(','))}` : basePath;
          try {
            return await client.get<Snapshot>(path);
          } catch (error) {
            if (error instanceof GatewayError && error.status === 404) return null;
            throw error;
          }
        }),
      );
      const present = snapshots.filter((snapshot): snapshot is Snapshot => snapshot !== null);
      return buildPlantDevice(model, peerId, mergeDeviceSnapshots(present), now);
    }),
  )).filter((item): item is PlantDevice => item !== null);

  if (peerDevices.length === 0) return null;
  const units = aggregatePlantUnits<PlantDevice, PlantReading>(peerDevices, { observedPointCount: (item) => item.readings.length })
    .map(toPlantUnit);
  return units[0] ?? null;
}
