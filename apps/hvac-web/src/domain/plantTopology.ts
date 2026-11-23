// Shared central-plant equipment taxonomy, hydraulic water-loop metadata, run-state rules, and
// parallel-branch grouping shared across HVAC Web and WeChat mini program via sync-shared.ts.

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

/** The plant's moving equipment; meters and weather stations never run. */
export const EQUIPMENT_CATEGORIES: ReadonlySet<PlantCategory> = new Set<PlantCategory>([
  'CHILLER',
  'CHILLED_WATER_PUMP',
  'COOLING_WATER_PUMP',
  'COOLING_TOWER',
]);

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

export const CATEGORY_BY_ASSET_TYPE: Readonly<Record<string, PlantCategory>> = {
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

// Registry source-key namespaces identify equipment roles when a device is unbound to an Asset.
export const CATEGORY_BY_SOURCE_NAMESPACE: Readonly<Record<string, PlantCategory>> = {
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

// Fallback equipment role for Gateway-wired devices with neither Asset bindings nor namespaces.
export const CATEGORY_BY_DEVICE_TYPE: Readonly<Record<string, PlantCategory>> = {
  ISOLATION_VALVE: 'ACQUISITION',
  MOTORIZED_VALVE: 'ACQUISITION',
  DIFFERENTIAL_PRESSURE_SENSOR: 'METER',
  HVAC_POWER_METER: 'METER',
  POWER_METER: 'METER',
  BTU_METER: 'METER',
  FLOW_METER: 'METER',
  TEMPERATURE_MODULE: 'ACQUISITION',
  CONTROL_MODULE: 'ACQUISITION',
  PROVISIONAL_READINGS: 'METER',
};

export const RUN_STATE_NAMESPACE_BY_CATEGORY: Readonly<Partial<Record<PlantCategory, string>>> = {
  CHILLER: 'chiller',
  CHILLED_WATER_PUMP: 'chwp',
  COOLING_WATER_PUMP: 'cwp',
  COOLING_TOWER: 'cooling_tower',
};

export const LOOP_DIFFERENTIAL_PRESSURE_KEYS = [
  'dp_sensor.differential_pressure',
  'header.differential_pressure',
] as const;

export type RunState = 'RUNNING' | 'STOPPED' | 'FAULT' | 'UNKNOWN';
export type ConnectionState = 'ONLINE' | 'SLEEPING' | 'OFFLINE' | 'UNKNOWN';

export function classifyPlantCategory(input: {
  readonly assetType?: string | null;
  readonly sourceKeys: readonly string[];
  readonly deviceType?: string | null;
}): PlantCategory {
  if (input.assetType) {
    const fromAsset = CATEGORY_BY_ASSET_TYPE[input.assetType];
    if (fromAsset) return fromAsset;
  }
  const isProvisionalUnbound = !input.assetType && input.deviceType === 'PROVISIONAL_READINGS';
  for (const sourceKey of input.sourceKeys) {
    const namespace = sourceKey.split('.')[0];
    if (namespace) {
      const fromSource = CATEGORY_BY_SOURCE_NAMESPACE[namespace];
      if (fromSource) {
        // Unbound PROVISIONAL_READINGS devices carry loop/header points (for example cooling-tower,
        // chwp-main) rather than physical equipment instances; ambient channels remain WEATHER/ZONE.
        if (isProvisionalUnbound && EQUIPMENT_CATEGORIES.has(fromSource)) return 'METER';
        return fromSource;
      }
    }
  }
  return input.deviceType ? (CATEGORY_BY_DEVICE_TYPE[input.deviceType] ?? 'OTHER') : 'OTHER';
}

export function parseRunState(value: unknown): RunState {
  return value === 'RUNNING' || value === 'STOPPED' || value === 'FAULT' ? value : 'UNKNOWN';
}

/** Running means reporting RUNNING while online (not OFFLINE or SLEEPING). */
export function isPlantDeviceRunning(device: {
  readonly connection: ConnectionState;
  readonly runState: RunState;
}): boolean {
  return device.connection !== 'OFFLINE' && device.connection !== 'SLEEPING' && device.runState === 'RUNNING';
}

export function parseWaterLoop(value: string | null | undefined): WaterLoopKind | null {
  if (value === 'CHILLED_WATER' || value === 'CONDENSER_WATER' || value === 'COOLING_TOWER_LOOP') {
    return value;
  }
  return null;
}

export function cleanBranchCode(value: string | null | undefined): string | null {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed ? trimmed : null;
}

export interface HydraulicAssetMeta {
  readonly waterLoop: WaterLoopKind | null;
  readonly branchCode: string | null;
}

export interface HydraulicAssetInput {
  readonly id: string;
  readonly waterLoop?: string | null;
  readonly branchCode?: string | null;
}

export interface HydraulicRelationshipInput {
  readonly fromType: string;
  readonly fromId: string;
  readonly toType: string;
  readonly toId: string;
  readonly role?: string;
}

/**
 * Indexes each asset's explicit loop/branch metadata and infers missing branch/loop fields when
 * active `ISOLATES`, `SERVES`, or `FEEDS_HEADER` asset relationships unambiguously resolve to one.
 */
export function inferHydraulicAssetIndex(
  assets: readonly HydraulicAssetInput[],
  activeRelationships: readonly HydraulicRelationshipInput[],
): Map<string, HydraulicAssetMeta> {
  const byAssetId = new Map<string, HydraulicAssetMeta>();
  for (const asset of assets) {
    if (!byAssetId.has(asset.id)) {
      byAssetId.set(asset.id, {
        waterLoop: parseWaterLoop(asset.waterLoop),
        branchCode: cleanBranchCode(asset.branchCode),
      });
    }
  }
  if (activeRelationships.length === 0) return byAssetId;

  const branchTargets = new Map<string, { waterLoop: WaterLoopKind | null; branchCode: string }[]>();
  const loopTargets = new Map<string, WaterLoopKind[]>();
  for (const rel of activeRelationships) {
    if (rel.fromType !== 'ASSET' || rel.toType !== 'ASSET') continue;
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

export interface BranchDeviceLike {
  readonly deviceId: string;
  readonly name: string;
  readonly category: PlantCategory;
  readonly assetType: string | null;
  readonly waterLoop: WaterLoopKind | null;
  readonly branchCode: string | null;
  readonly connection: ConnectionState;
  readonly runState: RunState;
  readonly hasStaleData: boolean;
  reading(sourceKey: string): unknown | null;
}

export interface ParallelBranchGroup<TDevice extends BranchDeviceLike> {
  readonly key: string;
  readonly waterLoop: WaterLoopKind | null;
  readonly loopLabel: string;
  readonly branchCode: string;
  readonly devices: readonly TDevice[];
  readonly equipmentCount: number;
  readonly runningCount: number;
  readonly hasAlert: boolean;
}

export function branchDeviceOrder(device: BranchDeviceLike): number {
  if (device.category === 'CHILLER') return 0;
  if (device.category === 'CHILLED_WATER_PUMP') return 1;
  if (device.category === 'COOLING_WATER_PUMP') return 2;
  if (device.category === 'COOLING_TOWER') return 3;
  if (
    device.assetType === 'MOTORIZED_VALVE'
    || device.assetType === 'ISOLATION_VALVE'
    || device.reading('valve.open_state') !== null
    || device.reading('valve.position_feedback') !== null
  ) {
    return 4;
  }
  if (device.category === 'METER') return 5;
  return 6;
}

export function buildParallelBranchGroups<TDevice extends BranchDeviceLike>(
  devices: readonly TDevice[],
): {
  branches: readonly ParallelBranchGroup<TDevice>[];
  loopBranches: Readonly<Partial<Record<WaterLoopKind, number>>>;
} {
  const grouped = new Map<string, { waterLoop: WaterLoopKind | null; branchCode: string; devices: TDevice[] }>();
  for (const device of devices) {
    if (!device.branchCode) continue;
    const key = `${device.waterLoop ?? 'UNSPECIFIED'}::${device.branchCode}`;
    const existing = grouped.get(key);
    if (existing) existing.devices.push(device);
    else grouped.set(key, { waterLoop: device.waterLoop, branchCode: device.branchCode, devices: [device] });
  }
  const branches: ParallelBranchGroup<TDevice>[] = [...grouped.entries()]
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
        runningCount: equipment.filter(isPlantDeviceRunning).length,
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

export function findLoopDifferentialPressure<TReading>(
  devices: readonly {
    readonly waterLoop: WaterLoopKind | null;
    reading(sourceKey: string): TReading | null;
  }[],
  waterLoop: WaterLoopKind,
): TReading | null {
  const firstOf = (subset: typeof devices): TReading | null => {
    for (const key of LOOP_DIFFERENTIAL_PRESSURE_KEYS) {
      for (const device of subset) {
        const reading = device.reading(key);
        if (reading !== null) return reading;
      }
    }
    return null;
  };
  const scopedMatch = firstOf(devices.filter((device) => device.waterLoop === waterLoop));
  if (scopedMatch !== null) return scopedMatch;
  if (waterLoop === 'CHILLED_WATER') {
    return firstOf(devices.filter((device) => device.waterLoop === null));
  }
  return null;
}

export interface RunStatePointCandidate {
  readonly sourceKey: string;
  readonly pointCode: string;
  readonly pointType?: string;
  readonly status?: string;
}

const EQUIPMENT_RUN_STATE_KEYS: ReadonlySet<string> = new Set(
  Object.values(RUN_STATE_NAMESPACE_BY_CATEGORY).map((namespace) => `${namespace}.run_state`),
);

export function findRunStatePoint<TPoint extends RunStatePointCandidate>(
  points: readonly TPoint[],
  category: PlantCategory,
): TPoint | undefined {
  const active = points.filter(
    (point) => point.status !== 'INACTIVE' && point.status !== 'RETIRED' && point.pointType !== 'COMMAND',
  );
  const namespace = RUN_STATE_NAMESPACE_BY_CATEGORY[category];
  if (!namespace) {
    // Non-equipment carriers (such as PROVISIONAL_READINGS chwp-main for interlocks) only match
    // explicit equipment-namespaced run_state keys, never bare run_state (such as ET1010 DO state).
    return active.find((point) => EQUIPMENT_RUN_STATE_KEYS.has(point.sourceKey));
  }
  const namespaced = `${namespace}.run_state`;
  return active.find((point) => point.sourceKey === namespaced)
    ?? active.find((point) => point.sourceKey === 'run_state' || point.pointCode === 'run_state');
}

export function findFaultCodePoint<TPoint extends RunStatePointCandidate>(
  points: readonly TPoint[],
): TPoint | undefined {
  const active = points.filter(
    (point) => point.status !== 'INACTIVE' && point.status !== 'RETIRED' && point.pointType !== 'COMMAND',
  );
  return active.find((point) => point.sourceKey.endsWith('.fault_code'))
    ?? active.find((point) => point.sourceKey === 'fault_code' || point.pointCode === 'fault_code');
}

export interface AggregatableReadingLike {
  readonly current: boolean;
  readonly lowConfidence?: boolean;
  readonly state?: string;
  readonly sampledAt?: string | null;
}

export interface AggregatableDeviceLike<TReading extends AggregatableReadingLike> extends BranchDeviceLike {
  readonly assetId: string | null;
  readonly faultCode: string | null;
  readonly latestSampleAt: string | null;
  reading(sourceKey: string): TReading | null;
}

export interface AggregatedPlantUnitMeta<
  TDevice extends AggregatableDeviceLike<TReading>,
  TReading extends AggregatableReadingLike,
> {
  readonly unitId: string;
  readonly primary: TDevice;
  readonly members: readonly TDevice[];
  readonly deviceIds: readonly string[];
  readonly offlineMemberCount: number;
  readonly connection: ConnectionState;
  readonly runState: RunState;
  readonly faultCode: string | null;
  readonly latestSampleAt: string | null;
  readonly hasStaleData: boolean;
  pickReading(sourceKey: string): TReading | null;
}

export function readingQualityRank(reading: AggregatableReadingLike): number {
  if (reading.current) return 3;
  if (reading.lowConfidence) return 2;
  if (reading.state !== undefined) return reading.state === 'PRESENT' ? 1 : 0;
  return reading.sampledAt != null ? 1 : 0;
}

const RUN_STATE_PRIORITY: readonly RunState[] = ['FAULT', 'RUNNING', 'STOPPED', 'UNKNOWN'];

function latestIsoTimestamp(values: readonly (string | null)[]): string | null {
  let result: string | null = null;
  let resultMs = Number.NEGATIVE_INFINITY;
  for (const value of values) {
    if (!value) continue;
    const ms = Date.parse(value);
    if (Number.isFinite(ms) && ms > resultMs) {
      result = value;
      resultMs = ms;
    }
  }
  return result;
}

function aggregateConnectionState(members: readonly { readonly connection: ConnectionState }[]): ConnectionState {
  if (members.some((member) => member.connection === 'ONLINE')) return 'ONLINE';
  if (members.some((member) => member.connection === 'SLEEPING')) return 'SLEEPING';
  if (members.some((member) => member.connection === 'OFFLINE')) return 'OFFLINE';
  return 'UNKNOWN';
}

function aggregateRunStateAndFault(
  members: readonly { readonly connection: ConnectionState; readonly runState: RunState; readonly faultCode: string | null }[],
): { readonly runState: RunState; readonly faultCode: string | null } {
  const active = members.filter((member) => member.connection !== 'OFFLINE' && member.connection !== 'SLEEPING');
  const pool = active.length > 0 ? active : members;
  let runState: RunState = 'UNKNOWN';
  for (const candidate of RUN_STATE_PRIORITY) {
    if (pool.some((member) => member.runState === candidate)) {
      runState = candidate;
      break;
    }
  }
  const faultCode = pool.find((member) => member.runState === 'FAULT' && member.faultCode)?.faultCode
    ?? pool.find((member) => member.faultCode)?.faultCode
    ?? members.find((member) => member.faultCode)?.faultCode
    ?? null;
  return { runState, faultCode };
}

/**
 * Collapses multiple communication devices bound to the same physical Asset into one operational
 * unit while keeping unbound devices standalone.
 */
export function aggregatePlantUnits<
  TDevice extends AggregatableDeviceLike<TReading>,
  TReading extends AggregatableReadingLike,
>(
  devices: readonly TDevice[],
  options: { readonly observedPointCount: (device: TDevice) => number },
): AggregatedPlantUnitMeta<TDevice, TReading>[] {
  const grouped = new Map<string, TDevice[]>();
  for (const device of devices) {
    const key = device.assetId ? `asset:${device.assetId}` : `device:${device.deviceId}`;
    const bucket = grouped.get(key);
    if (bucket) bucket.push(device);
    else grouped.set(key, [device]);
  }

  const compareMembers = (left: TDevice, right: TDevice): number => {
    const pointDiff = options.observedPointCount(right) - options.observedPointCount(left);
    if (pointDiff !== 0) return pointDiff;
    const knownDiff = (right.runState !== 'UNKNOWN' ? 1 : 0) - (left.runState !== 'UNKNOWN' ? 1 : 0);
    if (knownDiff !== 0) return knownDiff;
    const onlineDiff = (right.connection === 'ONLINE' ? 1 : 0) - (left.connection === 'ONLINE' ? 1 : 0);
    if (onlineDiff !== 0) return onlineDiff;
    return left.deviceId.localeCompare(right.deviceId);
  };

  const units: AggregatedPlantUnitMeta<TDevice, TReading>[] = [];
  for (const rawMembers of grouped.values()) {
    const members = [...rawMembers].sort(compareMembers);
    const primary = members[0];
    const unitId = primary.assetId ?? primary.deviceId;
    const { runState, faultCode } = aggregateRunStateAndFault(members);
    units.push({
      unitId,
      primary,
      members,
      deviceIds: members.map((member) => member.deviceId),
      offlineMemberCount: members.filter((member) => member.connection === 'OFFLINE').length,
      connection: aggregateConnectionState(members),
      runState,
      faultCode,
      latestSampleAt: latestIsoTimestamp(members.map((member) => member.latestSampleAt)),
      hasStaleData: members.some((member) => member.hasStaleData),
      pickReading: (sourceKey) => {
        let best: TReading | null = null;
        let bestRank = -1;
        for (const member of members) {
          const candidate = member.reading(sourceKey);
          if (!candidate) continue;
          const rank = readingQualityRank(candidate);
          if (best === null || rank > bestRank) {
            best = candidate;
            bestRank = rank;
          }
        }
        return best;
      },
    });
  }

  const categoryRank = (category: PlantCategory): number => {
    const index = PLANT_CATEGORY_ORDER.indexOf(category);
    return index === -1 ? PLANT_CATEGORY_ORDER.length : index;
  };
  return units.sort((left, right) => {
    const catDiff = categoryRank(left.primary.category) - categoryRank(right.primary.category);
    if (catDiff !== 0) return catDiff;
    const byName = left.primary.name.localeCompare(right.primary.name, 'zh-CN');
    return byName !== 0 ? byName : left.unitId.localeCompare(right.unitId);
  });
}
