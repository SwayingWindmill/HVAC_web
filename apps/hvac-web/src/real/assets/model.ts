import type {
  Area,
  AssetRelationship,
  Device,
  Equipment,
  SiteAssetModel,
  TelemetryPoint,
} from '../../api/generated/platformGateway.gen.ts';
import type { DeviceObservationSnapshot, ProblemDetails, TelemetryKeyState } from '../../api/generated/s2Telemetry.gen.ts';
import { formatTelemetryDisplayValue, formatTelemetryUnit } from '../../domain/centralPlantTelemetry.ts';
import {
  listPointDefinitions,
  resolveRealAssetsProfile,
  type RealAssetsPointDefinition,
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
  | 'TELEMETRY_SUSPECT'
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
  readonly quality: 'GOOD' | 'SUSPECT' | null;
  readonly qualityReasons: readonly string[];
  readonly sampledAt: string | null;
  readonly receivedAt: string | null;
  readonly policyRevision: number | null;
  readonly missingReason?: 'NEVER_OBSERVED' | 'ONLY_REJECTED_CANDIDATES' | 'POLICY_NOT_CONFIGURED';
}

export type RealAssetsBindingState =
  | { readonly state: 'bound'; readonly relationship: AssetRelationship; readonly equipment: Equipment }
  | { readonly state: 'multi-bound'; readonly bindings: readonly { readonly relationship: AssetRelationship; readonly equipment: Equipment }[] }
  | { readonly state: 'unbound' }
  | { readonly state: 'ambiguous'; readonly relationshipIds: readonly string[] };

export type RealAssetsAreaState =
  | { readonly state: 'bound'; readonly relationship: AssetRelationship; readonly area: Area }
  | { readonly state: 'unbound' }
  | { readonly state: 'ambiguous'; readonly relationshipIds: readonly string[] };

export interface RealAssetsDeviceRow {
  readonly device: Device;
  readonly profile: RealAssetsProfileResolution;
  readonly binding: RealAssetsBindingState;
  readonly area: RealAssetsAreaState;
  readonly registeredPointCount: number;
  readonly snapshotResult?: RealAssetsSnapshotResult;
  readonly operatingState: RealAssetsOperatingState;
  readonly attentionReasons: readonly RealAssetsAttentionReason[];
  readonly points: readonly RealAssetsPointView[];
}

export interface BuildRealAssetsRowsInput {
  readonly assetModel: SiteAssetModel;
  readonly snapshots?: ReadonlyMap<string, RealAssetsSnapshotResult>;
  readonly now?: Date;
}

export type RealAssetsHierarchyKind = 'site' | 'area' | 'equipment' | 'device' | 'sensor' | 'point' | 'virtual-sensor';

export interface RealAssetsHierarchyNode {
  readonly key: string;
  readonly kind: RealAssetsHierarchyKind;
  readonly label: string;
  readonly meta: string;
  readonly deviceIds: readonly string[];
  readonly children: readonly RealAssetsHierarchyNode[];
}

function compareText(left: string, right: string): number {
  return left.localeCompare(right, 'zh-CN', { sensitivity: 'base', numeric: true });
}

interface RegistryIdentity {
  readonly id: string;
  readonly displayName: string;
  readonly code?: string;
  readonly pointKey?: string;
}

function compareRegistryIdentity(left: RegistryIdentity, right: RegistryIdentity): number {
  return compareText(left.displayName, right.displayName)
    || compareText(left.code ?? left.pointKey ?? '', right.code ?? right.pointKey ?? '')
    || left.id.localeCompare(right.id);
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
  equipmentById: ReadonlyMap<string, Equipment>,
  now = new Date(),
): RealAssetsBindingState {
  const candidates = currentRelationships(relationships, 'DEVICE', device.id, 'EQUIPMENT', now);
  if (candidates.length === 0) return { state: 'unbound' };
  const bindings: { relationship: AssetRelationship; equipment: Equipment }[] = [];
  const seenEquipmentIds = new Set<string>();
  for (const relationship of candidates) {
    if (seenEquipmentIds.has(relationship.toId)) continue;
    const equipment = equipmentById.get(relationship.toId);
    if (!equipment) return { state: 'ambiguous', relationshipIds: candidates.map((candidate) => candidate.id) };
    seenEquipmentIds.add(relationship.toId);
    bindings.push({ relationship, equipment });
  }
  if (bindings.length === 1) return { state: 'bound', ...bindings[0] };
  return { state: 'multi-bound', bindings };
}

function resolvedEquipmentBindings(binding: RealAssetsBindingState): readonly { readonly relationship: AssetRelationship; readonly equipment: Equipment }[] {
  if (binding.state === 'bound') return [binding];
  if (binding.state === 'multi-bound') return binding.bindings;
  return [];
}

export function resolveDeviceArea(
  device: Device,
  binding: RealAssetsBindingState,
  relationships: readonly AssetRelationship[],
  areaById: ReadonlyMap<string, Area>,
  now = new Date(),
): RealAssetsAreaState {
  const direct = currentRelationships(relationships, 'DEVICE', device.id, 'AREA', now);
  const inherited = resolvedEquipmentBindings(binding).flatMap((item) => (
    currentRelationships(relationships, 'EQUIPMENT', item.equipment.id, 'AREA', now)
  ));
  const candidates = direct.length > 0 ? direct : inherited;
  if (candidates.length === 0) return { state: 'unbound' };
  const areaIds = new Set(candidates.map((relationship) => relationship.toId));
  if (areaIds.size !== 1) return { state: 'ambiguous', relationshipIds: candidates.map((relationship) => relationship.id) };
  const relationship = candidates[0];
  const area = areaById.get(relationship.toId);
  if (!area) return { state: 'ambiguous', relationshipIds: candidates.map((candidate) => candidate.id) };
  return { state: 'bound', relationship, area };
}

function pointView(definition: RealAssetsPointDefinition, state: TelemetryKeyState | undefined): RealAssetsPointView {
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
    if (value.quality === 'SUSPECT') reasons.add('TELEMETRY_SUSPECT');
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
  const equipmentById = new Map(input.assetModel.equipment.map((item) => [item.id, item]));
  const areaById = new Map(input.assetModel.areas.map((item) => [item.id, item]));
  const pointCountByDevice = new Map<string, number>();
  for (const point of input.assetModel.telemetryPoints) {
    pointCountByDevice.set(point.reportingDeviceId, (pointCountByDevice.get(point.reportingDeviceId) ?? 0) + 1);
  }
  const rows = input.assetModel.devices.map((device): RealAssetsDeviceRow => {
    const profile = resolveRealAssetsProfile(device.deviceType);
    const snapshotResult = input.snapshots?.get(device.id);
    const projection = projectRealAssetsOperatingState(snapshotResult, profile);
    const binding = resolveDeviceBinding(device, input.assetModel.relationships, equipmentById, now);
    return {
      device,
      profile,
      binding,
      area: resolveDeviceArea(device, binding, input.assetModel.relationships, areaById, now),
      registeredPointCount: pointCountByDevice.get(device.id) ?? 0,
      snapshotResult,
      operatingState: projection.state,
      attentionReasons: projection.reasons,
      points: projection.points,
    };
  });
  return rows.sort((left, right) => {
    const leftArea = left.area.state === 'bound' ? left.area.area : undefined;
    const rightArea = right.area.state === 'bound' ? right.area.area : undefined;
    if (leftArea && rightArea) {
      const areaOrder = compareRegistryIdentity(leftArea, rightArea);
      if (areaOrder) return areaOrder;
    } else if (leftArea) return -1;
    else if (rightArea) return 1;
    const leftEquipment = [...resolvedEquipmentBindings(left.binding)].map((item) => item.equipment).sort(compareRegistryIdentity)[0];
    const rightEquipment = [...resolvedEquipmentBindings(right.binding)].map((item) => item.equipment).sort(compareRegistryIdentity)[0];
    if (leftEquipment && rightEquipment) return compareRegistryIdentity(leftEquipment, rightEquipment) || compareRegistryIdentity(left.device, right.device);
    if (leftEquipment) return -1;
    if (rightEquipment) return 1;
    return compareRegistryIdentity(left.device, right.device);
  });
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
): RealAssetsHierarchyNode {
  return { key: `${kind}:${id}`, kind, label, meta, deviceIds: [...new Set(deviceIds)], children };
}

export function buildRealAssetsHierarchy(model: SiteAssetModel, siteLabel: string, now = new Date()): RealAssetsHierarchyNode {
  const equipmentArea = new Map(model.equipment.map((item) => [
    item.id,
    oneCurrentTargetId(model.relationships, 'EQUIPMENT', item.id, 'AREA', now),
  ]));
  const deviceEquipment = new Map(model.devices.map((item) => [
    item.id,
    currentTargetIds(model.relationships, 'DEVICE', item.id, 'EQUIPMENT', now),
  ]));
  const deviceArea = new Map(model.devices.map((item) => [
    item.id,
    oneCurrentTargetId(model.relationships, 'DEVICE', item.id, 'AREA', now),
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
        sensor.sensorType,
        [device.id],
        (pointsBySensor.get(sensor.id) ?? []).sort(compareRegistryIdentity).map((point) => hierarchyNode(
          'point', `${branchKey}:${sensor.id}:${point.id}`, point.displayName, `${point.pointKind} · ${point.unit ?? point.valueType}`, [device.id],
        )),
      ));
    const directPoints = (directPointsByDevice.get(device.id) ?? []).sort(compareRegistryIdentity);
    const children = directPoints.length > 0
      ? [...sensors, hierarchyNode(
        'virtual-sensor',
        branchKey,
        'Device Endpoint 内建与计算点',
        '非 Sensor 输出',
        [device.id],
        directPoints.map((point) => hierarchyNode(
          'point', `${branchKey}:direct:${point.id}`, point.displayName, `${point.pointKind} · ${point.unit ?? point.valueType}`, [device.id],
        )),
      )]
      : sensors;
    return hierarchyNode('device', branchKey, device.displayName, device.deviceType, [device.id], children);
  };

  const equipmentNode = (equipment: Equipment): RealAssetsHierarchyNode => {
    const devices = model.devices
      .filter((device) => deviceEquipment.get(device.id)?.includes(equipment.id))
      .sort(compareRegistryIdentity)
      .map((device) => deviceNode(device, `equipment:${equipment.id}`));
    return hierarchyNode('equipment', equipment.id, equipment.displayName, equipment.equipmentType, devices.flatMap((node) => node.deviceIds), devices);
  };

  const areaChildren = new Map<string | null, Area[]>();
  for (const area of model.areas) {
    const siblings = areaChildren.get(area.parentAreaId) ?? [];
    siblings.push(area);
    areaChildren.set(area.parentAreaId, siblings);
  }
  const areaNode = (area: Area): RealAssetsHierarchyNode => {
    const nestedAreas = (areaChildren.get(area.id) ?? []).sort(compareRegistryIdentity).map(areaNode);
    const equipment = model.equipment
      .filter((item) => equipmentArea.get(item.id) === area.id)
      .sort(compareRegistryIdentity)
      .map(equipmentNode);
    const directDevices = model.devices
      .filter((device) => deviceEquipment.get(device.id)?.length === 0 && deviceArea.get(device.id) === area.id)
      .sort(compareRegistryIdentity)
      .map((device) => deviceNode(device, `area:${area.id}`));
    const directDeviceGroup = directDevices.length > 0
      ? [hierarchyNode('equipment', `unbound:${area.id}`, '未绑定 Equipment', '关系待治理', directDevices.flatMap((node) => node.deviceIds), directDevices)]
      : [];
    const children = [...nestedAreas, ...equipment, ...directDeviceGroup];
    return hierarchyNode('area', area.id, area.displayName, area.areaType, children.flatMap((node) => node.deviceIds), children);
  };

  const roots = (areaChildren.get(null) ?? []).sort(compareRegistryIdentity).map(areaNode);
  const unassignedEquipment = model.equipment.filter((item) => !equipmentArea.get(item.id)).sort(compareRegistryIdentity).map(equipmentNode);
  const unassignedDevices = model.devices
    .filter((device) => deviceEquipment.get(device.id)?.length === 0 && !deviceArea.get(device.id))
    .sort(compareRegistryIdentity)
    .map((device) => deviceNode(device, `site:${model.siteId}`));
  const orphanChildren = [
    ...unassignedEquipment,
    ...(unassignedDevices.length > 0
      ? [hierarchyNode('equipment', 'unbound:site', '未绑定 Equipment', '关系待治理', unassignedDevices.flatMap((node) => node.deviceIds), unassignedDevices)]
      : []),
  ];
  const children = orphanChildren.length > 0
    ? [...roots, hierarchyNode('area', 'unbound:site', '未绑定 Area', '关系待治理', orphanChildren.flatMap((node) => node.deviceIds), orphanChildren)]
    : roots;
  return hierarchyNode('site', model.siteId, siteLabel, 'Site Asset Model', model.devices.map((device) => device.id), children);
}

export function isRealAssetsAttentionState(state: RealAssetsOperatingState): boolean {
  return state !== 'NORMAL';
}
