import type {
  GatewayAutonomyPolicy,
  GatewayBACnetDriverSpec,
  GatewayConfigShadow,
  GatewayConfigShadowUpdateRequest,
  GatewayDesiredConfig,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
  GatewayOPCUADriverSpec,
  GatewayPointMapping,
  GatewaySamplingPolicy,
} from '../../api/generated/platformGateway.gen.ts';
import {
  POINT_CODE_PATTERN,
  compareAscii,
  normalizeZero,
  type GatewayConfigShadowEditorDraft,
} from './gateway-driver-constants.ts';
import { parsePointCopyTargets } from './gateway-driver-drafts.ts';
import { defaultBACnetDataType } from './gateway-driver-safety.ts';
import { validateAutonomyPolicy, validateDriverConfigs } from './gateway-driver-validation.ts';

export * from './gateway-driver-constants.ts';
export * from './gateway-driver-drafts.ts';
export * from './gateway-driver-safety.ts';

function areStringRecordsEqual(
  left: Readonly<Record<string, string>> | undefined,
  right: Readonly<Record<string, string>> | undefined,
): boolean {
  const leftKeys = left ? Object.keys(left) : [];
  const rightKeys = right ? Object.keys(right) : [];
  if (leftKeys.length !== rightKeys.length) return false;
  return leftKeys.every((key) => left?.[key] === right?.[key]);
}

function compareRegisterMappings(
  left: GatewayModbusRegisterMapping,
  right: GatewayModbusRegisterMapping,
): number {
  const byPoint = compareAscii(left.pointCode, right.pointCode);
  if (byPoint !== 0) return byPoint;
  if (left.address !== right.address) return left.address - right.address;
  if (left.functionCode !== right.functionCode) return left.functionCode - right.functionCode;
  return compareAscii(left.rawType, right.rawType);
}

function canonicalizeDriverConfigs(
  drivers: readonly GatewayModbusDriverConfig[],
): readonly GatewayModbusDriverConfig[] {
  return [...drivers]
    .map((driver) => ({
      ...driver,
      ...(driver.registers ? { registers: [...driver.registers].sort(compareRegisterMappings) } : {}),
      ...(driver.bacnet
        ? {
            bacnet: {
              ...driver.bacnet,
              objects: [...driver.bacnet.objects].sort((left, right) =>
                compareAscii(left.pointCode, right.pointCode),
              ),
            },
          }
        : {}),
      ...(driver.opcua
        ? {
            opcua: {
              ...driver.opcua,
              nodes: [...driver.opcua.nodes].sort((left, right) =>
                compareAscii(left.pointCode, right.pointCode),
              ),
            },
          }
        : {}),
    }))
    .sort((left, right) => compareAscii(left.deviceKey, right.deviceKey));
}

function areBACnetSpecsEqual(
  left: GatewayBACnetDriverSpec | undefined,
  right: GatewayBACnetDriverSpec | undefined,
): boolean {
  if (!left && !right) return true;
  if (!left || !right) return false;
  if (
    left.deviceInstance !== right.deviceInstance ||
    (left.networkNumber ?? 0) !== (right.networkNumber ?? 0) ||
    (left.covLifetimeSeconds ?? 0) !== (right.covLifetimeSeconds ?? 0) ||
    (left.writePriority ?? 8) !== (right.writePriority ?? 8) ||
    left.objects.length !== right.objects.length
  ) {
    return false;
  }
  for (let i = 0; i < left.objects.length; i += 1) {
    const oa = left.objects[i]!;
    const ob = right.objects[i]!;
    const scaleA = oa.scale === undefined || oa.scale === 0 ? 1 : oa.scale;
    const scaleB = ob.scale === undefined || ob.scale === 0 ? 1 : ob.scale;
    if (
      oa.pointCode !== ob.pointCode ||
      (oa.semanticChannel ?? '') !== (ob.semanticChannel ?? '') ||
      (oa.safetyBoundary ?? '') !== (ob.safetyBoundary ?? '') ||
      oa.objectType !== ob.objectType ||
      oa.objectInstance !== ob.objectInstance ||
      (oa.propertyId ?? 85) !== (ob.propertyId ?? 85) ||
      (oa.dataType ?? defaultBACnetDataType(oa.objectType)) !==
        (ob.dataType ?? defaultBACnetDataType(ob.objectType)) ||
      Boolean(oa.writable) !== Boolean(ob.writable) ||
      Boolean(oa.covSubscribe) !== Boolean(ob.covSubscribe) ||
      scaleA !== scaleB ||
      normalizeZero(oa.offset ?? 0) !== normalizeZero(ob.offset ?? 0) ||
      oa.pollPriority !== ob.pollPriority ||
      (oa.unit ?? '') !== (ob.unit ?? '') ||
      !areStringRecordsEqual(oa.enumMap, ob.enumMap)
    ) {
      return false;
    }
  }
  return true;
}

function areOPCUASpecsEqual(
  left: GatewayOPCUADriverSpec | undefined,
  right: GatewayOPCUADriverSpec | undefined,
): boolean {
  if (!left && !right) return true;
  if (!left || !right) return false;
  if (
    left.securityPolicy !== right.securityPolicy ||
    left.securityMode !== right.securityMode ||
    (left.authMode ?? 'ANONYMOUS') !== (right.authMode ?? 'ANONYMOUS') ||
    (left.username ?? '') !== (right.username ?? '') ||
    (left.credentialRef ?? '') !== (right.credentialRef ?? '') ||
    (left.subscriptionIntervalMs ?? 1000) !== (right.subscriptionIntervalMs ?? 1000) ||
    left.nodes.length !== right.nodes.length
  ) {
    return false;
  }
  for (let i = 0; i < left.nodes.length; i += 1) {
    const na = left.nodes[i]!;
    const nb = right.nodes[i]!;
    const scaleA = na.scale === undefined || na.scale === 0 ? 1 : na.scale;
    const scaleB = nb.scale === undefined || nb.scale === 0 ? 1 : nb.scale;
    if (
      na.pointCode !== nb.pointCode ||
      (na.semanticChannel ?? '') !== (nb.semanticChannel ?? '') ||
      (na.safetyBoundary ?? '') !== (nb.safetyBoundary ?? '') ||
      na.nodeId !== nb.nodeId ||
      (na.namespaceIndex ?? 0) !== (nb.namespaceIndex ?? 0) ||
      (na.dataType ?? 'DOUBLE') !== (nb.dataType ?? 'DOUBLE') ||
      Boolean(na.writable) !== Boolean(nb.writable) ||
      Boolean(na.subscribe) !== Boolean(nb.subscribe) ||
      scaleA !== scaleB ||
      normalizeZero(na.offset ?? 0) !== normalizeZero(nb.offset ?? 0) ||
      na.pollPriority !== nb.pollPriority ||
      (na.unit ?? '') !== (nb.unit ?? '')
    ) {
      return false;
    }
  }
  return true;
}

function areDriverConfigsEqual(
  left: readonly GatewayModbusDriverConfig[],
  right: readonly GatewayModbusDriverConfig[],
): boolean {
  if (left.length !== right.length) return false;
  const sortedLeft = canonicalizeDriverConfigs(left);
  const sortedRight = canonicalizeDriverConfigs(right);
  for (let i = 0; i < sortedLeft.length; i += 1) {
    const a = sortedLeft[i]!;
    const b = sortedRight[i]!;
    const regsA = a.registers ?? [];
    const regsB = b.registers ?? [];
    if (
      a.deviceKey !== b.deviceKey ||
      a.driverType !== b.driverType ||
      a.endpoint !== b.endpoint ||
      (a.unitId ?? 0) !== (b.unitId ?? 0) ||
      (a.capabilityProfile ?? '') !== (b.capabilityProfile ?? '') ||
      a.timeoutMs !== b.timeoutMs ||
      a.retries !== b.retries ||
      regsA.length !== regsB.length
    ) {
      return false;
    }
    for (let j = 0; j < regsA.length; j += 1) {
      const ra = regsA[j]!;
      const rb = regsB[j]!;
      if (
        ra.pointCode !== rb.pointCode ||
        (ra.semanticChannel ?? '') !== (rb.semanticChannel ?? '') ||
        ra.functionCode !== rb.functionCode ||
        ra.writeFunctionCode !== rb.writeFunctionCode ||
        ra.address !== rb.address ||
        ra.registerCount !== rb.registerCount ||
        ra.rawType !== rb.rawType ||
        ra.byteOrder !== rb.byteOrder ||
        ra.wordOrder !== rb.wordOrder ||
        ra.bitIndex !== rb.bitIndex ||
        normalizeZero(ra.scale) !== normalizeZero(rb.scale) ||
        normalizeZero(ra.offset) !== normalizeZero(rb.offset) ||
        ra.pollPriority !== rb.pollPriority ||
        !areStringRecordsEqual(ra.enumMap, rb.enumMap)
      ) {
        return false;
      }
    }
    if (!areBACnetSpecsEqual(a.bacnet, b.bacnet) || !areOPCUASpecsEqual(a.opcua, b.opcua)) {
      return false;
    }
  }
  return true;
}

function areAutonomyPoliciesEqual(
  left: GatewayAutonomyPolicy | null,
  right: GatewayAutonomyPolicy | null | undefined,
): boolean {
  const normalizedRight = right ?? null;
  if (left === null && normalizedRight === null) return true;
  if (left === null || normalizedRight === null) return false;
  return (
    left.enabled === normalizedRight.enabled &&
    left.offlineGraceSeconds === normalizedRight.offlineGraceSeconds &&
    left.defaultChilledWaterSupplySetpointC === normalizedRight.defaultChilledWaterSupplySetpointC &&
    left.defaultPumpFrequencyHz === normalizedRight.defaultPumpFrequencyHz &&
    left.minPumpFrequencyHz === normalizedRight.minPumpFrequencyHz &&
    left.maxPumpFrequencyHz === normalizedRight.maxPumpFrequencyHz &&
    left.minRunSeconds === normalizedRight.minRunSeconds &&
    left.minStopSeconds === normalizedRight.minStopSeconds &&
    left.snapshotIntervalSeconds === normalizedRight.snapshotIntervalSeconds
  );
}

export function buildValidatedShadowUpdateRequestFromDesired(
  draft: GatewayConfigShadowEditorDraft,
  expectedDesiredRevision: number,
  currentDesired: GatewayDesiredConfig | undefined,
):
  | { readonly ok: true; readonly request: GatewayConfigShadowUpdateRequest }
  | { readonly ok: false; readonly error: string } {
  const reason = draft.reason.trim();
  if (!reason) {
    return { ok: false, error: '请填写配置影子变更原因' };
  }
  if (reason.length > 1000) {
    return { ok: false, error: '配置影子变更原因不能超过 1000 个字符' };
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

  const driversResult = validateDriverConfigs(draft.driverConfigs);
  if (!driversResult.ok) return driversResult;

  let validatedAutonomy: GatewayAutonomyPolicy | null = null;
  if (draft.autonomyPolicy !== null) {
    const autonomyResult = validateAutonomyPolicy(draft.autonomyPolicy);
    if (!autonomyResult.ok) return autonomyResult;
    validatedAutonomy = autonomyResult.autonomyPolicy;
  }

  const includePointMappings =
    pointMappings.length > 0 || (currentDesired?.pointMappings.length ?? 0) > 0;
  const includeSamplingPolicies =
    samplingPolicies.length > 0 || (currentDesired?.samplingPolicies.length ?? 0) > 0;
  const driverConfigsChanged = !areDriverConfigsEqual(
    driversResult.driverConfigs,
    currentDesired?.driverConfigs ?? [],
  );
  const autonomyChanged = !areAutonomyPoliciesEqual(validatedAutonomy, currentDesired?.autonomyPolicy);

  // Omit concentratorConfigs and unchanged driverConfigs/autonomyPolicy so partial PUT
  // preserves offline-staged concentrator state and avoids resending unmodified sections.
  const request: GatewayConfigShadowUpdateRequest = {
    expectedDesiredRevision,
    ...(includePointMappings ? { pointMappings } : {}),
    ...(includeSamplingPolicies ? { samplingPolicies } : {}),
    ...(driverConfigsChanged ? { driverConfigs: driversResult.driverConfigs } : {}),
    ...(autonomyChanged ? { autonomyPolicy: validatedAutonomy } : {}),
    reason,
  };

  return { ok: true, request };
}

export function buildValidatedShadowUpdateRequest(
  draft: GatewayConfigShadowEditorDraft,
  shadow: GatewayConfigShadow | undefined,
):
  | { readonly ok: true; readonly request: GatewayConfigShadowUpdateRequest }
  | { readonly ok: false; readonly error: string } {
  return buildValidatedShadowUpdateRequestFromDesired(
    draft,
    shadow?.desired?.desiredRevision ?? 0,
    shadow?.desired,
  );
}
