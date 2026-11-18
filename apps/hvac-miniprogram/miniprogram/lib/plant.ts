import { formatTelemetryDisplayValue, formatTelemetryUnit, telemetryPointDefinition } from '../shared/centralPlantTelemetry';
import { GatewayError, type Gateway } from './gateway';

// The plant as the web's 实时运行 and 设备 pages read it (apps/hvac-web/src/features/operations/realtime/plant-model.ts
// over features/assets): the same equipment roles, run states and readings from the Site's asset model and the
// devices' current snapshots. test/plant-parity.test.ts runs both over the same recorded data.

export type WaterLoopKind = 'CHILLED_WATER' | 'CONDENSER_WATER' | 'COOLING_TOWER_LOOP';

export const WATER_LOOP_ORDER: readonly WaterLoopKind[] = [
  'CHILLED_WATER',
  'CONDENSER_WATER',
  'COOLING_TOWER_LOOP',
];

export const WATER_LOOP_LABELS: Readonly<Record<WaterLoopKind, string>> = {
  CHILLED_WATER: '冷冻水环路',
  CONDENSER_WATER: '冷却水环路',
  COOLING_TOWER_LOOP: '冷却塔外循环',
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

export type PlantCategory =
  | 'CHILLER' | 'CHILLED_WATER_PUMP' | 'COOLING_WATER_PUMP' | 'COOLING_TOWER'
  | 'ZONE' | 'METER' | 'WEATHER' | 'ACQUISITION' | 'OTHER';

const CATEGORY_ORDER: readonly PlantCategory[] = [
  'CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER', 'ZONE', 'METER', 'WEATHER', 'ACQUISITION', 'OTHER',
];

export const CATEGORY_LABELS: Readonly<Record<PlantCategory, string>> = {
  CHILLER: '冷水机组',
  CHILLED_WATER_PUMP: '冷冻水泵',
  COOLING_WATER_PUMP: '冷却水泵',
  COOLING_TOWER: '冷却塔',
  ZONE: '室内环境',
  METER: '计量',
  WEATHER: '室外环境',
  ACQUISITION: '采集与控制',
  OTHER: '其他设备',
};

const CATEGORY_BY_ASSET_TYPE: Readonly<Record<string, PlantCategory>> = {
  CHILLER: 'CHILLER',
  CHILLED_WATER_PUMP: 'CHILLED_WATER_PUMP',
  COOLING_WATER_PUMP: 'COOLING_WATER_PUMP',
  COOLING_TOWER: 'COOLING_TOWER',
  ISOLATION_VALVE: 'ACQUISITION',
  MOTORIZED_VALVE: 'ACQUISITION',
  DIFFERENTIAL_PRESSURE_SENSOR: 'METER',
  HVAC_POWER_METER: 'METER',
  POWER_METER: 'METER',
  BTU_METER: 'METER',
  WEATHER_STATION: 'WEATHER',
  ZONE_TEMPERATURE_SENSOR: 'ZONE',
};

const CATEGORY_BY_SOURCE_NAMESPACE: Readonly<Record<string, PlantCategory>> = {
  chiller: 'CHILLER',
  chwp: 'CHILLED_WATER_PUMP',
  cwp: 'COOLING_WATER_PUMP',
  cooling_tower: 'COOLING_TOWER',
  valve: 'ACQUISITION',
  vfd: 'ACQUISITION',
  dp_sensor: 'METER',
  header: 'METER',
  branch: 'METER',
  hvac_meter: 'METER',
  btu_meter: 'METER',
  weather: 'WEATHER',
  zone: 'ZONE',
};

const CATEGORY_BY_DEVICE_TYPE: Readonly<Record<string, PlantCategory>> = {
  ISOLATION_VALVE: 'ACQUISITION',
  MOTORIZED_VALVE: 'ACQUISITION',
  DIFFERENTIAL_PRESSURE_SENSOR: 'METER',
  HVAC_POWER_METER: 'METER',
  POWER_METER: 'METER',
  BTU_METER: 'METER',
  FLOW_METER: 'METER',
  TEMPERATURE_MODULE: 'ACQUISITION',
  CONTROL_MODULE: 'ACQUISITION',
};

const RUN_STATE_NAMESPACE: Partial<Record<PlantCategory, string>> = {
  CHILLER: 'chiller', CHILLED_WATER_PUMP: 'chwp', COOLING_WATER_PUMP: 'cwp', COOLING_TOWER: 'cooling_tower',
};

/** The plant's moving equipment; meters and the weather station never run. */
export const EQUIPMENT_CATEGORIES: ReadonlySet<PlantCategory> = new Set<PlantCategory>(['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER']);

export type RunState = 'RUNNING' | 'STOPPED' | 'FAULT' | 'UNKNOWN';
export type Connection = 'ONLINE' | 'OFFLINE' | 'UNKNOWN';

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
  readonly devices: readonly PlantDevice[];
  readonly equipmentCount: number;
  readonly runningCount: number;
  readonly hasAlert: boolean;
}

export interface Plant {
  readonly groups: readonly PlantGroup[];
  /** The groups' devices in order. */
  readonly devices: readonly PlantDevice[];
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

function parseWaterLoop(value: string | null | undefined): WaterLoopKind | null {
  if (value === 'CHILLED_WATER' || value === 'CONDENSER_WATER' || value === 'COOLING_TOWER_LOOP') return value;
  return null;
}

function cleanBranchCode(value: string | null | undefined): string | null {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed ? trimmed : null;
}

interface HydraulicAssetMeta {
  readonly waterLoop: WaterLoopKind | null;
  readonly branchCode: string | null;
}

function buildHydraulicAssetIndex(model: AssetModel, now: Date): Map<string, HydraulicAssetMeta> {
  const byAssetId = new Map<string, HydraulicAssetMeta>();
  for (const asset of model.assets) {
    byAssetId.set(asset.id, {
      waterLoop: parseWaterLoop(asset.waterLoop),
      branchCode: cleanBranchCode(asset.branchCode),
    });
  }
  const branchTargets = new Map<string, { waterLoop: WaterLoopKind | null; branchCode: string }[]>();
  const loopTargets = new Map<string, WaterLoopKind[]>();
  for (const rel of model.relationships) {
    if (!isCurrentRelationship(rel, now) || rel.fromType !== 'ASSET' || rel.toType !== 'ASSET') continue;
    const role = rel.role ?? '';
    if (role !== 'ISOLATES' && role !== 'SERVES' && role !== 'FEEDS_HEADER') continue;
    const toMeta = byAssetId.get(rel.toId);
    if (!toMeta) continue;
    if (toMeta.waterLoop) {
      const list = loopTargets.get(rel.fromId) ?? [];
      list.push(toMeta.waterLoop);
      loopTargets.set(rel.fromId, list);
    }
    if ((role === 'ISOLATES' || role === 'SERVES') && toMeta.branchCode) {
      const list = branchTargets.get(rel.fromId) ?? [];
      list.push({ waterLoop: toMeta.waterLoop, branchCode: toMeta.branchCode });
      branchTargets.set(rel.fromId, list);
    }
  }
  for (const [assetId, meta] of byAssetId.entries()) {
    let waterLoop = meta.waterLoop;
    let branchCode = meta.branchCode;
    if (!branchCode) {
      const candidates = branchTargets.get(assetId) ?? [];
      const keys = new Set(candidates.map((item) => `${item.waterLoop ?? ''}::${item.branchCode}`));
      if (keys.size === 1 && candidates[0]) {
        branchCode = candidates[0].branchCode;
        waterLoop ??= candidates[0].waterLoop;
      }
    }
    if (!waterLoop) {
      const candidates = new Set(loopTargets.get(assetId) ?? []);
      if (candidates.size === 1) [waterLoop] = [...candidates];
    }
    byAssetId.set(assetId, { waterLoop, branchCode });
  }
  return byAssetId;
}

function branchDeviceOrder(device: PlantDevice): number {
  if (device.category === 'CHILLER') return 0;
  if (device.category === 'CHILLED_WATER_PUMP') return 1;
  if (device.category === 'COOLING_WATER_PUMP') return 2;
  if (device.category === 'COOLING_TOWER') return 3;
  if (device.assetType === 'MOTORIZED_VALVE' || device.assetType === 'ISOLATION_VALVE'
    || device.reading('valve.open_state') !== null || device.reading('valve.position_feedback') !== null) {
    return 4;
  }
  if (device.category === 'METER') return 5;
  return 6;
}

function buildParallelBranches(devices: readonly PlantDevice[]): {
  branches: readonly ParallelBranch[];
  loopBranches: Readonly<Partial<Record<WaterLoopKind, number>>>;
} {
  const grouped = new Map<string, { waterLoop: WaterLoopKind | null; branchCode: string; devices: PlantDevice[] }>();
  for (const device of devices) {
    if (!device.branchCode) continue;
    const key = `${device.waterLoop ?? 'UNSPECIFIED'}::${device.branchCode}`;
    const existing = grouped.get(key);
    if (existing) existing.devices.push(device);
    else grouped.set(key, { waterLoop: device.waterLoop, branchCode: device.branchCode, devices: [device] });
  }
  const branches: ParallelBranch[] = [...grouped.entries()]
    .map(([key, group]) => {
      const sortedDevices = [...group.devices].sort((left, right) => {
        const rank = branchDeviceOrder(left) - branchDeviceOrder(right);
        if (rank !== 0) return rank;
        const byName = left.name.localeCompare(right.name, 'zh-CN');
        return byName !== 0 ? byName : left.deviceId.localeCompare(right.deviceId);
      });
      const equipment = sortedDevices.filter((device) => EQUIPMENT_CATEGORIES.has(device.category));
      return {
        key,
        waterLoop: group.waterLoop,
        loopLabel: group.waterLoop ? WATER_LOOP_LABELS[group.waterLoop] : '水系统',
        branchCode: group.branchCode,
        devices: sortedDevices,
        equipmentCount: equipment.length,
        runningCount: equipment.filter(isRunning).length,
        hasAlert: sortedDevices.some((device) => device.connection === 'OFFLINE' || device.runState === 'FAULT' || device.hasStaleData),
      };
    })
    .sort((left, right) => {
      const leftRank = left.waterLoop ? WATER_LOOP_ORDER.indexOf(left.waterLoop) : 99;
      const rightRank = right.waterLoop ? WATER_LOOP_ORDER.indexOf(right.waterLoop) : 99;
      if (leftRank !== rightRank) return leftRank - rightRank;
      return left.branchCode.localeCompare(right.branchCode, 'zh-CN', { numeric: true });
    });
  const loopCounts: Partial<Record<WaterLoopKind, number>> = {};
  for (const branch of branches) {
    if (branch.waterLoop) loopCounts[branch.waterLoop] = (loopCounts[branch.waterLoop] ?? 0) + 1;
  }
  return { branches, loopBranches: loopCounts };
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

  let category: PlantCategory | undefined = asset ? CATEGORY_BY_ASSET_TYPE[asset.assetType] : undefined;
  for (const point of points) {
    if (category) break;
    category = CATEGORY_BY_SOURCE_NAMESPACE[point.sourceKey.split('.')[0]];
  }
  category ??= CATEGORY_BY_DEVICE_TYPE[device.deviceType] ?? 'OTHER';

  const namespace = RUN_STATE_NAMESPACE[category];
  const runValue = namespace ? presentValue(snapshot, available, points.find((point) => point.sourceKey === `${namespace}.run_state`)) : undefined;
  const faultValue = presentValue(snapshot, available, points.find((point) => point.sourceKey.endsWith('.fault_code')));
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
    connection: presence === 'ONLINE' || presence === 'OFFLINE' ? presence : 'UNKNOWN',
    runState: runValue === 'RUNNING' || runValue === 'STOPPED' || runValue === 'FAULT' ? runValue : 'UNKNOWN',
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
  const { branches, loopBranches } = buildParallelBranches(devices);
  const totalPower = firstReading(devices, 'hvac_meter.active_power');
  const coolingCapacity = firstReading(devices, 'btu_meter.instant_cooling_capacity');
  const plantCop = totalPower?.current && (coolingCapacity?.current || coolingCapacity?.lowConfidence)
    && totalPower.numeric !== null && coolingCapacity.numeric !== null && totalPower.numeric > 0
    ? coolingCapacity.numeric / totalPower.numeric
    : null;
  return {
    groups,
    devices,
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
  return device.connection !== 'OFFLINE' && device.runState === 'RUNNING';
}

/** The first device of the plant that reports this point. */
export function plantReading(plant: Plant, sourceKey: string): PlantReading | null {
  return firstReading(plant.devices, sourceKey);
}

export function loopDifferentialPressure(plant: Plant, waterLoop: WaterLoopKind): PlantReading | null {
  const keys = ['dp_sensor.differential_pressure', 'header.differential_pressure'];
  const scoped = plant.devices.filter((device) => device.waterLoop === waterLoop);
  for (const key of keys) {
    const reading = firstReading(scoped, key);
    if (reading) return reading;
  }
  if (waterLoop === 'CHILLED_WATER') {
    const fallback = plant.devices.filter((device) => device.waterLoop === null);
    for (const key of keys) {
      const reading = firstReading(fallback, key);
      if (reading) return reading;
    }
  }
  return null;
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
): Promise<PlantDevice | null> {
  const model = await readAssetModel(client, siteId, fresh);
  const device = model.devices.find((candidate) => candidate.id === deviceId);
  if (device === undefined || device.deviceType === 'GATEWAY') return null;

  const batches = deviceSnapshotKeyBatches(model, deviceId);
  const basePath = `/api/v1/devices/${encodeURIComponent(deviceId)}/observation-snapshot`;
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
  return buildPlantDevice(model, deviceId, mergeDeviceSnapshots(present), now);
}
