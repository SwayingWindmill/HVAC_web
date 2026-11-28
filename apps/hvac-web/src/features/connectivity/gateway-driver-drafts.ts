import type {
  ConcentratorUnitShadow,
  EdgeRuleBundle,
  GatewayAutonomyPolicy,
  GatewayConfigShadow,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
  PointCopyTarget,
} from '../../api/generated/platformGateway.gen.ts';
import {
  POINT_CODE_PATTERN,
  UINT16_DECIMAL_PATTERN,
  compareAscii,
  nextDraftId,
  type AutonomyPolicySummaryState,
  type ConcentratorShadowUnitSummary,
  type GatewayAutonomyPolicyDraft,
  type GatewayBACnetObjectDraft,
  type GatewayBACnetSpecDraft,
  type GatewayConfigShadowEditorDraft,
  type GatewayModbusDriverDraft,
  type GatewayModbusRegisterDraft,
  type GatewayOPCUANodeDraft,
  type GatewayOPCUASpecDraft,
  type GatewayPointMappingDraft,
  type GatewaySamplingPolicyDraft,
} from './gateway-driver-constants.ts';

export function createEmptyPointMappingDraft(): GatewayPointMappingDraft {
  return {
    draftId: nextDraftId('map'),
    sourceChannel: '',
    primaryDeviceKey: '',
    primaryPointCode: '',
    copyTargetsText: '',
  };
}

export function createEmptySamplingPolicyDraft(): GatewaySamplingPolicyDraft {
  return {
    draftId: nextDraftId('samp'),
    deviceKey: '',
    pointCode: '',
    sampleIntervalMs: '5000',
    publishIntervalMs: '5000',
  };
}

export function createEmptyModbusRegisterDraft(): GatewayModbusRegisterDraft {
  return {
    draftId: nextDraftId('reg'),
    pointCode: '',
    semanticChannel: '',
    functionCode: '3',
    writeFunctionCode: '',
    address: '0',
    registerCount: '1',
    rawType: 'UINT16',
    byteOrder: 'BIG_ENDIAN',
    wordOrder: 'BIG_ENDIAN',
    bitIndex: '',
    scale: '1',
    offset: '0',
    pollPriority: 'MEDIUM',
    enumMapText: '',
  };
}

export function createEmptyBACnetObjectDraft(): GatewayBACnetObjectDraft {
  return {
    draftId: nextDraftId('bacobj'),
    pointCode: '',
    semanticChannel: '',
    safetyBoundary: '',
    objectType: 'ANALOG_INPUT',
    objectInstance: '1',
    propertyId: '85',
    dataType: '',
    writable: false,
    covSubscribe: false,
    scale: '1',
    offset: '0',
    pollPriority: 'MEDIUM',
    unit: '',
    enumMapText: '',
  };
}

export function createEmptyBACnetSpecDraft(): GatewayBACnetSpecDraft {
  return {
    deviceInstance: '1001',
    networkNumber: '',
    covLifetimeSeconds: '',
    writePriority: '8',
    objects: [createEmptyBACnetObjectDraft()],
  };
}

export function createEmptyOPCUANodeDraft(): GatewayOPCUANodeDraft {
  return {
    draftId: nextDraftId('uanode'),
    pointCode: '',
    semanticChannel: '',
    safetyBoundary: '',
    nodeId: '',
    namespaceIndex: '',
    dataType: '',
    writable: false,
    subscribe: false,
    scale: '1',
    offset: '0',
    pollPriority: 'MEDIUM',
    unit: '',
  };
}

export function createEmptyOPCUASpecDraft(): GatewayOPCUASpecDraft {
  return {
    securityPolicy: 'None',
    securityMode: 'None',
    authMode: 'ANONYMOUS',
    username: '',
    credentialRef: '',
    subscriptionIntervalMs: '1000',
    nodes: [createEmptyOPCUANodeDraft()],
  };
}

export function createEmptyModbusDriverDraft(): GatewayModbusDriverDraft {
  return {
    draftId: nextDraftId('drv'),
    deviceKey: '',
    driverType: 'MODBUS_TCP',
    endpoint: '',
    unitId: '1',
    capabilityProfile: '',
    timeoutMs: '1000',
    retries: '2',
    registers: [createEmptyModbusRegisterDraft()],
    bacnet: createEmptyBACnetSpecDraft(),
    opcua: createEmptyOPCUASpecDraft(),
  };
}

export function createDefaultAutonomyPolicyDraft(): GatewayAutonomyPolicyDraft {
  return {
    enabled: true,
    offlineGraceSeconds: '120',
    defaultChilledWaterSupplySetpointC: '7',
    defaultPumpFrequencyHz: '40',
    minPumpFrequencyHz: '25',
    maxPumpFrequencyHz: '50',
    minRunSeconds: '300',
    minStopSeconds: '180',
    snapshotIntervalSeconds: '300',
  };
}

export function defaultRegisterCountForRawType(
  rawType: GatewayModbusRegisterMapping['rawType'],
): '1' | '2' {
  return rawType === 'UINT32' || rawType === 'INT32' || rawType === 'FLOAT32' ? '2' : '1';
}

export function summarizeAutonomyPolicyState(
  policy: GatewayAutonomyPolicy | null | undefined,
): AutonomyPolicySummaryState {
  if (!policy) return 'UNCONFIGURED';
  return policy.enabled ? 'ENABLED' : 'DISABLED';
}

export interface DriverPointCountSummary {
  readonly driverCount: number;
  readonly modbusRegisterCount: number;
  readonly bacnetObjectCount: number;
  readonly opcuaNodeCount: number;
  readonly totalPointCount: number;
}

export function summarizeDriverPointCounts(
  driverConfigs: readonly GatewayModbusDriverConfig[] | undefined,
): DriverPointCountSummary {
  if (!driverConfigs || driverConfigs.length === 0) {
    return {
      driverCount: 0,
      modbusRegisterCount: 0,
      bacnetObjectCount: 0,
      opcuaNodeCount: 0,
      totalPointCount: 0,
    };
  }
  let modbusRegisterCount = 0;
  let bacnetObjectCount = 0;
  let opcuaNodeCount = 0;
  for (const driver of driverConfigs) {
    modbusRegisterCount += driver.registers?.length ?? 0;
    bacnetObjectCount += driver.bacnet?.objects.length ?? 0;
    opcuaNodeCount += driver.opcua?.nodes.length ?? 0;
  }
  return {
    driverCount: driverConfigs.length,
    modbusRegisterCount,
    bacnetObjectCount,
    opcuaNodeCount,
    totalPointCount: modbusRegisterCount + bacnetObjectCount + opcuaNodeCount,
  };
}

export function countDriverRegisters(
  driverConfigs: readonly GatewayModbusDriverConfig[] | undefined,
): number {
  return summarizeDriverPointCounts(driverConfigs).totalPointCount;
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

function isValidUint16DecimalKey(key: string): boolean {
  if (!UINT16_DECIMAL_PATTERN.test(key)) return false;
  const parsed = Number(key);
  return Number.isInteger(parsed) && parsed >= 0 && parsed <= 65535;
}

export function formatModbusEnumMap(enumMap?: Readonly<Record<string, string>>): string {
  if (!enumMap) return '';
  return Object.entries(enumMap)
    .sort(([left], [right]) => {
      const leftNum = Number(left);
      const rightNum = Number(right);
      if (Number.isInteger(leftNum) && Number.isInteger(rightNum) && leftNum !== rightNum) {
        return leftNum - rightNum;
      }
      return compareAscii(left, right);
    })
    .map(([key, label]) => `${key}=${label}`)
    .join(', ');
}

export function parseModbusEnumMap(
  raw: string,
): { readonly ok: true; readonly enumMap: Record<string, string> } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return {
      ok: false,
      error: '16 位枚举寄存器须至少配置一项枚举映射（例如 0=停机, 1=运行）',
    };
  }
  const tokens = trimmed
    .split(/[\n,，]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
  if (tokens.length === 0) {
    return {
      ok: false,
      error: '16 位枚举寄存器须至少配置一项枚举映射（例如 0=停机, 1=运行）',
    };
  }
  const enumMap: Record<string, string> = {};
  for (const token of tokens) {
    const sepIndex = token.indexOf('=');
    if (sepIndex <= 0) {
      return {
        ok: false,
        error: `枚举映射 "${token}" 格式须为 数值=状态标签`,
      };
    }
    const key = token.slice(0, sepIndex).trim();
    const label = token.slice(sepIndex + 1).trim();
    if (!isValidUint16DecimalKey(key)) {
      return {
        ok: false,
        error: `枚举键 "${key}" 须为 0 至 65535 的十进制无符号整数`,
      };
    }
    if (!label || label.length > 64) {
      return {
        ok: false,
        error: `枚举键 ${key} 的状态标签不能为空且不超过 64 个字符`,
      };
    }
    if (Object.prototype.hasOwnProperty.call(enumMap, key)) {
      return {
        ok: false,
        error: `枚举键 ${key} 重复`,
      };
    }
    enumMap[key] = label;
  }
  return { ok: true, enumMap };
}

export function createShadowEditorDraft(
  shadow: GatewayConfigShadow | undefined,
): GatewayConfigShadowEditorDraft {
  const desired = shadow?.desired;
  const pointMappings = (desired?.pointMappings ?? []).map((item) => ({
    draftId: nextDraftId('map'),
    sourceChannel: item.sourceChannel,
    primaryDeviceKey: item.primaryDeviceKey,
    primaryPointCode: item.primaryPointCode,
    copyTargetsText: formatPointCopyTargets(item.copyTargets),
  }));
  const samplingPolicies = (desired?.samplingPolicies ?? []).map((item) => ({
    draftId: nextDraftId('samp'),
    deviceKey: item.deviceKey,
    pointCode: item.pointCode ?? '',
    sampleIntervalMs: String(item.sampleIntervalMs),
    publishIntervalMs: String(item.publishIntervalMs),
  }));
  const driverConfigs = (desired?.driverConfigs ?? []).map((driver): GatewayModbusDriverDraft => {
    const registers =
      driver.registers && driver.registers.length > 0
        ? driver.registers.map((reg) => ({
            draftId: nextDraftId('reg'),
            pointCode: reg.pointCode,
            semanticChannel: reg.semanticChannel ?? '',
            functionCode: reg.functionCode === 4 ? ('4' as const) : ('3' as const),
            writeFunctionCode:
              reg.writeFunctionCode === 6
                ? ('6' as const)
                : reg.writeFunctionCode === 16
                  ? ('16' as const)
                  : ('' as const),
            address: String(reg.address),
            registerCount: String(reg.registerCount),
            rawType: reg.rawType,
            byteOrder: reg.byteOrder,
            wordOrder: reg.wordOrder,
            bitIndex: reg.bitIndex !== undefined ? String(reg.bitIndex) : '',
            scale: String(reg.scale),
            offset: String(reg.offset),
            pollPriority: reg.pollPriority,
            enumMapText: formatModbusEnumMap(reg.enumMap),
          }))
        : [createEmptyModbusRegisterDraft()];
    const bacnet: GatewayBACnetSpecDraft = driver.bacnet
      ? {
          deviceInstance: String(driver.bacnet.deviceInstance),
          networkNumber:
            driver.bacnet.networkNumber !== undefined && driver.bacnet.networkNumber > 0
              ? String(driver.bacnet.networkNumber)
              : '',
          covLifetimeSeconds:
            driver.bacnet.covLifetimeSeconds !== undefined && driver.bacnet.covLifetimeSeconds > 0
              ? String(driver.bacnet.covLifetimeSeconds)
              : '',
          writePriority:
            driver.bacnet.writePriority !== undefined ? String(driver.bacnet.writePriority) : '8',
          objects:
            driver.bacnet.objects.length > 0
              ? driver.bacnet.objects.map((obj) => ({
                  draftId: nextDraftId('bacobj'),
                  pointCode: obj.pointCode,
                  semanticChannel: obj.semanticChannel ?? '',
                  safetyBoundary: obj.safetyBoundary ?? '',
                  objectType: obj.objectType,
                  objectInstance: String(obj.objectInstance),
                  propertyId: obj.propertyId !== undefined ? String(obj.propertyId) : '85',
                  dataType: obj.dataType ?? '',
                  writable: Boolean(obj.writable),
                  covSubscribe: Boolean(obj.covSubscribe),
                  scale: obj.scale !== undefined && obj.scale !== 0 ? String(obj.scale) : '1',
                  offset: obj.offset !== undefined ? String(obj.offset) : '0',
                  pollPriority: obj.pollPriority,
                  unit: obj.unit ?? '',
                  enumMapText: formatModbusEnumMap(obj.enumMap),
                }))
              : [createEmptyBACnetObjectDraft()],
        }
      : createEmptyBACnetSpecDraft();
    const opcua: GatewayOPCUASpecDraft = driver.opcua
      ? {
          securityPolicy: driver.opcua.securityPolicy,
          securityMode: driver.opcua.securityMode,
          authMode: driver.opcua.authMode ?? 'ANONYMOUS',
          username: driver.opcua.username ?? '',
          credentialRef: driver.opcua.credentialRef ?? '',
          subscriptionIntervalMs:
            driver.opcua.subscriptionIntervalMs !== undefined
              ? String(driver.opcua.subscriptionIntervalMs)
              : '1000',
          nodes:
            driver.opcua.nodes.length > 0
              ? driver.opcua.nodes.map((node) => ({
                  draftId: nextDraftId('uanode'),
                  pointCode: node.pointCode,
                  semanticChannel: node.semanticChannel ?? '',
                  safetyBoundary: node.safetyBoundary ?? '',
                  nodeId: node.nodeId,
                  namespaceIndex:
                    node.namespaceIndex !== undefined ? String(node.namespaceIndex) : '',
                  dataType: node.dataType ?? '',
                  writable: Boolean(node.writable),
                  subscribe: Boolean(node.subscribe),
                  scale: node.scale !== undefined && node.scale !== 0 ? String(node.scale) : '1',
                  offset: node.offset !== undefined ? String(node.offset) : '0',
                  pollPriority: node.pollPriority,
                  unit: node.unit ?? '',
                }))
              : [createEmptyOPCUANodeDraft()],
        }
      : createEmptyOPCUASpecDraft();

    return {
      draftId: nextDraftId('drv'),
      deviceKey: driver.deviceKey,
      driverType: driver.driverType,
      endpoint: driver.endpoint,
      unitId: driver.unitId !== undefined ? String(driver.unitId) : '1',
      capabilityProfile: driver.capabilityProfile ?? '',
      timeoutMs: String(driver.timeoutMs),
      retries: String(driver.retries),
      registers,
      bacnet,
      opcua,
    };
  });
  const autonomyPolicy = desired?.autonomyPolicy
    ? {
        enabled: desired.autonomyPolicy.enabled,
        offlineGraceSeconds: String(desired.autonomyPolicy.offlineGraceSeconds),
        defaultChilledWaterSupplySetpointC: String(
          desired.autonomyPolicy.defaultChilledWaterSupplySetpointC,
        ),
        defaultPumpFrequencyHz: String(desired.autonomyPolicy.defaultPumpFrequencyHz),
        minPumpFrequencyHz: String(desired.autonomyPolicy.minPumpFrequencyHz),
        maxPumpFrequencyHz: String(desired.autonomyPolicy.maxPumpFrequencyHz),
        minRunSeconds: String(desired.autonomyPolicy.minRunSeconds),
        minStopSeconds: String(desired.autonomyPolicy.minStopSeconds),
        snapshotIntervalSeconds: String(desired.autonomyPolicy.snapshotIntervalSeconds),
      }
    : null;

  return {
    pointMappings,
    samplingPolicies,
    driverConfigs,
    autonomyPolicy,
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

export interface EdgeRuleBundleSummary {
  readonly configured: boolean;
  readonly ruleCount: number;
  readonly bundleRevision: number;
  readonly shortDigest: string;
  readonly compiledAt: string;
}

/** Summarizes a desired or reported edge rule bundle for gateway shadow cards and compilation sheets. */
export function summarizeEdgeRuleBundle(bundle: EdgeRuleBundle | null | undefined): EdgeRuleBundleSummary {
  if (!bundle) {
    return {
      configured: false,
      ruleCount: 0,
      bundleRevision: 0,
      shortDigest: '',
      compiledAt: '',
    };
  }
  return {
    configured: true,
    ruleCount: bundle.rules.length,
    bundleRevision: bundle.bundleRevision,
    shortDigest: bundle.edgeBundleDigest.slice(0, 12),
    compiledAt: bundle.compiledAt ?? '',
  };
}
