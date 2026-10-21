import { formatTelemetryDisplayValue, formatTelemetryUnit, telemetryPointDefinition } from '../shared/centralPlantTelemetry';

// The plant as the web's 实时运行 and 设备 pages read it (apps/hvac-web/src/features/operations/realtime/plant-model.ts
// over features/assets): the same equipment roles, run states and readings from the Site's asset model and the
// devices' current snapshots. test/plant-parity.test.ts runs both over the same recorded data.

export interface AssetModel {
  readonly assets: readonly { readonly id: string; readonly displayName: string; readonly assetType: string }[];
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
}

interface Snapshot {
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
  hvac_meter: 'METER',
  btu_meter: 'METER',
  weather: 'WEATHER',
  zone: 'ZONE',
};

const CATEGORY_BY_DEVICE_TYPE: Readonly<Record<string, PlantCategory>> = {
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
  readonly stale: boolean;
  readonly sampledAt: string | null;
}

export interface PlantDevice {
  readonly deviceId: string;
  readonly name: string;
  readonly category: PlantCategory;
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

export interface Plant {
  readonly groups: readonly PlantGroup[];
  /** The groups' devices in order. */
  readonly devices: readonly PlantDevice[];
  readonly onlineCount: number;
  readonly latestSampleAt: string | null;
  readonly totalPower: PlantReading | null;
  readonly coolingCapacity: PlantReading | null;
  readonly plantCop: number | null;
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

function readingOf(point: TelemetryPoint, value: ValueState | undefined, available: boolean): PlantReading {
  const definition = telemetryPointDefinition(point.sourceKey);
  const label = definition.label === point.sourceKey ? point.displayName : definition.label;
  if (!available || !value || value.state !== 'PRESENT') {
    return { sourceKey: point.sourceKey, label, display: '—', unit: null, numeric: null, current: false, stale: false, sampledAt: null };
  }
  return {
    sourceKey: point.sourceKey,
    label,
    display: formatTelemetryDisplayValue(value.value, definition.precision ?? 3),
    unit: formatTelemetryUnit(value.unit ?? point.unit ?? definition.defaultUnit),
    numeric: typeof value.value === 'number' && Number.isFinite(value.value) ? value.value : null,
    current: value.freshness === 'FRESH' && value.quality === 'GOOD',
    stale: value.freshness === 'STALE',
    sampledAt: value.sampledAt ?? null,
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

function toDevice(model: AssetModel, device: AssetModel['devices'][number], snapshot: Snapshot | undefined, now: Date): PlantDevice {
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

  return {
    deviceId: device.id,
    name: asset ? asset.displayName : device.displayName,
    category,
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

export function buildPlant(model: AssetModel, snapshots: SnapshotsResponse, now: Date): Plant {
  const snapshotByDevice = new Map(snapshots.items
    .filter((item) => item.status === 'OK' && item.snapshot)
    .map((item) => [item.deviceId, item.snapshot]));
  // A gateway is how the plant reaches the platform, not plant equipment.
  const all = model.devices.filter((device) => device.deviceType !== 'GATEWAY')
    .map((device) => toDevice(model, device, snapshotByDevice.get(device.id), now));
  const groups = CATEGORY_ORDER
    .map((category) => ({
      category,
      label: CATEGORY_LABELS[category],
      devices: all.filter((device) => device.category === category).sort((left, right) => left.name.localeCompare(right.name, 'zh-CN')),
    }))
    .filter((group) => group.devices.length > 0);
  const devices = groups.flatMap((group) => group.devices);
  const totalPower = firstReading(devices, 'hvac_meter.active_power');
  const coolingCapacity = firstReading(devices, 'btu_meter.instant_cooling_capacity');
  const plantCop = totalPower?.current && coolingCapacity?.current
    && totalPower.numeric !== null && coolingCapacity.numeric !== null && totalPower.numeric > 0
    ? coolingCapacity.numeric / totalPower.numeric
    : null;
  return {
    groups,
    devices,
    onlineCount: devices.filter((device) => device.connection === 'ONLINE').length,
    latestSampleAt: latest(devices.map((device) => device.latestSampleAt)),
    totalPower,
    coolingCapacity,
    plantCop,
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

export interface SnapshotRequest {
  readonly requestId: string;
  readonly deviceId: string;
  readonly keys: readonly string[];
}

// The gateway's limits for one current-value request (the web's features/assets/data.ts).
const MAX_BATCH_DEVICES = 100;
const MAX_BATCH_KEYS = 2048;

/** Every device's observed points, packed into as few POST /api/v1/telemetry/observation-snapshots:batchGet requests as the limits allow. */
export function snapshotBatches(model: AssetModel): SnapshotRequest[][] {
  const batches: SnapshotRequest[][] = [];
  let batch: SnapshotRequest[] = [];
  let keyCount = 0;
  for (const device of model.devices) {
    const keys = model.telemetryPoints
      .filter((point) => point.reportingDeviceId === device.id && point.status === 'ACTIVE' && point.pointType !== 'COMMAND')
      .map((point) => point.pointCode)
      .sort();
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
