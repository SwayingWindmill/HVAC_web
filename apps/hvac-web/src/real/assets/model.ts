import type {
  Space,
  AssetRelationship,
  Device,
  Asset,
  Sensor,
  SiteAssetModel,
  TelemetryPoint,
} from '../../api/generated/platformGateway.gen.ts';
import type { DeviceObservationSnapshot, ProblemDetails, TelemetryKeyState, TelemetryQuality } from '../../api/generated/s2Telemetry.gen.ts';
import {
  formatTelemetryDisplayValue,
  formatTelemetryUnit,
  getDeviceTelemetryProfile,
  telemetryPointDefinition,
} from '../../domain/centralPlantTelemetry.ts';
import {
  listPointDefinitions,
  resolveRealAssetsProfile,
  type RealAssetsProfileResolution,
} from './catalog.ts';

export type RealAssetsOperatingState = 'UNKNOWN' | 'OFFLINE' | 'ATTENTION' | 'NORMAL';

export type RealAssetsAttentionReason =
  | 'CURRENT_STATE_UNAVAILABLE'
  | 'CURRENT_STATE_NOT_VISIBLE'
  | 'POINT_CATALOG_CONTRACT_DRIFT'
  | 'POINT_CATALOG_UNCONFIGURED'
  | 'PRESENCE_UNKNOWN'
  | 'PRESENCE_OFFLINE'
  | 'TELEMETRY_STALE'
  | 'TELEMETRY_QUALITY_DEGRADED'
  | 'CRITICAL_POINT_MISSING'
  | 'TELEMETRY_INCOMPLETE';

export type RealAssetsSnapshotResult =
  | { readonly status: 'ok'; readonly snapshot: DeviceObservationSnapshot }
  | { readonly status: 'error'; readonly problem: ProblemDetails };

export interface RealAssetsPointView {
  readonly key: string;
  readonly label: string;
  readonly state: 'PRESENT' | 'MISSING';
  readonly displayValue: string;
  readonly unit: string | null;
  readonly freshness: 'FRESH' | 'STALE' | 'MISSING';
  readonly quality: TelemetryQuality | null;
  readonly qualityReasons: readonly string[];
  readonly sampledAt: string | null;
  readonly receivedAt: string | null;
  readonly policyRevision: number | null;
  readonly missingReason?: 'NEVER_OBSERVED' | 'ONLY_REJECTED_CANDIDATES' | 'POLICY_NOT_CONFIGURED';
}

export type RealAssetsBindingState =
  | { readonly state: 'bound'; readonly relationship: AssetRelationship; readonly asset: Asset }
  | { readonly state: 'multi-bound'; readonly bindings: readonly { readonly relationship: AssetRelationship; readonly asset: Asset }[] }
  | { readonly state: 'unbound' }
  | { readonly state: 'ambiguous'; readonly relationshipIds: readonly string[] };

export type RealAssetsSpaceState =
  | { readonly state: 'bound'; readonly relationship: AssetRelationship; readonly space: Space }
  | { readonly state: 'unbound' }
  | { readonly state: 'ambiguous'; readonly relationshipIds: readonly string[] };

export interface RealAssetsDeviceRow {
  readonly device: Device;
  readonly profile: RealAssetsProfileResolution;
  readonly binding: RealAssetsBindingState;
  readonly space: RealAssetsSpaceState;
  readonly registeredPointCount: number;
  readonly snapshotResult?: RealAssetsSnapshotResult;
  readonly operatingState: RealAssetsOperatingState;
  readonly attentionReasons: readonly RealAssetsAttentionReason[];
  readonly points: readonly RealAssetsPointView[];
}

export interface RealAssetsAssetRow {
  readonly asset: Asset;
  readonly space: RealAssetsSpaceState;
  readonly devices: readonly RealAssetsDeviceRow[];
  readonly sensors: readonly Sensor[];
  readonly points: readonly RealAssetsTelemetryPointRow[];
  readonly controlPoints: readonly RealAssetsTelemetryPointRow[];
  readonly operatingState: RealAssetsOperatingState;
  readonly attentionReasons: readonly RealAssetsAttentionReason[];
}

export interface BuildRealAssetsRowsInput {
  readonly assetModel: SiteAssetModel;
  readonly snapshots?: ReadonlyMap<string, RealAssetsSnapshotResult>;
  readonly now?: Date;
}

export type RealAssetsHierarchyKind = 'site' | 'space' | 'asset' | 'device' | 'sensor' | 'point' | 'virtual-sensor';

export interface RealAssetsHierarchyNode {
  readonly key: string;
  readonly kind: RealAssetsHierarchyKind;
  readonly label: string;
  readonly meta: string;
  readonly deviceIds: readonly string[];
  readonly pointIds: readonly string[];
  readonly children: readonly RealAssetsHierarchyNode[];
}

export interface RealAssetsTelemetryPointRow {
  readonly point: TelemetryPoint;
  readonly device: Device;
  readonly sensor: Sensor | null;
  readonly binding: RealAssetsBindingState;
  readonly space: RealAssetsSpaceState;
  readonly label: string;
  readonly current: RealAssetsPointView | null;
}

export interface BuildRealAssetsPointRowsInput {
  readonly assetModel: SiteAssetModel;
  readonly deviceRows: readonly RealAssetsDeviceRow[];
}

export interface BuildRealAssetsAssetRowsInput {
  readonly assetModel: SiteAssetModel;
  readonly deviceRows: readonly RealAssetsDeviceRow[];
  readonly pointRows: readonly RealAssetsTelemetryPointRow[];
  readonly now?: Date;
}

function compareText(left: string, right: string): number {
  return left.localeCompare(right, 'zh-CN', { sensitivity: 'base', numeric: true });
}

interface RegistryIdentity {
  readonly id: string;
  readonly displayName: string;
  readonly code?: string;
  readonly pointCode?: string;
}

function compareRegistryIdentity(left: RegistryIdentity, right: RegistryIdentity): number {
  return compareText(left.displayName, right.displayName)
    || compareText(left.code ?? left.pointCode ?? '', right.code ?? right.pointCode ?? '')
    || left.id.localeCompare(right.id);
}

const SPACE_TYPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  BUILDING: '建筑',
  PLANT_ROOM: '机房',
  ROOFTOP: '屋面',
  OUTDOOR: '室外',
});

const ASSET_TYPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  CHILLER: '冷水机组',
  CHILLED_WATER_PUMP: '冷冻水泵',
  COOLING_WATER_PUMP: '冷却水泵',
  COOLING_TOWER: '冷却塔',
  HVAC_POWER_METER: '中央空调总电表',
  BTU_METER: '中央空调总冷量表',
  WEATHER_STATION: '室外气象站',
});

const DEVICE_TYPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  CHILLER: '冷水机组控制器',
  WATER_COOLED_CHILLER: '冷水机组控制器',
  CHILLER_CONTROLLER: '冷水机组控制器',
  CHILLED_WATER_PUMP: '冷冻水泵控制器',
  COOLING_WATER_PUMP: '冷却水泵控制器',
  PUMP_CONTROLLER: '水泵控制器',
  COOLING_TOWER: '冷却塔控制器',
  COOLING_TOWER_CONTROLLER: '冷却塔控制器',
  HVAC_POWER_METER: '电表通信端点',
  POWER_METER: '电表通信端点',
  BTU_METER: '冷量表通信端点',
  WEATHER_STATION: '气象站通信端点',
});

const SENSOR_TYPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  TEMPERATURE: '温度',
  POWER: '功率',
  FLOW: '流量',
  ELECTRICAL_METER: '电能计量',
  THERMAL_METER: '冷量计量',
  WEATHER: '气象',
});

const POINT_TYPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  TELEMETRY: '遥测',
  COUNTER: '累计量',
  STATE: '状态',
  SETTING: '设定',
  COMMAND: '命令',
});

function normalizedType(value: string): string {
  return value.trim().toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_+|_+$/g, '');
}

function localizedType(value: string, labels: Readonly<Record<string, string>>, fallback: string): string {
  return labels[normalizedType(value)] ?? fallback;
}

export function realAssetsSpaceTypeLabel(value: string): string {
  return localizedType(value, SPACE_TYPE_LABELS, '区域');
}

export function realAssetsAssetTypeLabel(value: string): string {
  return localizedType(value, ASSET_TYPE_LABELS, getDeviceTelemetryProfile(value).title);
}

export function realAssetsDeviceTypeLabel(value: string): string {
  return localizedType(value, DEVICE_TYPE_LABELS, '通信设备');
}

export function realAssetsSensorTypeLabel(value: string): string {
  return localizedType(value, SENSOR_TYPE_LABELS, '传感器');
}

export function realAssetsPointTypeLabel(value: string): string {
  return localizedType(value, POINT_TYPE_LABELS, '点位');
}

export function realAssetsTelemetryPointLabel(point: TelemetryPoint): string {
  const definition = telemetryPointDefinition(point.pointCode);
  return definition.label === point.pointCode ? point.displayName : definition.label;
}

export function realAssetsTelemetryPointMeta(point: TelemetryPoint): string {
  const unit = formatTelemetryUnit(point.unit);
  return unit ? `${realAssetsPointTypeLabel(point.pointType)} · ${unit}` : realAssetsPointTypeLabel(point.pointType);
}

function isCurrentRelationship(relationship: AssetRelationship, now: Date): boolean {
  if (relationship.status !== 'ACTIVE') return false;
  const from = Date.parse(relationship.validFrom);
  const to = relationship.validTo ? Date.parse(relationship.validTo) : Number.POSITIVE_INFINITY;
  return Number.isFinite(from) && from <= now.getTime() && now.getTime() < to;
}

function currentRelationships(
  relationships: readonly AssetRelationship[],
  fromType: AssetRelationship['fromType'],
  fromId: string,
  toType: AssetRelationship['toType'],
  now: Date,
): AssetRelationship[] {
  return relationships
    .filter((relationship) => relationship.fromType === fromType
      && relationship.fromId === fromId
      && relationship.toType === toType
      && isCurrentRelationship(relationship, now))
    .sort((left, right) => right.revision - left.revision || left.id.localeCompare(right.id));
}

export function resolveDeviceBinding(
  device: Device,
  relationships: readonly AssetRelationship[],
  assetById: ReadonlyMap<string, Asset>,
  now = new Date(),
): RealAssetsBindingState {
  const candidates = currentRelationships(relationships, 'DEVICE', device.id, 'ASSET', now);
  if (candidates.length === 0) return { state: 'unbound' };
  const bindings: { relationship: AssetRelationship; asset: Asset }[] = [];
  const seenAssetIds = new Set<string>();
  for (const relationship of candidates) {
    if (seenAssetIds.has(relationship.toId)) continue;
    const asset = assetById.get(relationship.toId);
    if (!asset) return { state: 'ambiguous', relationshipIds: candidates.map((candidate) => candidate.id) };
    seenAssetIds.add(relationship.toId);
    bindings.push({ relationship, asset });
  }
  if (bindings.length === 1) return { state: 'bound', ...bindings[0] };
  return { state: 'multi-bound', bindings };
}

function resolvedAssetBindings(binding: RealAssetsBindingState): readonly { readonly relationship: AssetRelationship; readonly asset: Asset }[] {
  if (binding.state === 'bound') return [binding];
  if (binding.state === 'multi-bound') return binding.bindings;
  return [];
}

export function resolveDeviceSpace(
  device: Device,
  binding: RealAssetsBindingState,
  relationships: readonly AssetRelationship[],
  spaceById: ReadonlyMap<string, Space>,
  now = new Date(),
): RealAssetsSpaceState {
  const direct = currentRelationships(relationships, 'DEVICE', device.id, 'SPACE', now);
  const inherited = resolvedAssetBindings(binding).flatMap((item) => (
    currentRelationships(relationships, 'ASSET', item.asset.id, 'SPACE', now)
  ));
  const candidates = direct.length > 0 ? direct : inherited;
  if (candidates.length === 0) return { state: 'unbound' };
  const spaceIds = new Set(candidates.map((relationship) => relationship.toId));
  if (spaceIds.size !== 1) return { state: 'ambiguous', relationshipIds: candidates.map((relationship) => relationship.id) };
  const relationship = candidates[0];
  const space = spaceById.get(relationship.toId);
  if (!space) return { state: 'ambiguous', relationshipIds: candidates.map((candidate) => candidate.id) };
  return { state: 'bound', relationship, space };
}

export function resolveAssetSpace(
  asset: Asset,
  relationships: readonly AssetRelationship[],
  spaceById: ReadonlyMap<string, Space>,
  now = new Date(),
): RealAssetsSpaceState {
  const candidates = currentRelationships(relationships, 'ASSET', asset.id, 'SPACE', now);
  if (candidates.length === 0) return { state: 'unbound' };
  const spaceIds = new Set(candidates.map((relationship) => relationship.toId));
  if (spaceIds.size !== 1) return { state: 'ambiguous', relationshipIds: candidates.map((relationship) => relationship.id) };
  const relationship = candidates[0];
  const space = spaceById.get(relationship.toId);
  if (!space) return { state: 'ambiguous', relationshipIds: candidates.map((candidate) => candidate.id) };
  return { state: 'bound', relationship, space };
}

interface PointDisplayDefinition {
  readonly key: string;
  readonly label: string;
  readonly defaultUnit?: string;
  readonly precision?: number;
}

function pointView(definition: PointDisplayDefinition, state: TelemetryKeyState | undefined): RealAssetsPointView {
  if (!state || state.state === 'MISSING') {
    return {
      key: definition.key,
      label: definition.label,
      state: 'MISSING',
      displayValue: state?.missingReason === 'ONLY_REJECTED_CANDIDATES' ? '当前值不可用' : '未观测',
      unit: null,
      freshness: 'MISSING',
      quality: null,
      qualityReasons: [],
      sampledAt: null,
      receivedAt: null,
      policyRevision: state?.policyRevision ?? null,
      missingReason: state?.missingReason,
    };
  }
  return {
    key: definition.key,
    label: definition.label,
    state: 'PRESENT',
    displayValue: formatTelemetryDisplayValue(state.value, definition.precision ?? 3),
    unit: formatTelemetryUnit(state.unit ?? definition.defaultUnit),
    freshness: state.freshness,
    quality: state.quality,
    qualityReasons: [...state.qualityReasons],
    sampledAt: state.sampledAt,
    receivedAt: state.receivedAt,
    policyRevision: state.policyRevision,
  };
}

export function projectRealAssetsOperatingState(
  snapshotResult: RealAssetsSnapshotResult | undefined,
  profile: RealAssetsProfileResolution,
): { readonly state: RealAssetsOperatingState; readonly reasons: readonly RealAssetsAttentionReason[]; readonly points: readonly RealAssetsPointView[] } {
  if (!snapshotResult) {
    return { state: 'UNKNOWN', reasons: ['CURRENT_STATE_UNAVAILABLE'], points: [] };
  }
  if (snapshotResult.status === 'error') {
    const reason: RealAssetsAttentionReason = snapshotResult.problem.code === 'TELEMETRY_KEY_INVALID'
      ? 'POINT_CATALOG_CONTRACT_DRIFT'
      : snapshotResult.problem.code === 'RESOURCE_NOT_FOUND'
        ? 'CURRENT_STATE_NOT_VISIBLE'
        : 'CURRENT_STATE_UNAVAILABLE';
    return { state: 'UNKNOWN', reasons: [reason], points: [] };
  }
  const snapshot = snapshotResult.snapshot;
  const definitions = listPointDefinitions(profile);
  const valueByKey = new Map(snapshot.values.map((value) => [value.key, value]));
  const points = definitions.map((definition) => pointView(definition, valueByKey.get(definition.key)));
  if (snapshot.evaluationAvailability !== 'AVAILABLE') {
    return { state: 'UNKNOWN', reasons: ['CURRENT_STATE_UNAVAILABLE'], points };
  }
  if (snapshot.presence.applicability !== 'APPLICABLE'
    || snapshot.presence.currentState === null
    || snapshot.presence.currentState === 'UNKNOWN') {
    return { state: 'UNKNOWN', reasons: ['PRESENCE_UNKNOWN'], points };
  }
  if (snapshot.presence.currentState === 'OFFLINE') {
    return { state: 'OFFLINE', reasons: ['PRESENCE_OFFLINE'], points };
  }
  if (profile.state === 'unconfigured') {
    return { state: 'UNKNOWN', reasons: ['POINT_CATALOG_UNCONFIGURED'], points };
  }

  const reasons = new Set<RealAssetsAttentionReason>();
  for (const definition of definitions) {
    if (!definition.critical) continue;
    const value = valueByKey.get(definition.key);
    if (!value || value.state === 'MISSING') {
      reasons.add('CRITICAL_POINT_MISSING');
      continue;
    }
    if (value.freshness === 'STALE') reasons.add('TELEMETRY_STALE');
    if (value.quality !== 'GOOD') reasons.add('TELEMETRY_QUALITY_DEGRADED');
  }
  if (snapshot.telemetryReadiness === 'DEGRADED' || snapshot.telemetryReadiness === 'INCOMPLETE') {
    reasons.add('TELEMETRY_INCOMPLETE');
  }
  return reasons.size > 0
    ? { state: 'ATTENTION', reasons: [...reasons], points }
    : { state: 'NORMAL', reasons: [], points };
}

export function buildRealAssetsRows(input: BuildRealAssetsRowsInput): RealAssetsDeviceRow[] {
  const now = input.now ?? new Date();
  const assetById = new Map(input.assetModel.assets.map((item) => [item.id, item]));
  const spaceById = new Map(input.assetModel.spaces.map((item) => [item.id, item]));
  const pointCountByDevice = new Map<string, number>();
  for (const point of input.assetModel.telemetryPoints) {
    pointCountByDevice.set(point.reportingDeviceId, (pointCountByDevice.get(point.reportingDeviceId) ?? 0) + 1);
  }
  const rows = input.assetModel.devices.map((device): RealAssetsDeviceRow => {
    const profile = resolveRealAssetsProfile(device.deviceType);
    const snapshotResult = input.snapshots?.get(device.id);
    const projection = projectRealAssetsOperatingState(snapshotResult, profile);
    const binding = resolveDeviceBinding(device, input.assetModel.relationships, assetById, now);
    return {
      device,
      profile,
      binding,
      space: resolveDeviceSpace(device, binding, input.assetModel.relationships, spaceById, now),
      registeredPointCount: pointCountByDevice.get(device.id) ?? 0,
      snapshotResult,
      operatingState: projection.state,
      attentionReasons: projection.reasons,
      points: projection.points,
    };
  });
  return rows.sort((left, right) => {
    const leftSpace = left.space.state === 'bound' ? left.space.space : undefined;
    const rightSpace = right.space.state === 'bound' ? right.space.space : undefined;
    if (leftSpace && rightSpace) {
      const spaceOrder = compareRegistryIdentity(leftSpace, rightSpace);
      if (spaceOrder) return spaceOrder;
    } else if (leftSpace) return -1;
    else if (rightSpace) return 1;
    const leftAsset = [...resolvedAssetBindings(left.binding)].map((item) => item.asset).sort(compareRegistryIdentity)[0];
    const rightAsset = [...resolvedAssetBindings(right.binding)].map((item) => item.asset).sort(compareRegistryIdentity)[0];
    if (leftAsset && rightAsset) return compareRegistryIdentity(leftAsset, rightAsset) || compareRegistryIdentity(left.device, right.device);
    if (leftAsset) return -1;
    if (rightAsset) return 1;
    return compareRegistryIdentity(left.device, right.device);
  });
}

export function buildRealAssetsAssetRows(input: BuildRealAssetsAssetRowsInput): RealAssetsAssetRow[] {
  const now = input.now ?? new Date();
  const spaceById = new Map(input.assetModel.spaces.map((space) => [space.id, space]));
  const sensorById = new Map(input.assetModel.sensors.map((sensor) => [sensor.id, sensor]));
  const deviceRowsByAsset = new Map<string, RealAssetsDeviceRow[]>();
  for (const deviceRow of input.deviceRows) {
    for (const binding of resolvedAssetBindings(deviceRow.binding)) {
      const rows = deviceRowsByAsset.get(binding.asset.id) ?? [];
      rows.push(deviceRow);
      deviceRowsByAsset.set(binding.asset.id, rows);
    }
  }

  return input.assetModel.assets.map((asset): RealAssetsAssetRow => {
    const devices = [...(deviceRowsByAsset.get(asset.id) ?? [])].sort((left, right) => compareRegistryIdentity(left.device, right.device));
    const sensorIds = new Set(input.assetModel.relationships
      .filter((relationship) => relationship.fromType === 'SENSOR'
        && relationship.toType === 'ASSET'
        && relationship.toId === asset.id
        && isCurrentRelationship(relationship, now))
      .map((relationship) => relationship.fromId));
    const sensors = [...sensorIds]
      .map((sensorId) => sensorById.get(sensorId))
      .filter((sensor): sensor is Sensor => Boolean(sensor))
      .sort(compareRegistryIdentity);
    const pointRelationships = input.assetModel.relationships.filter((relationship) => relationship.fromType === 'POINT'
      && relationship.toType === 'ASSET'
      && relationship.toId === asset.id
      && isCurrentRelationship(relationship, now));
    const pointRelationshipById = new Map(pointRelationships.map((relationship) => [relationship.fromId, relationship]));
    const points = input.pointRows
      .filter((row) => pointRelationshipById.has(row.point.id))
      .sort((left, right) => compareRegistryIdentity(left.point, right.point));
    const controlPoints = points.filter((row) => {
      const relationship = pointRelationshipById.get(row.point.id);
      return row.point.status === 'ACTIVE'
        && row.point.pointType === 'COMMAND'
        && row.point.writable
        && relationship?.role === 'CONTROLS';
    });
    const attentionReasons = [...new Set(devices.flatMap((row) => row.attentionReasons))];
    const operatingState: RealAssetsOperatingState = devices.length === 0
      ? 'UNKNOWN'
      : devices.every((row) => row.operatingState === 'NORMAL')
        ? 'NORMAL'
        : devices.every((row) => row.operatingState === 'OFFLINE')
          ? 'OFFLINE'
          : 'ATTENTION';
    return {
      asset,
      space: resolveAssetSpace(asset, input.assetModel.relationships, spaceById, now),
      devices,
      sensors,
      points,
      controlPoints,
      operatingState,
      attentionReasons,
    };
  }).sort((left, right) => {
    const leftSpace = left.space.state === 'bound' ? left.space.space : undefined;
    const rightSpace = right.space.state === 'bound' ? right.space.space : undefined;
    if (leftSpace && rightSpace) return compareRegistryIdentity(leftSpace, rightSpace) || compareRegistryIdentity(left.asset, right.asset);
    if (leftSpace) return -1;
    if (rightSpace) return 1;
    return compareRegistryIdentity(left.asset, right.asset);
  });
}

export function buildRealAssetsPointRows(input: BuildRealAssetsPointRowsInput): RealAssetsTelemetryPointRow[] {
  const deviceRowById = new Map(input.deviceRows.map((row) => [row.device.id, row]));
  const deviceOrder = new Map(input.deviceRows.map((row, index) => [row.device.id, index]));
  const sensorById = new Map(input.assetModel.sensors.map((sensor) => [sensor.id, sensor]));
  const rows = input.assetModel.telemetryPoints.map((point): RealAssetsTelemetryPointRow => {
    const deviceRow = deviceRowById.get(point.reportingDeviceId);
    if (!deviceRow) throw new Error(`Telemetry Point ${point.id} has no visible Device Endpoint row`);
    const definition = telemetryPointDefinition(point.pointCode);
    const snapshot = deviceRow.snapshotResult?.status === 'ok' ? deviceRow.snapshotResult.snapshot : null;
    const currentState = snapshot?.values.find((value) => value.key === point.pointCode);
    return {
      point,
      device: deviceRow.device,
      sensor: point.sensorId ? sensorById.get(point.sensorId) ?? null : null,
      binding: deviceRow.binding,
      space: deviceRow.space,
      label: realAssetsTelemetryPointLabel(point),
      current: snapshot ? pointView({
        key: point.pointCode,
        label: realAssetsTelemetryPointLabel(point),
        defaultUnit: point.unit ?? definition.defaultUnit,
        precision: definition.precision,
      }, currentState) : null,
    };
  });
  return rows.sort((left, right) => (
    (deviceOrder.get(left.device.id) ?? Number.MAX_SAFE_INTEGER) - (deviceOrder.get(right.device.id) ?? Number.MAX_SAFE_INTEGER)
    || compareText(left.label, right.label)
    || compareText(left.point.pointCode, right.point.pointCode)
    || left.point.id.localeCompare(right.point.id)
  ));
}

function oneCurrentTargetId(
  relationships: readonly AssetRelationship[],
  fromType: AssetRelationship['fromType'],
  fromId: string,
  toType: AssetRelationship['toType'],
  now: Date,
): string | null {
  const targets = new Set(currentRelationships(relationships, fromType, fromId, toType, now).map((relationship) => relationship.toId));
  return targets.size === 1 ? [...targets][0] : null;
}

function currentTargetIds(
  relationships: readonly AssetRelationship[],
  fromType: AssetRelationship['fromType'],
  fromId: string,
  toType: AssetRelationship['toType'],
  now: Date,
): readonly string[] {
  return [...new Set(currentRelationships(relationships, fromType, fromId, toType, now).map((relationship) => relationship.toId))];
}

function hierarchyNode(
  kind: RealAssetsHierarchyKind,
  id: string,
  label: string,
  meta: string,
  deviceIds: readonly string[],
  children: readonly RealAssetsHierarchyNode[] = [],
  pointIds: readonly string[] = [],
): RealAssetsHierarchyNode {
  return {
    key: `${kind}:${id}`,
    kind,
    label,
    meta,
    deviceIds: [...new Set(deviceIds)],
    pointIds: [...new Set([...pointIds, ...children.flatMap((child) => child.pointIds)])],
    children,
  };
}

export function buildRealAssetsHierarchy(model: SiteAssetModel, siteLabel: string, now = new Date()): RealAssetsHierarchyNode {
  const assetSpace = new Map(model.assets.map((item) => [
    item.id,
    oneCurrentTargetId(model.relationships, 'ASSET', item.id, 'SPACE', now),
  ]));
  const deviceAsset = new Map(model.devices.map((item) => [
    item.id,
    currentTargetIds(model.relationships, 'DEVICE', item.id, 'ASSET', now),
  ]));
  const deviceSpace = new Map(model.devices.map((item) => [
    item.id,
    oneCurrentTargetId(model.relationships, 'DEVICE', item.id, 'SPACE', now),
  ]));
  const sensorDevice = new Map(model.sensors.map((item) => [
    item.id,
    oneCurrentTargetId(model.relationships, 'SENSOR', item.id, 'DEVICE', now),
  ]));
  const pointsBySensor = new Map<string, TelemetryPoint[]>();
  const directPointsByDevice = new Map<string, TelemetryPoint[]>();
  for (const point of model.telemetryPoints) {
    if (point.sensorId) {
      const items = pointsBySensor.get(point.sensorId) ?? [];
      items.push(point);
      pointsBySensor.set(point.sensorId, items);
    } else {
      const items = directPointsByDevice.get(point.reportingDeviceId) ?? [];
      items.push(point);
      directPointsByDevice.set(point.reportingDeviceId, items);
    }
  }

  const deviceNode = (device: Device, parentKey: string): RealAssetsHierarchyNode => {
    const branchKey = `${parentKey}:${device.id}`;
    const sensors = model.sensors
      .filter((sensor) => sensorDevice.get(sensor.id) === device.id)
      .sort(compareRegistryIdentity)
      .map((sensor) => hierarchyNode(
        'sensor',
        `${branchKey}:${sensor.id}`,
        sensor.displayName,
        `传感器 · ${realAssetsSensorTypeLabel(sensor.sensorType)}`,
        [device.id],
        (pointsBySensor.get(sensor.id) ?? []).sort(compareRegistryIdentity).map((point) => hierarchyNode(
          'point',
          `${branchKey}:${sensor.id}:${point.id}`,
          realAssetsTelemetryPointLabel(point),
          realAssetsTelemetryPointMeta(point),
          [device.id],
          [],
          [point.id],
        )),
      ));
    const directPoints = (directPointsByDevice.get(device.id) ?? []).sort(compareRegistryIdentity);
    const children = directPoints.length > 0
      ? [...sensors, hierarchyNode(
        'virtual-sensor',
        branchKey,
        '设备直连点位',
        '设备直连标准 Point',
        [device.id],
        directPoints.map((point) => hierarchyNode(
          'point',
          `${branchKey}:direct:${point.id}`,
          realAssetsTelemetryPointLabel(point),
          realAssetsTelemetryPointMeta(point),
          [device.id],
          [],
          [point.id],
        )),
      )]
      : sensors;
    return hierarchyNode(
      'device',
      branchKey,
      '通讯端点',
      `${device.displayName} · ${realAssetsDeviceTypeLabel(device.deviceType)}`,
      [device.id],
      children,
    );
  };

  const assetNode = (asset: Asset): RealAssetsHierarchyNode => {
    const boundDevices = model.devices
      .filter((device) => deviceAsset.get(device.id)?.includes(asset.id))
      .sort(compareRegistryIdentity);
    const endpointNodes = boundDevices.map((device) => deviceNode(device, `asset:${asset.id}`));
    const collapseSingleEndpoint = boundDevices.length === 1
      && deviceAsset.get(boundDevices[0].id)?.length === 1;
    const children = collapseSingleEndpoint ? endpointNodes[0].children : endpointNodes;
    return hierarchyNode(
      'asset',
      asset.id,
      realAssetsAssetTypeLabel(asset.assetType),
      `设备 · ${asset.code || asset.displayName}`,
      endpointNodes.flatMap((node) => node.deviceIds),
      children,
    );
  };

  const spaceChildren = new Map<string | null, Space[]>();
  for (const space of model.spaces) {
    const siblings = spaceChildren.get(space.parentSpaceId) ?? [];
    siblings.push(space);
    spaceChildren.set(space.parentSpaceId, siblings);
  }
  const spaceNode = (space: Space): RealAssetsHierarchyNode => {
    const nestedSpaces = (spaceChildren.get(space.id) ?? []).sort(compareRegistryIdentity).map(spaceNode);
    const asset = model.assets
      .filter((item) => assetSpace.get(item.id) === space.id)
      .sort(compareRegistryIdentity)
      .map(assetNode);
    const directDevices = model.devices
      .filter((device) => deviceAsset.get(device.id)?.length === 0 && deviceSpace.get(device.id) === space.id)
      .sort(compareRegistryIdentity)
      .map((device) => deviceNode(device, `space:${space.id}`));
    const directDeviceGroup = directDevices.length > 0
      ? [hierarchyNode('asset', `unbound:${space.id}`, '未绑定设备', '关系待治理', directDevices.flatMap((node) => node.deviceIds), directDevices)]
      : [];
    const children = [...nestedSpaces, ...asset, ...directDeviceGroup];
    return hierarchyNode(
      'space',
      space.id,
      space.displayName,
      `区域 · ${realAssetsSpaceTypeLabel(space.spaceType)}`,
      children.flatMap((node) => node.deviceIds),
      children,
    );
  };

  const roots = (spaceChildren.get(null) ?? []).sort(compareRegistryIdentity).map(spaceNode);
  const unassignedAsset = model.assets.filter((item) => !assetSpace.get(item.id)).sort(compareRegistryIdentity).map(assetNode);
  const unassignedDevices = model.devices
    .filter((device) => deviceAsset.get(device.id)?.length === 0 && !deviceSpace.get(device.id))
    .sort(compareRegistryIdentity)
    .map((device) => deviceNode(device, `site:${model.siteId}`));
  const orphanChildren = [
    ...unassignedAsset,
    ...(unassignedDevices.length > 0
      ? [hierarchyNode('asset', 'unbound:site', '未绑定设备', '关系待治理', unassignedDevices.flatMap((node) => node.deviceIds), unassignedDevices)]
      : []),
  ];
  const children = orphanChildren.length > 0
    ? [...roots, hierarchyNode('space', 'unbound:site', '未分配区域', '关系待治理', orphanChildren.flatMap((node) => node.deviceIds), orphanChildren)]
    : roots;
  return hierarchyNode('site', model.siteId, siteLabel, '站点资产', model.devices.map((device) => device.id), children);
}

export function isRealAssetsAttentionState(state: RealAssetsOperatingState): boolean {
  return state !== 'NORMAL';
}
