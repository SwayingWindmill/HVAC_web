import type { Device, DeviceBinding, Equipment } from '../../api/generated/platformGateway.gen.ts';
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
  readonly missingReason?: 'NEVER_OBSERVED' | 'ONLY_REJECTED_CANDIDATES' | 'POLICY_NOT_CONFIGURED';
}

export type RealAssetsBindingState =
  | { readonly state: 'bound'; readonly binding: DeviceBinding; readonly equipment: Equipment }
  | { readonly state: 'unbound' }
  | { readonly state: 'ambiguous'; readonly bindingIds: readonly string[] };

export interface RealAssetsDeviceRow {
  readonly device: Device;
  readonly profile: RealAssetsProfileResolution;
  readonly binding: RealAssetsBindingState;
  readonly snapshotResult?: RealAssetsSnapshotResult;
  readonly operatingState: RealAssetsOperatingState;
  readonly attentionReasons: readonly RealAssetsAttentionReason[];
  readonly points: readonly RealAssetsPointView[];
}

export interface BuildRealAssetsRowsInput {
  readonly devices: readonly Device[];
  readonly equipment: readonly Equipment[];
  readonly bindings: readonly DeviceBinding[];
  readonly snapshots?: ReadonlyMap<string, RealAssetsSnapshotResult>;
  readonly now?: Date;
}

function compareText(left: string, right: string): number {
  return left.localeCompare(right, 'zh-CN', { sensitivity: 'base', numeric: true });
}

function compareRegistryIdentity(left: Pick<Device | Equipment, 'displayName' | 'code' | 'id'>, right: Pick<Device | Equipment, 'displayName' | 'code' | 'id'>): number {
  return compareText(left.displayName, right.displayName)
    || compareText(left.code, right.code)
    || left.id.localeCompare(right.id);
}

function isCurrentBinding(binding: DeviceBinding, now: Date): boolean {
  if (binding.status !== 'ACTIVE') return false;
  const from = Date.parse(binding.validFrom);
  const to = binding.validTo ? Date.parse(binding.validTo) : Number.POSITIVE_INFINITY;
  return Number.isFinite(from) && from <= now.getTime() && now.getTime() < to;
}

export function resolveDeviceBinding(
  device: Device,
  bindings: readonly DeviceBinding[],
  equipmentById: ReadonlyMap<string, Equipment>,
  now = new Date(),
): RealAssetsBindingState {
  const candidates = bindings
    .filter((binding) => binding.deviceId === device.id && isCurrentBinding(binding, now))
    .sort((left, right) => right.revision - left.revision || left.id.localeCompare(right.id));
  if (candidates.length === 0) return { state: 'unbound' };
  const equipmentIds = new Set(candidates.map((binding) => binding.equipmentId));
  if (equipmentIds.size !== 1) return { state: 'ambiguous', bindingIds: candidates.map((binding) => binding.id) };
  const binding = candidates[0];
  const equipment = equipmentById.get(binding.equipmentId);
  if (!equipment) return { state: 'ambiguous', bindingIds: candidates.map((candidate) => candidate.id) };
  return { state: 'bound', binding, equipment };
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
  const equipmentById = new Map(input.equipment.map((item) => [item.id, item]));
  const rows = input.devices.map((device): RealAssetsDeviceRow => {
    const profile = resolveRealAssetsProfile(device.deviceType);
    const snapshotResult = input.snapshots?.get(device.id);
    const projection = projectRealAssetsOperatingState(snapshotResult, profile);
    return {
      device,
      profile,
      binding: resolveDeviceBinding(device, input.bindings, equipmentById, now),
      snapshotResult,
      operatingState: projection.state,
      attentionReasons: projection.reasons,
      points: projection.points,
    };
  });
  return rows.sort((left, right) => {
    const leftEquipment = left.binding.state === 'bound' ? left.binding.equipment : undefined;
    const rightEquipment = right.binding.state === 'bound' ? right.binding.equipment : undefined;
    if (leftEquipment && rightEquipment) return compareRegistryIdentity(leftEquipment, rightEquipment) || compareRegistryIdentity(left.device, right.device);
    if (leftEquipment) return -1;
    if (rightEquipment) return 1;
    return compareRegistryIdentity(left.device, right.device);
  });
}

export function isRealAssetsAttentionState(state: RealAssetsOperatingState): boolean {
  return state !== 'NORMAL';
}
