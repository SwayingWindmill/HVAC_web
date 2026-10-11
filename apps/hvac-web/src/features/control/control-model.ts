import type { AssetRelationship, SiteAssetModel, TelemetryPoint } from '../../api/generated/platformGateway.gen';
import { formatTelemetryUnit } from '../../domain/centralPlantTelemetry.ts';

// What an operator can command on the plant, read from the Registry: each COMMAND point that
// CONTROLS an asset, with its range and the reading that confirms it took effect.

export type ActionCapability = 'START' | 'STOP' | 'RESET_FAULT';
export type SettingCapability =
  | 'SET_TEMPERATURE_SETPOINT'
  | 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT'
  | 'SET_FREQUENCY'
  | 'SET_FAN_SPEED'
  | 'SET_LOAD_LIMIT'
  | 'SET_OPENING';

export interface ControlAction {
  readonly assetId: string;
  readonly pointId: string;
  readonly capability: ActionCapability;
  readonly label: string;
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
  readonly deviceId: string;
  readonly actions: readonly ControlAction[];
  readonly settings: readonly ControlSetting[];
}

const ACTION_ORDER: readonly ActionCapability[] = ['START', 'STOP', 'RESET_FAULT'];

const isAction = (capability: string): capability is ActionCapability => (ACTION_ORDER as readonly string[]).includes(capability);

function controls(relationship: AssetRelationship, at: Date): boolean {
  return relationship.role === 'CONTROLS' && relationship.status === 'ACTIVE' && relationship.fromType === 'POINT'
    && relationship.toType === 'ASSET' && new Date(relationship.validFrom) <= at
    && (relationship.validTo === null || new Date(relationship.validTo) > at);
}

const metadataNumber = (point: TelemetryPoint, key: string): number | null => {
  const value = point.sourceMetadata[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
};

export function controlTargets(model: Pick<SiteAssetModel, 'assets' | 'telemetryPoints' | 'relationships'>, at: Date): ControlTarget[] {
  const pointsById = new Map(model.telemetryPoints.filter((point) => point.status === 'ACTIVE').map((point) => [point.id, point]));
  const byAsset = new Map<string, { deviceId: string; actions: ControlAction[]; settings: ControlSetting[] }>();
  for (const relationship of model.relationships) {
    const point = pointsById.get(relationship.fromId);
    if (!point || point.pointType !== 'COMMAND' || !controls(relationship, at)) continue;
    const capability = String(point.sourceMetadata.capability ?? '');
    const assetId = relationship.toId;
    const target = byAsset.get(assetId) ?? { deviceId: point.reportingDeviceId, actions: [], settings: [] };
    byAsset.set(assetId, target);
    if (isAction(capability)) {
      target.actions.push({ assetId, pointId: point.id, capability, label: point.displayName });
      continue;
    }
    const minimum = metadataNumber(point, 'minimum');
    const maximum = metadataNumber(point, 'maximum');
    const parameterKey = point.sourceMetadata.parameterKey;
    // A setting without a declared range or parameter cannot be commanded safely, so it is not offered.
    if (minimum === null || maximum === null || typeof parameterKey !== 'string') continue;
    const feedbackCode = point.sourceMetadata.feedbackPointKey;
    const feedback = model.telemetryPoints.find((candidate) =>
      candidate.reportingDeviceId === point.reportingDeviceId && candidate.pointType !== 'COMMAND' && candidate.pointCode === feedbackCode);
    target.settings.push({
      assetId, pointId: point.id, capability: capability as SettingCapability, label: point.displayName, parameterKey,
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
