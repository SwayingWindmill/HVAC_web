import {
  commandCapabilityProfiles,
  commandCapabilitySchema,
  type CommandCapability,
} from '@/api/command-contract';
import type {
  Asset,
  AssetRelationship,
  Device,
  SiteAssetModel,
  TelemetryPoint,
} from '@/api/generated/platformGateway.gen';

export interface RegisteredControlTarget {
  readonly key: string;
  readonly asset: Asset;
  readonly device: Device;
  readonly commandPoint: TelemetryPoint;
  readonly feedbackPoint: TelemetryPoint;
  readonly capability: CommandCapability;
}

export interface ControlTargetProjection {
  readonly targets: readonly RegisteredControlTarget[];
  readonly incompleteRegistrationCount: number;
}

const CAPABILITY_LABELS: Readonly<Record<CommandCapability, string>> = Object.freeze({
  START: '启动设备',
  STOP: '停止设备',
  RESET_FAULT: '复位故障',
  SET_TEMPERATURE_SETPOINT: '温度设定值',
  SET_CHILLED_WATER_TEMPERATURE_SETPOINT: '冷冻水供水设定值',
  SET_FREQUENCY: '运行频率',
  SET_FAN_SPEED: '风机转速',
  SET_LOAD_LIMIT: '负荷上限',
  SET_OPENING: '开度设定',
});

function relationshipIsCurrent(relationship: AssetRelationship, now: Date): boolean {
  if (relationship.status !== 'ACTIVE') return false;
  if (Date.parse(relationship.validFrom) > now.getTime()) return false;
  return !relationship.validTo || Date.parse(relationship.validTo) > now.getTime();
}

function metadataString(point: TelemetryPoint, key: string): string | null {
  const value = point.sourceMetadata[key];
  return typeof value === 'string' && value.trim() ? value.trim() : null;
}

function targetKey(assetId: string, commandPointId: string): string {
  return `${assetId}:${commandPointId}`;
}

export function controlCapabilityLabel(capability: CommandCapability): string {
  return CAPABILITY_LABELS[capability];
}

export function controlCapabilityRange(target: RegisteredControlTarget): string | null {
  const profile = commandCapabilityProfiles[target.capability];
  if (!profile.parameterKey || profile.minimum === undefined || profile.maximum === undefined) return null;
  const unit = target.commandPoint.unit ? ` ${target.commandPoint.unit}` : '';
  const step = profile.step === undefined ? '' : ` · 步进 ${profile.step}${unit}`;
  return `${profile.minimum}–${profile.maximum}${unit}${step}`;
}

export function projectRegisteredControlTargets(
  model: SiteAssetModel,
  now = new Date(),
): ControlTargetProjection {
  const assetById = new Map(model.assets.map((asset) => [asset.id, asset]));
  const deviceById = new Map(model.devices.map((device) => [device.id, device]));
  const pointsByDeviceAndSource = new Map(
    model.telemetryPoints.map((point) => [`${point.reportingDeviceId}\u0000${point.sourceKey}`, point] as const),
  );
  const controlRelationships = model.relationships.filter((relationship) => (
    relationship.fromType === 'POINT'
    && relationship.toType === 'ASSET'
    && relationship.role === 'CONTROLS'
    && relationshipIsCurrent(relationship, now)
  ));
  const relationshipsByPoint = new Map<string, AssetRelationship[]>();
  for (const relationship of controlRelationships) {
    const relationships = relationshipsByPoint.get(relationship.fromId) ?? [];
    relationships.push(relationship);
    relationshipsByPoint.set(relationship.fromId, relationships);
  }

  const targets: RegisteredControlTarget[] = [];
  let incompleteRegistrationCount = 0;

  for (const point of model.telemetryPoints) {
    if (point.pointType !== 'COMMAND') continue;
    const relationships = relationshipsByPoint.get(point.id) ?? [];
    if (relationships.length === 0) continue;

    const capabilityResult = commandCapabilitySchema.safeParse(metadataString(point, 'capability'));
    const device = deviceById.get(point.reportingDeviceId);
    const feedbackSourceKey = metadataString(point, 'feedbackSourceKey');
    const feedbackPoint = feedbackSourceKey
      ? pointsByDeviceAndSource.get(`${point.reportingDeviceId}\u0000${feedbackSourceKey}`)
      : undefined;

    for (const relationship of relationships) {
      const asset = assetById.get(relationship.toId);
      const capability = capabilityResult.success ? capabilityResult.data : null;
      const profile = capability ? commandCapabilityProfiles[capability] : null;
      const parameterKey = metadataString(point, 'parameterKey');
      const declarationMatches = Boolean(
        asset?.status === 'ACTIVE'
        && device?.status === 'ACTIVE'
        && point.status === 'ACTIVE'
        && point.writable
        && capability
        && profile
        && metadataString(point, 'capabilityRevision') === profile.revision
        && feedbackPoint?.status === 'ACTIVE'
        && (feedbackPoint?.pointType === 'STATE' || feedbackPoint?.pointType === 'TELEMETRY')
        && (!profile.parameterKey || parameterKey === profile.parameterKey)
      );

      if (!declarationMatches || !asset || !device || !feedbackPoint || !capability) {
        incompleteRegistrationCount += 1;
        continue;
      }

      targets.push({
        key: targetKey(asset.id, point.id),
        asset,
        device,
        commandPoint: point,
        feedbackPoint,
        capability,
      });
    }
  }

  targets.sort((left, right) => (
    left.asset.displayName.localeCompare(right.asset.displayName, 'zh-CN')
    || left.commandPoint.displayName.localeCompare(right.commandPoint.displayName, 'zh-CN')
    || left.key.localeCompare(right.key)
  ));

  return {
    targets,
    incompleteRegistrationCount,
  };
}
