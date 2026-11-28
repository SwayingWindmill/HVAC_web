import type {
  GatewayAutonomyPolicy,
  GatewayModbusDriverConfig,
  GatewayOPCUADriverSpec,
  GatewayOPCUANodeMapping,
} from '../../api/generated/platformGateway.gen.ts';
import { isOneOf } from '../../lib/typed.ts';
import {
  AUTONOMY_POLICY_BOUNDS,
  DRIVER_CHANNEL_DATA_TYPES,
  MODBUS_CAPABILITY_PROFILES,
  MODBUS_POLL_PRIORITIES,
  OPCUA_AUTH_MODES,
  OPCUA_SECURITY_MODES,
  OPCUA_SECURITY_POLICIES,
  SOUTHBOUND_DRIVER_TYPES,
  normalizeZero,
  type DriverChannelDataType,
  type GatewayAutonomyPolicyDraft,
  type GatewayModbusDriverDraft,
  type GatewayOPCUASpecDraft,
  type ModbusCapabilityProfile,
} from './gateway-driver-constants.ts';
import {
  parseFiniteNumber,
  parseIntegerInRange,
  parseNumberInRange,
  validateBACnetDriverEntry,
  validateCommonPointMeta,
  validateModbusDriverEntry,
} from './gateway-driver-field-validation.ts';
import {
  isValidOpcuaEndpoint,
  parseOPCUANodeId,
} from './gateway-driver-safety.ts';

function validateOPCUADriverEntry(
  prefix: string,
  deviceKey: string,
  endpoint: string,
  capabilityProfile: ModbusCapabilityProfile | undefined,
  timeoutMs: number,
  retries: number,
  spec: GatewayOPCUASpecDraft,
):
  | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
  | { readonly ok: false; readonly error: string } {
  if (!isValidOpcuaEndpoint(endpoint)) {
    return {
      ok: false,
      error: `${prefix}的 OPC-UA 地址须以 opc.tcp:// 开头并包含 host:port（端口 1 至 65535）`,
    };
  }
  if (!isOneOf(OPCUA_SECURITY_POLICIES, spec.securityPolicy)) {
    return { ok: false, error: `${prefix}的 OPC-UA 安全策略无效` };
  }
  if (!isOneOf(OPCUA_SECURITY_MODES, spec.securityMode)) {
    return { ok: false, error: `${prefix}的 OPC-UA 安全模式无效` };
  }
  if (spec.securityPolicy === 'None' && spec.securityMode !== 'None') {
    return { ok: false, error: `${prefix}安全策略为 None 时，安全模式必须为 None` };
  }
  if (spec.securityPolicy === 'Basic256Sha256' && spec.securityMode === 'None') {
    return { ok: false, error: `${prefix}安全策略为 Basic256Sha256 时，安全模式须为 Sign 或 SignAndEncrypt` };
  }

  if (!isOneOf(OPCUA_AUTH_MODES, spec.authMode)) {
    return { ok: false, error: `${prefix}的 OPC-UA 认证方式无效` };
  }
  const username = spec.username.trim();
  const credentialRef = spec.credentialRef.trim();
  if (spec.authMode === 'ANONYMOUS' && username) {
    return { ok: false, error: `${prefix}选择匿名认证时不能填写用户名` };
  }
  if (spec.authMode === 'USERNAME' && (!username || !credentialRef || username.length > 128 || credentialRef.length > 128)) {
    return {
      ok: false,
      error: `${prefix}选择用户名认证时，须同时填写用户名和凭据引用（均不超过 128 个字符）`,
    };
  }

  let subscriptionIntervalMs = 1000;
  if (spec.subscriptionIntervalMs.trim() !== '') {
    const subRes = parseIntegerInRange(
      spec.subscriptionIntervalMs,
      50,
      60000,
      `${prefix}的订阅间隔`,
    );
    if (!subRes.ok) return subRes;
    subscriptionIntervalMs = subRes.value;
  }

  if (spec.nodes.length === 0 || spec.nodes.length > 256) {
    return { ok: false, error: `${prefix}至少需要配置 1 个 OPC-UA 节点，最多 256 个` };
  }

  const seenPointCodes = new Set<string>();
  const seenSemantics = new Set<string>();
  const seenNodeIds = new Set<string>();
  const nodes: GatewayOPCUANodeMapping[] = [];

  for (let nIndex = 0; nIndex < spec.nodes.length; nIndex += 1) {
    const node = spec.nodes[nIndex]!;
    const rowPrefix = `${prefix}第 ${nIndex + 1} 个 OPC-UA 节点`;
    const meta = validateCommonPointMeta(
      rowPrefix,
      prefix,
      node.pointCode,
      node.semanticChannel,
      node.safetyBoundary,
      node.unit,
      node.writable,
      seenPointCodes,
      seenSemantics,
    );
    if (!meta.ok) return meta;

    const nodeIdRes = parseOPCUANodeId(node.nodeId);
    if (!nodeIdRes.ok) {
      return { ok: false, error: `${rowPrefix}：${nodeIdRes.error}` };
    }
    if (node.namespaceIndex.trim() !== '') {
      const nsRes = parseIntegerInRange(
        node.namespaceIndex,
        0,
        65535,
        `${rowPrefix}的命名空间索引`,
      );
      if (!nsRes.ok) return nsRes;
      if (nsRes.value !== nodeIdRes.namespaceIndex) {
        return {
          ok: false,
          error: `${rowPrefix}的命名空间索引 (${nsRes.value}) 与节点 ID (${nodeIdRes.namespaceIndex}) 不一致`,
        };
      }
    }
    if (seenNodeIds.has(nodeIdRes.canonical)) {
      return { ok: false, error: `${prefix}中 OPC-UA 节点 ID "${nodeIdRes.canonical}" 重复` };
    }
    seenNodeIds.add(nodeIdRes.canonical);

    if (!isOneOf(MODBUS_POLL_PRIORITIES, node.pollPriority)) {
      return { ok: false, error: `${rowPrefix}的轮询优先级无效` };
    }

    let dataType: DriverChannelDataType = 'DOUBLE';
    if (node.dataType) {
      if (!isOneOf(DRIVER_CHANNEL_DATA_TYPES, node.dataType)) {
        return { ok: false, error: `${rowPrefix}的数据类型无效` };
      }
      dataType = node.dataType;
    }

    const scaleRes = parseFiniteNumber(node.scale, `${rowPrefix}的倍率`);
    if (!scaleRes.ok) return scaleRes;
    if (scaleRes.value === 0) {
      return { ok: false, error: `${rowPrefix}的倍率不能为 0` };
    }

    const offsetRes = parseFiniteNumber(node.offset, `${rowPrefix}的偏置`);
    if (!offsetRes.ok) return offsetRes;

    nodes.push({
      pointCode: meta.pointCode,
      ...(meta.semanticChannel ? { semanticChannel: meta.semanticChannel } : {}),
      ...(meta.safetyBoundary ? { safetyBoundary: meta.safetyBoundary } : {}),
      nodeId: nodeIdRes.canonical,
      namespaceIndex: nodeIdRes.namespaceIndex,
      dataType,
      ...(node.writable ? { writable: true } : {}),
      ...(node.subscribe ? { subscribe: true } : {}),
      scale: normalizeZero(scaleRes.value),
      offset: normalizeZero(offsetRes.value),
      pollPriority: node.pollPriority,
      ...(meta.unit ? { unit: meta.unit } : {}),
    });
  }

  const opcuaSpec: GatewayOPCUADriverSpec = {
    securityPolicy: spec.securityPolicy,
    securityMode: spec.securityMode,
    authMode: spec.authMode,
    ...(username ? { username } : {}),
    ...(credentialRef ? { credentialRef } : {}),
    subscriptionIntervalMs,
    nodes,
  };

  return {
    ok: true,
    config: {
      deviceKey,
      driverType: 'OPC_UA',
      endpoint,
      ...(capabilityProfile ? { capabilityProfile } : {}),
      timeoutMs,
      retries,
      opcua: opcuaSpec,
    },
  };
}

export function validateDriverConfigs(
  draftDrivers: readonly GatewayModbusDriverDraft[],
):
  | { readonly ok: true; readonly driverConfigs: GatewayModbusDriverConfig[] }
  | { readonly ok: false; readonly error: string } {
  const seenDeviceKeys = new Set<string>();
  const driverConfigs: GatewayModbusDriverConfig[] = [];
  for (let dIndex = 0; dIndex < draftDrivers.length; dIndex += 1) {
    const driver = draftDrivers[dIndex]!;
    const prefix = `协议驱动第 ${dIndex + 1} 项`;
    const deviceKey = driver.deviceKey.trim();
    const endpoint = driver.endpoint.trim();
    const rawProfile = driver.capabilityProfile.trim();

    if (!deviceKey || deviceKey.length > 128 || deviceKey.includes('/')) {
      return { ok: false, error: `${prefix}的本地设备标识须为 1 至 128 个字符且不能包含 "/"` };
    }
    if (seenDeviceKeys.has(deviceKey)) {
      return { ok: false, error: `协议驱动设备标识 "${deviceKey}" 重复` };
    }
    seenDeviceKeys.add(deviceKey);

    if (!isOneOf(SOUTHBOUND_DRIVER_TYPES, driver.driverType)) {
      return { ok: false, error: `${prefix}的协议类型无效` };
    }
    if (!endpoint || endpoint.length > 256) {
      return { ok: false, error: `${prefix}的采集地址不能为空且不超过 256 个字符` };
    }

    let capabilityProfile: ModbusCapabilityProfile | undefined;
    if (rawProfile) {
      if (!isOneOf(MODBUS_CAPABILITY_PROFILES, rawProfile)) {
        return { ok: false, error: `${prefix}的能力档位无效` };
      }
      capabilityProfile = rawProfile;
    }

    const timeoutRes = parseIntegerInRange(driver.timeoutMs, 100, 30000, `${prefix}的超时时间`);
    if (!timeoutRes.ok) return timeoutRes;

    const retriesRes = parseIntegerInRange(driver.retries, 0, 10, `${prefix}的重试次数`);
    if (!retriesRes.ok) return retriesRes;

    let validated:
      | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
      | { readonly ok: false; readonly error: string };
    switch (driver.driverType) {
      case 'MODBUS_TCP':
      case 'MODBUS_RTU':
        validated = validateModbusDriverEntry(
          prefix,
          deviceKey,
          driver.driverType,
          endpoint,
          capabilityProfile,
          timeoutRes.value,
          retriesRes.value,
          driver,
        );
        break;
      case 'BACNET_IP':
        validated = validateBACnetDriverEntry(
          prefix,
          deviceKey,
          endpoint,
          capabilityProfile,
          timeoutRes.value,
          retriesRes.value,
          driver.bacnet,
        );
        break;
      case 'OPC_UA':
        validated = validateOPCUADriverEntry(
          prefix,
          deviceKey,
          endpoint,
          capabilityProfile,
          timeoutRes.value,
          retriesRes.value,
          driver.opcua,
        );
        break;
    }
    if (!validated.ok) return validated;
    driverConfigs.push(validated.config);
  }
  return { ok: true, driverConfigs };
}

export function validateAutonomyPolicy(
  draftPolicy: GatewayAutonomyPolicyDraft,
):
  | { readonly ok: true; readonly autonomyPolicy: GatewayAutonomyPolicy }
  | { readonly ok: false; readonly error: string } {
  const graceRes = parseIntegerInRange(
    draftPolicy.offlineGraceSeconds,
    AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.min,
    AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.max,
    '断网宽限时间',
  );
  if (!graceRes.ok) return graceRes;

  const setpointRes = parseNumberInRange(
    draftPolicy.defaultChilledWaterSupplySetpointC,
    AUTONOMY_POLICY_BOUNDS.defaultChilledWaterSupplySetpointC.min,
    AUTONOMY_POLICY_BOUNDS.defaultChilledWaterSupplySetpointC.max,
    ' °C ',
    '冷冻水供水设定值',
  );
  if (!setpointRes.ok) return setpointRes;

  const minFreqRes = parseNumberInRange(
    draftPolicy.minPumpFrequencyHz,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max,
    ' Hz ',
    '水泵最小频率',
  );
  if (!minFreqRes.ok) return minFreqRes;

  const defaultFreqRes = parseNumberInRange(
    draftPolicy.defaultPumpFrequencyHz,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max,
    ' Hz ',
    '水泵默认频率',
  );
  if (!defaultFreqRes.ok) return defaultFreqRes;

  const maxFreqRes = parseNumberInRange(
    draftPolicy.maxPumpFrequencyHz,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max,
    ' Hz ',
    '水泵最大频率',
  );
  if (!maxFreqRes.ok) return maxFreqRes;

  if (
    minFreqRes.value > defaultFreqRes.value ||
    defaultFreqRes.value > maxFreqRes.value
  ) {
    return {
      ok: false,
      error: `水泵频率须满足：最小频率 <= 默认频率 <= 最大频率（均在 ${AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min} 至 ${AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max} Hz 之间）`,
    };
  }

  const minRunRes = parseIntegerInRange(
    draftPolicy.minRunSeconds,
    AUTONOMY_POLICY_BOUNDS.minRunSeconds.min,
    AUTONOMY_POLICY_BOUNDS.minRunSeconds.max,
    '最短运行保护时间',
  );
  if (!minRunRes.ok) return minRunRes;

  const minStopRes = parseIntegerInRange(
    draftPolicy.minStopSeconds,
    AUTONOMY_POLICY_BOUNDS.minStopSeconds.min,
    AUTONOMY_POLICY_BOUNDS.minStopSeconds.max,
    '最短停机保护时间',
  );
  if (!minStopRes.ok) return minStopRes;

  const snapshotRes = parseIntegerInRange(
    draftPolicy.snapshotIntervalSeconds,
    AUTONOMY_POLICY_BOUNDS.snapshotIntervalSeconds.min,
    AUTONOMY_POLICY_BOUNDS.snapshotIntervalSeconds.max,
    '全量快照周期',
  );
  if (!snapshotRes.ok) return snapshotRes;

  return {
    ok: true,
    autonomyPolicy: {
      enabled: draftPolicy.enabled,
      offlineGraceSeconds: graceRes.value,
      defaultChilledWaterSupplySetpointC: setpointRes.value,
      defaultPumpFrequencyHz: defaultFreqRes.value,
      minPumpFrequencyHz: minFreqRes.value,
      maxPumpFrequencyHz: maxFreqRes.value,
      minRunSeconds: minRunRes.value,
      minStopSeconds: minStopRes.value,
      snapshotIntervalSeconds: snapshotRes.value,
    },
  };
}
