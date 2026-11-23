import type { SiteAssetModel } from '@/api/generated/platformGateway.gen';
import type { AssetsDeviceRow, AssetsPointView } from '@/features/assets/model';
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
  PLANT_CATEGORY_LABELS,
  PLANT_CATEGORY_ORDER,
  readingQualityRank,
  WATER_LOOP_LABELS,
  WATER_LOOP_ORDER,
  type AggregatedPlantUnitMeta,
  type ConnectionState,
  type HydraulicAssetInput,
  type HydraulicAssetMeta,
  type PlantCategory,
  type RunState,
  type WaterLoopKind,
} from '../../../domain/plantTopology.ts';

export {
  EQUIPMENT_CATEGORIES,
  PLANT_CATEGORY_LABELS,
  PLANT_CATEGORY_ORDER,
  WATER_LOOP_LABELS,
  WATER_LOOP_ORDER,
  type ConnectionState,
  type PlantCategory,
  type RunState,
  type WaterLoopKind,
};

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

export interface PlantView {
  readonly groups: readonly PlantGroup[];
  /** Raw communication devices (excluding GATEWAY): used for online counts, history queries, and header/probe reads. */
  readonly devices: readonly PlantDevice[];
  /** Asset-grouped physical plant units: used for equipment counts, schematic nodes, branches, twin, and ledger. */
  readonly units: readonly PlantUnit[];
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
  return classifyPlantCategory({
    assetType: row.binding.state === 'bound' ? row.binding.asset.assetType : null,
    sourceKeys: row.telemetryPoints.map((point) => point.sourceKey),
    deviceType: row.device.deviceType,
  });
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
  if (state === 'SLEEPING') return 'SLEEPING';
  if (state === 'OFFLINE') return 'OFFLINE';
  return 'UNKNOWN';
}

function runStateOf(row: AssetsDeviceRow, category: PlantCategory): RunState {
  const point = findRunStatePoint(row.telemetryPoints, category);
  const value = point ? snapshotValue(row, point.pointCode) : undefined;
  return parseRunState(value);
}

function faultCodeOf(row: AssetsDeviceRow): string | null {
  const point = findFaultCodePoint(row.telemetryPoints);
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

function buildHydraulicAssetIndex(
  rows: readonly AssetsDeviceRow[],
  assetModel?: Pick<SiteAssetModel, 'assets' | 'relationships'>,
): Map<string, HydraulicAssetMeta> {
  const assets: HydraulicAssetInput[] = [];
  for (const row of rows) {
    if (row.binding.state === 'bound') assets.push(row.binding.asset);
  }
  for (const asset of assetModel?.assets ?? []) {
    assets.push(asset);
  }
  const activeRelationships = (assetModel?.relationships ?? []).filter((rel) => rel.status === 'ACTIVE');
  return inferHydraulicAssetIndex(assets, activeRelationships);
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

function toPlantUnit(meta: AggregatedPlantUnitMeta<PlantDevice, PlantReading>): PlantUnit {
  const { primary, members, deviceIds, unitId, offlineMemberCount, connection, runState, faultCode, latestSampleAt, hasStaleData, pickReading } = meta;
  return {
    ...primary,
    unitId,
    members,
    deviceIds,
    offlineMemberCount,
    connection,
    runState,
    faultCode,
    latestSampleAt,
    hasStaleData,
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

export function plantUnits(
  view: Pick<PlantView, 'devices'> & { readonly units?: readonly PlantUnit[] },
): readonly PlantUnit[] {
  return view.units ?? view.devices.map(toStandaloneUnit);
}

export function findPlantUnit(
  view: Pick<PlantView, 'devices'> & { readonly units?: readonly PlantUnit[] },
  id: string | undefined,
): PlantUnit | undefined {
  if (!id) return undefined;
  return plantUnits(view).find(
    (unit) => unit.unitId === id || unit.deviceId === id || unit.assetId === id || unit.deviceIds.includes(id),
  );
}

export interface UnitPointEntry {
  readonly device: PlantDevice;
  readonly point: AssetsPointView;
}

export function unitPoints(unit: PlantUnit | PlantDevice): readonly UnitPointEntry[] {
  const members = 'members' in unit ? unit.members : [unit];
  return members.flatMap((member) => member.row.operational.points.map((point) => ({ device: member, point })));
}

export function unitPointTarget(
  unit: PlantUnit | PlantDevice,
  sourceKey: string,
): { readonly deviceId: string; readonly pointCode: string } | null {
  const members = 'members' in unit ? unit.members : [unit];
  let best: { readonly deviceId: string; readonly pointCode: string; readonly rank: number } | null = null;
  for (const member of members) {
    const point = member.row.telemetryPoints.find(
      (candidate) => candidate.sourceKey === sourceKey && candidate.status === 'ACTIVE',
    );
    if (!point) continue;
    const reading = member.reading(sourceKey);
    const rank = reading ? readingQualityRank(reading) : -1;
    if (!best || rank > best.rank) {
      best = { deviceId: member.deviceId, pointCode: point.pointCode, rank };
    }
  }
  return best ? { deviceId: best.deviceId, pointCode: best.pointCode } : null;
}

export function provisionalHeaderReading(view: Pick<PlantView, 'devices'>, sourceKey: string): PlantReading | null {
  for (const device of view.devices) {
    if (device.assetId !== null || device.row?.device?.deviceType !== 'PROVISIONAL_READINGS') continue;
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
  const units = aggregatePlantUnits<PlantDevice, PlantReading>(devices, { observedPointCount: (device) => device.row.operational.points.length })
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
    plantCopRange: copRange(plantCop, coolingCapacity),
  };
}

export function plantReading(view: PlantView, sourceKey: string): PlantReading | null {
  return firstReading(view.devices, sourceKey);
}

export function loopDifferentialPressure(view: PlantView, waterLoop: WaterLoopKind): PlantReading | null {
  return findLoopDifferentialPressure<PlantReading>(view.devices, waterLoop);
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

export interface TileReadings {
  readonly lead: { readonly sourceKey: string; readonly reading: PlantReading } | null;
  readonly facts: readonly PlantReading[];
}

/**
 * What a device tile shows: the first of the role's preferred readings it has, then up to two of
 * its role's other key readings. A device without any of them (a Gateway-wired meter or acquisition
 * module) shows its own numeric measurements instead, current ones first, so its tile is never blank.
 */
export function tileReadings(device: PlantUnit | PlantDevice, preferredKeys: readonly string[], factKeys: readonly string[]): TileReadings {
  const pick = (keys: readonly string[]) => keys
    .map((sourceKey) => ({ sourceKey, reading: device.reading(sourceKey) }))
    .filter((entry): entry is { sourceKey: string; reading: PlantReading } => Boolean(entry.reading));
  const preferred = pick(preferredKeys);
  if (preferred.length > 0) {
    const lead = preferred[0];
    return { lead, facts: pick(factKeys).filter((entry) => entry.sourceKey !== lead.sourceKey).map((entry) => entry.reading).slice(0, 2) };
  }
  const members = 'members' in device ? device.members : [device];
  const ownKeys: string[] = [];
  const seen = new Set<string>();
  for (const member of members) {
    for (const point of member.row.telemetryPoints) {
      if (point.status === 'ACTIVE' && point.pointType === 'TELEMETRY' && point.valueType === 'NUMBER' && !seen.has(point.sourceKey)) {
        seen.add(point.sourceKey);
        ownKeys.push(point.sourceKey);
      }
    }
  }
  const own = pick(ownKeys);
  const ordered = [...own.filter((entry) => entry.reading.current), ...own.filter((entry) => !entry.reading.current)];
  return { lead: ordered[0] ?? null, facts: ordered.slice(1, 3).map((entry) => entry.reading) };
}

export function isRunning(device: PlantDevice): boolean {
  return isPlantDeviceRunning(device);
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

export function equipmentStatus(view: Pick<PlantView, 'devices'> & { readonly units?: readonly PlantUnit[] }): EquipmentStatus {
  const equipment = plantUnits(view).filter((unit) => EQUIPMENT_CATEGORIES.has(unit.category));
  return {
    total: equipment.length,
    running: equipment.filter(isRunning).length,
    faults: equipment.filter((unit) => unit.connection !== 'OFFLINE' && unit.runState === 'FAULT').length,
    offline: view.devices.filter((device) => device.connection === 'OFFLINE').length,
    withoutData: view.devices.filter((device) => device.latestSampleAt === null).length,
    stale: view.devices.filter((device) => device.hasStaleData).length,
  };
}

/** A machine probe read as its pipe's value only when the category has one physical unit; with several, each node shows its own. */
export function soleReading(
  view: Pick<PlantView, 'devices'> & { readonly units?: readonly PlantUnit[] },
  category: PlantCategory,
  sourceKey: string,
): PlantReading | null {
  const units = plantUnits(view).filter((unit) => unit.category === category);
  return units.length === 1 ? units[0].reading(sourceKey) : null;
}
