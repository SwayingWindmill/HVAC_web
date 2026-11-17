import type {
  ConcentratorUnitShadow,
  GatewayConfigShadow,
  GatewayConfigShadowUpdateRequest,
  GatewayPointMapping,
  GatewaySamplingPolicy,
  PointCopyTarget,
} from '@/api/generated/platformGateway.gen';

const POINT_CODE_PATTERN = /^[a-z][a-z0-9_]{0,127}$/;

export interface GatewayPointMappingDraft {
  readonly sourceChannel: string;
  readonly primaryDeviceKey: string;
  readonly primaryPointCode: string;
  readonly copyTargetsText: string;
}

export interface GatewaySamplingPolicyDraft {
  readonly deviceKey: string;
  readonly pointCode: string;
  readonly sampleIntervalMs: string;
  readonly publishIntervalMs: string;
}

export interface GatewayConfigShadowEditorDraft {
  readonly pointMappings: readonly GatewayPointMappingDraft[];
  readonly samplingPolicies: readonly GatewaySamplingPolicyDraft[];
  readonly reason: string;
}

export interface ConcentratorShadowUnitSummary {
  readonly unitId: string;
  readonly hasParams: boolean;
  readonly boundEidCount: number;
}

export function formatPointCopyTargets(targets?: readonly PointCopyTarget[]): string {
  if (!targets || targets.length === 0) return '';
  return targets.map((item) => `${item.deviceKey}:${item.pointCode}`).join(', ');
}

export function parsePointCopyTargets(
  raw: string,
): { readonly ok: true; readonly targets: PointCopyTarget[] } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: true, targets: [] };
  }
  const tokens = trimmed
    .split(/[\n,]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
  const targets: PointCopyTarget[] = [];
  for (const token of tokens) {
    const sepIndex = token.indexOf(':');
    if (sepIndex <= 0 || sepIndex === token.length - 1) {
      return {
        ok: false,
        error: `复用目标 "${token}" 格式须为 deviceKey:pointCode`,
      };
    }
    const deviceKey = token.slice(0, sepIndex).trim();
    const pointCode = token.slice(sepIndex + 1).trim();
    if (!deviceKey || deviceKey.length > 128) {
      return {
        ok: false,
        error: `复用目标 "${token}" 的设备标识无效`,
      };
    }
    if (!POINT_CODE_PATTERN.test(pointCode)) {
      return {
        ok: false,
        error: `复用目标 "${token}" 的点位编码须以小写字母开头，仅含小写字母、数字和下划线`,
      };
    }
    targets.push({ deviceKey, pointCode });
  }
  return { ok: true, targets };
}

export function createShadowEditorDraft(
  shadow: GatewayConfigShadow | undefined,
): GatewayConfigShadowEditorDraft {
  const desired = shadow?.desired;
  const pointMappings = (desired?.pointMappings ?? []).map((item) => ({
    sourceChannel: item.sourceChannel,
    primaryDeviceKey: item.primaryDeviceKey,
    primaryPointCode: item.primaryPointCode,
    copyTargetsText: formatPointCopyTargets(item.copyTargets),
  }));
  const samplingPolicies = (desired?.samplingPolicies ?? []).map((item) => ({
    deviceKey: item.deviceKey,
    pointCode: item.pointCode ?? '',
    sampleIntervalMs: String(item.sampleIntervalMs),
    publishIntervalMs: String(item.publishIntervalMs),
  }));
  return {
    pointMappings,
    samplingPolicies,
    reason: '',
  };
}

export function summarizeConcentratorShadowUnits(
  configs: Readonly<Record<string, ConcentratorUnitShadow>> | undefined,
): readonly ConcentratorShadowUnitSummary[] {
  if (!configs) return [];
  return Object.entries(configs)
    .map(([unitId, unit]) => ({
      unitId,
      hasParams: unit.params !== undefined,
      boundEidCount: (unit.eids ?? []).filter((slot) => slot.eid > 0).length,
    }))
    .sort((left, right) => {
      const leftNum = Number(left.unitId);
      const rightNum = Number(right.unitId);
      if (Number.isInteger(leftNum) && Number.isInteger(rightNum)) {
        return leftNum - rightNum;
      }
      return left.unitId.localeCompare(right.unitId);
    });
}

export function buildValidatedShadowUpdateRequest(
  draft: GatewayConfigShadowEditorDraft,
  shadow: GatewayConfigShadow | undefined,
): { readonly ok: true; readonly request: GatewayConfigShadowUpdateRequest } | { readonly ok: false; readonly error: string } {
  const reason = draft.reason.trim();
  if (!reason) {
    return { ok: false, error: '请填写配置影子变更原因' };
  }

  const pointMappings: GatewayPointMapping[] = [];
  for (let index = 0; index < draft.pointMappings.length; index += 1) {
    const row = draft.pointMappings[index]!;
    const sourceChannel = row.sourceChannel.trim();
    const primaryDeviceKey = row.primaryDeviceKey.trim();
    const primaryPointCode = row.primaryPointCode.trim();
    if (!sourceChannel || sourceChannel.length > 128) {
      return { ok: false, error: `点位映射第 ${index + 1} 行的源通道标识不能为空` };
    }
    if (!primaryDeviceKey || primaryDeviceKey.length > 128) {
      return { ok: false, error: `点位映射第 ${index + 1} 行的主设备标识不能为空` };
    }
    if (!POINT_CODE_PATTERN.test(primaryPointCode)) {
      return {
        ok: false,
        error: `点位映射第 ${index + 1} 行的点位编码 "${primaryPointCode || '(空)'}" 须以小写字母开头，仅含小写字母、数字和下划线`,
      };
    }
    const copyResult = parsePointCopyTargets(row.copyTargetsText);
    if (!copyResult.ok) {
      return { ok: false, error: `点位映射第 ${index + 1} 行：${copyResult.error}` };
    }
    pointMappings.push(
      copyResult.targets.length > 0
        ? {
            sourceChannel,
            primaryDeviceKey,
            primaryPointCode,
            copyTargets: copyResult.targets,
          }
        : {
            sourceChannel,
            primaryDeviceKey,
            primaryPointCode,
          },
    );
  }

  const samplingPolicies: GatewaySamplingPolicy[] = [];
  for (let index = 0; index < draft.samplingPolicies.length; index += 1) {
    const row = draft.samplingPolicies[index]!;
    const deviceKey = row.deviceKey.trim();
    const pointCode = row.pointCode.trim();
    const sampleIntervalMs = Number(row.sampleIntervalMs);
    const publishIntervalMs = Number(row.publishIntervalMs);
    if (!deviceKey || deviceKey.length > 128) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的设备标识不能为空` };
    }
    if (pointCode.length > 128) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的点位编码不能超过 128 个字符` };
    }
    if (!Number.isInteger(sampleIntervalMs) || sampleIntervalMs < 100 || sampleIntervalMs > 86_400_000) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的采样间隔需为 100 至 86400000 毫秒的整数` };
    }
    if (!Number.isInteger(publishIntervalMs) || publishIntervalMs < 100 || publishIntervalMs > 86_400_000) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的上报间隔需为 100 至 86400000 毫秒的整数` };
    }
    if (publishIntervalMs < sampleIntervalMs) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的上报间隔不能小于采样间隔` };
    }
    samplingPolicies.push(
      pointCode.length > 0
        ? { deviceKey, pointCode, sampleIntervalMs, publishIntervalMs }
        : { deviceKey, sampleIntervalMs, publishIntervalMs },
    );
  }

  // Omit concentratorConfigs when updating point mappings or sampling policies so the server
  // preserves any offline-staged concentrator parameters/EIDs atomically without resending 200 EID slots.
  const request: GatewayConfigShadowUpdateRequest = {
    expectedDesiredRevision: shadow?.desired?.desiredRevision ?? 0,
    pointMappings,
    samplingPolicies,
    reason,
  };

  return { ok: true, request };
}
