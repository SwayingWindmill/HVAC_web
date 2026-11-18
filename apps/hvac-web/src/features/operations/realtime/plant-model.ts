import type { SiteAssetModel } from '@/api/generated/platformGateway.gen';
import type { AssetsDeviceRow, AssetsPointView } from '@/features/assets/model';

function formatFixed(value: number, digits: number): string {
  return value.toLocaleString('zh-CN', { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

// Central-plant view of the Site's Registry devices and their current observations.
// Every value shown here comes from a Registry point and the device Snapshot/Stream;
// nothing is estimated in the browser except the plant COP, which is the ratio of two
// current measurements and is withheld unless both are fresh, of good quality, and healthy. A cooling
// load whose only doubt is a temperature difference small next to the probes (LOW_CONFIDENCE) is
// still shown, with its interval and a 低可信 label, and so is the COP it gives; the automated
// figures (COP averages, diagnostics, M&V) keep leaving those samples out on the server.

export type PlantCategory =
  | 'CHILLER'
  | 'CHILLED_WATER_PUMP'
  | 'COOLING_WATER_PUMP'
  | 'COOLING_TOWER'
  | 'ZONE'
  | 'METER'
  | 'WEATHER'
  | 'ACQUISITION'
  | 'OTHER';

export const PLANT_CATEGORY_ORDER: readonly PlantCategory[] = [
  'CHILLER',
  'CHILLED_WATER_PUMP',
  'COOLING_WATER_PUMP',
  'COOLING_TOWER',
  'ZONE',
  'METER',
  'WEATHER',
  'ACQUISITION',
  'OTHER',
];

export const PLANT_CATEGORY_LABELS: Readonly<Record<PlantCategory, string>> = {
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

// Registry source-key namespaces identify the equipment role when a device is not bound
// to an Asset.
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

// A Gateway-wired device that is neither bound to an Asset nor named by a source-key namespace
// still says what it is through its Registry device type.
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

export type RunState = 'RUNNING' | 'STOPPED' | 'FAULT' | 'UNKNOWN';
export type ConnectionState = 'ONLINE' | 'OFFLINE' | 'UNKNOWN';

export interface PlantReading {
  readonly label: string;
  readonly display: string;
  readonly unit: string | null;
  readonly numeric: number | null;
  /** Present, fresh and of good quality: safe to use for derived figures. */
  readonly current: boolean;
  /** Fresh and present, and the only doubt is LOW_CONFIDENCE: shown as a number with a 低可信 label. */
  readonly lowConfidence: boolean;
  readonly measurement: AssetsPointView['measurement'];
  readonly probeOffsets: AssetsPointView['probeOffsets'];
  readonly state: AssetsPointView['state'];
  readonly freshness: AssetsPointView['freshness'];
  readonly sampledAt: string | null;
}

export interface PlantDevice {
  readonly row: AssetsDeviceRow;
  readonly deviceId: string;
  readonly name: string;
  readonly category: PlantCategory;
  readonly assetId: string | null;
  readonly assetType: string | null;
  readonly waterLoop: WaterLoopKind | null;
  readonly branchCode: string | null;
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

export interface PlantView {
  readonly groups: readonly PlantGroup[];
  readonly devices: readonly PlantDevice[];
  readonly branches: readonly ParallelBranch[];
  readonly loopBranches: Readonly<Partial<Record<WaterLoopKind, number>>>;
  readonly onlineCount: number;
  readonly latestSampleAt: string | null;
  readonly totalPower: PlantReading | null;
  readonly coolingCapacity: PlantReading | null;
  readonly plantCop: number | null;
  /** The COP is computed from a 低可信 cooling load. */
  readonly plantCopLowConfidence?: boolean;
  /** The COP's interval from the cooling load's uncertainty, when the meter gives one. */
  readonly plantCopRange?: { readonly low: number; readonly high: number } | null;
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
  return CATEGORY_BY_DEVICE_TYPE[row.device.deviceType] ?? 'OTHER';
}

function deviceName(row: AssetsDeviceRow): string {
  return row.binding.state === 'bound' ? row.binding.asset.displayName : row.device.displayName;
}

function snapshotValue(row: AssetsDeviceRow, pointCode: string): unknown {
  if (row.snapshotResult?.status !== 'ok') return undefined;
  const state = row.snapshotResult.snapshot.values.find((value) => value.key === pointCode);
  return state && state.state === 'PRESENT' ? state.value : undefined;
}

/**
 * A reading whose only doubt is LOW_CONFIDENCE: present, fresh, partial for that reason alone, and
 * not judged bad. The value is measured; only its uncertainty is wide.
 */
export function isLowConfidenceOnly(view: Pick<AssetsPointView, 'state' | 'freshness' | 'quality' | 'qualityReasons' | 'health'>): boolean {
  return view.state === 'PRESENT' && view.freshness === 'FRESH' && view.quality === 'PARTIAL'
    && view.qualityReasons.length > 0 && view.qualityReasons.every((reason) => reason === 'LOW_CONFIDENCE')
    && (view.health == null || view.health.status === 'HEALTHY' || view.health.status === 'SUSPECT');
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
    current: view.state === 'PRESENT' && view.freshness === 'FRESH' && view.quality === 'GOOD' && (view.health == null || view.health.status === 'HEALTHY'),
    lowConfidence: isLowConfidenceOnly(view),
    measurement: view.measurement,
    probeOffsets: view.probeOffsets,
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

const NAMESPACE_BY_CATEGORY: Partial<Record<PlantCategory, string>> = {
  CHILLER: 'chiller',
  CHILLED_WATER_PUMP: 'chwp',
  COOLING_WATER_PUMP: 'cwp',
  COOLING_TOWER: 'cooling_tower',
};

function runStateOf(row: AssetsDeviceRow, category: PlantCategory): RunState {
  const namespace = NAMESPACE_BY_CATEGORY[category];
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

function buildHydraulicAssetIndex(
  rows: readonly AssetsDeviceRow[],
  assetModel?: Pick<SiteAssetModel, 'assets' | 'relationships'>,
): Map<string, HydraulicAssetMeta> {
  const byAssetId = new Map<string, { waterLoop: WaterLoopKind | null; branchCode: string | null }>();
  for (const row of rows) {
    if (row.binding.state === 'bound') {
      const asset = row.binding.asset;
      byAssetId.set(asset.id, {
        waterLoop: parseWaterLoop(asset.waterLoop),
        branchCode: cleanBranchCode(asset.branchCode),
      });
    }
  }
  for (const asset of assetModel?.assets ?? []) {
    if (!byAssetId.has(asset.id)) {
      byAssetId.set(asset.id, {
        waterLoop: parseWaterLoop(asset.waterLoop),
        branchCode: cleanBranchCode(asset.branchCode),
      });
    }
  }
  if (!assetModel?.relationships?.length) return byAssetId;

  const branchTargets = new Map<string, { waterLoop: WaterLoopKind | null; branchCode: string }[]>();
  const loopTargets = new Map<string, WaterLoopKind[]>();
  for (const rel of assetModel.relationships) {
    if (rel.status !== 'ACTIVE' || rel.fromType !== 'ASSET' || rel.toType !== 'ASSET') continue;
    if (rel.role !== 'ISOLATES' && rel.role !== 'SERVES' && rel.role !== 'FEEDS_HEADER') continue;
    const toMeta = byAssetId.get(rel.toId);
    if (!toMeta) continue;
    if (toMeta.waterLoop) {
      const list = loopTargets.get(rel.fromId) ?? [];
      list.push(toMeta.waterLoop);
      loopTargets.set(rel.fromId, list);
    }
    if ((rel.role === 'ISOLATES' || rel.role === 'SERVES') && toMeta.branchCode) {
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

function toPlantDevice(row: AssetsDeviceRow, hydraulicByAsset: ReadonlyMap<string, HydraulicAssetMeta>): PlantDevice {
  const category = categoryOf(row);
  const boundAsset = row.binding.state === 'bound' ? row.binding.asset : null;
  const meta = boundAsset ? hydraulicByAsset.get(boundAsset.id) : undefined;
  return {
    row,
    deviceId: row.device.id,
    name: deviceName(row),
    category,
    assetId: boundAsset?.id ?? null,
    assetType: boundAsset?.assetType ?? null,
    waterLoop: meta?.waterLoop ?? (boundAsset ? parseWaterLoop(boundAsset.waterLoop) : null),
    branchCode: meta?.branchCode ?? (boundAsset ? cleanBranchCode(boundAsset.branchCode) : null),
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

export function buildPlantView(
  rows: readonly AssetsDeviceRow[],
  assetModel?: Pick<SiteAssetModel, 'assets' | 'relationships'>,
): PlantView {
  const hydraulicByAsset = buildHydraulicAssetIndex(rows, assetModel);
  // A gateway is how the plant reaches the platform, not plant equipment.
  const devices = rows.filter((row) => row.device.deviceType !== 'GATEWAY').map((row) => toPlantDevice(row, hydraulicByAsset));
  const groups = PLANT_CATEGORY_ORDER
    .map((category) => ({
      category,
      label: PLANT_CATEGORY_LABELS[category],
      devices: devices
        .filter((device) => device.category === category)
        .sort((left, right) => left.name.localeCompare(right.name, 'zh-CN')),
    }))
    .filter((group) => group.devices.length > 0);
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
    plantCopRange: copRange(plantCop, coolingCapacity),
  };
}

export function plantReading(view: PlantView, sourceKey: string): PlantReading | null {
  return firstReading(view.devices, sourceKey);
}

export function loopDifferentialPressure(view: PlantView, waterLoop: WaterLoopKind): PlantReading | null {
  const keys = ['dp_sensor.differential_pressure', 'header.differential_pressure'];
  const scoped = view.devices.filter((device) => device.waterLoop === waterLoop);
  for (const key of keys) {
    const reading = firstReading(scoped, key);
    if (reading) return reading;
  }
  if (waterLoop === 'CHILLED_WATER') {
    const fallback = view.devices.filter((device) => device.waterLoop === null);
    for (const key of keys) {
      const reading = firstReading(fallback, key);
      if (reading) return reading;
    }
  }
  return null;
}

/** ±16% for 0.158, ±5.1% for a difference that is still large next to the probes. */
export function formatRelativeUncertainty(relative: number): string {
  const percent = relative * 100;
  return `±${formatFixed(percent, percent >= 10 ? 0 : 1)}%`;
}

export const LOW_CONFIDENCE_LABEL = '低可信';

function copRange(cop: number | null, cooling: PlantReading | null | undefined): { low: number; high: number } | null {
  const band = cooling?.measurement;
  if (cop === null || !band) return null;
  const relative = band.copRelative ?? band.relative;
  if (!Number.isFinite(relative) || relative < 0) return null;
  return { low: Math.max(0, cop * (1 - relative)), high: cop * (1 + relative) };
}

/** 低可信 · 431–593 kW（±16%），温差相对探头误差偏小; 不确定度 ±5.1% for a confident load. */
export function coolingUncertaintyText(reading: PlantReading | null): string | null {
  const band = reading?.measurement;
  if (!band || !Number.isFinite(band.relative)) return null;
  const percent = formatRelativeUncertainty(band.relative);
  if (!band.lowConfidence) return `不确定度 ${percent}`;
  const range = Number.isFinite(band.low) && Number.isFinite(band.high) && band.high > 0
    ? `${formatFixed(band.low, 0)}–${formatFixed(band.high, 0)} kW（${percent}）`
    : `不确定度 ${percent}`;
  return `${LOW_CONFIDENCE_LABEL} · ${range}，温差相对探头误差偏小`;
}

/** The live COP's uncertainty; a 低可信 COP is shown, with its interval, and kept out of averages. */
export function copUncertaintyText(plant: PlantView): string | null {
  const cooling = plant.coolingCapacity?.measurement;
  if (plant.plantCop === null || !cooling || !Number.isFinite(cooling.relative)) return null;
  const percent = formatRelativeUncertainty(cooling.copRelative ?? cooling.relative);
  const lowConfidence = plant.plantCopLowConfidence ?? cooling.lowConfidence;
  if (!lowConfidence) return `不确定度 ${percent}`;
  const range = plant.plantCopRange ?? copRange(plant.plantCop, plant.coolingCapacity);
  const interval = range ? `${formatFixed(range.low, 2)}–${formatFixed(range.high, 2)}（${percent}）` : `不确定度 ${percent}`;
  return `${LOW_CONFIDENCE_LABEL} · ${interval}，不计入能效均值和诊断`;
}

/** Suggested supply-minus-return corrections. applied stays false; the text says the suggestion was not used. */
export function probeOffsetText(plant: PlantView): string | null {
  const seen = new Set<string>();
  const lines: string[] = [];
  for (const device of plant.devices) {
    for (const point of device.row.operational.points) {
      for (const suggestion of point.probeOffsets) {
        if (!suggestion.suggested || suggestion.applied || seen.has(suggestion.pairCode)) continue;
        seen.add(suggestion.pairCode);
        const magnitude = formatFixed(Math.abs(suggestion.offsetK), 2);
        const verb = suggestion.offsetK >= 0 ? '减去' : '加上';
        const alarm = suggestion.alarm ? '，偏差已超过提醒线' : '';
        lines.push(`探头零点建议把温差${verb} ${magnitude} K（未采用${alarm}）`);
      }
    }
  }
  return lines.length > 0 ? lines.join('；') : null;
}

/** The plant's moving equipment; meters and the weather station never run. */
export const EQUIPMENT_CATEGORIES: ReadonlySet<PlantCategory> = new Set(['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER']);

/** Running means reporting RUNNING while not offline: an offline device is offline whatever it last reported. */
export interface TileReadings {
  readonly lead: { readonly sourceKey: string; readonly reading: PlantReading } | null;
  readonly facts: readonly PlantReading[];
}

/**
 * What a device tile shows: the first of the role's preferred readings it has, then up to two of
 * its role's other key readings. A device without any of them (a Gateway-wired meter or acquisition
 * module) shows its own numeric measurements instead, current ones first, so its tile is never blank.
 */
export function tileReadings(device: PlantDevice, preferredKeys: readonly string[], factKeys: readonly string[]): TileReadings {
  const pick = (keys: readonly string[]) => keys
    .map((sourceKey) => ({ sourceKey, reading: device.reading(sourceKey) }))
    .filter((entry): entry is { sourceKey: string; reading: PlantReading } => Boolean(entry.reading));
  const preferred = pick(preferredKeys);
  if (preferred.length > 0) {
    const lead = preferred[0];
    return { lead, facts: pick(factKeys).filter((entry) => entry.sourceKey !== lead.sourceKey).map((entry) => entry.reading).slice(0, 2) };
  }
  const own = pick(device.row.telemetryPoints
    .filter((point) => point.status === 'ACTIVE' && point.pointType === 'TELEMETRY' && point.valueType === 'NUMBER')
    .map((point) => point.sourceKey));
  const ordered = [...own.filter((entry) => entry.reading.current), ...own.filter((entry) => !entry.reading.current)];
  return { lead: ordered[0] ?? null, facts: ordered.slice(1, 3).map((entry) => entry.reading) };
}

export function isRunning(device: PlantDevice): boolean {
  return device.connection !== 'OFFLINE' && device.runState === 'RUNNING';
}

export interface EquipmentStatus {
  readonly total: number;
  readonly running: number;
  readonly faults: number;
  /** Devices of any kind. */
  readonly offline: number;
  readonly withoutData: number;
  readonly stale: number;
}

export function equipmentStatus(view: PlantView): EquipmentStatus {
  const equipment = view.devices.filter((device) => EQUIPMENT_CATEGORIES.has(device.category));
  return {
    total: equipment.length,
    running: equipment.filter(isRunning).length,
    faults: equipment.filter((device) => device.connection !== 'OFFLINE' && device.runState === 'FAULT').length,
    offline: view.devices.filter((device) => device.connection === 'OFFLINE').length,
    withoutData: view.devices.filter((device) => device.latestSampleAt === null).length,
    stale: view.devices.filter((device) => device.hasStaleData).length,
  };
}

/** A device probe read as its pipe's value only when the category has one device; with several, each node shows its own. */
export function soleReading(view: PlantView, category: PlantCategory, sourceKey: string): PlantReading | null {
  const devices = view.devices.filter((device) => device.category === category);
  return devices.length === 1 ? devices[0].reading(sourceKey) : null;
}
