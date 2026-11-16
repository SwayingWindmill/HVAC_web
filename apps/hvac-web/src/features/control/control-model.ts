import type { AssetRelationship, Device, SiteAssetModel, TelemetryPoint } from '../../api/generated/platformGateway.gen';
import { formatTelemetryUnit } from '../../domain/centralPlantTelemetry.ts';
import { isOneOf, isPlainRecord } from '../../lib/typed.ts';

// What an operator can command on the plant, read from the Registry: each COMMAND point that
// CONTROLS an asset, with its range and the reading that confirms it took effect.

export type ActionCapability = 'START' | 'STOP' | 'START_UNIT_SEQUENCE' | 'STOP_UNIT_SEQUENCE' | 'RESET_FAULT';
export type SettingCapability =
  | 'SET_TEMPERATURE_SETPOINT'
  | 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT'
  | 'SET_FREQUENCY'
  | 'SET_FAN_SPEED'
  | 'SET_LOAD_LIMIT'
  | 'SET_OPENING';

/** A device whose run state must match before the plant lets this action through. */
export interface Interlock {
  readonly deviceCode: string;
  readonly expected: 'RUNNING' | 'STOPPED';
  /** The Registry's own sentence for the rule, e.g. 1# 主机尚未停机，禁止断开 1# 冻水泵. */
  readonly reason: string;
}

export interface ControlAction {
  readonly assetId: string;
  readonly pointId: string;
  readonly capability: ActionCapability;
  readonly label: string;
  readonly interlocks: readonly Interlock[];
}

export interface ControlSetting {
  readonly assetId: string;
  readonly pointId: string;
  readonly capability: SettingCapability;
  readonly label: string;
  readonly parameterKey: string;
  readonly unit: string | null;
  readonly minimum: number;
  readonly maximum: number;
  readonly step: number;
  /** The telemetry point whose reading confirms the setting; null when the Registry names none. */
  readonly feedbackSourceKey: string | null;
}

export interface ControlTarget {
  readonly assetId: string;
  readonly assetName: string;
  readonly assetType: string;
  readonly deviceId: string;
  readonly actions: readonly ControlAction[];
  readonly settings: readonly ControlSetting[];
}

const ACTION_ORDER = ['START', 'STOP', 'START_UNIT_SEQUENCE', 'STOP_UNIT_SEQUENCE', 'RESET_FAULT'] as const satisfies readonly ActionCapability[];
const SETTING_CAPABILITIES = [
  'SET_TEMPERATURE_SETPOINT',
  'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
  'SET_FREQUENCY',
  'SET_FAN_SPEED',
  'SET_LOAD_LIMIT',
  'SET_OPENING',
] as const satisfies readonly SettingCapability[];

const isAction = (capability: string): capability is ActionCapability => isOneOf(ACTION_ORDER, capability);
const isSettingCapability = (capability: string): capability is SettingCapability => isOneOf(SETTING_CAPABILITIES, capability);

function controls(relationship: AssetRelationship, at: Date): boolean {
  return relationship.role === 'CONTROLS' && relationship.status === 'ACTIVE' && relationship.fromType === 'POINT'
    && relationship.toType === 'ASSET' && new Date(relationship.validFrom) <= at
    && (relationship.validTo === null || new Date(relationship.validTo) > at);
}

const metadataNumber = (point: TelemetryPoint, key: string): number | null => {
  const value = point.sourceMetadata[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
};

function interlocksOf(point: TelemetryPoint): Interlock[] {
  const declared = point.sourceMetadata.interlocks;
  if (!Array.isArray(declared)) return [];
  return declared.flatMap((entry: unknown): Interlock[] => {
    if (!isPlainRecord(entry)) return [];
    const { device, expected, reason } = entry;
    if (typeof device !== 'string' || (expected !== 'RUNNING' && expected !== 'STOPPED')) return [];
    return [{ deviceCode: device, expected, reason: typeof reason === 'string' ? reason : '' }];
  });
}

export function controlTargets(model: Pick<SiteAssetModel, 'assets' | 'telemetryPoints' | 'relationships'>, at: Date): ControlTarget[] {
  const pointsById = new Map(model.telemetryPoints.filter((point) => point.status === 'ACTIVE').map((point) => [point.id, point]));
  const byAsset = new Map<string, { deviceId: string; actions: ControlAction[]; settings: ControlSetting[] }>();
  for (const relationship of model.relationships) {
    const point = pointsById.get(relationship.fromId);
    if (!point || point.pointType !== 'COMMAND' || !controls(relationship, at)) continue;
    // A STOP that drops every output channel at once is the plant's emergency release, reached only
    // through the site shutdown flow; on a device row it would read as an ordinary stop.
    if (point.sourceMetadata.actuationMode === 'EMERGENCY_DO_RELEASE') continue;
    const capability = String(point.sourceMetadata.capability ?? '');
    const assetId = relationship.toId;
    const target = byAsset.get(assetId) ?? { deviceId: point.reportingDeviceId, actions: [], settings: [] };
    byAsset.set(assetId, target);
    if (isAction(capability)) {
      target.actions.push({ assetId, pointId: point.id, capability, label: point.displayName, interlocks: interlocksOf(point) });
      continue;
    }
    if (!isSettingCapability(capability)) continue;
    const minimum = metadataNumber(point, 'minimum');
    const maximum = metadataNumber(point, 'maximum');
    const parameterKey = point.sourceMetadata.parameterKey;
    // A setting without a declared range or parameter cannot be commanded safely, so it is not offered.
    if (minimum === null || maximum === null || typeof parameterKey !== 'string') continue;
    const feedbackCode = point.sourceMetadata.feedbackPointKey;
    const feedback = model.telemetryPoints.find((candidate) =>
      candidate.reportingDeviceId === point.reportingDeviceId && candidate.pointType !== 'COMMAND' && candidate.pointCode === feedbackCode);
    target.settings.push({
      assetId, pointId: point.id, capability, label: point.displayName, parameterKey,
      unit: formatTelemetryUnit(point.unit), minimum, maximum, step: metadataNumber(point, 'step') ?? 1,
      feedbackSourceKey: feedback?.sourceKey ?? null,
    });
  }
  const assetsById = new Map(model.assets.map((asset) => [asset.id, asset]));
  return [...byAsset.entries()]
    .filter(([assetId]) => assetsById.get(assetId)?.status === 'ACTIVE')
    .map(([assetId, target]) => ({
      assetId,
      assetName: assetsById.get(assetId)!.displayName,
      assetType: assetsById.get(assetId)!.assetType,
      deviceId: target.deviceId,
      actions: [...target.actions].sort((left, right) => ACTION_ORDER.indexOf(left.capability) - ACTION_ORDER.indexOf(right.capability)),
      settings: target.settings,
    }))
    .sort((left, right) =>
      plantPosition(assetsById.get(left.assetId)!.assetType) - plantPosition(assetsById.get(right.assetId)!.assetType)
      || left.assetName.localeCompare(right.assetName));
}

// Chilled water leaves the chiller, the pumps move both loops, the tower rejects the heat.
const PLANT_ORDER = ['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER'];

function plantPosition(assetType: string): number {
  const position = PLANT_ORDER.indexOf(assetType);
  return position === -1 ? PLANT_ORDER.length : position;
}

/** What a command point was asked to do, in the operator's words. */
export interface CommandLabel {
  readonly assetName: string;
  readonly label: string;
  readonly unit: string | null;
}

/** Each command point's asset and name, so a command reads as the operator issued it. */
export function commandLabels(targets: readonly ControlTarget[]): ReadonlyMap<string, CommandLabel> {
  const labels = new Map<string, CommandLabel>();
  for (const target of targets) {
    for (const action of target.actions) labels.set(action.pointId, { assetName: target.assetName, label: action.label, unit: null });
    for (const setting of target.settings) labels.set(setting.pointId, { assetName: target.assetName, label: setting.label, unit: setting.unit });
  }
  return labels;
}

/**
 * A chiller with the pumps and tower its start interlocks name: the group the plant starts and stops
 * together. Assets no chiller names stay in `others`, so a Site without interlocks still lists them.
 */
export interface PlantUnit {
  readonly chiller: ControlTarget;
  readonly members: readonly ControlTarget[];
}

export function plantUnits(targets: readonly ControlTarget[], devices: readonly Pick<Device, 'id' | 'code'>[]): { units: PlantUnit[]; others: ControlTarget[] } {
  const deviceIdByCode = new Map(devices.map((device) => [device.code, device.id]));
  const byDevice = new Map(targets.map((target) => [target.deviceId, target]));
  const claimed = new Set<string>();
  const units = targets.filter((target) => target.assetType === 'CHILLER').map((chiller): PlantUnit => {
    claimed.add(chiller.assetId);
    const named = chiller.actions.find((action) => action.capability === 'START')?.interlocks ?? [];
    const members = named.flatMap((interlock) => {
      const member = byDevice.get(deviceIdByCode.get(interlock.deviceCode) ?? '');
      if (!member || claimed.has(member.assetId)) return [];
      claimed.add(member.assetId);
      return [member];
    });
    return { chiller, members: [...members].sort((left, right) => plantPosition(left.assetType) - plantPosition(right.assetType)) };
  });
  return { units, others: targets.filter((target) => !claimed.has(target.assetId)) };
}

/** The interlocks that would stop this action now, given each device's run state by device code. */
export function blockingInterlocks(action: ControlAction, runStateByDeviceCode: ReadonlyMap<string, string>): Interlock[] {
  return action.interlocks.filter((interlock) => runStateByDeviceCode.get(interlock.deviceCode) !== interlock.expected);
}
