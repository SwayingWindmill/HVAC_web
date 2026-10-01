import type { AssetsDeviceRow, AssetsPointView } from '@/features/assets/model';

// Central-plant view of the Site's Registry devices and their current observations.
// Every value shown here comes from a Registry point and the device Snapshot/Stream;
// nothing is estimated in the browser except the plant COP, which is the ratio of two
// current measurements and is withheld unless both are fresh and of good quality.

export type PlantCategory =
  | 'CHILLER'
  | 'CHILLED_WATER_PUMP'
  | 'COOLING_WATER_PUMP'
  | 'COOLING_TOWER'
  | 'METER'
  | 'WEATHER'
  | 'OTHER';

export const PLANT_CATEGORY_ORDER: readonly PlantCategory[] = [
  'CHILLER',
  'CHILLED_WATER_PUMP',
  'COOLING_WATER_PUMP',
  'COOLING_TOWER',
  'METER',
  'WEATHER',
  'OTHER',
];

export const PLANT_CATEGORY_LABELS: Readonly<Record<PlantCategory, string>> = {
  CHILLER: '冷水机组',
  CHILLED_WATER_PUMP: '冷冻水泵',
  COOLING_WATER_PUMP: '冷却水泵',
  COOLING_TOWER: '冷却塔',
  METER: '计量',
  WEATHER: '室外环境',
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
};

// Registry source-key namespaces identify the equipment role when a device is not bound
// to an Asset.
const CATEGORY_BY_SOURCE_NAMESPACE: Readonly<Record<string, PlantCategory>> = {
  chiller: 'CHILLER',
  chwp: 'CHILLED_WATER_PUMP',
  cwp: 'COOLING_WATER_PUMP',
  cooling_tower: 'COOLING_TOWER',
  hvac_meter: 'METER',
  btu_meter: 'METER',
  weather: 'WEATHER',
};

export type RunState = 'RUNNING' | 'STOPPED' | 'FAULT' | 'UNKNOWN';
export type ConnectionState = 'ONLINE' | 'OFFLINE' | 'UNKNOWN';

export interface PlantReading {
  readonly label: string;
  readonly display: string;
  readonly unit: string | null;
  readonly numeric: number | null;
  /** Present, fresh and of good quality: safe to use for derived figures. */
  readonly current: boolean;
  readonly state: AssetsPointView['state'];
  readonly freshness: AssetsPointView['freshness'];
  readonly sampledAt: string | null;
}

export interface PlantDevice {
  readonly row: AssetsDeviceRow;
  readonly deviceId: string;
  readonly name: string;
  readonly category: PlantCategory;
  readonly connection: ConnectionState;
  readonly runState: RunState;
  readonly faultCode: string | null;
  readonly latestSampleAt: string | null;
  readonly hasStaleData: boolean;
  reading(sourceKey: string): PlantReading | null;
}

export interface PlantGroup {
  readonly category: PlantCategory;
  readonly label: string;
  readonly devices: readonly PlantDevice[];
}

export interface PlantView {
  readonly groups: readonly PlantGroup[];
  readonly devices: readonly PlantDevice[];
  readonly onlineCount: number;
  readonly latestSampleAt: string | null;
  readonly totalPower: PlantReading | null;
  readonly coolingCapacity: PlantReading | null;
  readonly plantCop: number | null;
}

function categoryOf(row: AssetsDeviceRow): PlantCategory {
  if (row.binding.state === 'bound') {
    const fromAsset = CATEGORY_BY_ASSET_TYPE[row.binding.asset.assetType];
    if (fromAsset) return fromAsset;
  }
  for (const point of row.telemetryPoints) {
    const namespace = point.sourceKey.split('.')[0];
    const fromSource = CATEGORY_BY_SOURCE_NAMESPACE[namespace];
    if (fromSource) return fromSource;
  }
  return 'OTHER';
}

function deviceName(row: AssetsDeviceRow): string {
  return row.binding.state === 'bound' ? row.binding.asset.displayName : row.device.displayName;
}

function snapshotValue(row: AssetsDeviceRow, pointCode: string): unknown {
  if (row.snapshotResult?.status !== 'ok') return undefined;
  const state = row.snapshotResult.snapshot.values.find((value) => value.key === pointCode);
  return state && state.state === 'PRESENT' ? state.value : undefined;
}

function readingFor(row: AssetsDeviceRow, sourceKey: string): PlantReading | null {
  const point = row.telemetryPoints.find((candidate) => candidate.sourceKey === sourceKey && candidate.status === 'ACTIVE');
  if (!point) return null;
  const view = row.operational.points.find((candidate) => candidate.key === point.pointCode);
  if (!view) return null;
  const raw = snapshotValue(row, point.pointCode);
  const numeric = typeof raw === 'number' && Number.isFinite(raw) ? raw : null;
  return {
    label: view.label,
    display: view.state === 'PRESENT' ? view.displayValue : '—',
    unit: view.unit,
    numeric,
    current: view.state === 'PRESENT' && view.freshness === 'FRESH' && view.quality === 'GOOD',
    state: view.state,
    freshness: view.freshness,
    sampledAt: view.sampledAt,
  };
}

function connectionOf(row: AssetsDeviceRow): ConnectionState {
  const state = row.operational.connection.state;
  if (state === 'ONLINE') return 'ONLINE';
  if (state === 'OFFLINE') return 'OFFLINE';
  return 'UNKNOWN';
}

function runStateOf(row: AssetsDeviceRow, category: PlantCategory): RunState {
  const namespace = { CHILLER: 'chiller', CHILLED_WATER_PUMP: 'chwp', COOLING_WATER_PUMP: 'cwp', COOLING_TOWER: 'cooling_tower' }[
    category as 'CHILLER' | 'CHILLED_WATER_PUMP' | 'COOLING_WATER_PUMP' | 'COOLING_TOWER'
  ];
  if (!namespace) return 'UNKNOWN';
  const point = row.telemetryPoints.find((candidate) => candidate.sourceKey === `${namespace}.run_state`);
  const value = point ? snapshotValue(row, point.pointCode) : undefined;
  return value === 'RUNNING' || value === 'STOPPED' || value === 'FAULT' ? value : 'UNKNOWN';
}

function faultCodeOf(row: AssetsDeviceRow): string | null {
  const point = row.telemetryPoints.find((candidate) => candidate.sourceKey.endsWith('.fault_code'));
  const value = point ? snapshotValue(row, point.pointCode) : undefined;
  return typeof value === 'string' && value.trim() ? value.trim() : null;
}

function latest(values: readonly (string | null)[]): string | null {
  let result: string | null = null;
  for (const value of values) {
    if (value && (!result || Date.parse(value) > Date.parse(result))) result = value;
  }
  return result;
}

function toPlantDevice(row: AssetsDeviceRow): PlantDevice {
  const category = categoryOf(row);
  return {
    row,
    deviceId: row.device.id,
    name: deviceName(row),
    category,
    connection: connectionOf(row),
    runState: runStateOf(row, category),
    faultCode: faultCodeOf(row),
    latestSampleAt: latest(row.operational.points.map((point) => point.sampledAt)),
    hasStaleData: row.operational.points.some((point) => point.freshness === 'STALE'),
    reading: (sourceKey) => readingFor(row, sourceKey),
  };
}

function firstReading(devices: readonly PlantDevice[], sourceKey: string): PlantReading | null {
  for (const device of devices) {
    const reading = device.reading(sourceKey);
    if (reading) return reading;
  }
  return null;
}

export function buildPlantView(rows: readonly AssetsDeviceRow[]): PlantView {
  const devices = rows.map(toPlantDevice);
  const groups = PLANT_CATEGORY_ORDER
    .map((category) => ({
      category,
      label: PLANT_CATEGORY_LABELS[category],
      devices: devices
        .filter((device) => device.category === category)
        .sort((left, right) => left.name.localeCompare(right.name, 'zh-CN')),
    }))
    .filter((group) => group.devices.length > 0);
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

export function plantReading(view: PlantView, sourceKey: string): PlantReading | null {
  return firstReading(view.devices, sourceKey);
}
